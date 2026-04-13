package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// reactionEmoji represents a single emoji reaction option.
type reactionEmoji struct {
	emoji string
	label string
}

// defaultReactions is the list of emoji reactions available to the user.
var defaultReactions = []reactionEmoji{
	{"👍", "Thumbs up"},
	{"❤️", "Love"},
	{"😂", "Laugh"},
	{"🎉", "Celebrate"},
	{"🙏", "Thanks"},
	{"💯", "Perfect"},
	{"👀", "Eyes"},
	{"✅", "Check"},
}

// viewReaction renders the emoji picker overlay.
func (m Model) viewReaction() string {
	if m.reactionEmail == nil {
		return "no email selected"
	}

	// Header: subject and from
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#7E9CD8")).
		Render("React to: " + truncate(m.reactionEmail.Subject, 40))

	from := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#727169")).
		Render("From: " + m.reactionEmail.From)

	// Emoji list
	var items []string
	for i, r := range defaultReactions {
		style := lipgloss.NewStyle()
		if i == m.reactionSelected {
			style = style.Background(lipgloss.Color("#2D4F67"))
		}

		line := fmt.Sprintf("  %d  %s  %s", i+1, r.emoji, r.label)
		items = append(items, style.Render(line))
	}

	help := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#727169")).
		Render("Press 1-8 or j/k + enter • esc cancel")

	// Combine all elements
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		from,
		"",
		strings.Join(items, "\n"),
		"",
		help,
	)

	// Box around the content
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#54546D")).
		Padding(1, 2).
		Width(50).
		Render(content)

	// Center the box
	return lipgloss.Place(
		m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		box,
	)
}
