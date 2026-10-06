package imap

import (
	"context"
	"strings"
	"testing"

	goimap "github.com/emersion/go-imap/v2"
)

func TestKeywordProbe_FullRoundTripOnMemserver(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "kw-probe", true)

	res := cli.KeywordProbe(context.Background(), "INBOX", 1, "NeomdTest", false)

	// The in-memory server advertises \* and stores arbitrary flags, so the
	// full save → search → cleanup round trip must pass.
	if res.Err != "" {
		t.Fatalf("probe error: %s", res.Err)
	}
	if !res.WildcardAllowed {
		t.Errorf("WildcardAllowed = false; memserver advertises \\* (permanent: %v)", res.PermanentFlags)
	}
	if !res.StoreSupported {
		t.Errorf("StoreSupported = false; steps: %+v", res.Steps)
	}
	if !res.SearchSupported {
		t.Errorf("SearchSupported = false; steps: %+v", res.Steps)
	}
	if !res.CleanupOK {
		t.Errorf("CleanupOK = false; steps: %+v", res.Steps)
	}
	if len(res.Steps) != 7 {
		t.Errorf("len(Steps) = %d, want 7", len(res.Steps))
	}
	for i, s := range res.Steps {
		if !s.OK {
			t.Errorf("step %d (%s) failed: %s %s", i, s.Name, s.Err, s.Detail)
		}
	}
	// The seeded message was \Seen; the probe must leave exactly that behind.
	if len(res.BaselineFlags) != 1 || res.BaselineFlags[0] != string(goimap.FlagSeen) {
		t.Errorf("BaselineFlags = %v, want [\\Seen]", res.BaselineFlags)
	}
}

func TestKeywordProbe_LeavesOriginalFlagsUntouched(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "kw-keep", false)

	res := cli.KeywordProbe(context.Background(), "INBOX", 1, "NeomdTest", false)
	if !res.CleanupOK {
		t.Fatalf("cleanup failed: %+v", res.Steps)
	}
	flags, err := fetchMessageFlagsOnClient(cli, "INBOX", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(flags) != 0 {
		t.Errorf("flags after probe = %v, want none (message was seeded without flags)", flags)
	}
}

func TestKeywordProbe_MissingMessageReportsGracefully(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "only-one", true)

	// UID 99 does not exist; every step must still run and report, not panic.
	res := cli.KeywordProbe(context.Background(), "INBOX", 99, "NeomdTest", false)
	if res.Err != "" {
		t.Fatalf("unexpected fatal error: %s", res.Err)
	}
	if len(res.Steps) != 7 {
		t.Fatalf("len(Steps) = %d, want 7 (steps must continue past failures)", len(res.Steps))
	}
	if res.StoreSupported || res.SearchSupported || res.CleanupOK {
		t.Errorf("unsupported message reported as supported: %+v", res)
	}
	if res.Steps[1].OK || res.Steps[1].Detail == "" {
		t.Errorf("baseline step should report the missing message: %+v", res.Steps[1])
	}
}

func TestKeywordProbe_KeepLeavesKeywordForNextRun(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "kw-keepmode", true)

	kept := cli.KeywordProbe(context.Background(), "INBOX", 1, "NeomdTest", true)
	if !kept.Kept || kept.CleanupOK {
		t.Fatalf("keep mode: Kept=%v CleanupOK=%v; steps: %+v", kept.Kept, kept.CleanupOK, kept.Steps)
	}
	if len(kept.Steps) != 6 {
		t.Fatalf("keep mode len(Steps) = %d, want 6 (no cleanup steps)", len(kept.Steps))
	}

	// A later normal run must see the kept keyword in the server-side
	// baseline flags and remove it again.
	again := cli.KeywordProbe(context.Background(), "INBOX", 1, "NeomdTest", false)
	foundKept := false
	for _, f := range again.BaselineFlags {
		if strings.EqualFold(f, "NeomdTest") {
			foundKept = true
		}
	}
	if !foundKept {
		t.Errorf("normal run did not see the kept keyword in baseline flags: %v", again.BaselineFlags)
	}
	if !again.CleanupOK {
		t.Errorf("normal run did not clean up the kept keyword: %+v", again.Steps)
	}
}

// fetchMessageFlagsOnClient re-selects and FETCHes via the public path so the
// test observes the true post-probe server state, not a probe-internal view.
func fetchMessageFlagsOnClient(cli *Client, folder string, uid uint32) ([]string, error) {
	emails, err := cli.FetchHeaders(context.Background(), folder, 0)
	if err != nil {
		return nil, err
	}
	for _, e := range emails {
		if e.UID == uid {
			var out []string
			if e.Seen {
				out = append(out, "\\Seen")
			}
			if e.Answered {
				out = append(out, "\\Answered")
			}
			if e.Flagged {
				out = append(out, "\\Flagged")
			}
			return out, nil
		}
	}
	return nil, nil
}
