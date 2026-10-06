package ui

// IMAP keyword tagging (space k): the picker toggles optimistically (chips
// flip at once, STORE behind the list, no spinner/reload), a refresh landing
// mid-flight cannot snap chips back, an error reverts them, reserved
// keywords are rejected at input, foreign keywords never chip, and rows with
// chips never overflow the terminal width.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/tags"
)

// tagsTestDir is the registry directory of the most recent tagsTestModel
// call (tests run sequentially).
var tagsTestDir string

// waitTagFile waits for the background write-through (safeGo Save in
// handleTagsDone) to land on disk, so t.TempDir cleanup never races the
// save goroutine.
func waitTagFile(t *testing.T, rel string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(tagsTestDir, rel)); err == nil {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("tag file %s never landed on disk", rel)
}

// tagsTestModel wraps instantModel with the tag machinery enabled: a temp
// registry wired to the chip renderer and a tags/ directory.
func tagsTestModel(t *testing.T, n int) Model {
	t.Helper()
	m := instantModel(t, n)
	m.cfg.Tags.Enabled = true // the feature is opt-in
	tagsTestDir = t.TempDir() + "/tags"
	store, err := tags.Load(tagsTestDir)
	if err != nil {
		t.Fatal(err)
	}
	m.tags.store = store
	prev := tagKeywordSource
	tagKeywordSource = func() []string { return store.All("me@x") }
	t.Cleanup(func() { tagKeywordSource = prev; tagRuleSource = nil })
	return m
}

func keywordsOf(m Model, uid uint32) []string {
	for _, e := range m.emails {
		if e.UID == uid {
			return e.Keywords
		}
	}
	panic("uid not in list")
}

func openPicker(t *testing.T, m Model) Model {
	t.Helper()
	res, _ := m.handleChord(" ", m.cfg.Tags.TagsKey())
	mm, ok := res.(Model)
	if !ok {
		t.Fatal("handleChord did not return a Model")
	}
	if mm.state != stateTags {
		t.Fatalf("state = %v, want stateTags", mm.state)
	}
	return mm
}

func TestTagsPicker_ToggleOptimisticFlip(t *testing.T) {
	m := tagsTestModel(t, 3)                      // list 3,2,1, cursor on uid 3
	m.tags.store.Add("me@x", "work", "<other@x>") // a known tag to pick
	res, _ := m.handleChord(" ", "k")
	mm := res.(Model)
	if mm.state != stateTags {
		t.Fatalf("state = %v, want stateTags", mm.state)
	}
	if len(mm.tags.targets) != 1 || mm.tags.targets[0].UID != 3 {
		t.Fatalf("targets = %v, want uid 3", mm.tags.targets)
	}

	res, cmd := mm.updateTags(key(" "))
	mm = res.(Model)
	if cmd == nil {
		t.Fatal("toggle must fire the STORE command")
	}
	if mm.loading {
		t.Error("toggle must not trigger a spinner/reload (loading must stay false)")
	}
	if got := keywordsOf(mm, 3); len(got) != 1 || got[0] != "work" {
		t.Errorf("keywords after toggle = %v, want [work] immediately", got)
	}
	if got := keywordsOf(mm, 2); len(got) != 0 {
		t.Errorf("other rows must stay untagged, got %v", got)
	}
	// The pending overlay is armed so a fetch cannot snap the chip back.
	if !mm.tags.pending[tagPendingKey{account: "P", folder: "INBOX", uid: 3, keyword: "work"}] {
		t.Error("pendingTags must record the wanted keyword state")
	}
	// The chip is visible in the rendered row (after ANSI strip).
	if row := stripANSI(renderRow(listItemAt(mm, 3), 120)); !strings.Contains(row, " work ") {
		t.Errorf("row with tag must show a chip: %q", row)
	}

	// Completing the STORE releases the overlay and writes the registry through.
	msg := cmd()
	res, _ = mm.Update(msg)
	mm = res.(Model)
	if len(mm.tags.pending) != 0 {
		t.Errorf("pendingTags must be cleared, got %v", mm.tags.pending)
	}
	if got := mm.tags.store.KeywordsFor("me@x", "<m3@x>"); len(got) != 1 || got[0] != "work" {
		t.Errorf("registry after success = %v, want [work]", got)
	}
	waitTagFile(t, filepath.Join("me@x", "work.txt"))
	// Toggling again removes (all targets carry it now).
	res, _ = mm.updateTags(key(" "))
	mm = res.(Model)
	if got := keywordsOf(mm, 3); len(got) != 0 {
		t.Errorf("second toggle must remove the tag, got %v", got)
	}
}

