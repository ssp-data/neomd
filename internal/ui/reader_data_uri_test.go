package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/sspaeti/neomd/internal/imap"
)

// A newsletter that embeds its logo as a 500 KB base64 data: URI used to hang
// the reader (glamour's word-wrap is quadratic in word length). The body itself
// must stay intact — reply/forward, drafts and the `O` browser view use it —
// only the rendered view collapses the URI.
func TestReader_EmbeddedDataURIImageRendersFast(t *testing.T) {
	big := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAAAP0AAAErCAYAAAAVJYV6", 12000)
	body := "Firmware Update Available\n\n![BitBox](data:image/png;base64," + big + ")BitBox\n Firmware Update\n\nDear Customer,\n\n[Update](https://example.com/update)\n"
	links := extractLinks(body)
	vp := newReader(160, 50)
	e := &imap.Email{Subject: "Firmware", From: "a@example.com", To: "b@example.com"}
	start := time.Now()
	if err := loadEmailIntoReader(&vp, e, body, nil, imap.SpyPixelInfo{}, links, "dark", 160); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > 1500*time.Millisecond {
		t.Fatalf("embedded data: image took %v to render; URI not collapsed?", el)
	}
	view := stripANSI(vp.View())
	if !strings.Contains(view, "Image: BitBox") || !strings.Contains(view, "Dear Customer") {
		t.Fatalf("rendered body lost the image placeholder or its text")
	}
	if strings.Contains(view, big[:100]) {
		t.Fatalf("base64 payload leaked into the reader view")
	}
	if len(links) != 1 || links[0].URL != "https://example.com/update" {
		t.Fatalf("link numbering changed: %+v", links)
	}
}
