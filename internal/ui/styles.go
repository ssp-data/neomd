package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorPrimary  = lipgloss.Color("#ff5d62") // warm red, from newsletter theme
	colorMuted    = lipgloss.Color("#6c7086")
	colorSubtle   = lipgloss.Color("#313244")
	colorText     = lipgloss.Color("#cdd6f4")
	colorUnread   = lipgloss.Color("#cba6f7") // lavender for unread
	colorBg       = lipgloss.Color("#1e1e2e")
	colorBorder   = lipgloss.Color("#45475a")
	colorSelected = lipgloss.Color("#585b70")

	styleHeader = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			Padding(0, 1)

	styleFolder = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	styleStatus = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	styleError = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#f38ba8")).
			Padding(0, 1)

	styleEmailMeta = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1).
			MarginBottom(1)

	styleFrom = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true)

	styleSubject = lipgloss.NewStyle().
			Foreground(colorText).
			Bold(true)

	styleDate = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleUnread = lipgloss.NewStyle().
			Foreground(colorUnread).
			Bold(true)

	styleRead = lipgloss.NewStyle().
			Foreground(colorMuted)

	styleSelected = lipgloss.NewStyle().
			Background(colorSelected).
			Foreground(colorText)

	styleHelp = lipgloss.NewStyle().
			Foreground(colorMuted).
			Padding(0, 1)

	styleSeparator = lipgloss.NewStyle().
			Foreground(colorBorder)

	styleInputLabel = lipgloss.NewStyle().
			Foreground(colorPrimary).
			Bold(true).
			Width(10)

	styleInputField = lipgloss.NewStyle().
			Foreground(colorText)

	styleSuccess = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#a6e3a1"))
)

// folderTabs renders the folder switcher bar.
func folderTabs(folders []string, active string) string {
	var tabs []string
	for _, f := range folders {
		if f == active {
			tabs = append(tabs, styleHeader.Render(f))
		} else {
			tabs = append(tabs, styleFolder.Render(f))
		}
	}
	sep := styleSeparator.Render(" │ ")
	result := ""
	for i, t := range tabs {
		if i > 0 {
			result += sep
		}
		result += t
	}
	return result
}