func listItemAt(m Model, uid uint32) emailItem {
	for _, it := range m.inbox.Items() {
		if ei, ok := it.(emailItem); ok && ei.email.UID == uid {
			return ei
		}
	}
	panic("uid not in inbox list")
}

func TestTagsPicker_RefreshLandingMidFlightKeepsChip(t *testing.T) {
	m := tagsTestModel(t, 2)
	m.tags.store.Add("me@x", "work", "<x@x>")
	res, _ := m.handleChord(" ", "k")
	mm := res.(Model)
	res, _ = mm.updateTags(key(" "))
	mm = res.(Model)

	stale := append([]imap.Email(nil), mm.emails...) // server still untagged
	for i := range stale {
		stale[i].Keywords = nil
	}
	mm.refreshing = true
	res, _ = mm.Update(emailsLoadedMsg{emails: stale, folder: "INBOX", account: "P"})
	mm = res.(Model)
	if got := keywordsOf(mm, 2); len(got) != 1 || got[0] != "work" {
		t.Errorf("a refresh landing while the STORE is in flight must not snap the chip back, got %v", got)
	}
}

func TestTagsCmd_ErrorReverts(t *testing.T) {
	m := tagsTestModel(t, 2)
	m.tags.store.Add("me@x", "work", "<x@x>")
	res, _ := m.handleChord(" ", "k")
	mm := res.(Model)
	res, _ = mm.updateTags(key(" "))
	mm = res.(Model)
	if got := keywordsOf(mm, 2); len(got) != 1 {
		t.Fatalf("optimistic flip missing before error, got %v", got)
	}

	// done=0: the STORE never reached the server — the flip must revert.
	res, _ = mm.Update(tagsDoneMsg{
		keyword: "work", add: true,
		ops:  []tagOp{{account: "P", folder: "INBOX", uid: 2, messageID: "<m2@x>"}},
		done: 0, err: errors.New("STORE failed"),
	})
	mm = res.(Model)
	if got := keywordsOf(mm, 2); len(got) != 0 {
		t.Errorf("keywords after error = %v, want reverted to none", got)
	}
	if !mm.isError || !strings.Contains(mm.status, "reverted") {
		t.Errorf("status = %q isError=%v, want revert message in status line", mm.status, mm.isError)
	}
	if len(mm.tags.pending) != 0 {
		t.Errorf("pendingTags after error = %v, want released", mm.tags.pending)
	}
	if got := mm.tags.store.KeywordsFor("me@x", "<m2@x>"); len(got) != 0 {
		t.Errorf("registry must not record a failed STORE, got %v", got)
	}
}

func TestTagsPicker_NewTagLowercased(t *testing.T) {
	m := tagsTestModel(t, 1)
	mm := openPicker(t, m)
	res, _ := mm.updateTags(key("a"))
	mm = res.(Model)
	if !mm.tags.inputActive {
		t.Fatal("a must open the new-tag input")
	}
	for _, r := range "ProJect-X" {
		res, _ = mm.updateTags(key(string(r)))
		mm = res.(Model)
	}
	res, cmd := mm.updateTags(key("enter"))
	mm = res.(Model)
	if cmd == nil {
		t.Fatal("submit must fire the STORE command")
	}
	if got := keywordsOf(mm, 1); len(got) != 1 || got[0] != "project-x" {
		t.Errorf("new tag must be applied lowercased, got %v", got)
	}
	// Deliver the done message: the registry is written through there.
	res, _ = mm.Update(cmd())
	mm = res.(Model)
	if got := mm.tags.store.All("me@x"); len(got) != 1 || got[0] != "project-x" {
		t.Errorf("registry = %v, want [project-x]", got)
	}
	waitTagFile(t, filepath.Join("me@x", "project-x.txt"))
	if mm.tags.inputActive {
		t.Error("input must close after a successful submit")
	}
}

func TestTagsPicker_InvalidKeywordRejected(t *testing.T) {
	m := tagsTestModel(t, 1)
	mm := openPicker(t, m)
	res, _ := mm.updateTags(key("a"))
	mm = res.(Model)
	if !mm.tags.inputActive {
		t.Fatal("a must open the new-tag input")
	}
	for _, bad := range []string{"has space", "ä", "-lead", strings.Repeat("x", 33)} {
		mm.tags.input = bad
		res, _ := mm.updateTags(key("enter"))
		m2 := res.(Model)
		if m2.tags.err == "" {
			t.Errorf("input %q must be rejected with a message", bad)
		}
		if got := keywordsOf(m2, 1); len(got) != 0 {
			t.Errorf("input %q must not tag anything, got %v", bad, got)
		}
		if !m2.tags.inputActive {
			t.Errorf("input mode must stay open after rejecting %q", bad)
		}
	}
}

