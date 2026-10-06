package ui

import (
	"strings"
	"testing"

	"github.com/sspaeti/neomd/internal/imap"
)

func TestKeywordTestVerdict(t *testing.T) {
	tests := []struct {
		name string
		res  *imap.KeywordProbeResult
		want string // substring of the verdict
		good bool
	}{
		{
			name: "keep mode leaves the keyword",
			res: &imap.KeywordProbeResult{
				Kept: true, StoreSupported: true, SearchSupported: true,
			},
			want: "keep mode",
			good: true,
		},
		{
			name: "full support",
			res: &imap.KeywordProbeResult{
				WildcardAllowed: true, StoreSupported: true, SearchSupported: true, CleanupOK: true,
			},
			want: "Full keyword support",
			good: true,
		},
		{
			name: "stores but no wildcard advertised",
			res: &imap.KeywordProbeResult{
				WildcardAllowed: false, StoreSupported: true, SearchSupported: true, CleanupOK: true,
			},
			want: "not advertised",
			good: true,
		},
		{
			name: "store without search",
			res: &imap.KeywordProbeResult{
				StoreSupported: true, SearchSupported: false, CleanupOK: true,
			},
			want: "UID SEARCH KEYWORD does not find",
			good: false,
		},
		{
			name: "store rejected",
			res:  &imap.KeywordProbeResult{},
			want: "rejected",
			good: false,
		},
		{
			name: "fatal before steps",
			res:  &imap.KeywordProbeResult{Err: "SELECT failed"},
			want: "SELECT failed",
			good: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, good := keywordTestVerdict(tt.res)
			if good != tt.good {
				t.Errorf("good = %v, want %v (verdict %q)", good, tt.good, got)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("verdict %q does not contain %q", got, tt.want)
			}
		})
	}
}

func TestKeywordTestVerdictNil(t *testing.T) {
	if got, good := keywordTestVerdict(nil); got != "" || good {
		t.Errorf("nil result: got %q, %v; want empty, false", got, good)
	}
}

func TestViewKeywordTestRenders(t *testing.T) {
	m := Model{width: 120, height: 40}
	m.state = stateKeywordTest
	m.keywordTest.target = keywordTestTarget{account: "acc", host: "imap.example.com:993", folder: "INBOX", uid: 42, subject: "s"}
	m.keywordTest.result = &imap.KeywordProbeResult{
		Folder: "INBOX", UID: 42, Keyword: "NeomdTest",
		PermanentFlags:  []string{"\\Seen", "\\*"},
		WildcardAllowed: true,
		BaselineFlags:   []string{"\\Seen"},
		StoreSupported:  true, SearchSupported: true, CleanupOK: true,
		Steps: []imap.KeywordProbeStep{{Name: "SELECT — PERMANENTFLAGS", OK: true, Detail: "…"}},
	}
	out := m.viewKeywordTest()
	for _, want := range []string{"IMAP Keyword (Tag) Support Test", "Full keyword support", "✓", "esc/q close"} {
		if !strings.Contains(out, want) {
			t.Errorf("dialog render missing %q", want)
		}
	}

	// Running state renders the spinner line without touching the result.
	m2 := Model{width: 120, height: 40}
	m2.state = stateKeywordTest
	m2.keywordTest.running = true
	m2.keywordTest.target = m.keywordTest.target
	out2 := m2.viewKeywordTest()
	if !strings.Contains(out2, "Probing imap.example.com") {
		t.Errorf("running render missing probe line: %q", out2)
	}
}
