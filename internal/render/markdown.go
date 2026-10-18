// Package render handles Markdown rendering for display and email sending.
package render

import (
	"regexp"

	"github.com/charmbracelet/glamour"
)

// dataURIDestRe matches a markdown link/image destination that is a data: URI,
// e.g. `](data:image/png;base64,iVBOR…)` or `](<data:image/png;base64,…>)`, with
// an optional trailing title. Group 1 is the media type (and base64 marker)
// kept in the placeholder.
var dataURIDestRe = regexp.MustCompile(`\]\(<?(data:[A-Za-z0-9.+/-]+(?:;base64)?)[^)]*\)`)

// CollapseDataURIs shortens inline data: URI destinations to
// `](data:image/png;base64,…)` for terminal display. Emails that embed their
// pictures as base64 data: URIs put a single 100 KB–1 MB "word" on one line,
// and glamour's paragraph word-wrap is quadratic in word length (a 464 KB
// logo never finished rendering). Glamour cannot show the picture anyway; the
// placeholder keeps its usual `Image: alt → dest` look. Display only — the
// stored body (reply/forward, drafts, `O` browser view) is never collapsed.
func CollapseDataURIs(md string) string {
	return dataURIDestRe.ReplaceAllString(md, "]($1,…)")
}

// ToANSI renders markdown as ANSI-styled terminal output for the reader view.
// theme should be "dark", "light", or "auto". width is the terminal column
// count; pass 0 to use the default (80).
func ToANSI(markdown, theme string, width int) (string, error) {
	if theme == "" {
		theme = "dark"
	}
	if width <= 0 {
		width = 80
	}
	markdown = CollapseDataURIs(markdown)
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(theme),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return glamour.Render(markdown, "notty")
	}
	return r.Render(markdown)
}
