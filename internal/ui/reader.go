package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/render"
)

// newReader creates a viewport for reading emails.
func newReader(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.Style = styleInputField
	return vp
}

// loadEmailIntoReader renders the email and sets the viewport content.
func loadEmailIntoReader(vp *viewport.Model, email *imap.Email, body, theme string, width int) error {
	header := renderEmailHeader(email, width)

	rendered, err := render.ToANSI(body, theme, width)
	if err != nil {
		rendered = body // fall back to raw markdown
	}

	vp.SetContent(header + "\n" + rendered)
	vp.GotoTop()
	return nil
}

func renderEmailHeader(e *imap.Email, width int) string {
	if e == nil {
		return ""
	}

	lines := []string{
		styleFrom.Render("From:    ") + e.From,
		styleDate.Render("To:      ") + e.To,
		styleSubject.Render("Subject: ") + e.Subject,
		styleDate.Render("Date:    ") + fmtDate(e.Date),
	}

	content := strings.Join(lines, "\n")
	_ = width // box will size itself

	return styleEmailMeta.Render(content) + "\n"
}

// readerHelp returns the one-line help string for the reader view.
func readerHelp() string {
	keys := []string{"j/k scroll", "space/d page", "h/q back", "r reply", "O open in browser", "? help"}
	return styleHelp.Render("  " + strings.Join(keys, " · "))
}

// inboxHelp returns the one-line help string for the inbox view.
func inboxHelp(folder string) string {
	base := []string{"enter/l open", "r reply", "c compose", "I/O/F/P/A screen", "g goto", "M move", "/ filter", "R reload", "? help", "q quit"}
	_ = folder
	if folder == "ToScreen" {
		base = []string{"I approve", "O block", "F feed", "P papertrail", "q back"}
	}
	return styleHelp.Render("  " + strings.Join(base, " · "))
}

// composeHelp returns the one-line help string for the compose view.
func composeHelp(step int) string {
	switch step {
	case 0:
		return styleHelp.Render("  tab next field · enter next field")
	case 1:
		return styleHelp.Render("  enter open editor")
	default:
		return styleHelp.Render("  esc cancel · enter send")
	}
}

// statusBar formats a status/error message for the bottom bar.
func statusBar(msg string, isErr bool) string {
	if isErr {
		return styleError.Render(fmt.Sprintf("  ✗ %s", msg))
	}
	if msg != "" {
		return styleSuccess.Render(fmt.Sprintf("  ✓ %s", msg))
	}
	return ""
}
