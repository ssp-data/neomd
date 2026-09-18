package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sspaeti/neomd/internal/imap"
)

// Quoting a mail whose HTML embeds images as <img src="cid:..."> used to carry
// dangling cid: references into the reply (the parts were never re-attached),
// so quoted images broke — or, with colliding Content-IDs, showed the wrong
// picture. materializeInlineImages writes the original's inline parts to disk
// and points the quoted markdown at those files, so the sender's normal
// local-image pass re-embeds them under fresh Content-IDs.
func TestMaterializeInlineImages_RewritesCIDRefsToFiles(t *testing.T) {
	dir := t.TempDir()
	pngBytes := []byte("\x89PNG\r\n\x1a\nfake")
	atts := []imap.Attachment{
		{Filename: "chart.png", ContentType: "image/png", ContentID: "abc@example", Data: pngBytes},
		{Filename: "report.pdf", ContentType: "application/pdf", Data: []byte("%PDF")}, // no Content-ID → untouched
		{Filename: "unused.png", ContentType: "image/png", ContentID: "zzz@example", Data: pngBytes},
	}
	body := "> hello\n> ![chart.png](cid:abc@example)\n> <img src=\"cid:abc@example\">\n> ![other](cid:missing@example)\n"

	got := materializeInlineImages(body, atts, dir)

	want := filepath.Join(dir, "chart.png")
	if strings.Contains(got, "cid:abc@example") {
		t.Errorf("cid ref not rewritten:\n%s", got)
	}
	if !strings.Contains(got, "("+want+")") || !strings.Contains(got, `src="`+want+`"`) {
		t.Errorf("expected both markdown and html refs to point at %s, got:\n%s", want, got)
	}
	data, err := os.ReadFile(want)
	if err != nil || string(data) != string(pngBytes) {
		t.Errorf("image file not written with original bytes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "unused.png")); !os.IsNotExist(err) {
		t.Error("attachment not referenced by the body must not be written")
	}
	if !strings.Contains(got, "(cid:missing@example)") {
		t.Error("a cid with no matching part must be left as-is")
	}
}

func TestMaterializeInlineImages_SafeFilenames(t *testing.T) {
	dir := t.TempDir()
	atts := []imap.Attachment{
		{Filename: "../../evil.png", ContentType: "image/png", ContentID: "a@x", Data: []byte("x")},
		{Filename: "", ContentType: "image/jpeg", ContentID: "b@x", Data: []byte("y")},
	}
	got := materializeInlineImages("![](cid:a@x) ![](cid:b@x)", atts, dir)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("expected 2 files inside %s, got %d", dir, len(entries))
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "..") || strings.ContainsAny(e.Name(), `/\`) {
			t.Errorf("unsafe filename written: %q", e.Name())
		}
		if !strings.Contains(got, filepath.Join(dir, e.Name())) {
			t.Errorf("body does not reference %s:\n%s", e.Name(), got)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "evil.png")); err == nil {
		t.Error("path traversal: file written outside dir")
	}
}

func TestMaterializeInlineImages_NoInlineParts(t *testing.T) {
	body := "plain body"
	if got := materializeInlineImages(body, nil, t.TempDir()); got != body {
		t.Errorf("body changed with no attachments: %q", got)
	}
}
