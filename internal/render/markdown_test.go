package render

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func TestCollapseDataURIs(t *testing.T) {
	big := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAA", 2000)
	cases := []struct{ name, in, want string }{
		{"png image", "![BitBox](data:image/png;base64," + big + ")BitBox", "![BitBox](data:image/png;base64,…)BitBox"},
		{"image with size title", "![logo](data:image/jpeg;base64," + big + " \"70x70\")", "![logo](data:image/jpeg;base64,…)"},
		{"angle-bracket dest", "![x](<data:image/gif;base64," + big + ">)", "![x](data:image/gif;base64,…)"},
		{"link form", "[download](data:text/plain;base64,SGVsbG8=)", "[download](data:text/plain;base64,…)"},
		{"svg utf8 not base64", "![v](data:image/svg+xml;utf8,<svg/>)", "![v](data:image/svg+xml,…)"},
		{"https image untouched", "![BitBox](https://example.com/logo.png \"70x70\")", "![BitBox](https://example.com/logo.png \"70x70\")"},
		{"cid image untouched", "![a](cid:part1@neomd)", "![a](cid:part1@neomd)"},
		{"prose mentioning data: untouched", "see data:image/png in the spec (data:foo)", "see data:image/png in the spec (data:foo)"},
	}
	for _, c := range cases {
		if got := CollapseDataURIs(c.in); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// A 500 KB base64 data: image on one line made glamour's paragraph word-wrap
// go quadratic (never returned); the reader must render it in well under a second.
func TestToANSI_EmbeddedDataURIImageRendersFast(t *testing.T) {
	big := strings.Repeat("iVBORw0KGgoAAAANSUhEUgAAAP0AAAErCAYAAAAVJYV6", 12000) // ~500 KB
	md := "Firmware Update Available\n\n![BitBox](data:image/png;base64," + big + ")BitBox\n Firmware Update\n\nDear Customer,\n"
	start := time.Now()
	out, err := ToANSI(md, "dark", 120)
	if err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el > time.Second {
		t.Fatalf("embedded data: image took %v to render; data URI not collapsed?", el)
	}
	plain := ansiRe.ReplaceAllString(out, "")
	if !strings.Contains(plain, "Image: BitBox → data:image/png;base64,…") || !strings.Contains(plain, "Dear Customer") {
		t.Fatalf("rendered output lost the image placeholder or the text:\n%.400s", plain)
	}
	if strings.Contains(plain, big[:200]) {
		t.Fatalf("base64 payload leaked into rendered output")
	}
}