func TestTagsPicker_ReservedKeywordRejected(t *testing.T) {
	m := tagsTestModel(t, 1)
	mm := openPicker(t, m)
	res, _ := mm.updateTags(key("a"))
	mm = res.(Model)
	for _, reserved := range []string{"$important", "\\Seen", "$label1"} {
		mm.tags.input = reserved
		res, _ := mm.updateTags(key("enter"))
		m2 := res.(Model)
		if m2.tags.err != "not allowed to use this keyword, it is reserved for other systems" {
			t.Errorf("reserved keyword %q: error = %q", reserved, m2.tags.err)
		}
	}
}

func TestTagChips_OnlyRegistryKeywordsRender(t *testing.T) {
	m := tagsTestModel(t, 1)
	m.tags.store.Add("me@x", "work", "<m1@x>")
	m.emails[0].Keywords = []string{"$HasNoAttachment", "$label1", "Work", "foreign-tag"}
	m.applyFilter()
	row := stripANSI(renderRow(listItemAt(m, 1), 120))
	if !strings.Contains(row, " work ") {
		t.Errorf("registry-known keyword must chip: %q", row)
	}
	if strings.Contains(row, "foreign-tag") || strings.Contains(row, "$") || strings.Contains(row, "label1") {
		t.Errorf("foreign/reserved keywords must never chip: %q", row)
	}
	if !strings.Contains(row, " work  s") {
		t.Errorf("chips must render BEFORE the subject with one uncoloured space before it: %q", row)
	}
}

func TestTagChips_WidthAccounting(t *testing.T) {
	m := tagsTestModel(t, 1)
	for _, kw := range []string{"work", "invoice", "project-x", "very-important"} {
		m.tags.store.Add("me@x", kw, "<m1@x>")
	}
	m.emails[0].Keywords = []string{"work", "invoice", "project-x", "very-important"}
	m.applyFilter()
	item := listItemAt(m, 1)
	for _, w := range []int{80, 120, 190, 70, 60} {
		row := stripANSI(renderRow(item, w))
		if got := runewidth.StringWidth(row); got > w {
			t.Errorf("width=%d: rendered row is %d cells (> %d)\n  row: %q", w, got, w, row)
		}
	}
	// Wide enough: all four chips show, before the subject.
	row := stripANSI(renderRow(item, 120))
	for _, kw := range []string{"work", "invoice", "project-x", "very-important"} {
		if !strings.Contains(row, " "+kw+" ") {
			t.Errorf("width=120 must show chip %s: %q", kw, row)
		}
	}
	// Narrow: degrade to fewer chips + "…" subject, never overflow. At 62 the
	// first chip (▐invoice▐, 10 cells incl. separator) fits into the 11-cell
	// budget; at 60 even that does not and the row falls back to chip-less.
	for _, w := range []int{62, 60} {
		row := stripANSI(renderRow(item, w))
		if got := runewidth.StringWidth(row); got > w {
			t.Errorf("width=%d: row is %d cells: %q", w, got, row)
		}
		if w == 62 {
			if !strings.Contains(row, " invoice ") {
				t.Errorf("width=62 should still show the first chip: %q", row)
			}
			if !strings.Contains(row, "…") {
				t.Errorf("width=62 should degrade the subject to …: %q", row)
			}
		}
	}
	// A chip row is never wider than the same row without chips: chips can
	// only shrink the subject budget.
	noChip := m.emails[0]
	noChip.Keywords = nil
	plain := emailItem{email: noChip, index: item.index}
	for _, w := range []int{80, 120, 190, 60} {
		got := runewidth.StringWidth(stripANSI(renderRow(item, w)))
		base := runewidth.StringWidth(stripANSI(renderRow(plain, w)))
		if got > base {
			t.Errorf("width=%d: chip row (%d cells) wider than chip-less row (%d)", w, got, base)
		}
	}
}

func TestTagChips_DisabledFeatureNoChips(t *testing.T) {
	// tagKeywordSource nil (feature never wired, e.g. unreadable tags/):
	// keywords on the email must not render anything.
	m := tagsTestModel(t, 1)
	prev := tagKeywordSource
	tagKeywordSource = nil
	defer func() { tagKeywordSource = prev }()
	m.emails[0].Keywords = []string{"work"}
	m.applyFilter()
	row := stripANSI(renderRow(listItemAt(m, 1), 120))
	if strings.Contains(row, " work ") {
		t.Errorf("chips must not render while the registry is unavailable: %q", row)
	}
}

