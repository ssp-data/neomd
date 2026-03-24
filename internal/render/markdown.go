// Package render handles Markdown rendering for display and email sending.
package render

import (
	"github.com/charmbracelet/glamour"
)

// ToANSI renders markdown as ANSI-styled terminal output for the reader view.
// theme should be "dark", "light", or "auto".
func ToANSI(markdown, theme string) (string, error) {
	if theme == "" {
		theme = "dark"
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylePath(theme),
		glamour.WithWordWrap(100),
	)
	if err != nil {
		// Fall back to notty (no styling)
		return glamour.Render(markdown, "notty")
	}
	return r.Render(markdown)
}
