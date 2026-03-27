package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Kanagawa palette — https://github.com/rebelot/kanagawa.nvim
var (
	// ── Base chrome ─────────────────────────────────────────────────────────
	colorBg       = lipgloss.Color("#1F1F28") // sumiInk1  — default background
	colorBorder   = lipgloss.Color("#54546D") // sumiInk4  — borders, float edges
	colorSubtle   = lipgloss.Color("#363646") // sumiInk3  — cursorline
	colorSelected = lipgloss.Color("#223249") // waveBlue1 — visual selection
	colorText     = lipgloss.Color("#DCD7BA") // fujiWhite — default foreground
	colorMuted    = lipgloss.Color("#727169") // fujiGray  — comments, dim text

	// ── Primary accent (header, active tab) ─────────────────────────────────
	colorPrimary = lipgloss.Color("#7E9CD8") // crystalBlue — functions & titles

	// ── Unread indicator ────────────────────────────────────────────────────
	colorUnread = lipgloss.Color("#957FB8") // oniViolet — statements & keywords

	// ── Index column colours ────────────────────────────────────────────────
	colorNumber        = lipgloss.Color("#7E9CD8") // crystalBlue  — row number
	colorDateCol       = lipgloss.Color("#E6C384") // carpYellow   — date
	colorAuthorRead    = lipgloss.Color("#E46876") // waveRed      — sender (read)
	colorSubjectRead   = lipgloss.Color("#7AA89F") // waveAqua2    — subject (read)
	colorSizeCol       = lipgloss.Color("#727169") // fujiGray     — size
	colorAuthorUnread  = lipgloss.Color("#DCA561") // autumnYellow — sender (unread, warm standout)
	colorSubjectUnread = lipgloss.Color("#7FB4CA") // springBlue   — subject (unread)

	// ── Status colours ──────────────────────────────────────────────────────
	colorError   = lipgloss.Color("#C34043") // autumnRed
	colorSuccess = lipgloss.Color("#98BB6C") // springGreen
)

var (
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
			Foreground(colorError).
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
			Foreground(colorSuccess)

	styleOffTab = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true).
			Padding(0, 1)
)

// folderTabs renders the folder switcher bar.
func folderTabs(folders []string, active string, counts map[string]int) string {
	var tabs []string
	for _, f := range folders {
		label := f
		if n, ok := counts[f]; ok && n > 0 {
			label = fmt.Sprintf("%s (%d)", f, n)
		}
		if f == active {
			tabs = append(tabs, styleHeader.Render(label))
		} else {
			tabs = append(tabs, styleFolder.Render(label))
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
