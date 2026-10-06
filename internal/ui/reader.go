package ui

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/calendar"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/render"
)

// emailLink holds an extracted link from the email body.
type emailLink struct {
	Text string
	URL  string
}

// mdLinkRe matches [text](url) in markdown.
// Uses non-greedy .+? to allow brackets inside link text (e.g. [[text]](url))
var mdLinkRe = regexp.MustCompile(`\[(.+?)\]\((https?://[^)]+)\)`)

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
	if len(links) > 99 {
		links = links[:99]
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

// maxReaderQuoteDepth is the deepest blockquote nesting the reader displays.
// Glamour's blockquote cost grows superlinearly with nesting: a 50 KB reply
// chain quoted 33 levels deep ("> > > > …", Gmail style) took 2.3 s to render
// and 0.2 s capped at 3. Beyond three bars the levels are unreadable in a
// terminal anyway. Display only — the markdown body kept for reply/forward/
// editor/browser is never capped.
const maxReaderQuoteDepth = 3

// capQuoteDepth rewrites every markdown line quoted deeper than max to exactly
// max levels. Adjacent lines whose original depths differ but both exceed max
// get a blank quoted line between them so they stay separate paragraphs
// instead of merging under the now-identical prefix. Fenced code blocks (```
// or ~~~ at the top level) are left untouched.
func capQuoteDepth(md string, max int) string {
	if max < 1 {
		return md
	}
	lines := strings.Split(md, "\n")
	out := make([]string, 0, len(lines)+8)
	cappedPrefix := strings.TrimRight(strings.Repeat("> ", max), " ")
	inFence := false
	prevDeep := 0 // original depth of the previous line when it exceeded max, else 0
	for _, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			out = append(out, line)
			prevDeep = 0
			continue
		}
		if inFence {
			out = append(out, line)
			continue
		}
		depth, rest := splitQuotePrefix(line)
		if depth <= max {
			out = append(out, line)
			prevDeep = 0
			continue
		}
		if prevDeep != 0 && prevDeep != depth {
			out = append(out, cappedPrefix)
		}
		prevDeep = depth
		if rest == "" {
			out = append(out, cappedPrefix)
		} else {
			out = append(out, cappedPrefix+" "+rest)
		}
	}
	return strings.Join(out, "\n")
}

// splitQuotePrefix returns how many '>' markers open the line and the text
// after them (leading spaces around the markers removed).
func splitQuotePrefix(line string) (int, string) {
	depth := 0
	rest := line
	for {
		t := strings.TrimLeft(rest, " ")
		if !strings.HasPrefix(t, ">") {
			if depth == 0 {
				return 0, line
			}
			return depth, t
		}
		depth++
		rest = t[1:]
	}
}

// loadEmailIntoReader renders the email and sets the viewport content.
func loadEmailIntoReader(vp *viewport.Model, email *imap.Email, body string, attachments []imap.Attachment, spyPixels imap.SpyPixelInfo, links []emailLink, theme string, width int) error {
	header := renderEmailHeader(email, attachments, spyPixels, width)

	// Inject link numbers inline before glamour rendering
	numbered := numberLinks(capQuoteDepth(body, maxReaderQuoteDepth), links)

	rendered, err := render.ToANSI(numbered, theme, width)
	if err != nil {
		rendered = body // fall back to raw markdown
	}

	vp.SetContent(header + "\n" + rendered)
	vp.GotoTop()
	return nil
}

