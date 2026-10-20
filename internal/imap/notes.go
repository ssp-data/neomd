package imap

// Private per-email notes (space n) live as small "sidecar" messages in a
// dedicated folder (see internal/notes): one text/markdown message per note
// whose X-Neomd-Note-For header names the email's Message-ID. The email
// itself is never touched — editing a note APPENDs a fresh sidecar and
// expunges the old one, so only the sidecar's UID changes. All three
// operations resolve "/" in the configured folder name to the server's
// hierarchy delimiter (Dovecot with "." separators, Gmail with "/").

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/sspaeti/neomd/internal/notes"
)

// hierarchyDelim returns the server's mailbox hierarchy delimiter (one LIST
// "" "" round trip, cached for the connection's lifetime). 0 = unknown.
func (c *Client) hierarchyDelim(conn *imapclient.Client) rune {
	if c.hierDelimKnown {
		return c.hierDelim
	}
	data, err := conn.List("", "", nil).Collect()
	if err == nil && len(data) > 0 {
		c.hierDelim = data[0].Delim
	}
	c.hierDelimKnown = true
	return c.hierDelim
}

// resolveFolder maps a config folder name with "/" separators to the
// server's own delimiter. Names without "/" pass through untouched.
func (c *Client) resolveFolder(conn *imapclient.Client, folder string) string {
	if !strings.Contains(folder, "/") {
		return folder
	}
	d := c.hierarchyDelim(conn)
	if d == 0 || d == '/' {
		return folder
	}
	return strings.ReplaceAll(folder, "/", string(d))
}

// FetchNotes returns every note in folder (UID SEARCH ALL + one FETCH of the
// full, small bodies — PEEK, so nothing changes). A folder that does not
// exist yet yields no notes and no error: it is created on the first save.
// Read-only ⇒ withConnRetry.
func (c *Client) FetchNotes(ctx context.Context, folder string) ([]notes.Note, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer trace(time.Now(), "FetchNotes %s", folder)
	var out []notes.Note
	err := c.withConnRetry(ctx, func(conn *imapclient.Client) error {
		out = nil
		name := c.resolveFolder(conn, folder)
		if err := c.selectMailbox(name); err != nil {
			if isNetErr(err) {
				return err
			}
			return nil // no folder yet (NONEXISTENT / TRYCREATE / NO) ⇒ no notes
		}
		searchData, err := conn.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return fmt.Errorf("UID SEARCH: %w", err)
		}
		uidSet, ok := searchData.All.(imap.UIDSet)
		if !ok {
			return nil
		}
		if nums, _ := uidSet.Nums(); len(nums) == 0 {
			return nil
		}
		msgs, err := conn.Fetch(uidSet, &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{Peek: true}},
		}).Collect()
		if err != nil {
			return fmt.Errorf("FETCH notes: %w", err)
		}
		for _, m := range msgs {
			if len(m.BodySection) == 0 {
				continue
			}
			n, ok := notes.Parse(m.BodySection[0].Bytes)
			if !ok {
				continue // foreign message in the folder: not a note
			}
			n.UID = uint32(m.UID)
			out = append(out, n)
		}
		return nil
	})
	return out, err
}

// SaveNote APPENDs raw (a notes.Build sidecar) to folder — creating and
// subscribing the folder on first use — and, when replaceUID is set,
// expunges that previous sidecar afterwards (append first, so a failure
// never loses the old note). Returns the new sidecar's UID (0 when the
// server lacks UIDPLUS; the next FetchNotes fills it in). Mutating ⇒ withConn.
func (c *Client) SaveNote(ctx context.Context, folder string, raw []byte, replaceUID uint32) (uint32, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer trace(time.Now(), "SaveNote %s replace=%d", folder, replaceUID)
	var newUID uint32
	err := c.withConn(ctx, func(conn *imapclient.Client) error {
		name := c.resolveFolder(conn, folder)
		if err := ensureFolder(conn, name); err != nil {
			return err
		}
		cmd := conn.Append(name, int64(len(raw)), &imap.AppendOptions{
			Flags: []imap.Flag{imap.FlagSeen},
			Time:  time.Now(),
		})
		if _, err := cmd.Write(raw); err != nil {
			return fmt.Errorf("APPEND write: %w", err)
		}
		if err := cmd.Close(); err != nil {
			return fmt.Errorf("APPEND close: %w", err)
		}
		data, err := cmd.Wait()
		if err != nil {
			return fmt.Errorf("APPEND wait: %w", err)
		}
		if data != nil {
			newUID = uint32(data.UID)
		}
		audit("NOTE APPEND %s uid=%d replaces=%d", name, newUID, replaceUID)
		if replaceUID != 0 && replaceUID != newUID {
			return c.expungeUIDs(conn, name, []uint32{replaceUID})
		}
		return nil
	})
	return newUID, err
}

// DeleteNote expunges the sidecar uid from folder. Mutating ⇒ withConn.
func (c *Client) DeleteNote(ctx context.Context, folder string, uid uint32) error {
	if ctx == nil {
		ctx = context.Background()
	}
	defer trace(time.Now(), "DeleteNote %s uid=%d", folder, uid)
	return c.withConn(ctx, func(conn *imapclient.Client) error {
		name := c.resolveFolder(conn, folder)
		return c.expungeUIDs(conn, name, []uint32{uid})
	})
}

// ensureFolder CREATEs name (ALREADYEXISTS is fine) and subscribes it.
func ensureFolder(conn *imapclient.Client, name string) error {
	if err := conn.Create(name, nil).Wait(); err != nil {
		var imapErr *imap.Error
		if !errors.As(err, &imapErr) || imapErr.Code != imap.ResponseCodeAlreadyExists {
			// Some servers answer a plain NO for an existing mailbox: try
			// selecting before giving up.
			if _, selErr := conn.Select(name, nil).Wait(); selErr != nil {
				return fmt.Errorf("CREATE %s: %w", name, err)
			}
			return nil
		}
	}
	_ = conn.Subscribe(name).Wait()
	return nil
}

// expungeUIDs marks uids \Deleted in folder and UID EXPUNGEs exactly them
// (the ExpungeAll contract, on an already-held connection).
func (c *Client) expungeUIDs(conn *imapclient.Client, folder string, uids []uint32) error {
	if err := c.selectMailbox(folder); err != nil {
		return err
	}
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	if err := conn.Store(set, &imap.StoreFlags{Op: imap.StoreFlagsAdd, Silent: true, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		return fmt.Errorf("STORE \\Deleted: %w", err)
	}
	if _, err := conn.UIDExpunge(set).Collect(); err != nil {
		return fmt.Errorf("UID EXPUNGE: %w", err)
	}
	audit("NOTE EXPUNGE %s uids=%v", folder, uids)
	return nil
}
