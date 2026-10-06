package ui

// ── IMAP keyword tags (space k) ──────────────────────────────────────────
//
// Tags are stored as IMAP keywords on the message itself (server-side, works
// in other clients, survives reinstalls); <config dir>/tags/ is the local
// write-through registry (internal/tags — one folder per account, one
// line-based file per keyword) used as the purge fallback and the
// "known tags" list. The picker toggles optimistically — the chip flips at
// once and the STORE runs behind the list, exactly like the n \Seen toggle:
// no spinner, no reload, error reverts with the error in the status line.

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/sspaeti/neomd/internal/config"
	"github.com/sspaeti/neomd/internal/imap"
	"github.com/sspaeti/neomd/internal/tags"
)

// tagKeywordSource feeds the chip renderer with the registry's known
// keywords. It is a package-level indirection (set once in New) because the
// list delegate renders rows without access to the Model — the same pattern
// as the mutable theme colour vars. nil (tests, disabled feature) = no chips.
// The closure reads tagActiveAccount so chips follow the active account.
var tagKeywordSource func() []string

// tagActiveAccount mirrors the active account name for the package-level
// chip pipeline (setTagAccount keeps it in sync; single-goroutine UI).
var tagActiveAccount string

// tagOp is one planned keyword change, captured by value so the STORE
// goroutine never reads through a pointer into the list. cli is resolved on
// the main goroutine at command construction; nil = imap_disabled account
// (the tag is registry-only for that op). registryKey is the account's
// tags/<key>/ folder (its email address) captured at toggle time — a done
// message landing after an account switch must still write the right
// account's registry.
type tagOp struct {
	account     string
	registryKey string
	folder      string
	uid         uint32
	messageID   string
	cli         *imap.Client
}

// tagsDoneMsg reports the keyword STOREs of one tag toggle. ops[:done]
// reached the server; ops[done:] did not (err set) and must be reverted.
type tagsDoneMsg struct {
	keyword string
	add     bool
	ops     []tagOp
	done    int
	err     error
}

// tagPendingKey names one in-flight optimistic keyword flip:
// account + source folder + UID + keyword.
type tagPendingKey struct {
	account string
	folder  string
	uid     uint32
	keyword string
}

// tagsModel is the whole tag-feature state on Model (the composeModel
// pattern: the state lives here, next to its methods; Model keeps one
// field, tags). targets is captured by value when the picker opens and
// flipped in place on every toggle (main goroutine only).
type tagsModel struct {
	store        *tags.Store
	targets      []imap.Email
	cursor       int
	filter       string
	filterActive bool
	inputActive  bool // "a" — typing a new tag name
	input        string
	err          string // validation error shown inside the picker

	// pending holds the wanted keyword state of in-flight tag toggles
	// (space k); fetch results landing meanwhile take this state so a
	// refresh cannot snap the chips back. Cleared by tagsDoneMsg.
	pending map[tagPendingKey]bool
}

// newTagsModel loads the local write-through registry (one folder per
// account under <config dir>/tags/) and resolves the chip styling from
// config; called once from New. The store is nil when the directory cannot
// be read — tagging still works server-side, only chips and the picker's
// known-tag list degrade. Returns a startup notice (registry failure or
// unrecognized colour values), "" when all is well. The built-in theme
// palettes are never touched; the package-level chip pipeline is wired here.
func newTagsModel(cfg *config.Config, themeFG, themeBG lipgloss.Color) (tagsModel, string) {
	// [tags] enabled = false is the GLOBAL kill-switch (the feature is
	// opt-in): the chip pipeline stays unwired — a nil tagKeywordSource is
	// the no-chips state, same as an unreadable registry — so no keyword
	// ever renders, no matter what a [tags.<keyword>] section says. Per-tag
	// enabled = false is the fine-grained form: hide single keywords while
	// the feature is on. The picker has its own gate in openTags.
	if !cfg.Tags.Enabled {
		return tagsModel{}, ""
	}
	var notice string
	store, err := tags.Load(cfg.TagsDir)
	if err != nil {
		notice = "tags registry: " + err.Error()
		store = nil
	}
	tagKeywordSource = func() []string { return store.All(tagActiveAccount) }
	chipStyle, tagRules, colorWarn := buildTagStyles(&cfg.Tags, themeFG, themeBG)
	styleTagChip = chipStyle
	tagRuleSource = tagRules
	tagPillMode = cfg.Tags.NerdPill
	if colorWarn != "" && notice == "" {
		notice = colorWarn
	}
	return tagsModel{store: store}, notice
}

