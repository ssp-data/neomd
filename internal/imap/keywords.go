package imap

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// KeywordProbeStep is the outcome of one IMAP round trip inside KeywordProbe.
type KeywordProbeStep struct {
	Name   string
	Detail string // result detail (current flags, UIDs found, …)
	Err    string // raw error, empty when the step succeeded
	OK     bool
}

// KeywordProbeResult reports everything KeywordProbe learned about a server's
// support for arbitrary message keywords (RFC 9051 §7.1.2, PERMANENTFLAGS \*).
type KeywordProbeResult struct {
	Folder  string
	UID     uint32
	Keyword string

	PermanentFlags  []string
	WildcardAllowed bool     // \* in PERMANENTFLAGS — server advertises new keywords
	MailboxKeywords []string // non-system flags in use in the mailbox
	BaselineFlags   []string // message flags before the probe

	Steps []KeywordProbeStep

	StoreSupported  bool // STORE +FLAGS succeeded and a following FETCH saw the keyword
	SearchSupported bool // UID SEARCH KEYWORD returned the probed message
	CleanupOK       bool // keyword absent after the cleanup STORE
	Kept            bool // keep mode: cleanup skipped, keyword left on the message
	Err             string
}

// KeywordProbe runs a save/retrieve/cleanup round trip for one arbitrary
// keyword on one message to determine whether the server supports
// user-defined keywords. It SELECTs the mailbox (capturing PERMANENTFLAGS),
// FETCHes the message's baseline flags, STOREs the keyword, verifies it via
// FETCH and UID SEARCH, then removes it again — the message ends with its
// original flags. With keep=true the final cleanup is skipped and the keyword
// stays on the message (to verify persistence across sessions; a later normal
// run removes it). Every step is reported separately and a failing step does
// not abort the later ones (cleanup always runs in non-keep mode). Only a
// failed SELECT or connection error aborts; that error lands in Result.Err.
func (c *Client) KeywordProbe(ctx context.Context, folder string, uid uint32, keyword string, keep bool) *KeywordProbeResult {
	if ctx == nil {
		ctx = context.Background()
	}
	defer trace(time.Now(), "KeywordProbe %s uid=%d keyword=%s", folder, uid, keyword)
	res := &KeywordProbeResult{Folder: folder, UID: uid, Keyword: keyword}
	kw := imap.Flag(keyword)
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		// Always a fresh SELECT: the response carries the PERMANENTFLAGS this
		// probe exists to inspect, even when the mailbox is already selected.
		data, err := conn.Select(folder, nil).Wait()
		if err != nil {
			c.selectedMailbox = "" // a failed SELECT leaves no mailbox selected
			return fmt.Errorf("SELECT %q: %w", folder, err)
		}
		c.selectedMailbox = folder
		res.PermanentFlags = flagsToStrings(data.PermanentFlags)
		for _, f := range data.PermanentFlags {
			if f == imap.FlagWildcard {
				res.WildcardAllowed = true
			}
		}
		res.MailboxKeywords = keywordFlags(data.Flags)
		res.Steps = append(res.Steps, KeywordProbeStep{
			Name:   "SELECT — PERMANENTFLAGS",
			OK:     true,
			Detail: "permanent: [" + strings.Join(res.PermanentFlags, " ") + "]  keywords in use: [" + strings.Join(res.MailboxKeywords, " ") + "]",
		})

		step := func(name string, err error, ok bool, detail string) {
			s := KeywordProbeStep{Name: name, OK: ok, Detail: detail}
			if err != nil {
				s.Err = err.Error()
			}
			res.Steps = append(res.Steps, s)
		}

		baseline, found, err := fetchMessageFlags(conn, uid)
		switch {
		case err != nil:
			step("FETCH — baseline flags", err, false, "")
		case !found:
			step("FETCH — baseline flags", nil, false, "message not returned by FETCH (UID moved or vanished?)")
		default:
			res.BaselineFlags = flagsToStrings(baseline)
			step("FETCH — baseline flags", nil, true, "current: ["+strings.Join(res.BaselineFlags, " ")+"]")
		}

		var uidSet imap.UIDSet
		uidSet.AddNum(imap.UID(uid))
		storeErr := conn.Store(uidSet, &imap.StoreFlags{
			Op:    imap.StoreFlagsAdd,
			Flags: []imap.Flag{kw},
		}, nil).Close()
		step("STORE +FLAGS "+keyword, storeErr, storeErr == nil, "")

		afterAdd, found, err := fetchMessageFlags(conn, uid)
		stored := keywordCase(afterAdd, kw)
		hasKw := err == nil && found && stored != ""
		res.StoreSupported = storeErr == nil && hasKw
		switch {
		case err != nil:
			step("FETCH — keyword set?", err, false, "")
		case !found:
			step("FETCH — keyword set?", nil, false, "message not returned by FETCH")
		case hasKw && stored != keyword:
			step("FETCH — keyword set?", nil, true, "keyword present, stored as \""+stored+"\" (server normalized the case)")
		case hasKw:
			step("FETCH — keyword set?", nil, true, "keyword present in flags")
		default:
			step("FETCH — keyword set?", nil, false, "STORE reported success but the keyword is not in the flags")
		}

		detail := ""
		searchData, err := conn.UIDSearch(&imap.SearchCriteria{Flag: []imap.Flag{kw}}, nil).Wait()
		if err == nil {
			if us, ok := searchData.All.(imap.UIDSet); ok {
				if nums, _ := us.Nums(); len(nums) > 0 {
					found = true
					strs := make([]string, len(nums))
					for i, n := range nums {
						strs[i] = fmt.Sprintf("%d", n)
					}
					detail = "found UID " + strings.Join(strs, ",")
				}
			}
			if !found {
				detail = "no messages carry the keyword"
			}
		}
		res.SearchSupported = found
		step("UID SEARCH KEYWORD "+keyword, err, err == nil && found, detail)

		if keep {
			res.Kept = true
			step("STORE -FLAGS "+keyword+" (cleanup skipped)", nil, true, "keep mode — keyword left on the message; re-run the test (r) to remove it")
			return nil
		}

		cleanErr := conn.Store(uidSet, &imap.StoreFlags{
			Op:    imap.StoreFlagsDel,
			Flags: []imap.Flag{kw},
		}, nil).Close()
		step("STORE -FLAGS "+keyword+" (cleanup)", cleanErr, cleanErr == nil, "")

		afterDel, found, err := fetchMessageFlags(conn, uid)
		gone := err == nil && found && keywordCase(afterDel, kw) == ""
		res.CleanupOK = gone
		switch {
		case err != nil:
			step("FETCH — keyword gone?", err, false, "")
		case !found:
			step("FETCH — keyword gone?", nil, false, "message not returned by FETCH")
		case gone:
			step("FETCH — keyword gone?", nil, true, "message back to its original flags")
		default:
			step("FETCH — keyword gone?", nil, false, "keyword still present after cleanup")
		}
		return nil
	})
	if err != nil && res.Err == "" {
		res.Err = err.Error()
	}
	return res
}

