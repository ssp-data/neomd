package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// asciiLogo returns the neomd ASCII art logo styled with the given color
func asciiLogo(fg lipgloss.TerminalColor) string {
	logo := lipgloss.NewStyle().Foreground(fg)
	return logo.Render(`
  ███╗   ██╗███████╗ ██████╗ ███╗   ███╗██████╗
  ████╗  ██║██╔════╝██╔═══██╗████╗ ████║██╔══██╗
  ██╔██╗ ██║█████╗  ██║   ██║██╔████╔██║██║  ██║
  ██║╚██╗██║██╔══╝  ██║   ██║██║╚██╔╝██║██║  ██║
  ██║ ╚████║███████╗╚██████╔╝██║ ╚═╝ ██║██████╔╝
  ╚═╝  ╚═══╝╚══════╝ ╚═════╝ ╚═╝     ╚═╝╚═════╝ `)
}

// asciiLogoCompact returns a smaller version for constrained spaces
func asciiLogoCompact(fg lipgloss.TerminalColor) string {
	logo := lipgloss.NewStyle().Foreground(fg)
	return logo.Render(`
  ███╗   ██╗███████╗ ██████╗ ███╗   ███╗██████╗
  ████╗  ██║██╔════╝██╔═══██╗████╗ ████║██╔══██╗
  ██╔██╗ ██║█████╗  ██║   ██║██╔████╔██║██║  ██║
  ██║╚██╗██║██╔══╝  ██║   ██║██║╚██╔╝██║██║  ██║
  ██║ ╚████║███████╗╚██████╔╝██║ ╚═╝ ██║██████╔╝
  ╚═╝  ╚═══╝╚══════╝ ╚═════╝ ╚═╝     ╚═╝╚═════╝`)
}

func (m Model) viewWelcome() string {
	boxWidth := 100
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(1, 2).
		Width(boxWidth)

	title := lipgloss.NewStyle().Foreground(colorPrimary).Bold(true)
	key := lipgloss.NewStyle().Foreground(colorAuthorUnread).Bold(true)
	dim := lipgloss.NewStyle().Foreground(colorDateCol)
	warn := lipgloss.NewStyle().Foreground(colorError)

	subtitle := dim.Render("                     terminal email, reimagined                     ")

	// Left column - Philosophy
	leftCol := title.Render("Philosophy") + "\n\n" +
		"Your IMAP folders have been created.\n\n" +
		"The Screener is a HEY-style workflow:\n" +
		"You decide who reaches your Inbox.\n\n" +
		dim.Render("Your screener lists are empty, so") + "\n" +
		warn.Render("auto-screening is paused") + dim.Render(" until you") + "\n" +
		dim.Render("classify your first senders.") + "\n\n" +
		dim.Render("Once classified, senders are") + "\n" +
		dim.Render("remembered forever. New emails") + "\n" +
		dim.Render("auto-sort on every load.") + "\n\n" +
		title.Render("Getting Started") + "\n\n" +
		"1. Navigate to " + key.Render("ToScreen") + " tab\n" +
		"2. Screen each sender:\n" +
		"   " + key.Render("I") + " screen in  " + dim.Render("→ Inbox") + "\n" +
		"   " + key.Render("O") + " screen out " + dim.Render("→ ScreenedOut") + "\n" +
		"   " + key.Render("F") + " feed       " + dim.Render("→ Feed") + "\n" +
		"   " + key.Render("P") + " papertrail " + dim.Render("→ PaperTrail") + "\n" +
		"3. Use " + key.Render("m") + " to mark multiple\n" +
		"4. Try " + key.Render(":screen-all") + " for full scan"

	// Right column - Essential shortcuts
	rightCol := title.Render("Essential Shortcuts") + "\n\n" +
		title.Render("Navigation") + "\n" +
		key.Render("  j/k") + "      move up/down\n" +
		key.Render("  enter") + "    open email\n" +
		key.Render("  ] / [") + "    next/prev tab\n" +
		key.Render("  gi") + "       go to Inbox\n" +
		key.Render("  gk") + "       go to ToScreen\n" +
		key.Render("  space+1-9") + " jump to tab 1-9\n\n" +
		title.Render("Email Actions") + "\n" +
		key.Render("  c") + "        compose\n" +
		key.Render("  r") + "        reply\n" +
		key.Render("  ctrl+r") + "   reply-all\n" +
		key.Render("  f") + "        forward\n" +
		key.Render("  ctrl+e") + "   emoji reaction\n" +
		key.Render("  n") + "        toggle read/unread\n\n" +
		title.Render("Power User") + "\n" +
		key.Render("  /") + "        filter emails\n" +
		key.Render("  space+/") + "  IMAP search\n" +
		key.Render("  T") + "        thread view\n" +
		key.Render("  ?") + "        all keybindings\n" +
		key.Render("  :debug") + "   diagnostics\n"

	// Create two columns side by side
	leftStyle := lipgloss.NewStyle().Width(46).Align(lipgloss.Left)
	rightStyle := lipgloss.NewStyle().Width(46).Align(lipgloss.Left).PaddingLeft(2)

	columns := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftStyle.Render(leftCol),
		rightStyle.Render(rightCol),
	)

	content := asciiLogo(colorPrimary) + "\n" + subtitle + "\n\n" + columns + "\n\n" +
		dim.Render("                          Press any key to continue                          ")

	rendered := box.Render(content)

	// Center vertically and horizontally
	lines := strings.Count(rendered, "\n") + 1
	padTop := (m.height - lines) / 2
	if padTop < 0 {
		padTop = 0
	}
	padLeft := (m.width - boxWidth) / 2
	if padLeft < 0 {
		padLeft = 0
	}
	prefix := strings.Repeat(" ", padLeft)
	var b strings.Builder
	for i := 0; i < padTop; i++ {
		b.WriteByte('\n')
	}
	for _, line := range strings.Split(rendered, "\n") {
		b.WriteString(prefix + line + "\n")
	}
	return b.String()
}
