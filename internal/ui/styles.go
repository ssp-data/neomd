package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/config"
)

// Built-in palettes. Default = kanagawa (byte-for-byte identical to pre-theme
// state). Switch with `[ui].theme = "<name>"`; per-slot overrides via the
// optional [theme] block in config.toml.
var themes = map[string]config.Theme{
	"kanagawa": {
		// https://github.com/rebelot/kanagawa.nvim
		Bg: "#1F1F28", Border: "#54546D", Subtle: "#363646", Selected: "#223249",
		Text: "#DCD7BA", Muted: "#727169",
		Primary: "#7E9CD8", Unread: "#957FB8",
		Number: "#7E9CD8", Date: "#E6C384",
		AuthorRead: "#E46876", SubjectRead: "#7AA89F", SizeCol: "#727169",
		AuthorUnread: "#DCA561", SubjectUnread: "#7FB4CA",
		Error: "#C34043", Success: "#98BB6C",
	},
	"kanagawa-paper": {
		// https://github.com/thesimonho/kanagawa-paper.nvim — muted variant
		Bg: "#1F1F28", Border: "#4A4A5E", Subtle: "#363646", Selected: "#2A3A52",
		Text: "#C8C2A8", Muted: "#6E6D67",
		Primary: "#7090C2", Unread: "#876FA8",
		Number: "#7090C2", Date: "#D8B470",
		AuthorRead: "#D45F6E", SubjectRead: "#6FA08F", SizeCol: "#6E6D67",
		AuthorUnread: "#C19459", SubjectUnread: "#6FA0BC",
		Error: "#B83C3D", Success: "#88AB60",
	},
	"kanagawa-light": {
		// Lotus palette from rebelot/kanagawa.nvim's day variant, with the
		// paperwhite background popularised by Spacheck's Obsidian port. The
		// only built-in light theme — pick this for daylight terminals.
		Bg: "#F2EFE9", Border: "#A09CAC", Subtle: "#E5DDB0", Selected: "#B5CBD2",
		Text: "#545464", Muted: "#8A8980",
		Primary: "#4D699B", Unread: "#624C83",
		Number: "#4D699B", Date: "#CC6D00",
		AuthorRead: "#C84053", SubjectRead: "#597B75", SizeCol: "#8A8980",
		AuthorUnread: "#CC6D00", SubjectUnread: "#4E8CA2",
		Error: "#C84053", Success: "#6F894E",
	},
	"rose-pine": {
		// https://github.com/rose-pine/rose-pine-theme — main variant
		Bg: "#191724", Border: "#26233A", Subtle: "#1F1D2E", Selected: "#403D52",
		Text: "#E0DEF4", Muted: "#6E6A86",
		Primary: "#C4A7E7", Unread: "#EBBCBA",
		Number: "#31748F", Date: "#F6C177",
		AuthorRead: "#EB6F92", SubjectRead: "#9CCFD8", SizeCol: "#6E6A86",
		AuthorUnread: "#F6C177", SubjectUnread: "#9CCFD8",
		Error: "#EB6F92", Success: "#31748F",
	},
	"gruvbox": {
		// https://github.com/morhetz/gruvbox — dark medium
		Bg: "#282828", Border: "#504945", Subtle: "#3C3836", Selected: "#45403D",
		Text: "#EBDBB2", Muted: "#928374",
		Primary: "#83A598", Unread: "#D3869B",
		Number: "#83A598", Date: "#FABD2F",
		AuthorRead: "#FB4934", SubjectRead: "#8EC07C", SizeCol: "#928374",
		AuthorUnread: "#FE8019", SubjectUnread: "#B8BB26",
		Error: "#FB4934", Success: "#B8BB26",
	},
	"osaka-jade": {
		// https://github.com/Justikun/omarchy-osaka-jade-theme
		Bg: "#111C18", Border: "#53685B", Subtle: "#23372B", Selected: "#2D4537",
		Text: "#C1C497", Muted: "#53685B",
		Primary: "#2DD5B7", Unread: "#D2689C",
		Number: "#2DD5B7", Date: "#E5C736",
		AuthorRead: "#FF5345", SubjectRead: "#549E6A", SizeCol: "#53685B",
		AuthorUnread: "#E5C736", SubjectUnread: "#ACD4CF",
		Error: "#FF5345", Success: "#549E6A",
	},
}

// Mutable colour vars — populated by ApplyTheme. All UI files reference these
// directly, so reassigning them at startup updates every callsite that builds
// styles at render time.
var (
	colorBg, colorBorder, colorSubtle, colorSelected lipgloss.Color
	colorText, colorMuted                            lipgloss.Color
	colorPrimary, colorUnread                        lipgloss.Color
	colorNumber, colorDateCol                        lipgloss.Color
	colorAuthorRead, colorSubjectRead, colorSizeCol  lipgloss.Color
	colorAuthorUnread, colorSubjectUnread            lipgloss.Color
	colorError, colorSuccess                         lipgloss.Color
)

// Mutable style vars rebuilt by rebuildStyles after ApplyTheme. The ones built
// at render time in other files don't need rebuilding; only the package-level
// ones do.
var (
	styleHeader             lipgloss.Style
	styleFolder             lipgloss.Style
	styleStatus             lipgloss.Style
	styleError              lipgloss.Style
	styleEmailMeta          lipgloss.Style
	styleFrom               lipgloss.Style
	styleSubject            lipgloss.Style
	styleDate               lipgloss.Style
	styleUnread             lipgloss.Style
	styleRead               lipgloss.Style
	styleSelected           lipgloss.Style
	styleHelp               lipgloss.Style
	styleSeparator          lipgloss.Style
	styleInputLabel         lipgloss.Style
	styleInputField         lipgloss.Style
	styleSuccess            lipgloss.Style
	styleOffTab             lipgloss.Style
	styleSuggestion         lipgloss.Style
	styleSuggestionSelected lipgloss.Style
)