// activeAccountKey returns the stable registry key of the active account:
// the bare, lowercased From address (falling back to the IMAP user, then the
// account name). Email addresses are valid folder names on every platform
// neomd supports, and every account has its own address — so the key doubles
// as the tags/<key>/ directory name without any mangling.
func (m Model) activeAccountKey() string {
	if m.accountI < len(m.accounts) {
		acc := m.accounts[m.accountI]
		for _, cand := range []string{acc.From, acc.User, acc.Name} {
			if a := strings.ToLower(extractEmailAddr(strings.TrimSpace(cand))); a != "" {
				return a
			}
		}
	}
	return "default"
}

// openTags opens the tag picker on the marked set (or the cursor email —
// same target rule as every bulk action) and captures the targets by value.
func (m Model) openTags() (tea.Model, tea.Cmd) {
	if m.cfg != nil && !m.cfg.Tags.Enabled {
		m.status = "Tags are disabled — opt in with [tags] enabled = true."
		m.isError = false
		return m, nil
	}
	targets := m.targetEmails()
	if len(targets) == 0 {
		m.status = "Tags: no email selected."
		m.isError = true
		return m, nil
	}
	m.prevState = m.state
	m.state = stateTags
	m.tags.targets = append([]imap.Email(nil), targets...)
	m.tags.cursor = 0
	m.tags.filter = ""
	m.tags.filterActive = false
	m.tags.inputActive = false
	m.tags.input = ""
	m.tags.err = ""
	return m, nil
}

// pickerTags returns the keywords the picker lists: every registry keyword
// plus custom (non-reserved) keywords the targets already carry — those must
// be removable even when the local registry never saw them. Sorted, deduped
// case-insensitively.
func (m Model) pickerTags() []string {
	var kws []string
	if m.tags.store != nil {
		kws = append(kws, m.tags.store.All(m.activeAccountKey())...)
	}
	for _, e := range m.tags.targets {
		for _, kw := range e.Keywords {
			if !strings.HasPrefix(kw, "$") && !strings.HasPrefix(kw, "\\") {
				kws = append(kws, strings.ToLower(kw))
			}
		}
	}
	sort.Strings(kws)
	out := kws[:0]
	for i, kw := range kws {
		if i == 0 || !strings.EqualFold(kw, kws[i-1]) {
			out = append(out, kw)
		}
	}
	return out
}

// filteredPickerTags applies the picker's / filter (case-insensitive substring).
func (m Model) filteredPickerTags() []string {
	all := m.pickerTags()
	q := strings.ToLower(strings.TrimSpace(m.tags.filter))
	if q == "" {
		return all
	}
	var out []string
	for _, kw := range all {
		if strings.Contains(kw, q) {
			out = append(out, kw)
		}
	}
	return out
}

// tagVerdict returns '✓' when every target carries kw, '~' when some do,
// ' ' when none does. Case-insensitive (servers may normalize the case).
func tagVerdict(targets []imap.Email, kw string) rune {
	some, all := false, true
	for _, e := range targets {
		if tags.Has(e.Keywords, kw) {
			some = true
		} else {
			all = false
		}
	}
	switch {
	case all && len(targets) > 0:
		return '✓'
	case some:
		return '~'
	default:
		return ' '
	}
}