func TestTagChips_DisabledConfigIsGlobalKillSwitch(t *testing.T) {
	// [tags] enabled = false must turn the feature off COMPLETELY: no chip
	// may render, even for registry-known keywords and even when a per-tag
	// section says enabled = true (per-tag enabled is the fine-grained hide
	// for while the feature is ON). Pins the newTagsModel wiring: disabled
	// must leave the chip pipeline unwired.
	dir := t.TempDir()
	seed, err := tags.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	seed.Add("me@x", "work", "<m1@x>")
	if err := seed.Save(); err != nil {
		t.Fatal(err)
	}

	prevKw, prevRule, prevAcc, prevPill, prevStyle := tagKeywordSource, tagRuleSource, tagActiveAccount, tagPillMode, styleTagChip
	defer func() {
		tagKeywordSource, tagRuleSource, tagActiveAccount, tagPillMode, styleTagChip = prevKw, prevRule, prevAcc, prevPill, prevStyle
	}()
	tagActiveAccount = "me@x" // the wired closure reads the active account
	// Fresh-process state between phases: New() runs once per process with
	// the pipeline vars at their zero values, so "disabled" means "never
	// wired", not "unwired again after an enabled run".
	resetPipeline := func() { tagKeywordSource, tagRuleSource, tagPillMode = nil, nil, false }

	yes := true
	cfg := &config.Config{TagsDir: dir, Tags: config.TagsConfig{
		Enabled: true, // control first: wired and chipping
		Rules:   map[string]config.TagRule{"work": {Enabled: &yes}},
	}}
	resetPipeline()
	newTagsModel(cfg, colorBg, colorText)
	if tagKeywordSource == nil {
		t.Fatal("control: chip pipeline must be wired when [tags] enabled = true")
	}
	m := instantModel(t, 1)
	m.emails[0].Keywords = []string{"work"}
	m.applyFilter()
	if row := stripANSI(renderRow(listItemAt(m, 1), 120)); !strings.Contains(row, " work ") {
		t.Fatalf("control: chip must render when enabled = true: %q", row)
	}

	cfg.Tags.Enabled = false
	resetPipeline()
	tm, notice := newTagsModel(cfg, colorBg, colorText)
	if notice != "" {
		t.Errorf("notice = %q, want none while the feature is off", notice)
	}
	if tm.store != nil {
		t.Error("registry must not load while the feature is off")
	}
	if tagKeywordSource != nil {
		t.Error("chip pipeline must stay unwired while [tags] enabled = false")
	}
	if row := stripANSI(renderRow(listItemAt(m, 1), 120)); strings.Contains(row, " work ") {
		t.Errorf("[tags] enabled = false is the global kill-switch — no chips, per-tag rules notwithstanding: %q", row)
	}
}

func TestTagsPicker_DisabledConfigRefused(t *testing.T) {
	m := tagsTestModel(t, 1)
	m.cfg.Tags.Enabled = false
	res, _ := m.handleChord(" ", "k")
	mm := res.(Model)
	if mm.state == stateTags {
		t.Error("picker must not open when [tags] enabled = false")
	}
	if !strings.Contains(mm.status, "disabled") {
		t.Errorf("status = %q, want a disabled hint", mm.status)
	}
}

func TestTagsPicker_LocalOnlyAccount(t *testing.T) {
	// imap_disabled account: cli is nil, the toggle is registry-only and
	// the done message says so.
	m := tagsTestModel(t, 1)
	m.tags.store.Add("me@x", "work", "<x@x>")
	res, _ := m.handleChord(" ", "k")
	mm := res.(Model)
	res, cmd := mm.updateTags(key(" "))
	mm = res.(Model)
	msg := cmd().(tagsDoneMsg)
	if msg.err != nil {
		t.Fatalf("local-only toggle must not error: %v", msg.err)
	}
	res, _ = mm.Update(msg)
	mm = res.(Model)
	if !strings.Contains(mm.status, "locally") {
		t.Errorf("status = %q, want the local-only notice", mm.status)
	}
	if got := mm.tags.store.KeywordsFor("me@x", "<m1@x>"); len(got) != 1 || got[0] != "work" {
		t.Errorf("registry = %v, want [work] — the registry is the only storage here", got)
	}
	waitTagFile(t, filepath.Join("me@x", "work.txt"))
}