// fetchMessageFlags UID-FETCHes the flags of one message. found is false when
// the server did not return that UID (moved or vanished); flags is then nil.
// A returned message with no flags yields found with an empty list.
func fetchMessageFlags(conn *imapclient.Client, uid uint32) (flags []imap.Flag, found bool, err error) {
	var set imap.UIDSet
	set.AddNum(imap.UID(uid))
	msgs, err := conn.Fetch(set, &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil {
		return nil, false, err
	}
	for _, m := range msgs {
		if uint32(m.UID) == uid {
			return m.Flags, true, nil
		}
	}
	return nil, false, nil
}

// keywordCase returns the spelling under which the server stored the keyword,
// or "" when it is absent. IMAP flags compare case-insensitively (RFC 9051
// §2.6) and some servers normalize the case (the in-memory server lowercases).
func keywordCase(flags []imap.Flag, kw imap.Flag) string {
	for _, x := range flags {
		if strings.EqualFold(string(x), string(kw)) {
			return string(x)
		}
	}
	return ""
}

func flagsToStrings(flags []imap.Flag) []string {
	out := make([]string, len(flags))
	for i, f := range flags {
		out[i] = string(f)
	}
	return out
}

// keywordFlags extracts the keywords (non-system flags — everything without
// the backslash prefix, e.g. "ProjectX" or "$Label1") from a flag list.
func keywordFlags(flags []imap.Flag) []string {
	var out []string
	for _, f := range flags {
		if !strings.HasPrefix(string(f), "\\") {
			out = append(out, string(f))
		}
	}
	return out
}