func (m Model) updateTags(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// New-tag input mode: printable runes extend the keyword, enter submits
	// (validated + applied to all targets), esc returns to the list.
	if m.tags.inputActive {
		switch key {
		case "esc":
			m.tags.inputActive = false
			m.tags.input = ""
			m.tags.err = ""
			return m, nil
		case "enter":
			kw, err := tags.NormalizeKeyword(m.tags.input)
			if err != nil {
				m.tags.err = err.Error()
				return m, nil
			}
			m.tags.inputActive = false
			m.tags.input = ""
			m.tags.err = ""
			return m.applyTag(kw)
		case "backspace", "ctrl+h":
			if r := []rune(m.tags.input); len(r) > 0 {
				m.tags.input = string(r[:len(r)-1])
			}
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		default:
			if len([]rune(key)) == 1 {
				m.tags.input += key
				m.tags.err = ""
			}
			return m, nil
		}
	}

	// Filter typing mode (same contract as the contacts picker).
	entries := m.filteredPickerTags()
	clamp := func() {
		if m.tags.cursor >= len(entries) {
			m.tags.cursor = len(entries) - 1
		}
		if m.tags.cursor < 0 {
			m.tags.cursor = 0
		}
	}
	clamp()
	if m.tags.filterActive {
		switch key {
		case "esc":
			if m.tags.filter != "" {
				m.tags.filter = ""
				m.tags.cursor = 0
			} else {
				m.tags.filterActive = false
			}
			return m, nil
		case "enter":
			m.tags.filterActive = false
			return m, nil
		case "backspace", "ctrl+h":
			if r := []rune(m.tags.filter); len(r) > 0 {
				m.tags.filter = string(r[:len(r)-1])
				m.tags.cursor = 0
			}
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		default:
			if len([]rune(key)) == 1 {
				m.tags.filter += key
				m.tags.cursor = 0
			}
			return m, nil
		}
	}

	switch key {
	case "esc", "q":
		m.state = m.prevState
		if m.state == stateTags {
			m.state = stateInbox
		}
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	case "/":
		m.tags.filterActive = true
		return m, nil
	case "a":
		m.tags.inputActive = true
		m.tags.input = ""
		m.tags.err = ""
		return m, nil
	case "j", "down":
		if m.tags.cursor < len(entries)-1 {
			m.tags.cursor++
		}
	case "k", "up":
		if m.tags.cursor > 0 {
			m.tags.cursor--
		}
	case "G":
		m.tags.cursor = len(entries) - 1
		clamp()
	case "g":
		m.tags.cursor = 0
	case " ", "enter":
		if len(entries) == 0 {
			return m, nil
		}
		return m.applyTag(entries[m.tags.cursor])
	}
	return m, nil
}

// tagsClientFor resolves the IMAP client of one account STRICTLY: nil for
// imap_disabled (and unknown) accounts — never the primary fallback
// imapCliForAccount does — because a keyword STORE addressed with another
// account's connection would tag the wrong server's folder+UID.
func (m Model) tagsClientFor(account string) *imap.Client {
	for i, a := range m.accounts {
		if strings.EqualFold(a.Name, account) && i < len(m.clients) {
			return m.clients[i] // nil = imap_disabled → registry-only op
		}
	}
	return nil
}

// applyTag toggles kw on every target: when all targets carry it the tag is
// removed, otherwise it is added. The local flip happens at once (list +
// cache + the picker's captured targets); the STOREs run behind the list.
func (m Model) applyTag(kw string) (tea.Model, tea.Cmd) {
	account := m.activeAccountName()
	add := tagVerdict(m.tags.targets, kw) != '✓'
	if m.tags.pending == nil {
		m.tags.pending = make(map[tagPendingKey]bool)
	}
	cli := m.tagsClientFor(account)
	ops := make([]tagOp, 0, len(m.tags.targets))
	for i := range m.tags.targets {
		e := &m.tags.targets[i]
		m.setKeywordLocal(account, e.Folder, e.UID, kw, add)
		e.Keywords = flipEmailKeywords(e.Keywords, kw, add)
		m.tags.pending[tagPendingKey{account: account, folder: e.Folder, uid: e.UID, keyword: kw}] = add
		ops = append(ops, tagOp{
			account:     account,
			registryKey: m.activeAccountKey(),
			folder:      e.Folder,
			uid:         e.UID,
			messageID:   e.MessageID,
			cli:         cli,
		})
	}
	// Marks keep their meaning (the rows stay); only the chips change.
	return m, tea.Batch(m.applyFilter(), m.tagsCmd(kw, add, ops))
}

// flipKeywords returns keywords with kw added/removed — the pure helper
// behind the optimistic flip (tags.WithKeyword / WithoutKeyword).
// setKeywordLocal sets keyword presence on folder/uid in the visible list and
// in account's cached snapshot of that folder (mirrors setSeenLocal — set,
// not toggle, so it is safe when the two alias). The account is passed
// explicitly: a done/revert message can land after the user switched
// accounts, and the cache key must never point into the wrong account.
func (m *Model) setKeywordLocal(account, folder string, uid uint32, keyword string, add bool) {
	for i := range m.emails {
		if m.emails[i].UID == uid && m.emails[i].Folder == folder {
			m.emails[i].Keywords = flipEmailKeywords(m.emails[i].Keywords, keyword, add)
		}
	}
	if snap, ok := m.folderCache[cacheKey(account, folder)]; ok {
		for i := range snap.emails {
			if snap.emails[i].UID == uid && snap.emails[i].Folder == folder {
				snap.emails[i].Keywords = flipEmailKeywords(snap.emails[i].Keywords, keyword, add)
			}
		}
	}
}

