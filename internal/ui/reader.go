package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/render"
)

// emailLink holds an extracted link from the email body.
type emailLink struct {
	Text string
	URL  string
}

// mdLinkRe matches [text](url) in markdown.
var mdLinkRe = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)

// extractLinks pulls all [text](url) links from markdown, deduplicating by URL.
func extractLinks(markdown string) []emailLink {
	matches := mdLinkRe.FindAllStringSubmatch(markdown, -1)
	seen := make(map[string]bool)
	var links []emailLink
	for _, m := range matches {
		if len(m) < 3 {
			continue
		}
		url := m[2]
		if seen[url] {
			continue
		}
		seen[url] = true
		text := m[1]
		if len(text) > 40 {
			text = text[:37] + "..."
		}
		links = append(links, emailLink{Text: text, URL: url})
	}
	if len(links) > 10 {
		links = links[:10]
	}
	return links
}

// newReader creates a viewport for reading emails.
func newReader(width, height int) viewport.Model {
	vp := viewport.New(width, height)
	vp.Style = styleInputField
	return vp
}

// numberLinks replaces [text](url) in markdown with [text [N]](url) so glamour
// renders the link number inline where the link appears in the body.
func numberLinks(body string, links []emailLink) string {
	if len(links) == 0 {
		return body
	}
	// Build URL → number map
	urlToNum := make(map[string]int, len(links))
	for i, l := range links {
		n := i + 1
		if n == 10 {
			n = 0
		}
		urlToNum[l.URL] = n
	}
	return mdLinkRe.ReplaceAllStringFunc(body, func(m string) string {
		parts := mdLinkRe.FindStringSubmatch(m)
		if len(parts) < 3 {
			return m
		}
		text, url := parts[1], parts[2]
		if n, ok := urlToNum[url]; ok {
			return fmt.Sprintf("[%s [%d]](%s)", text, n, url)
		}
		return m
	})
}

// loadEmailIntoReader renders the email and sets the viewport content.
func loadEmailIntoReader(vp *viewport.Model, email *imap.Email, body string, attachments []imap.Attachment, links []emailLink, theme string, width int) error {
	header := renderEmailHeader(email, attachments, width)

	// Inject link numbers inline before glamour rendering
	numbered := numberLinks(body, links)

	rendered, err := render.ToANSI(numbered, theme, width)
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
		styleDate.Render("Date:    ") + fmtDateFull(e.Date),
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
// When isDraft is true, "E draft" is shown so the user knows they can re-open in compose.
func readerHelp(isDraft bool, hasLinks bool) string {
	keys := []string{"j/k scroll", "h/q back", "r reply", "ctrl+r reply-all", "ctrl+e react", "f fwd", "e nvim"}
	if isDraft {
		keys = append(keys, "E draft")
	}
	keys = append(keys, "o w3m", "O browser", "ctrl+o web", "1-9 attach")
	if hasLinks {
		keys = append(keys, "space+1-9 links")
	}
	keys = append(keys, "? help")
	return styleHelp.Render("  " + strings.Join(keys, " · "))
}

// inboxHelp returns the one-line help string for the inbox view.
func inboxHelp(folder string) string {
	base := []string{"enter/l open", "d/u page", "r reply", "ctrl+r reply-all", "ctrl+e react", "f fwd", "c compose", "I/O/F/P/A screen", "g goto", "M move", ", sort", "/ filter", "R reload", ": cmds", "space more", "? help", "q quit"}
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
		return styleHelp.Render("  tab next · shift+tab prev · ctrl+b hide Cc/Bcc · ctrl+t attach" + fromHint + " · esc cancel")
	default: // stepSubject
		return styleHelp.Render("  enter open editor · shift+tab prev · ctrl+t attach · D remove last" + fromHint + " · esc cancel")
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