func TestTagsPicker_NoSelectionGuard(t *testing.T) {
	m := tagsTestModel(t, 1)
	m.emails = nil
	m.applyFilter()
	res, _ := m.handleChord(" ", "k")
	mm := res.(Model)
	if mm.state == stateTags {
		t.Error("picker must not open without a target")
	}
	if !mm.isError {
		t.Error("missing target must set an error status")
	}
}

func TestTagsPicker_EscCloses(t *testing.T) {
	m := tagsTestModel(t, 1)
	mm := openPicker(t, m)
	res, _ := mm.updateTags(key("esc"))
	m2 := res.(Model)
	if m2.state == stateTags {
		t.Error("esc must close the picker")
	}
}

func TestTagVerdict(t *testing.T) {
	targets := []imap.Email{
		{UID: 1, Keywords: []string{"work"}},
		{UID: 2, Keywords: nil},
	}
	if got := tagVerdict(targets, "work"); got != '~' {
		t.Errorf("verdict for some = %q, want ~", got)
	}
	targets[1].Keywords = []string{"WORK"}
	if got := tagVerdict(targets, "work"); got != '✓' {
		t.Errorf("verdict for all (case-insensitive) = %q, want ✓", got)
	}
	if got := tagVerdict(targets, "none"); got != ' ' {
		t.Errorf("verdict for none = %q, want space", got)
	}
}

func TestTagRules_DisplayTextAndHidden(t *testing.T) {
	m := tagsTestModel(t, 1)
	for _, kw := range []string{"work", "important", "secret"} {
		m.tags.store.Add("me@x", kw, "<m1@x>")
	}
	hide := false
	cfg := config.TagsConfig{
		Rules: map[string]config.TagRule{
			"important": {Display: "\uf0e7 Important"}, // nerd-font bolt + text
			"secret":    {Enabled: &hide},              // enabled=false → chips hidden
		},
	}
	def, resolve, warn := buildTagStyles(&cfg, colorBg, colorText)
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	tagRuleSource = resolve
	styleTagChip = def
	defer func() { tagRuleSource = nil }()
	m.emails[0].Keywords = []string{"work", "important", "secret"}
	m.applyFilter()

	row := stripANSI(renderRow(listItemAt(m, 1), 120))
	if !strings.Contains(row, " \uf0e7 Important ") {
		t.Errorf("per-tag display text must chip verbatim: %q", row)
	}
	if !strings.Contains(row, " work ") {
		t.Errorf("tag without a rule must chip as the keyword: %q", row)
	}
	if strings.Contains(row, "secret") {
		t.Errorf("enabled=false must hide that tag's chip: %q", row)
	}
	// The keyword on the email is untouched — hiding is display-only.
	if got := keywordsOf(m, 1); len(got) != 3 {
		t.Errorf("Keywords = %v, want all three (hidden is display-only)", got)
	}
}

func TestTagRuleDisplayWidthBudgetsNonASCIIAsTwo(t *testing.T) {
	// A nerd-font icon in a display name must be budgeted as 2 cells so the
	// row can never overflow, whatever the terminal's font actually does.
	chips := []tagChip{{keyword: "important", view: tagRuleView{show: true, display: "\uf0e7 Important", fg: colorBg, bg: colorText}}}
	// padding + icon(2) + text + padding + the trailing space before the subject
	if w := tagChipsWidth(chips); w != len(" ")+2+len(" Important")+len(" ")+1 {
		t.Errorf("tagChipsWidth = %d, want icon counted as 2 cells (plus separators)", w)
	}
}

