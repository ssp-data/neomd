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
func loadEmailIntoReader(vp *viewport.Model, email *imap.Email, body string, attachments []imap.Attachment, theme string, width int) error {
	header := renderEmailHeader(email, attachments, width)

	rendered, err := render.ToANSI(body, theme, width)
	if err != nil {
		rendered = body // fall back to raw markdown
	}

	vp.SetContent(header + "\n" + rendered)
	vp.GotoTop()
	return nil
}

func renderEmailHeader(e *imap.Email, attachments []imap.Attachment, width int) string {
	if e == nil {
		return ""
	}

	lines := []string{
		styleFrom.Render("From:    ") + e.From,
		styleDate.Render("To:      ") + e.To,
		styleSubject.Render("Subject: ") + e.Subject,
		styleDate.Render("Date:    ") + fmtDate(e.Date),
	}

	if len(attachments) > 0 {
		var parts []string
		for i, a := range attachments {
			parts = append(parts, fmt.Sprintf("[%d] %s", i+1, a.Filename))
		}
		lines = append(lines, styleHelp.Render("Attach:  ")+strings.Join(parts, "  "))
	}

	content := strings.Join(lines, "\n")
	_ = width

	return styleEmailMeta.Render(content) + "\n"
}

// readerHelp returns the one-line help string for the reader view.
func readerHelp() string {
	keys := []string{"j/k scroll", "space/d page", "h/q back", "r reply", "e nvim", "o w3m", "O browser", "ctrl+o web", "1-9 attachment", "? help"}
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
func composeHelp(step int, hasSenders bool) string {
	fromHint := ""
	if hasSenders {
		fromHint = " · ctrl+f cycle from"
	}
	switch step {
	case 0: // stepTo
		return styleHelp.Render("  tab/enter next · ctrl+b toggle Cc/Bcc · ctrl+t attach" + fromHint + " · esc cancel")
	case 1, 2: // stepCC, stepBCC
		return styleHelp.Render("  tab/enter next (optional) · ctrl+b hide Cc/Bcc · ctrl+t attach" + fromHint + " · esc cancel")
	default: // stepSubject
		return styleHelp.Render("  enter open editor · ctrl+t attach · D remove last" + fromHint + " · esc cancel")
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