func flipEmailKeywords(keywords []string, keyword string, add bool) []string {
	if add {
		return tags.WithKeyword(keywords, keyword)
	}
	return tags.WithoutKeyword(keywords, keyword)
}

// tagsCmd runs the STOREs for ops in order on the primary connection(s),
// fail-fast like toggleSeenCmd. Local-only ops (nil client) count as done —
// the registry is their only storage.
func (m Model) tagsCmd(keyword string, add bool, ops []tagOp) tea.Cmd {
	return func() tea.Msg {
		for i, op := range ops {
			if op.cli == nil {
				continue
			}
			var err error
			if add {
				err = op.cli.AddKeyword(nil, op.folder, op.uid, keyword)
			} else {
				err = op.cli.RemoveKeyword(nil, op.folder, op.uid, keyword)
			}
			if err != nil {
				return tagsDoneMsg{keyword: keyword, add: add, ops: ops, done: i, err: err}
			}
		}
		return tagsDoneMsg{keyword: keyword, add: add, ops: ops, done: len(ops)}
	}
}

// handleTagsDone finishes a toggle: success updates the write-through
// registry (and saves it in the background); an error reverts every op that
// never reached the server, with the error in the status line.
func (m Model) handleTagsDone(msg tagsDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		for _, op := range msg.ops[msg.done:] {
			// Only touch rows still owned by the active account — the user
			// may have switched accounts while the STORE was in flight.
			if op.account == m.activeAccountName() {
				m.setKeywordLocal(op.account, op.folder, op.uid, msg.keyword, !msg.add)
			}
			delete(m.tags.pending, tagPendingKey{account: op.account, folder: op.folder, uid: op.uid, keyword: msg.keyword})
		}
		m.status = fmt.Sprintf("tag %q: %v (reverted)", msg.keyword, msg.err)
		m.isError = true
		return m, m.applyFilter()
	}
	// Success: release the pending overlay …
	for _, op := range msg.ops {
		delete(m.tags.pending, tagPendingKey{account: op.account, folder: op.folder, uid: op.uid, keyword: msg.keyword})
	}
	// … and write the registry through (background save, merges-style).
	var ids []string
	for _, op := range msg.ops {
		if op.messageID != "" {
			ids = append(ids, op.messageID)
		}
	}
	if len(ids) > 0 {
		key := ""
		if len(msg.ops) > 0 {
			key = msg.ops[0].registryKey
		}
		if msg.add {
			m.tags.store.Add(key, msg.keyword, ids...)
		} else {
			m.tags.store.Remove(key, msg.keyword, ids...)
		}
		store := m.tags.store
		safeGo(func() {
			if err := store.Save(); err != nil {
				log.Printf("tags: saving registry failed: %v", err)
			}
		})
	}
	localOnly := true
	for _, op := range msg.ops {
		if op.cli != nil {
			localOnly = false
			break
		}
	}
	if localOnly {
		m.status = "tag saved locally — account has no IMAP"
		m.isError = false
	}
	return m, nil
}

// withPendingTags overlays the wanted keyword state of in-flight tag toggles
// on a fetch result for account ("" = active), in place — a fetch that
// started before the STORE still carries the old keywords and must not snap
// the chips back (the withPendingSeen contract, applied to tags).
func (m Model) withPendingTags(account string, emails []imap.Email) []imap.Email {
	if len(m.tags.pending) == 0 {
		return emails
	}
	if account == "" {
		account = m.activeAccountName()
	}
	for i := range emails {
		e := &emails[i]
		for k, add := range m.tags.pending {
			if k.account == account && k.folder == e.Folder && k.uid == e.UID {
				e.Keywords = flipEmailKeywords(e.Keywords, k.keyword, add)
			}
		}
	}
	return emails
}

// ── chips ────────────────────────────────────────────────────────────────

// tagRuleView is a keyword's resolved display configuration (from
// [tags.<keyword>] in config.toml, with the defaults filled in). The colours
// are carried explicitly (not as a prebuilt style) so the pill caps can take
// the background directly — no getter round-trips.
type tagRuleView struct {
	show    bool
	display string
	fg, bg  lipgloss.Color
}