func TestResolveTagColor(t *testing.T) {
	fb := lipgloss.Color("#000000")
	cases := map[string]string{
		"":            "#000000", // empty → fallback
		"#FF0000":     "#FF0000", // hex passes through
		"21":          "21",      // ANSI number passes through
		"transparent": "",        // explicit no-colour (terminal transparency)
		"None":        "",        // …case-insensitive
		"yellow":      "#000000", // colour names are NOT supported (lipgloss would drop them) → fallback
		"nope":        "#000000", // unknown → fallback (typo never blanks a chip)
	}
	for in, want := range cases {
		if got := string(resolveTagColor(in, fb)); got != want {
			t.Errorf("resolveTagColor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildTagStyles_WarnsOnUnknownColors(t *testing.T) {
	cfg := config.TagsConfig{
		FG: "#FF0000", BG: "yelow", // typo
		Rules: map[string]config.TagRule{
			"x": {FG: "reddish"},
		},
	}
	_, _, warn := buildTagStyles(&cfg, colorBg, colorText)
	if !strings.Contains(warn, `[tags] bg = "yelow"`) || !strings.Contains(warn, `[tags.x] fg = "reddish"`) {
		t.Errorf("warning = %q, want both unknown colours named", warn)
	}
}

func TestTagChips_SeparatedByUncoloredSpace(t *testing.T) {
	chips := []tagChip{
		{keyword: "work", view: tagRuleView{show: true, fg: colorBg, bg: colorText}},
		{keyword: "invoice", view: tagRuleView{show: true, fg: colorBg, bg: colorText}},
	}
	// Between two words: the first chip's coloured pad, the UNcoloured
	// separator, the second chip's coloured pad — three spaces, the middle
	// one outside any coloured run, so chips read as separate entities. One
	// more uncoloured space follows the block, before the subject.
	if got := tagChipsPlain(chips); got != " work   invoice  " {
		t.Errorf("plain = %q, want %q", got, " work   invoice  ")
	}
	// The separators are part of the width budget.
	if w := tagChipsWidth(chips); w != tagSafeWidth(" work   invoice  ") {
		t.Errorf("width = %d, want %d", w, tagSafeWidth(" work   invoice  "))
	}
	if a, b := chips[0].styled(), chips[1].styled(); tagChipsStyled(chips) != a+" "+b+" " {
		t.Error("styled chips must be joined by one uncoloured space plus one before the subject")
	}
}

func TestTagChips_NerdPillMode(t *testing.T) {
	tagPillMode = true
	defer func() { tagPillMode = false }()

	chip := tagChip{keyword: "work", view: tagRuleView{show: true}}
	// Pill: half-disc caps + bare text, no padding spaces inside.
	if got := chip.measure(); got != "\ue0b6work\ue0b4" {
		t.Errorf("pill measure = %q, want caps without padding", got)
	}
	if a, b := "\ue0b6", "\ue0b4"; !strings.HasPrefix(chip.measure(), a) || !strings.HasSuffix(chip.measure(), b) {
		t.Errorf("pill must start/end with the half-disc glyphs: %q", chip.measure())
	}
	// Pills sit flush: no separator between pills, none before the subject —
	// the caps ARE the delimiters.
	chips := []tagChip{chip, {keyword: "invoice", view: tagRuleView{show: true}}}
	if got := tagChipsPlain(chips); got != "\ue0b6work\ue0b4\ue0b6invoice\ue0b4" {
		t.Errorf("pill block = %q", got)
	}
	// End caps are non-ASCII → budgeted as 2 cells each; no separator cells.
	if w := tagChipsWidth(chips); w != 2+4+2+2+7+2 {
		t.Errorf("pill width = %d, want flush pills with caps at 2 cells", w)
	}
	// Per-tag colours resolve explicitly on the view (caps take the bg).
	cfg := config.TagsConfig{Rules: map[string]config.TagRule{
		"important": {FG: "#FFFF00", BG: "#8B0000"},
	}}
	def, resolve, warn := buildTagStyles(&cfg, lipgloss.Color("#111111"), lipgloss.Color("#222222"))
	if warn != "" {
		t.Fatalf("unexpected warning: %q", warn)
	}
	v := resolve("important")
	if v.fg != lipgloss.Color("#FFFF00") || v.bg != lipgloss.Color("#8B0000") {
		t.Errorf("rule colours = %q/%q, want the configured hex", v.fg, v.bg)
	}
	if v.edgeStyle().GetForeground() != lipgloss.Color("#8B0000") {
		t.Error("pill caps must take the rule's background colour")
	}
	if def.GetForeground() != lipgloss.Color("#111111") || def.GetBackground() != lipgloss.Color("#222222") {
		t.Errorf("default style = %q/%q", def.GetForeground(), def.GetBackground())
	}
}

func TestTagChips_PillPaddingRule(t *testing.T) {
	tagPillMode = true
	defer func() { tagPillMode = false }()

	// Pill mode adds NO padding of its own: a clean single-symbol display
	// renders cap+content+cap with zero space runes anywhere…
	clean := tagChip{keyword: "important", view: tagRuleView{show: true, display: "\uf0e7"}}
	if got := clean.displayVerbatim(); got != "\uf0e7" {
		t.Errorf("clean symbol = %q", got)
	}
	if strings.ContainsAny(clean.measure(), " ") {
		t.Errorf("pill must not add spaces: %q", clean.measure())
	}
	if got := tagChipsStyled([]tagChip{clean}); strings.Contains(stripANSI(got), " ") {
		t.Errorf("pill styled output must contain no space: %q", got)
	}
	// …while display-typed spaces are honored as intentional pill padding —
	// and copy-paste stowaways (variation selector U+FE0F) are stripped
	// either way.
	padded := tagChip{keyword: "important", view: tagRuleView{show: true, display: " \uf0e7\ufe0f "}}
	if got := padded.displayVerbatim(); got != " \uf0e7 " {
		t.Errorf("display spaces must be honored, VS16 stripped: %q", got)
	}
	// U+FE0E (text presentation) is the cure for symbols that render with an
	// opaque emoji-fallback box — it must survive the stripping.
	vs15 := tagChip{keyword: "important", view: tagRuleView{show: true, display: "\u26a1\ufe0e"}}
	if got := vs15.displayVerbatim(); got != "\u26a1\ufe0e" {
		t.Errorf("text-presentation selector must be preserved: %q", got)
	}
	if got, want := padded.measure(), tagPillLeft+" \uf0e7 "+tagPillRight; got != want {
		t.Errorf("padded pill = %q, want %q", got, want)
	}

	// Fallback always normalizes to exactly one standard pad per side,
	// whatever spaces the display carries…
	tagPillMode = false
	opaque := func(v tagRuleView) tagRuleView { v.fg, v.bg = colorBg, colorText; return v }
	if got := (tagChip{keyword: "important", view: opaque(padded.view)}).text(); got != " \uf0e7 " {
		t.Errorf("fallback must trim display padding and add its own single pads: %q", got)
	}
	if got := (tagChip{keyword: "important", view: opaque(clean.view)}).text(); got != " \uf0e7 " {
		t.Errorf("fallback text = %q", got)
	}
	// …but with a TRANSPARENT background the pads are skipped — they would
	// be invisible dead cells — and the chip is tight fg-coloured text.
	ghost := tagRuleView{show: true, display: " \uf0e7\ufe0f ", fg: colorBg}
	if got := (tagChip{keyword: "important", view: ghost}).text(); got != "\uf0e7" {
		t.Errorf("transparent fallback must render tight: %q", got)
	}
}

func TestTagsPicker_ShowsDisplayPreviewRightOfKeyword(t *testing.T) {
	m := tagsTestModel(t, 1)
	for _, kw := range []string{"plain", "fancy"} {
		m.tags.store.Add("me@x", kw, "<m1@x>")
	}
	cfg := config.TagsConfig{Rules: map[string]config.TagRule{
		"fancy": {Display: "\uf0e7 Fancy"},
	}}
	def, resolve, _ := buildTagStyles(&cfg, lipgloss.Color("#111111"), lipgloss.Color("#222222"))
	tagRuleSource = resolve
	styleTagChip = def
	defer func() { tagRuleSource = nil }()

	mm := openPicker(t, m)
	view := stripANSI(mm.viewTags())

	// The fancy tag: keyword on the left, display preview to its right.
	if !strings.Contains(view, "fancy") || !strings.Contains(view, "\uf0e7 Fancy") {
		t.Errorf("picker must show keyword and display preview:\n%s", view)
	}
	iKw := strings.Index(view, "fancy")
	iPrev := strings.Index(view, "\uf0e7 Fancy")
	if iKw == -1 || iPrev == -1 || iPrev < iKw {
		t.Errorf("preview must be to the RIGHT of the keyword (kw@%d prev@%d):\n%s", iKw, iPrev, view)
	}
	// Tags without a display show the keyword only.
	if !strings.Contains(view, "plain") {
		t.Errorf("plain tag missing from picker:\n%s", view)
	}

	// Pill mode previews through the same path: the display keeps its
	// verbatim spaces inside the caps.
	tagPillMode = true
	view2 := stripANSI(mm.viewTags())
	tagPillMode = false
	if !strings.Contains(view2, "\ue0b6\uf0e7 Fancy\ue0b4") {
		t.Errorf("pill preview missing caps + verbatim display:\n%s", view2)
	}
}

func TestTagsPicker_PreviewColumnStableWithoutCount(t *testing.T) {
	m := tagsTestModel(t, 1)
	m.tags.store.Add("me@x", "kept", "<m1@x>") // count 1
	m.tags.store.Add("me@x", "gone")           // listed, count 0 (no ids)
	cfg := config.TagsConfig{Rules: map[string]config.TagRule{
		"kept": {Display: "Kept Tag"},
		"gone": {Display: "Gone Tag"},
	}}
	def, resolve, _ := buildTagStyles(&cfg, lipgloss.Color("#111111"), lipgloss.Color("#222222"))
	tagRuleSource = resolve
	styleTagChip = def
	defer func() { tagRuleSource = nil }()

	mm := openPicker(t, m)
	view := stripANSI(mm.viewTags())

	// Both previews START at the same column of their own row (left-aligned
	// in the fixed right-side column), whether or not a count is present.
	startCol := func(match string) int {
		for _, line := range strings.Split(view, "\n") {
			if i := strings.Index(line, match); i >= 0 {
				// cell column, not byte index (▸ is 3 bytes / 1 cell)
				return runewidth.StringWidth(line[:i])
			}
		}
		t.Fatalf("preview %q missing:\n%s", match, view)
		return -1
	}
	if kept, gone := startCol("Kept Tag"), startCol("Gone Tag"); kept != gone {
		t.Errorf("preview column must be fixed: kept starts at %d, gone at %d\n%s", kept, gone, view)
	}
	if !strings.Contains(view, "(1)") || strings.Contains(view, "(0)") {
		t.Errorf("count column wrong:\n%s", view)
	}
}

func TestTagColors_TransparentBackground(t *testing.T) {
	cfg := config.TagsConfig{Rules: map[string]config.TagRule{
		"ghost": {FG: "#FFFF00", BG: "transparent"},
	}}
	def, resolve, warn := buildTagStyles(&cfg, lipgloss.Color("#111111"), lipgloss.Color("#222222"))
	if warn != "" {
		t.Fatalf("transparent must be a valid colour: %q", warn)
	}
	v := resolve("ghost")
	if got := v.bodyStyle().GetBackground(); got != lipgloss.Color("") {
		t.Errorf("transparent bg must emit no background, got %q", got)
	}
	if got := v.edgeStyle().GetForeground(); got != lipgloss.Color("#FFFF00") {
		t.Errorf("pill caps must fall back to fg when bg is transparent, got %q", got)
	}
	// The default style is untouched by the transparent rule.
	if def.GetBackground() != lipgloss.Color("#222222") {
		t.Errorf("default bg changed: %q", def.GetBackground())
	}
	if (config.TagsConfig{}).BG != "" {
		t.Error("sanity")
	}
}

func TestCalculateSubjectMaxWithTags(t *testing.T) {
	m := tagsTestModel(t, 1)
	for _, kw := range []string{"work", "invoice", "project-x", "very-important"} {
		m.tags.store.Add("me@x", kw, "<m1@x>")
	}
	chips := []string{"work", "invoice", "project-x", "very-important"}

	// Feature off / no chips / merge row → exactly the original calculation.
	orig := 120 - 29 - 20 - 2
	if got, subj, c := calculateSubjectMaxWithTags(120, 29, 20, "s", nil, false); got != orig || subj != "s" || c != nil {
		t.Errorf("no-keywords: %d %q %v, want original formula %d", got, subj, c, orig)
	}
	if got, _, _ := calculateSubjectMaxWithTags(58, 29, 20, "s", nil, false); got != 8 {
		t.Errorf("floor lost: %d, want 8", got)
	}
	if got, _, c := calculateSubjectMaxWithTags(120, 29, 20, "s", chips, true); c != nil || got != orig {
		t.Errorf("merge rows must not chip: %d %v", got, c)
	}

	// Wide: all chips keep their budget.
	all := tagChipKeywords(chips)
	want := orig - tagChipsWidth(all)
	if got, subj, c := calculateSubjectMaxWithTags(120, 29, 20, "subject", chips, false); got != want || subj != "subject" || len(c) != 4 {
		t.Errorf("wide: %d %q %d chips, want %d with 4 chips", got, subj, len(c), want)
	}
	// Narrow (62): first chip only + ellipsis subject.
	fw := tagChipsWidth(all[:1])
	if got, subj, c := calculateSubjectMaxWithTags(62, 29, 20, "subject", chips, false); got != (62-51)-fw || subj != "…" || len(c) != 1 {
		t.Errorf("narrow: %d %q %d chips, want first-chip + ellipsis", got, subj, len(c))
	}
	// Very narrow (58): not even one chip fits → original behaviour, no chips.
	if got, subj, c := calculateSubjectMaxWithTags(58, 29, 20, "subject", chips, false); c != nil || subj != "subject" {
		t.Errorf("tight: must drop chips and keep the subject, got %d %q %v", got, subj, c)
	}
}