func renderEmailHeader(e *imap.Email, attachments []imap.Attachment, spyPixels imap.SpyPixelInfo, width int) string {
	if e == nil {
		return ""
	}

	lines := []string{
		styleFrom.Render("From:    ") + e.From,
		styleDate.Render("To:      ") + e.To,
	}
	if e.CC != "" {
		lines = append(lines, styleDate.Render("Cc:      ")+e.CC)
	}
	if e.BCC != "" {
		lines = append(lines, styleDate.Render("Bcc:     ")+e.BCC)
	}
	// displaySafe collapses complex-script runs to '·' so the rounded-border
	// box doesn't wrap or misalign when the subject contains chars whose
	// terminal-cell width disagrees with runewidth (Bengali, CJK, emoji, …).
	// Then truncate to fit inside the box: border(2) + padding(2) + "Subject: "(9) = 13.
	subjMax := width - 13
	if subjMax < 8 {
		subjMax = 8
	}
	subjVal := truncate(displaySafe(e.Subject), subjMax)
	lines = append(lines,
		styleSubject.Render("Subject: ")+subjVal,
		styleDate.Render("Date:    ")+fmtDateFull(e.Date),
	)

	attachStyle := lipgloss.NewStyle().Foreground(colorSubjectRead) // waveAqua2 — visible but not dominant
	if len(attachments) > 0 {
		var parts []string
		for i, a := range attachments {
			label := fmt.Sprintf("[%d] %s", i+1, a.Filename)
			if s := attachSize(a.Size); s != "" {
				label += " (" + s + ")"
			}
			parts = append(parts, attachStyle.Render(label))
		}
		lines = append(lines, styleDate.Render("Attach:  ")+strings.Join(parts, "  "))
	}

	if spyPixels.Count > 0 {
		spyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("208")) // orange
		label := fmt.Sprintf("%d spy pixel(s) blocked", spyPixels.Count)
		if len(spyPixels.Domains) > 0 {
			label += " (" + strings.Join(spyPixels.Domains, ", ") + ")"
		}
		lines = append(lines, spyStyle.Render("° "+label))
	}

	if card := calendarInviteCard(attachments); card != "" {
		lines = append(lines, card)
	}

	content := strings.Join(lines, "\n")
	return styleEmailMeta.Render(content) + "\n"
}

// attachSize formats an attachment's transfer size for the reader header;
// empty when unknown (the full body fetch does not record sizes).
func attachSize(b uint32) string {
	switch {
	case b == 0:
		return ""
	case b < 1024:
		return fmt.Sprintf("%d B", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.0f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	}
}

// calendarInviteCard renders a one-line summary of an iCalendar invite if one
// is attached. Empty string when no invite is found or parsing fails. The
// reader keeps it succinct because the full event detail is already in the
// email body — this just surfaces the action: "Press <space> v {a|d|t|o}".
func calendarInviteCard(attachments []imap.Attachment) string {
	for i := range attachments {
		if !attachments[i].IsCalendarInvite {
			continue
		}
		ev, err := calendar.Parse(attachments[i].Data)
		if err != nil {
			continue
		}
		when := ev.FormatTime()
		summary := ev.Summary
		if summary == "" {
			summary = "(untitled event)"
		}
		card := fmt.Sprintf("📅 %s", summary)
		if when != "" {
			card += "  ·  " + when
		}
		if ev.Location != "" {
			card += "  ·  " + ev.Location
		}
		card += "  ·  press <space> v {a|d|t|o}"
		cardStyle := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
		return cardStyle.Render(card)
	}
	return ""
}

// readerHelp returns the one-line help string for the reader view.
// When isDraft is true, "E draft" is shown so the user knows they can re-open in compose.
func readerHelp(isDraft bool, hasLinks bool) string {
	keys := []string{"j/k scroll", "h/q back", "r reply", "ctrl+r reply-all", "ctrl+e react", "f fwd", "e nvim"}
	if isDraft {
		keys = append(keys, "E draft")
	}
	keys = append(keys, "o w3m", "O browser", "ctrl+o web", "1-9 attach", "space+d eml")
	if hasLinks {
		keys = append(keys, "space+1-0 links", "space+l11-99 links 11+")
	}
	keys = append(keys, "? help")
	return styleHelp.Render("  " + strings.Join(keys, " · "))
}

// inboxHelp returns the one-line help string for the inbox view.
// moreBelow prepends the load-more cue (see Model.moreBelow).
func inboxHelp(folder string, moreBelow bool) string {
	base := []string{"enter/l open", "d/u page", "r reply", "ctrl+r reply-all", "ctrl+e react", "f fwd", "c compose", "I/O/F/P/A screen", "g goto", "M move", ", sort", "/ filter", "R reload", ": cmds", "space more", "? help", "q quit"}
	if folder == "ToScreen" {
		base = []string{"I approve", "O block", "F feed", "P papertrail", "q back"}
	}
	if moreBelow {
		base = append([]string{"↓ more below (j/d at last row)"}, base...)
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