// bodyStyle is the chip body: display text on the chip colours. An empty bg
// (bg = "transparent") emits no background at all — the terminal's own
// background (and its transparency) shows through.
func (v tagRuleView) bodyStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(v.fg).Background(v.bg)
}

// edgeStyle is a pill cap: the half-disc glyph is FILLED with the foreground
// colour, so painting it in the chip's background merges it into the pill.
// With a transparent background the caps fall back to the text colour —
// there is no pill fill for them to blend into.
func (v tagRuleView) edgeStyle() lipgloss.Style {
	capColor := v.bg
	if capColor == "" {
		capColor = v.fg
	}
	return lipgloss.NewStyle().Foreground(capColor)
}

// tagRuleSource resolves one keyword to its display rule; nil (feature not
// wired in tests) = show under the keyword with the default chip style.
var tagRuleSource func(kw string) tagRuleView

func tagRule(kw string) tagRuleView {
	if tagRuleSource == nil {
		// No resolver wired (tests): the theme's reverse video, so fallback
		// chips keep their coloured padding.
		return tagRuleView{show: true, fg: colorBg, bg: colorText}
	}
	return tagRuleSource(kw)
}

// tagChipKeywords filters keywords down to the registry-known ones (in
// registry order) — only neomd's own custom tags chip; foreign keywords
// ($HasAttachment, $label1-5, …) never render. Returns keyword/display pairs
// with each tag's per-tag rule applied (hidden tags drop out).
func tagChipKeywords(keywords []string) []tagChip {
	if tagKeywordSource == nil {
		return nil
	}
	known := tagKeywordSource()
	var out []tagChip
	for _, k := range known {
		for _, kw := range keywords {
			if !strings.EqualFold(kw, k) {
				continue
			}
			if r := tagRule(k); r.show {
				out = append(out, tagChip{keyword: k, view: r})
			}
			break
		}
	}
	return out
}

// tagChip is one rendered chip: the keyword it represents plus its resolved
// display rule.
type tagChip struct {
	keyword string
	view    tagRuleView
}

// tagPillMode renders chips as nerd-font pills — half-sphere end caps
// coloured like the chip body ([tags] nerd_pill = true). Default off: the
// padded-text chip needs no special font.
var tagPillMode bool

// Pill end caps (Powerline extras): left and right half-discs. Rendered in
// the chip's background colour they merge into a rounded pill around the
// text; no padding spaces needed in this mode.
const (
	tagPillLeft  = "\ue0b6"
	tagPillRight = "\ue0b4"
)

// displayVerbatim is the pill body: the rule display after invisible-rune
// stripping (variation selectors U+FE0F, ZWJ, BOM — copied nerd-font icons
// often carry them and terminals then render the symbol with wide "emoji
// presentation" padding, a phantom space inside the pill), with the user's
// own leading/trailing spaces preserved — in pill mode they ARE the padding,
// typed on purpose. Falls back to the keyword when no display is set.
func (c tagChip) displayVerbatim() string {
	if d := stripInvisible(c.view.display); d != "" {
		return d
	}
	return c.keyword
}

// displayTrimmed is the whitespace-trimmed form for the fallback chip, which
// supplies exactly one standard pad space per side itself — display-defined
// spaces never double the fallback's padding.
func (c tagChip) displayTrimmed() string {
	if d := strings.TrimSpace(stripInvisible(c.view.display)); d != "" {
		return d
	}
	return c.keyword
}

// stripInvisible removes zero-width / combining runes (the same rune classes
// displaySafe collapses for row safety) — EXCEPT the text-presentation
// selector U+FE0E: appending it to an emoji-range symbol (⚡) forces the
// monochrome text font instead of the terminal's color-emoji fallback, which
// paints an opaque box behind the glyph that no transparent background can
// survive. VS16 (U+FE0F, emoji presentation) is still stripped.
func stripInvisible(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\ufe0e' { // keep: explicit text presentation
			b.WriteRune(r)
			continue
		}
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// text is the chip's plain fallback form: the display text with exactly one
// standard space on each side (the padding is part of the coloured chip, so
// chips read as labels). No block glyphs — the colour IS the chip. With a
// transparent background the pads are skipped: they would be invisible dead
// cells, so the chip renders as tight fg-coloured text.
func (c tagChip) text() string {
	d := c.displayTrimmed()
	if c.view.bg == "" {
		return d
	}
	return " " + d + " "
}

