// Package render handles Markdown rendering for display and email sending.
package render

import (
	"github.com/charmbracelet/glamour"
)

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
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(theme),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return glamour.Render(markdown, "notty")
	}
	return r.Render(markdown)
}
