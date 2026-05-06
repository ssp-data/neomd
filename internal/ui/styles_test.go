package ui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/sspaeti/neomd/internal/config"
)

// TestKanagawaDefault — regression: default applied at init must match the
// historical hardcoded values byte-for-byte so unset [ui].theme = current main.
func TestKanagawaDefault(t *testing.T) {
	ApplyTheme("kanagawa", config.Theme{})
	expected := map[string]lipgloss.Color{
		"colorBg":            "#1F1F28",
		"colorBorder":        "#54546D",
		"colorSubtle":        "#363646",
		"colorSelected":      "#223249",
		"colorText":          "#DCD7BA",
		"colorMuted":         "#727169",
		"colorPrimary":       "#7E9CD8",
		"colorUnread":        "#957FB8",
		"colorNumber":        "#7E9CD8",
		"colorDateCol":       "#E6C384",
		"colorAuthorRead":    "#E46876",
		"colorSubjectRead":   "#7AA89F",
		"colorSizeCol":       "#727169",
		"colorAuthorUnread":  "#DCA561",
		"colorSubjectUnread": "#7FB4CA",
		"colorError":         "#C34043",
		"colorSuccess":       "#98BB6C",
	}
	got := map[string]lipgloss.Color{
		"colorBg":            colorBg,
		"colorBorder":        colorBorder,
		"colorSubtle":        colorSubtle,
		"colorSelected":      colorSelected,
		"colorText":          colorText,
		"colorMuted":         colorMuted,
		"colorPrimary":       colorPrimary,
		"colorUnread":        colorUnread,
		"colorNumber":        colorNumber,
		"colorDateCol":       colorDateCol,
		"colorAuthorRead":    colorAuthorRead,
		"colorSubjectRead":   colorSubjectRead,
		"colorSizeCol":       colorSizeCol,
		"colorAuthorUnread":  colorAuthorUnread,
		"colorSubjectUnread": colorSubjectUnread,
		"colorError":         colorError,
		"colorSuccess":       colorSuccess,
	}
	for k, want := range expected {
		if got[k] != want {
			t.Errorf("%s = %q, want %q (kanagawa default must not regress)", k, got[k], want)
		}
	}
}

func TestApplyTheme_KnownNames(t *testing.T) {
	for _, name := range []string{"kanagawa", "kanagawa-paper", "kanagawa-light", "rose-pine", "gruvbox", "osaka-jade"} {
		ApplyTheme(name, config.Theme{})
		if colorBg == "" || colorPrimary == "" {
			t.Errorf("ApplyTheme(%q): expected colors populated, got empty", name)
		}
	}
	// Restore default for downstream tests.
	ApplyTheme("kanagawa", config.Theme{})
}

func TestApplyTheme_UnknownFallsBackToKanagawa(t *testing.T) {
	ApplyTheme("nonexistent-theme", config.Theme{})
	if colorPrimary != lipgloss.Color("#7E9CD8") {
		t.Errorf("unknown theme should fall back to kanagawa primary #7E9CD8, got %q", colorPrimary)
	}
	ApplyTheme("kanagawa", config.Theme{})
}

func TestApplyTheme_OverrideMerges(t *testing.T) {
	ApplyTheme("kanagawa", config.Theme{Primary: "#FF0000", Error: "#00FF00"})
	if colorPrimary != lipgloss.Color("#FF0000") {
		t.Errorf("override Primary not applied: got %q want #FF0000", colorPrimary)
	}
	if colorError != lipgloss.Color("#00FF00") {
		t.Errorf("override Error not applied: got %q want #00FF00", colorError)
	}
	// Non-overridden slot still reflects kanagawa value.
	if colorBg != lipgloss.Color("#1F1F28") {
		t.Errorf("non-overridden Bg should stay kanagawa #1F1F28, got %q", colorBg)
	}
	ApplyTheme("kanagawa", config.Theme{})
}

func TestApplyTheme_DifferentThemesProduceDifferentStyles(t *testing.T) {
	// Lipgloss strips ANSI when stdout is not a TTY (test env), so compare the
	// underlying style colour values directly rather than rendered output.
	ApplyTheme("kanagawa", config.Theme{})
	kanagawaPrimary := styleHeader.GetForeground()

	ApplyTheme("rose-pine", config.Theme{})
	rosePinePrimary := styleHeader.GetForeground()

	if kanagawaPrimary == rosePinePrimary {
		t.Errorf("kanagawa and rose-pine should yield different styleHeader foreground; both = %v", kanagawaPrimary)
	}
	ApplyTheme("kanagawa", config.Theme{})
}

func TestGlamourStyleFor(t *testing.T) {
	// Regression for review #201 finding 2: passing the new named-theme
	// strings (kanagawa, rose-pine, …) directly into glamour silently
	// falls back to "notty" which strips colours/wrapping. The mapper must
	// always return one of glamour's built-in style names.
	cases := map[string]string{
		"kanagawa":       "dark",
		"kanagawa-paper": "dark",
		"kanagawa-light": "light",
		"rose-pine":      "dark",
		"gruvbox":        "dark",
		"osaka-jade":     "dark",
		"":               "dark", // empty config falls through to dark
		"unknown-theme":  "dark", // unknown names also fall through, never to notty
		"light":          "light", // legacy literal still respected
		"auto":           "dark",  // legacy "auto" collapses to dark for predictability
	}
	for in, want := range cases {
		if got := glamourStyleFor(in); got != want {
			t.Errorf("glamourStyleFor(%q) = %q, want %q", in, got, want)
		}
	}
}