// measure is the plain form whose safe-width is the chip's width budget —
// identical to text() in fallback mode, the pill glyph form otherwise (the
// end caps are non-ASCII and therefore budgeted as 2 cells each; a pill adds
// NO padding of its own — spaces in the display string are honored verbatim).
func (c tagChip) measure() string {
	if tagPillMode {
		return tagPillLeft + c.displayVerbatim() + tagPillRight
	}
	return c.text()
}

func (c tagChip) styled() string {
	if tagPillMode {
		return c.view.edgeStyle().Render(tagPillLeft) +
			c.view.bodyStyle().Render(c.displayVerbatim()) +
			c.view.edgeStyle().Render(tagPillRight)
	}
	return c.view.bodyStyle().Render(c.text())
}

// tagChipsPlain joins the chips' plain text for width accounting — matches
// tagChipsStyled. Fallback: one uncoloured separator space between chips and
// one more before the subject. Pill mode: pills touch directly and sit flush
// against the subject (the caps ARE the delimiters).
func tagChipsPlain(chips []tagChip) string {
	if len(chips) == 0 {
		return ""
	}
	texts := make([]string, len(chips))
	for i, c := range chips {
		texts[i] = c.measure()
	}
	if tagPillMode {
		return strings.Join(texts, "")
	}
	return strings.Join(texts, " ") + " "
}

// tagChipsStyled is the rendered form of tagChipsPlain.
func tagChipsStyled(chips []tagChip) string {
	if len(chips) == 0 {
		return ""
	}
	styled := make([]string, len(chips))
	for i, c := range chips {
		styled[i] = c.styled()
	}
	if tagPillMode {
		return strings.Join(styled, "")
	}
	return strings.Join(styled, " ") + " "
}

// tagChipsWidth is the display width budget the chips need. Non-ASCII runes
// (nerd-font symbols in a display name, umlauts, CJK) are budgeted as 2
// cells: terminal fonts disagree on their real width, and over-reserving can
// only leave slack — under-reserving would overflow the row.
func tagChipsWidth(chips []tagChip) int {
	return tagSafeWidth(tagChipsPlain(chips))
}

// tagSafeWidth measures s with every non-ASCII rune budgeted as 2 cells.
func tagSafeWidth(s string) int {
	w := 0
	for _, r := range s {
		if r < 0x80 {
			w += runewidth.RuneWidth(r)
		} else {
			w += 2
		}
	}
	return w
}

// resolveTagColor turns a configured colour value into a lipgloss.Color:
// empty → fallback, "transparent"/"none" → no colour at all (terminals
// cannot render semi-transparent fills — an unset background is the only
// transparency, the terminal's own shows through), hex ("#1F1F28") or an
// ANSI number ("21") passes through, anything else (typo, colour name)
// falls back — lipgloss would silently drop it to no colour at all.
func resolveTagColor(val string, fallback lipgloss.Color) lipgloss.Color {
	val = strings.TrimSpace(val)
	switch {
	case val == "":
		return fallback
	case strings.EqualFold(val, "transparent"), strings.EqualFold(val, "none"):
		return lipgloss.Color("") // explicit no-colour, not the fallback
	case strings.HasPrefix(val, "#"), strconvItoa(val):
		return lipgloss.Color(val)
	default:
		return fallback
	}
}