func init() {
	// Default to kanagawa so package-level style vars are populated before
	// any UI rendering. ApplyTheme can override later from config.
	ApplyTheme("kanagawa", config.Theme{})
}

// glamourStyleFor maps a neomd theme name to a glamour built-in style for
// rendering email markdown in the reader. Glamour ships with a fixed set of
// styles (`dark`, `light`, `auto`, `notty`, …); passing an unknown name
// silently falls back to `notty` which strips colours and wrapping, so we
// must translate. Light palettes → `light`; everything else (including the
// pre-theme legacy values "dark"/"light"/"auto" and unknown names) →
// `dark`. The legacy "auto" was rarely useful in practice and would now
// be ambiguous, so we collapse it to `dark` for predictability.
func glamourStyleFor(themeName string) string {
	if themeName == "kanagawa-light" || themeName == "light" {
		return "light"
	}
	return "dark"
}

// ApplyTheme switches the active palette and rebuilds the style vars. Pass an
// override theme to mutate individual slots; empty fields fall through to the
// named built-in. Unknown names fall back to kanagawa.
func ApplyTheme(name string, override config.Theme) {
	t, ok := themes[name]
	if !ok {
		t = themes["kanagawa"]
	}
	t = mergeTheme(t, override)

	colorBg = lipgloss.Color(t.Bg)
	colorBorder = lipgloss.Color(t.Border)
	colorSubtle = lipgloss.Color(t.Subtle)
	colorSelected = lipgloss.Color(t.Selected)
	colorText = lipgloss.Color(t.Text)
	colorMuted = lipgloss.Color(t.Muted)
	colorPrimary = lipgloss.Color(t.Primary)
	colorUnread = lipgloss.Color(t.Unread)
	colorNumber = lipgloss.Color(t.Number)
	colorDateCol = lipgloss.Color(t.Date)
	colorAuthorRead = lipgloss.Color(t.AuthorRead)
	colorSubjectRead = lipgloss.Color(t.SubjectRead)
	colorSizeCol = lipgloss.Color(t.SizeCol)
	colorAuthorUnread = lipgloss.Color(t.AuthorUnread)
	colorSubjectUnread = lipgloss.Color(t.SubjectUnread)
	colorError = lipgloss.Color(t.Error)
	colorSuccess = lipgloss.Color(t.Success)

	rebuildStyles()
}

func mergeTheme(base, over config.Theme) config.Theme {
	if over.Bg != "" {
		base.Bg = over.Bg
	}
	if over.Border != "" {
		base.Border = over.Border
	}
	if over.Subtle != "" {
		base.Subtle = over.Subtle
	}
	if over.Selected != "" {
		base.Selected = over.Selected
	}
	if over.Text != "" {
		base.Text = over.Text
	}
	if over.Muted != "" {
		base.Muted = over.Muted
	}
	if over.Primary != "" {
		base.Primary = over.Primary
	}
	if over.Unread != "" {
		base.Unread = over.Unread
	}
	if over.Number != "" {
		base.Number = over.Number
	}
	if over.Date != "" {
		base.Date = over.Date
	}
	if over.AuthorRead != "" {
		base.AuthorRead = over.AuthorRead
	}
	if over.SubjectRead != "" {
		base.SubjectRead = over.SubjectRead
	}
	if over.SizeCol != "" {
		base.SizeCol = over.SizeCol
	}
	if over.AuthorUnread != "" {
		base.AuthorUnread = over.AuthorUnread
	}
	if over.SubjectUnread != "" {
		base.SubjectUnread = over.SubjectUnread
	}
	if over.Error != "" {
		base.Error = over.Error
	}
	if over.Success != "" {
		base.Success = over.Success
	}
	return base
}

func rebuildStyles() {
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

	styleSuggestion = lipgloss.NewStyle().
		Foreground(colorMuted)

	styleSuggestionSelected = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true)
}

// tabZone records the X range for a clickable folder tab.
type tabZone struct {
	xStart, xEnd int // character range [xStart, xEnd)
	folderIndex  int
}

// folderTabs renders the folder switcher bar and returns click zones.
func folderTabs(folders []string, active string, counts map[string]int) (string, []tabZone) {
	// Compute raw label for each tab (before styling) to track character positions.
	labels := make([]string, len(folders))
	for i, f := range folders {
		labels[i] = f
		if n, ok := counts[f]; ok && n > 0 {
			labels[i] = fmt.Sprintf("%s (%d)", f, n)
		}
	}

	// styleHeader and styleFolder both add Padding(0,1) = 1 space each side.
	const padLeft = 1
	const padRight = 1
	const sepWidth = 3 // " │ " rendered width

	var zones []tabZone
	var tabs []string
	x := 0
	for i, f := range folders {
		label := labels[i]
		start := x + padLeft
		end := start + len(label)
		zones = append(zones, tabZone{xStart: x, xEnd: end + padRight, folderIndex: i})

		if f == active {
			tabs = append(tabs, styleHeader.Render(label))
		} else {
			tabs = append(tabs, styleFolder.Render(label))
		}
		x = end + padRight
		if i < len(folders)-1 {
			x += sepWidth
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
	return result, zones
}
