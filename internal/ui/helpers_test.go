package ui

import (
	"os"
	"testing"
	"time"
)

// waitForFile blocks until path exists on disk. Tests that trigger async
// persistence (safeGo → Save) into a t.TempDir must call this before
// returning: the test's TempDir cleanup otherwise races the goroutine and
// fails with "unlinkat … directory not empty" (flaky under -count / -race).
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatalf("%s never landed on disk", path)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestExtractHTMLSignatureMarker_SigAlias pins that the short `[sig]` alias
// behaves exactly like `[html-signature]`: it enables the HTML signature and
// is stripped from the body, while mid-line or unrelated brackets are kept.
func TestExtractHTMLSignatureMarker_SigAlias(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    bool
		wantOut string
	}{
		{"long marker", "Hi\n[html-signature]\nBye", true, "Hi\nBye"},
		{"short alias", "Hi\n[sig]\nBye", true, "Hi\nBye"},
		{"alias with whitespace", "Hi\n  [sig]  \nBye", true, "Hi\nBye"},
		{"both markers", "[sig]\nHi\n[html-signature]", true, "Hi"},
		{"no marker", "Hi\nBye", false, "Hi\nBye"},
		{"inline bracket kept", "see [sig] here", false, "see [sig] here"},
		{"similar word kept", "[signature]", false, "[signature]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, out := extractHTMLSignatureMarker(tc.body)
			if got != tc.want || out != tc.wantOut {
				t.Errorf("extractHTMLSignatureMarker(%q) = (%v, %q), want (%v, %q)", tc.body, got, out, tc.want, tc.wantOut)
			}
		})
	}
}