// strconvItoa reports whether s is a plain decimal number.
func strconvItoa(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// validTagColor reports whether val is a usable colour value (empty,
// transparent, hex, or ANSI number) — anything else falls back and is worth
// a startup warning.
func validTagColor(val string) bool {
	val = strings.TrimSpace(val)
	if strings.EqualFold(val, "transparent") || strings.EqualFold(val, "none") {
		return true
	}
	return val == "" || strings.HasPrefix(val, "#") || strconvItoa(val)
}

// buildTagStyles resolves the chip styling from config: the default chip
// style (theme reverse, [tags] fg/bg override) and the per-tag rule resolver
// ([tags.<keyword>] display/fg/bg/enabled, falling back per field to the
// [tags] defaults). Returns a warning about unrecognized colour values, if any.
func buildTagStyles(cfg *config.TagsConfig, themeFG, themeBG lipgloss.Color) (lipgloss.Style, func(string) tagRuleView, string) {
	defaultFG := resolveTagColor(cfg.FG, themeFG)
	defaultBG := resolveTagColor(cfg.BG, themeBG)
	def := lipgloss.NewStyle().Foreground(defaultFG).Background(defaultBG)

	var warn []string
	if !validTagColor(cfg.FG) {
		warn = append(warn, "[tags] fg = "+strconv.Quote(cfg.FG))
	}
	if !validTagColor(cfg.BG) {
		warn = append(warn, "[tags] bg = "+strconv.Quote(cfg.BG))
	}
	for name, rule := range cfg.Rules {
		if !validTagColor(rule.FG) {
			warn = append(warn, "[tags."+name+"] fg = "+strconv.Quote(rule.FG))
		}
		if !validTagColor(rule.BG) {
			warn = append(warn, "[tags."+name+"] bg = "+strconv.Quote(rule.BG))
		}
	}

	resolve := func(kw string) tagRuleView {
		rule, ok := cfg.Rule(kw)
		if !ok {
			return tagRuleView{show: true, fg: defaultFG, bg: defaultBG}
		}
		return tagRuleView{
			show:    rule.Shown(),
			display: rule.Display,
			fg:      resolveTagColor(rule.FG, defaultFG),
			bg:      resolveTagColor(rule.BG, defaultBG),
		}
	}
	warning := ""
	if len(warn) > 0 {
		warning = "unknown tag colour (falling back): " + strings.Join(warn, ", ")
	}
	return def, resolve, warning
}

// ── picker view ──────────────────────────────────────────────────────────

func (m Model) viewTags() string {
	entries := m.filteredPickerTags()
	w := 60
	var lines []string
	lines = append(lines, styleHeader.Render(fmt.Sprintf("Tags — %d email%s", len(m.tags.targets), plural(len(m.tags.targets)))), "")

	rows := 12
	start := 0
	if m.tags.cursor >= rows {
		start = m.tags.cursor - rows + 1
	}
	end := start + rows
	if end > len(entries) {
		end = len(entries)
	}
	for i := start; i < end; i++ {
		kw := entries[i]
		mark := "  "
		if v := tagVerdict(m.tags.targets, kw); v != ' ' {
			mark = string(v) + " "
		}
		count := ""
		if m.tags.store != nil {
			if n := m.tags.store.Count(m.activeAccountKey(), kw); n > 0 {
				count = fmt.Sprintf("(%d)", n)
			}
		}
		// Tags with a display value get a live chip preview, right-aligned
		// in a FIXED column at the row's right edge — rendered through the
		// same pill/fallback path and config colours as the inbox row. The
		// keyword and count columns are fixed too, so removing a tag (count
		// disappears) never shifts the preview.
		var previewField string
		if rule := tagRule(kw); strings.TrimSpace(stripInvisible(rule.display)) != "" {
			if tagSafeWidth(tagChip{keyword: kw, view: rule}.measure()) > 18 {
				rule.display = runewidth.Truncate(stripInvisible(rule.display), 16, "…") // keep the dialog tidy
			}
			previewField = lipgloss.NewStyle().Width(26).
				Render(tagChip{keyword: kw, view: rule}.styled())
		}
		countCol := ""
		if count != "" {
			countCol = count
		}
		kwCol := runewidth.Truncate(kw, 20, "…")
		line := fmt.Sprintf("  %s%-20s%-6s%s", mark, kwCol, countCol, previewField)
		if i == m.tags.cursor {
			line = styleSelected.Render("▸" + line[1:])
		}
		lines = append(lines, line)
	}
	if len(entries) == 0 {
		lines = append(lines, styleHelp.Render("  no tags yet — press a to add one"), "")
	}

	if m.tags.err != "" {
		lines = append(lines, " "+styleError.Render(truncate(m.tags.err, w-6)))
	}
	if m.tags.inputActive {
		lines = append(lines, "", styleInputLabel.Render("new tag:")+" "+m.tags.input+"█")
	} else if m.tags.filterActive {
		lines = append(lines, "", styleInputLabel.Render("filter:")+" "+m.tags.filter+"█")
	}
	lines = append(lines, "", styleHelp.Render(" enter/space toggle · a new tag · / filter · esc close"))

	// Border follows the active theme (built at render time, like the row styles).
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder).
		Padding(1, 2)
	box := boxStyle.Width(w).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// setTagAccount points the package-level chip pipeline at the active account
// (the registry is per-account; the delegate cannot see the Model).
func setTagAccount(name string) { tagActiveAccount = name }
