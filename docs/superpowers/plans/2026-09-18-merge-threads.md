# Merge Threads Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the user merge unrelated emails (e.g. mailer-daemon bounces) into one titled, collapsible row that opens as a cross-folder thread view, HEY-style.

**Architecture:** A new pure package `internal/merge` persists `title → sender rule + Message-IDs` in `~/.config/neomd/merges.toml`. A pure `collapseMerges` pass in `internal/ui/thread.go` runs after the existing threading and replaces members (plus any automatic thread they sit in) with a single `≡` row. Opening the row reuses the off-tab view mechanism (`offTabFolder`) that Thread/Sender views already use, backed by one new IMAP search that ORs `HEADER Message-ID` and `HEADER In-Reply-To` for every stored id.

**Tech Stack:** Go 1.22+, bubbletea v1.3.10, go-imap/v2, BurntSushi/toml v1.6.0.

**Spec:** `docs/superpowers/specs/2026-09-18-merge-threads-design.md`

## Global Constraints

- Keep diffs minimal; do not refactor adjacent code (CLAUDE.md).
- No new modifier-key bindings. Enter, `l`, `T` on a collapsed row only; commands are `:merge <title>`, `:merge-sender <title>`, `:unmerge`.
- Never bare `go func()` in `internal/ui` — use `safeGo()`.
- `T` on a regular (non-collapsed) email keeps today's behaviour exactly.
- Automatic threading output (`threadEmails`) is unchanged; collapsing is a separate pass.
- Store key is Message-ID, never UID. Emails without a Message-ID are skipped with a count in the status line.
- Absorbed replies (automatic thread mates of a member) are display-only; they are not written to the store unless the user runs `:merge` on the collapsed row.
- Every user-visible change: `AGENTS.md` invariant entry + `CHANGELOG.md` entry with regression test names + `make docs`.
- Work on branch `email-threads-manual`. Commit after every task.
- Verify with `go test ./...` and `go vet ./...` before each commit.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/merge/store.go` (new) | TOML-backed store: load/save, add/remove/dissolve, title lookup, sender rule match. Pure, no IMAP, nil-receiver safe. |
| `internal/merge/store_test.go` (new) | Store tests. |
| `internal/config/config.go` | `Config.MergesFile` set in `Load()` next to `OOOFile`. |
| `internal/ui/thread.go` | `mergeRow`, `threadedEmail.merge`, `collapseMerges()`. |
| `internal/ui/thread_test.go` (new) | `collapseMerges` tests. |
| `internal/ui/inbox.go` | `emailItem.merge`, collapsed-row rendering, `FilterValue`, `setEmails` gains `titleOf`. |
| `internal/ui/inbox_test.go` | Rendering test for the collapsed row. |
| `internal/imap/client.go` | `SearchByMessageIDs`, `messageIDCriteria`, `orCriteria`, shared `searchFolder`. |
| `internal/imap/client_test.go` or new `search_criteria_test.go` | Criteria-building tests. |
| `internal/ui/search.go` | `mergeResultMsg`, `fetchMergeCmd`, `handleMergeResult`. |
| `internal/ui/model.go` | Store on the model, `selectedItem`, `targetEmails` expansion, `m`/enter/`l`/`T` dispatch, sender rules on load, `pendingUnmerge` y/n, cmd args, title tab-completion, `inMergeView`. |
| `internal/ui/cmdline.go` | `runArgs` field, `splitCmdInput`, `:merge`, `:merge-sender`, `:unmerge`. |
| `internal/ui/cmdline_test.go`, `internal/ui/model_test.go` | Command and model tests. |
| `internal/ui/keys.go`, `AGENTS.md`, `CHANGELOG.md` | Docs. |

---

### Task 1: `internal/merge` store

**Files:**
- Create: `internal/merge/store.go`
- Create: `internal/merge/store_test.go`

**Interfaces:**
- Produces:
  ```go
  package merge
  type Merge struct { Title string; Sender string; MessageIDs []string }
  func Load(path string) (*Store, error)        // missing file → empty store, nil error
  func (s *Store) Save() error                    // atomic: tmp file + rename, 0600
  func (s *Store) Add(title string, ids ...string) int   // creates merge if missing; returns count of ids newly added
  func (s *Store) SetSender(title, addr string)   // creates merge if missing
  func (s *Store) Remove(id string) bool          // drop one id from whichever merge holds it
  func (s *Store) Dissolve(title string) bool
  func (s *Store) TitleOf(id string) (string, bool)
  func (s *Store) IDs(title string) []string      // copy
  func (s *Store) MatchSender(from string) (string, bool)
  func (s *Store) Titles() []string               // sorted
  ```
  All methods are safe on a nil `*Store` (return zero values / no-op, `Save` returns nil).

- [ ] **Step 1: Write the failing tests**

```go
// internal/merge/store_test.go
package merge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "merges.toml")
}

func TestLoad_MissingFileIsEmpty(t *testing.T) {
	s, err := Load(tmpPath(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.Titles(); len(got) != 0 {
		t.Errorf("Titles() = %v, want empty", got)
	}
}

func TestAddSaveLoad_RoundTrip(t *testing.T) {
	p := tmpPath(t)
	s, _ := Load(p)
	if n := s.Add("Bounces", "<a@x>", "<b@x>"); n != 2 {
		t.Fatalf("Add returned %d, want 2", n)
	}
	s.SetSender("Bounces", "mailer-daemon@")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s2, err := Load(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if title, ok := s2.TitleOf("<b@x>"); !ok || title != "Bounces" {
		t.Errorf("TitleOf(<b@x>) = %q,%v; want Bounces,true", title, ok)
	}
	if ids := s2.IDs("Bounces"); len(ids) != 2 {
		t.Errorf("IDs = %v, want 2 entries", ids)
	}
	if title, ok := s2.MatchSender("Mailer-Daemon <MAILER-DAEMON@mx.example.com>"); !ok || title != "Bounces" {
		t.Errorf("MatchSender = %q,%v; want Bounces,true", title, ok)
	}
	// No temp file left behind.
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
	info, _ := os.Stat(p)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}
}

func TestAdd_DeduplicatesAndTrims(t *testing.T) {
	s, _ := Load(tmpPath(t))
	s.Add(" Bounces ", "<a@x>")
	if n := s.Add("Bounces", "<a@x>", "", "<c@x>"); n != 1 {
		t.Errorf("second Add returned %d, want 1 (only <c@x> is new; empty id skipped)", n)
	}
	if ids := s.IDs("Bounces"); len(ids) != 2 {
		t.Errorf("IDs = %v, want [<a@x> <c@x>]", ids)
	}
	if titles := s.Titles(); len(titles) != 1 || titles[0] != "Bounces" {
		t.Errorf("Titles = %v, want [Bounces]", titles)
	}
}

func TestRemoveAndDissolve(t *testing.T) {
	s, _ := Load(tmpPath(t))
	s.Add("A", "<1>", "<2>")
	s.Add("B", "<3>")
	if !s.Remove("<1>") {
		t.Error("Remove(<1>) = false, want true")
	}
	if s.Remove("<nope>") {
		t.Error("Remove(<nope>) = true, want false")
	}
	if _, ok := s.TitleOf("<1>"); ok {
		t.Error("<1> still resolves after Remove")
	}
	if !s.Dissolve("B") {
		t.Error("Dissolve(B) = false")
	}
	if _, ok := s.TitleOf("<3>"); ok {
		t.Error("<3> still resolves after Dissolve")
	}
	if titles := s.Titles(); len(titles) != 1 || titles[0] != "A" {
		t.Errorf("Titles = %v, want [A]", titles)
	}
}

func TestMatchSender_NoRuleOrNoMatch(t *testing.T) {
	s, _ := Load(tmpPath(t))
	s.Add("NoRule", "<1>")
	if _, ok := s.MatchSender("someone@example.com"); ok {
		t.Error("matched without any rule")
	}
	s.SetSender("Bounces", "mailer-daemon@")
	if _, ok := s.MatchSender("alice@example.com"); ok {
		t.Error("matched non-matching sender")
	}
}

func TestNilStoreIsSafe(t *testing.T) {
	var s *Store
	if _, ok := s.TitleOf("<1>"); ok {
		t.Error("nil TitleOf ok=true")
	}
	if _, ok := s.MatchSender("x@y"); ok {
		t.Error("nil MatchSender ok=true")
	}
	if s.Titles() != nil || s.IDs("x") != nil {
		t.Error("nil Titles/IDs non-nil")
	}
	if s.Add("x", "<1>") != 0 || s.Remove("<1>") || s.Dissolve("x") || s.Save() != nil {
		t.Error("nil mutators should be no-ops")
	}
	s.SetSender("x", "y") // must not panic
}

func TestLoad_BadTOMLIsError(t *testing.T) {
	p := tmpPath(t)
	os.WriteFile(p, []byte("[[merges]\ntitle = 1\n"), 0o600)
	if _, err := Load(p); err == nil {
		t.Error("expected error for malformed TOML")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/merge/ 2>&1 | head -5`
Expected: build failure, `undefined: Load` (package has no non-test file yet).

- [ ] **Step 3: Write the implementation**

```go
// internal/merge/store.go
// Package merge persists user-defined "merged threads" (HEY-style): a title
// groups unrelated emails by Message-ID, optionally with a sender rule so
// future mail from that sender joins automatically. The file lives next to
// config.toml so it can be kept in dotfiles.
package merge

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
)

// Merge is one titled group.
type Merge struct {
	Title      string   `toml:"title"`
	Sender     string   `toml:"sender,omitempty"` // case-insensitive substring of the bare From address
	MessageIDs []string `toml:"message_ids"`
}

type fileFormat struct {
	Merges []Merge `toml:"merges"`
}

// Store is a concurrency-safe in-memory copy of merges.toml.
// All methods are safe on a nil receiver.
type Store struct {
	mu     sync.Mutex
	path   string
	merges []Merge
}

// Load reads path. A missing file yields an empty store; malformed TOML is an error.
func Load(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	var f fileFormat
	if _, err := toml.Decode(string(data), &f); err != nil {
		return nil, err
	}
	s.merges = f.Merges
	return s, nil
}

// Save writes the store atomically (temp file + rename, mode 0600).
func (s *Store) Save() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(fileFormat{Merges: s.merges}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// find returns the index of the merge with title, or -1. Caller holds mu.
func (s *Store) find(title string) int {
	for i := range s.merges {
		if s.merges[i].Title == title {
			return i
		}
	}
	return -1
}

// ensure returns the index of the merge with title, creating it if missing. Caller holds mu.
func (s *Store) ensure(title string) int {
	if i := s.find(title); i >= 0 {
		return i
	}
	s.merges = append(s.merges, Merge{Title: title})
	return len(s.merges) - 1
}

// Add puts ids into the merge titled title (created if missing) and
// returns how many were new. Empty ids and duplicates are skipped.
func (s *Store) Add(title string, ids ...string) int {
	if s == nil {
		return 0
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.ensure(title)
	have := make(map[string]bool, len(s.merges[i].MessageIDs))
	for _, id := range s.merges[i].MessageIDs {
		have[id] = true
	}
	added := 0
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || have[id] {
			continue
		}
		have[id] = true
		s.merges[i].MessageIDs = append(s.merges[i].MessageIDs, id)
		added++
	}
	return added
}

// SetSender stores the sender rule for title (created if missing).
func (s *Store) SetSender(title, addr string) {
	if s == nil {
		return
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.ensure(title)
	s.merges[i].Sender = strings.ToLower(strings.TrimSpace(addr))
}

// Remove drops id from whichever merge holds it.
func (s *Store) Remove(id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.merges {
		ids := s.merges[i].MessageIDs
		for j, have := range ids {
			if have == id {
				s.merges[i].MessageIDs = append(ids[:j:j], ids[j+1:]...)
				return true
			}
		}
	}
	return false
}

// Dissolve deletes the whole merge (ids and rule).
func (s *Store) Dissolve(title string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(strings.TrimSpace(title))
	if i < 0 {
		return false
	}
	s.merges = append(s.merges[:i:i], s.merges[i+1:]...)
	return true
}

// TitleOf returns the merge title holding id.
func (s *Store) TitleOf(id string) (string, bool) {
	if s == nil || id == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.merges {
		for _, have := range m.MessageIDs {
			if have == id {
				return m.Title, true
			}
		}
	}
	return "", false
}

// IDs returns a copy of the Message-IDs stored for title.
func (s *Store) IDs(title string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.find(strings.TrimSpace(title))
	if i < 0 {
		return nil
	}
	return append([]string(nil), s.merges[i].MessageIDs...)
}

// MatchSender returns the first merge whose sender rule is a substring of
// the bare, lower-cased address in from.
func (s *Store) MatchSender(from string) (string, bool) {
	if s == nil {
		return "", false
	}
	addr := bareAddr(from)
	if addr == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.merges {
		if m.Sender != "" && strings.Contains(addr, m.Sender) {
			return m.Title, true
		}
	}
	return "", false
}

// Titles returns all merge titles, sorted.
func (s *Store) Titles() []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.merges))
	for _, m := range s.merges {
		out = append(out, m.Title)
	}
	sort.Strings(out)
	return out
}

// bareAddr extracts "addr" from "Name <addr>" (or returns the input) lower-cased.
func bareAddr(from string) string {
	from = strings.TrimSpace(from)
	if i := strings.IndexByte(from, '<'); i >= 0 {
		if j := strings.IndexByte(from, '>'); j > i {
			from = from[i+1 : j]
		}
	}
	return strings.ToLower(strings.TrimSpace(from))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/merge/ -v 2>&1 | tail -15`
Expected: all `TestLoad_*`, `TestAdd*`, `TestRemoveAndDissolve`, `TestMatchSender_*`, `TestNilStoreIsSafe` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/merge/
git commit -m "merge: add TOML-backed store for merged threads

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: `Config.MergesFile`

**Files:**
- Modify: `internal/config/config.go` (struct near line 384, `Load()` near line 611)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `cfg.MergesFile string` — absolute path `<dir of config.toml>/merges.toml`, set during `Load()`.

- [ ] **Step 1: Write the failing test**

Append to `internal/config/config_test.go`:

```go
func TestLoad_SetsMergesFileNextToConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "neomd", "config.toml")
	// Load creates a placeholder config and returns a "please fill in" error;
	// we only need the side effect of a parsable file, so write a minimal valid one.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	minimal := "[[accounts]]\nname = \"t\"\nimap_host = \"imap.example.com:993\"\nsmtp_host = \"smtp.example.com:465\"\nuser = \"u@example.com\"\npassword = \"pw\"\nfrom = \"u@example.com\"\n"
	if err := os.WriteFile(path, []byte(minimal), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := filepath.Join(dir, "neomd", "merges.toml")
	if cfg.MergesFile != want {
		t.Errorf("MergesFile = %q, want %q", cfg.MergesFile, want)
	}
}
```

If the minimal TOML above fails validation, copy the account block from the existing default template in `defaults()`/`writeDefault` (see `TestWriteDefault_FilePermissions`) and replace the placeholder values; the assertion is only on `MergesFile`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad_SetsMergesFileNextToConfig 2>&1 | head -5`
Expected: compile error `cfg.MergesFile undefined`.

- [ ] **Step 3: Add the field and set it in Load**

In the `Config` struct, directly after `OOOFile string \`toml:"-"\``:

```go
	// MergesFile is <config dir>/merges.toml — user-defined merged threads
	// (see internal/merge). Set during Load(), not a TOML field.
	MergesFile string `toml:"-"`
```

In `Load()`, directly after the line `cfg.OOOFile = filepath.Join(filepath.Dir(path), "ooo.toml")`:

```go
	cfg.MergesFile = filepath.Join(filepath.Dir(path), "merges.toml")
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ 2>&1 | tail -3`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "config: MergesFile path next to config.toml

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: `collapseMerges` pure pass

**Files:**
- Modify: `internal/ui/thread.go` (`threadedEmail` at line 92; append new code at end of file)
- Create: `internal/ui/thread_test.go`

**Interfaces:**
- Consumes: `compareEmails(a, b imap.Email, sortField string) int`, `threadedEmail{email, threadPrefix}`.
- Produces:
  ```go
  type mergeRow struct { title string; members []imap.Email } // members newest first
  // threadedEmail gains: merge *mergeRow
  func collapseMerges(rows []threadedEmail, titleOf func(string) (string, bool), sortField string, sortReverse bool) []threadedEmail
  ```
  When `titleOf` is nil, `rows` is returned unchanged.

- [ ] **Step 1: Write the failing tests**

```go
// internal/ui/thread_test.go
package ui

import (
	"testing"
	"time"

	"github.com/sspaeti/neomd/internal/imap"
)

func mkEmail(uid uint32, msgID, subject, from string, daysAgo int, seen bool) imap.Email {
	return imap.Email{
		UID: uid, MessageID: msgID, Subject: subject, From: from,
		Date: time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC).AddDate(0, 0, -daysAgo),
		Seen: seen, Size: 100,
	}
}

func titleMap(m map[string]string) func(string) (string, bool) {
	return func(id string) (string, bool) { t, ok := m[id]; return t, ok }
}

func TestCollapseMerges_NilTitleOfIsNoop(t *testing.T) {
	rows := flatEmails([]imap.Email{mkEmail(1, "<a>", "x", "a@x", 0, true)}, "date", true)
	got := collapseMerges(rows, nil, "date", true)
	if len(got) != 1 || got[0].merge != nil {
		t.Fatalf("expected passthrough, got %+v", got)
	}
}

func TestCollapseMerges_Basic(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 5, true),
		mkEmail(2, "<keep>", "Invoice", "acme@x", 3, true),
		mkEmail(3, "<b2>", "Undelivered", "mailer-daemon@x", 1, false),
	}
	rows := threadEmails(emails, "date", true) // newest first
	got := collapseMerges(rows, titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces"}), "date", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (collapsed + Invoice); rows: %+v", len(got), got)
	}
	// Newest member (uid 3, 1 day ago) is newer than Invoice (3 days) → merged row first.
	if got[0].merge == nil || got[0].merge.title != "Bounces" {
		t.Fatalf("row 0 should be the merge, got %+v", got[0])
	}
	if got[0].email.UID != 3 {
		t.Errorf("representative UID = %d, want 3 (newest member)", got[0].email.UID)
	}
	if len(got[0].merge.members) != 2 || got[0].merge.members[0].UID != 3 || got[0].merge.members[1].UID != 1 {
		t.Errorf("members = %+v, want [uid3 uid1] newest first", got[0].merge.members)
	}
	if got[1].email.UID != 2 || got[1].merge != nil {
		t.Errorf("row 1 should be plain Invoice, got %+v", got[1])
	}
}

func TestCollapseMerges_SortPositionOldestFirst(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 5, true),
		mkEmail(2, "<keep>", "Invoice", "acme@x", 3, true),
		mkEmail(3, "<b2>", "Undelivered", "mailer-daemon@x", 1, false),
	}
	rows := threadEmails(emails, "date", false) // oldest first
	got := collapseMerges(rows, titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces"}), "date", false)
	// Representative is still the newest member (1 day ago) → sorts after Invoice (3 days ago).
	if got[0].email.UID != 2 || got[1].merge == nil {
		t.Errorf("want [Invoice, merge], got [%d, merge=%v]", got[0].email.UID, got[1].merge != nil)
	}
}

func TestCollapseMerges_SingleMemberStillCollapses(t *testing.T) {
	emails := []imap.Email{mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 0, true)}
	got := collapseMerges(flatEmails(emails, "date", true), titleMap(map[string]string{"<b1>": "Bounces"}), "date", true)
	if len(got) != 1 || got[0].merge == nil || len(got[0].merge.members) != 1 {
		t.Fatalf("single member should still collapse, got %+v", got)
	}
}

func TestCollapseMerges_AbsorbsAutomaticThread(t *testing.T) {
	// <b1> is a member; <reply> is an In-Reply-To child of <b1> → whole thread joins.
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 5, true),
		{UID: 2, MessageID: "<reply>", InReplyTo: "<b1>", Subject: "Re: Undelivered", From: "me@x",
			Date: time.Date(2026, 9, 16, 0, 0, 0, 0, time.UTC), Seen: true, Size: 10},
		mkEmail(3, "<keep>", "Invoice", "acme@x", 3, true),
	}
	rows := threadEmails(emails, "date", true)
	got := collapseMerges(rows, titleMap(map[string]string{"<b1>": "Bounces"}), "date", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; rows %+v", len(got), got)
	}
	var mr *mergeRow
	for _, r := range got {
		if r.merge != nil {
			mr = r.merge
		}
	}
	if mr == nil || len(mr.members) != 2 {
		t.Fatalf("expected merge with 2 members (b1 + absorbed reply), got %+v", mr)
	}
	if mr.members[0].UID != 2 {
		t.Errorf("newest member should be the reply (uid 2), got %d", mr.members[0].UID)
	}
}

func TestCollapseMerges_TwoMergesInOneFolder(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 4, true),
		mkEmail(2, "<n1>", "Digest", "news@x", 2, true),
		mkEmail(3, "<b2>", "Undelivered", "mailer-daemon@x", 1, true),
		mkEmail(4, "<n2>", "Digest", "news@x", 0, true),
	}
	got := collapseMerges(threadEmails(emails, "date", true),
		titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces", "<n1>": "News", "<n2>": "News"}), "date", true)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].merge == nil || got[0].merge.title != "News" || got[1].merge == nil || got[1].merge.title != "Bounces" {
		t.Errorf("want [News, Bounces], got [%v, %v]", got[0].merge, got[1].merge)
	}
}

func TestCollapseMerges_ThreadingDisabledStillCollapses(t *testing.T) {
	emails := []imap.Email{
		mkEmail(1, "<b1>", "Undelivered", "mailer-daemon@x", 2, true),
		mkEmail(2, "<b2>", "Undelivered", "mailer-daemon@x", 1, true),
	}
	got := collapseMerges(flatEmails(emails, "date", true), titleMap(map[string]string{"<b1>": "Bounces", "<b2>": "Bounces"}), "date", true)
	if len(got) != 1 || got[0].merge == nil || len(got[0].merge.members) != 2 {
		t.Fatalf("flat rows should collapse too, got %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run TestCollapseMerges 2>&1 | head -5`
Expected: compile error `undefined: collapseMerges` / `got[0].merge undefined`.

- [ ] **Step 3: Implement**

Change the struct at `internal/ui/thread.go:92`:

```go
// threadedEmail pairs an email with its tree-drawing prefix for the inbox list.
type threadedEmail struct {
	email        imap.Email
	threadPrefix string    // "│" = continuation, "╰" = root, "" = not threaded
	merge        *mergeRow // non-nil = this row stands for a user-merged group
}

// mergeRow is a collapsed, user-titled group of emails (HEY-style "merge
// threads"). email on the owning threadedEmail is the newest member.
type mergeRow struct {
	title   string
	members []imap.Email // present in the current list, newest first
}
```

Append to the end of `internal/ui/thread.go`:

```go
// collapseMerges replaces every row whose Message-ID belongs to a user merge
// (titleOf → title, true) with one collapsed row per merge. Rows are grouped
// into blocks first — an automatic thread (a run of "│"… rows ending in "╰")
// is one block, every other row is its own block — and a block joins a merge
// when ANY of its emails is a member, so replies to a merged mail are
// absorbed. The collapsed row sorts by its newest member under the caller's
// sort field/direction, exactly like threadEmails sorts a thread by its
// newest message. A nil titleOf returns rows unchanged.
func collapseMerges(rows []threadedEmail, titleOf func(string) (string, bool), sortField string, sortReverse bool) []threadedEmail {
	if titleOf == nil || len(rows) == 0 {
		return rows
	}

	// 1. Split rows into blocks.
	var blocks [][]threadedEmail
	for i := 0; i < len(rows); {
		if rows[i].threadPrefix == "" {
			blocks = append(blocks, rows[i:i+1])
			i++
			continue
		}
		j := i
		for j < len(rows) && rows[j].threadPrefix != "" {
			j++
			if rows[j-1].threadPrefix == "╰" {
				break
			}
		}
		blocks = append(blocks, rows[i:j])
		i = j
	}

	// 2. Assign blocks to merges (first matching member wins).
	type entry struct {
		rep   imap.Email // sort representative
		rows  []threadedEmail
		merge *mergeRow
	}
	var entries []entry
	byTitle := map[string]*mergeRow{}
	var order []string // titles in first-seen order
	merged := false
	for _, b := range blocks {
		title := ""
		for _, r := range b {
			if t, ok := titleOf(r.email.MessageID); ok {
				title = t
				break
			}
		}
		if title == "" {
			entries = append(entries, entry{rep: b[0].email, rows: b})
			continue
		}
		merged = true
		mr := byTitle[title]
		if mr == nil {
			mr = &mergeRow{title: title}
			byTitle[title] = mr
			order = append(order, title)
		}
		for _, r := range b {
			mr.members = append(mr.members, r.email)
		}
	}
	if !merged {
		return rows
	}

	// 3. One entry per merge, members newest first, representative = newest.
	for _, title := range order {
		mr := byTitle[title]
		sort.SliceStable(mr.members, func(i, j int) bool {
			return mr.members[i].Date.After(mr.members[j].Date)
		})
		rep := mr.members[0]
		entries = append(entries, entry{rep: rep, rows: []threadedEmail{{email: rep, merge: mr}}, merge: mr})
	}

	// 4. Stable sort so untouched blocks keep their order and merged rows
	//    land where their newest member would.
	sort.SliceStable(entries, func(i, j int) bool {
		cmp := compareEmails(entries[i].rep, entries[j].rep, sortField)
		if sortReverse {
			return cmp > 0
		}
		return cmp < 0
	})

	out := make([]threadedEmail, 0, len(rows))
	for _, e := range entries {
		out = append(out, e.rows...)
	}
	return out
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/ -run 'TestCollapseMerges|TestNormalizeSubject|TestReplyIndicator' -v 2>&1 | tail -20`
Expected: all PASS. If `TestCollapseMerges_Basic` fails on ordering, check that `threadEmails` with `sortReverse=true` yields newest first (it does: `cmp > 0`).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/thread.go internal/ui/thread_test.go
git commit -m "ui: collapseMerges pass for user-merged threads

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: Collapsed row in the list (`emailItem`, render, `setEmails`)

**Files:**
- Modify: `internal/ui/inbox.go` (`emailItem` line 19, `FilterValue` line 28, `Render` lines 57-135, `setEmails` line 388)
- Modify: `internal/ui/model.go:3679` (the single `setEmails` call in `applyFilter`)
- Test: `internal/ui/inbox_test.go`

**Interfaces:**
- Consumes: `mergeRow`, `collapseMerges` (Task 3).
- Produces:
  ```go
  // emailItem gains: merge *mergeRow
  func setEmails(l *list.Model, emails []imap.Email, marked map[uint32]bool, spyPixels map[string]bool,
      prefixFolders bool, sortField string, sortReverse bool, disableThreading bool,
      titleOf func(string) (string, bool)) tea.Cmd
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/ui/inbox_test.go`:

```go
func TestRenderCollapsedMergeRow(t *testing.T) {
	newest := imap.Email{UID: 9, From: "Mailer-Daemon <mailer-daemon@x>", Subject: "Undelivered", Date: time.Now(), Seen: true, Size: 512}
	older := imap.Email{UID: 3, From: "Mailer-Daemon <mailer-daemon@x>", Subject: "Undelivered", Date: time.Now().Add(-48 * time.Hour), Seen: false, Answered: true, Size: 256}
	item := emailItem{
		email: newest, index: 1,
		merge: &mergeRow{title: "Bounces", members: []imap.Email{newest, older}},
	}
	row := renderRow(item, 100)
	for _, want := range []string{"≡", "Bounces (2)", "N", "·"} {
		if !strings.Contains(row, want) {
			t.Errorf("collapsed row missing %q: %s", want, row)
		}
	}
	if strings.Contains(row, "Undelivered") {
		t.Errorf("collapsed row should show the title, not a member subject: %s", row)
	}
	if fv := item.FilterValue(); !strings.Contains(fv, "Bounces") || !strings.Contains(fv, "Undelivered") {
		t.Errorf("FilterValue should include title and member subjects, got %q", fv)
	}
}

func TestSetEmails_CollapsesMembers(t *testing.T) {
	emails := []imap.Email{
		{UID: 1, MessageID: "<b1>", Subject: "Undelivered", From: "d@x", Date: time.Now().Add(-2 * time.Hour), Seen: true},
		{UID: 2, MessageID: "<b2>", Subject: "Undelivered", From: "d@x", Date: time.Now().Add(-1 * time.Hour), Seen: true},
		{UID: 3, MessageID: "<k>", Subject: "Keep", From: "k@x", Date: time.Now(), Seen: true},
	}
	l := list.New(nil, emailDelegate{}, 100, 10)
	titleOf := func(id string) (string, bool) {
		if id == "<b1>" || id == "<b2>" {
			return "Bounces", true
		}
		return "", false
	}
	setEmails(&l, emails, map[uint32]bool{}, map[string]bool{}, false, "date", true, false, titleOf)
	if n := len(l.Items()); n != 2 {
		t.Fatalf("items = %d, want 2", n)
	}
	it := l.Items()[1].(emailItem)
	if it.merge == nil || it.merge.title != "Bounces" || it.index != 2 {
		t.Errorf("second item should be the Bounces merge with index 2, got %+v", it)
	}
	// Without titleOf nothing collapses.
	setEmails(&l, emails, map[uint32]bool{}, map[string]bool{}, false, "date", true, false, nil)
	if n := len(l.Items()); n != 3 {
		t.Errorf("items without titleOf = %d, want 3", n)
	}
}
```

Ensure `inbox_test.go` imports `github.com/charmbracelet/bubbles/list` (it already does, for `renderRow`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestRenderCollapsedMergeRow|TestSetEmails_CollapsesMembers' 2>&1 | head -5`
Expected: compile errors (`merge` field unknown, too many arguments to `setEmails`).

- [ ] **Step 3: Implement**

`emailItem` and `FilterValue` (replace lines 19-30):

```go
type emailItem struct {
	email        imap.Email
	index        int       // position in list (1-based)
	marked       bool      // selected for batch operation
	displaySubj  string    // rendered subject (may include folder prefix in temporary views)
	threadPrefix string    // tree chars e.g. "┌─>" for threaded display
	hasSpyPixel  bool      // tracking pixels were detected when body was loaded
	merge        *mergeRow // non-nil: collapsed user-merged group; email is its newest member
}

func (e emailItem) FilterValue() string {
	if e.merge != nil {
		parts := []string{e.merge.title}
		for _, m := range e.merge.members {
			parts = append(parts, m.From, m.Subject)
		}
		return strings.Join(parts, " ")
	}
	return e.email.From + " " + e.email.Subject
}
```

(`strings` is already imported in inbox.go; if not, add it.)

In `Render`, right after `isSelected := index == m.Index()`, replace `unread := !e.email.Seen` with an aggregation over members. `marked` for a merge row is computed in `setEmails` (below) and stored in `e.marked`, so only unread/answered need aggregation here:

```go
	unread := !e.email.Seen
	answered := e.email.Answered
	if e.merge != nil {
		unread, answered = false, false
		for _, mem := range e.merge.members {
			unread = unread || !mem.Seen
			answered = answered || mem.Answered
		}
	}
```

Then change the existing `switch` to use `unread` for both `*N` and `N ` cases (it already uses `e.marked` and `!e.email.Seen`; replace `!e.email.Seen` with `unread`), set `replyStr = "·"` when `answered` (instead of `e.email.Answered`), and for the thread column:

```go
	threadStr := "  "
	if e.merge != nil {
		threadStr = "≡ "
	} else if e.threadPrefix != "" {
		threadStr = e.threadPrefix + " "
	}
```

And for the subject:

```go
	subjectText := e.email.Subject
	if e.displaySubj != "" {
		subjectText = e.displaySubj
	}
	if e.merge != nil {
		subjectText = fmt.Sprintf("%s (%d)", e.merge.title, len(e.merge.members))
	}
	subjectText = sendLaterPrefix(e.email) + subjectText
```

`setEmails` (replace the whole function):

```go
func setEmails(l *list.Model, emails []imap.Email, marked map[uint32]bool, spyPixels map[string]bool, prefixFolders bool, sortField string, sortReverse bool, disableThreading bool, titleOf func(string) (string, bool)) tea.Cmd {
	var threaded []threadedEmail
	if disableThreading {
		threaded = flatEmails(emails, sortField, sortReverse)
	} else {
		threaded = threadEmails(emails, sortField, sortReverse)
	}
	threaded = collapseMerges(threaded, titleOf, sortField, sortReverse)
	items := make([]list.Item, len(threaded))
	for i, te := range threaded {
		displaySubj := te.email.Subject
		if prefixFolders {
			displaySubj = "[" + te.email.Folder + "] " + displaySubj
		}
		isMarked := marked[te.email.UID]
		if te.merge != nil {
			// A collapsed row counts as marked only when every member is.
			isMarked = len(te.merge.members) > 0
			for _, mem := range te.merge.members {
				isMarked = isMarked && marked[mem.UID]
			}
		}
		items[i] = emailItem{
			email:        te.email,
			index:        i + 1,
			marked:       isMarked,
			displaySubj:  displaySubj,
			threadPrefix: te.threadPrefix,
			hasSpyPixel:  spyPixels[spyPixelKey(te.email.Folder, te.email.UID)],
			merge:        te.merge,
		}
	}
	return l.SetItems(items)
}
```

In `internal/ui/model.go` `applyFilter()` change the last line to pass `nil` for now (Task 6 wires the real store):

```go
	return setEmails(&m.inbox, filtered, m.markedUIDs, m.spyPixelKeys, m.shouldPrefixFolderInSubject(), m.sortField, m.sortReverse, noThread, nil)
```

Fix any other `setEmails(` callers reported by the compiler (grep shows only this one plus tests).

- [ ] **Step 4: Run tests**

Run: `go build ./... && go test ./internal/ui/ 2>&1 | tail -5`
Expected: `ok`. Existing `TestReplyIndicator*` must still pass (they don't set `merge`).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/inbox.go internal/ui/inbox_test.go internal/ui/model.go
git commit -m "ui: render collapsed merge rows in the inbox list

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: IMAP `SearchByMessageIDs`

**Files:**
- Modify: `internal/imap/client.go` (`SearchMessages` at line 533, add new funcs after `SearchAllFolders`)
- Create: `internal/imap/search_criteria_test.go`

**Interfaces:**
- Consumes: `c.withConnRetry`, `c.selectMailbox`, `c.FetchHeadersByUID`.
- Produces:
  ```go
  func (c *Client) SearchByMessageIDs(ctx context.Context, folders []string, ids []string) ([]Email, error)
  func messageIDCriteria(ids []string) *imap.SearchCriteria   // nil when ids is empty
  func orCriteria(cs []imap.SearchCriteria) *imap.SearchCriteria
  ```

- [ ] **Step 1: Write the failing tests**

```go
// internal/imap/search_criteria_test.go
package imap

import (
	"testing"

	"github.com/emersion/go-imap/v2"
)

// collectHeaders walks an OR tree and returns every Key:Value leaf.
func collectHeaders(c *imap.SearchCriteria) []string {
	if c == nil {
		return nil
	}
	var out []string
	for _, h := range c.Header {
		out = append(out, h.Key+":"+h.Value)
	}
	for _, pair := range c.Or {
		out = append(out, collectHeaders(&pair[0])...)
		out = append(out, collectHeaders(&pair[1])...)
	}
	return out
}

func TestOrCriteria(t *testing.T) {
	if orCriteria(nil) != nil {
		t.Error("orCriteria(nil) should be nil")
	}
	single := imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "A", Value: "1"}}}
	if got := orCriteria([]imap.SearchCriteria{single}); len(got.Or) != 0 || len(got.Header) != 1 {
		t.Errorf("single criterion should be returned as-is, got %+v", got)
	}
	three := []imap.SearchCriteria{
		{Header: []imap.SearchCriteriaHeaderField{{Key: "A", Value: "1"}}},
		{Header: []imap.SearchCriteriaHeaderField{{Key: "B", Value: "2"}}},
		{Header: []imap.SearchCriteriaHeaderField{{Key: "C", Value: "3"}}},
	}
	got := collectHeaders(orCriteria(three))
	if len(got) != 3 {
		t.Errorf("OR of 3 should keep 3 leaves, got %v", got)
	}
}

func TestMessageIDCriteria(t *testing.T) {
	if messageIDCriteria(nil) != nil {
		t.Error("empty ids should yield nil")
	}
	got := collectHeaders(messageIDCriteria([]string{"<a@x>", "b@y"}))
	want := map[string]bool{
		"Message-ID:a@x": true, "In-Reply-To:a@x": true,
		"Message-ID:b@y": true, "In-Reply-To:b@y": true,
	}
	if len(got) != 4 {
		t.Fatalf("want 4 leaves, got %v", got)
	}
	for _, g := range got {
		if !want[g] {
			t.Errorf("unexpected leaf %q (angle brackets must be stripped)", g)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/imap/ -run 'TestOrCriteria|TestMessageIDCriteria' 2>&1 | head -5`
Expected: `undefined: orCriteria`.

- [ ] **Step 3: Implement**

Extract the search body of `SearchMessages` into a shared helper. Replace `SearchMessages` (lines 533-576) with:

```go
func (c *Client) SearchMessages(ctx context.Context, folder, query string) ([]Email, error) {
	if query == "" {
		return nil, nil
	}
	return c.searchFolder(ctx, folder, buildSearchCriteria(query))
}

// searchFolder runs UID SEARCH with criteria in folder and fetches the
// newest 100 matching headers.
func (c *Client) searchFolder(ctx context.Context, folder string, criteria *imap.SearchCriteria) ([]Email, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var uids []uint32
	err := c.withConnRetry(ctx, func(conn *imapclient.Client) error {
		uids = nil // reset on retry
		if err := c.selectMailbox(folder); err != nil {
			return err
		}

		searchData, err := conn.UIDSearch(criteria, nil).Wait()
		if err != nil {
			return fmt.Errorf("UID SEARCH: %w", err)
		}
		uidSet, ok := searchData.All.(imap.UIDSet)
		if !ok {
			return nil
		}
		nums, _ := uidSet.Nums()
		for _, u := range nums {
			uids = append(uids, uint32(u))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(uids) == 0 {
		return nil, nil
	}

	// Cap results per folder to avoid huge fetches
	if len(uids) > 100 {
		uids = uids[len(uids)-100:] // keep newest (highest UIDs)
	}

	return c.FetchHeadersByUID(ctx, folder, uids)
}
```

(Keep the body byte-identical to the old one apart from the `criteria` parameter and the removed `query == ""` check.)

After `SearchAllFolders` add:

```go
// SearchByMessageIDs returns, across folders, every message whose
// Message-ID is one of ids OR whose In-Reply-To points at one of ids —
// the members of a user-merged thread plus direct replies to them.
// Folders that fail to SELECT are skipped, like SearchAllFolders.
func (c *Client) SearchByMessageIDs(ctx context.Context, folders []string, ids []string) ([]Email, error) {
	criteria := messageIDCriteria(ids)
	if criteria == nil {
		return nil, nil
	}
	var all []Email
	for _, folder := range folders {
		emails, err := c.searchFolder(ctx, folder, criteria)
		if err != nil {
			continue
		}
		all = append(all, emails...)
	}
	return all, nil
}

// messageIDCriteria builds OR(HEADER Message-ID id, HEADER In-Reply-To id, …)
// for every id. Angle brackets are stripped: HEADER is a substring match and
// servers differ on whether they index the brackets.
func messageIDCriteria(ids []string) *imap.SearchCriteria {
	var parts []imap.SearchCriteria
	for _, id := range ids {
		id = strings.Trim(strings.TrimSpace(id), "<>")
		if id == "" {
			continue
		}
		parts = append(parts,
			imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: id}}},
			imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "In-Reply-To", Value: id}}},
		)
	}
	return orCriteria(parts)
}

// orCriteria folds cs into a right-nested OR tree (go-imap only has binary OR).
func orCriteria(cs []imap.SearchCriteria) *imap.SearchCriteria {
	switch len(cs) {
	case 0:
		return nil
	case 1:
		c := cs[0]
		return &c
	}
	rest := orCriteria(cs[1:])
	return &imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{cs[0], *rest}}}
}
```

- [ ] **Step 4: Run tests and vet**

Run: `go test ./internal/imap/ 2>&1 | tail -3 && go vet ./internal/imap/`
Expected: `ok`, no vet output.

- [ ] **Step 5: Commit**

```bash
git add internal/imap/client.go internal/imap/search_criteria_test.go
git commit -m "imap: SearchByMessageIDs across folders (Message-ID + In-Reply-To)

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: Model wiring — store, target expansion, `m`, open with enter/`l`/`T`, sender rules on load

**Files:**
- Modify: `internal/ui/model.go` (struct ~line 625, `New()` ~line 735, `targetEmails` line 1277, `emailsLoadedMsg` handler line 2152, `enter/l` line 3405, `T` line 3460, `m` line 3476, `shouldPrefixFolderInSubject` line 3613, `applyFilter` line 3679)
- Modify: `internal/ui/search.go` (append)
- Modify: `internal/ui/inbox.go` (append `selectedItem`)
- Test: `internal/ui/model_test.go`

**Interfaces:**
- Consumes: `merge.Store` (Task 1), `cfg.MergesFile` (Task 2), `emailItem.merge` (Task 4), `SearchByMessageIDs` (Task 5).
- Produces:
  ```go
  // Model gains: merges *merge.Store
  func selectedItem(l list.Model) (emailItem, bool)          // inbox.go
  func (m Model) inMergeView() bool                          // offTabFolder starts with "Merged: "
  func (m *Model) applySenderRules(emails []imap.Email) int  // returns count added; saves via safeGo when > 0
  func (m Model) fetchMergeCmd(title string, ids []string, fallback []imap.Email) tea.Cmd
  func (m *Model) handleMergeResult(msg mergeResultMsg) (tea.Model, tea.Cmd)
  type mergeResultMsg struct { title string; emails []imap.Email; err error }
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/ui/model_test.go` (it already imports `imap`, `config`, `tea`, `time`):

```go
func mergedInboxModel(t *testing.T) Model {
	t.Helper()
	s, err := merge.Load(filepath.Join(t.TempDir(), "merges.toml"))
	if err != nil {
		t.Fatal(err)
	}
	s.Add("Bounces", "<b1>", "<b2>")
	cfg := &config.Config{}
	m := Model{cfg: cfg, merges: s, markedUIDs: map[uint32]bool{}, spyPixelKeys: map[string]bool{}, sortField: "date", sortReverse: true}
	m.inbox = newInboxList(100, 10, "", "")
	m.emails = []imap.Email{
		{UID: 1, MessageID: "<b1>", Subject: "Undelivered", From: "mailer-daemon@x", Date: time.Now().Add(-2 * time.Hour), Seen: true},
		{UID: 2, MessageID: "<b2>", Subject: "Undelivered", From: "mailer-daemon@x", Date: time.Now().Add(-1 * time.Hour), Seen: true},
		{UID: 3, MessageID: "<k>", Subject: "Keep", From: "k@x", Date: time.Now(), Seen: true},
	}
	m.applyFilter()
	return m
}

func TestTargetEmails_ExpandsCollapsedRow(t *testing.T) {
	m := mergedInboxModel(t)
	m.inbox.Select(1) // second row = Bounces (Keep is newest)
	it, ok := selectedItem(m.inbox)
	if !ok || it.merge == nil {
		t.Fatalf("row 1 should be the collapsed merge, got %+v", it)
	}
	targets := m.targetEmails()
	if len(targets) != 2 {
		t.Fatalf("targetEmails on collapsed row = %d, want 2 members", len(targets))
	}
	m.inbox.Select(0)
	if got := m.targetEmails(); len(got) != 1 || got[0].UID != 3 {
		t.Errorf("plain row should still resolve to itself, got %+v", got)
	}
}

func TestMarkKey_TogglesAllMembers(t *testing.T) {
	m := mergedInboxModel(t)
	m.inbox.Select(1)
	res, _ := m.updateInbox(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	mm := res.(Model)
	if !mm.markedUIDs[1] || !mm.markedUIDs[2] || mm.markedUIDs[3] {
		t.Errorf("m on collapsed row should mark uids 1,2 only; got %v", mm.markedUIDs)
	}
	mm.inbox.Select(1)
	res, _ = mm.updateInbox(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
	mm = res.(Model)
	if len(mm.markedUIDs) != 0 {
		t.Errorf("second m should unmark all members, got %v", mm.markedUIDs)
	}
}

func TestApplySenderRules_PersistsMatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "merges.toml")
	s, _ := merge.Load(path)
	s.SetSender("Bounces", "mailer-daemon@")
	m := Model{merges: s}
	emails := []imap.Email{
		{UID: 1, MessageID: "<new1>", From: "Mailer Daemon <MAILER-DAEMON@mx.x>"},
		{UID: 2, MessageID: "", From: "mailer-daemon@mx.x"}, // no Message-ID → skipped
		{UID: 3, MessageID: "<other>", From: "alice@x"},
	}
	if n := m.applySenderRules(emails); n != 1 {
		t.Errorf("applySenderRules = %d, want 1", n)
	}
	if title, ok := s.TitleOf("<new1>"); !ok || title != "Bounces" {
		t.Errorf("<new1> should now belong to Bounces, got %q %v", title, ok)
	}
	if n := m.applySenderRules(emails); n != 0 {
		t.Errorf("second pass should add nothing, got %d", n)
	}
}

func TestHandleMergeResult_OpensOffTab(t *testing.T) {
	m := mergedInboxModel(t)
	res, _ := m.handleMergeResult(mergeResultMsg{title: "Bounces", emails: m.emails[:2]})
	mm := res.(*Model)
	if mm.offTabFolder != "Merged: Bounces" || !mm.inMergeView() {
		t.Errorf("offTabFolder = %q", mm.offTabFolder)
	}
	if n := len(mm.inbox.Items()); n != 2 {
		t.Errorf("merge view should list members uncollapsed, got %d items", n)
	}
	if !mm.shouldPrefixFolderInSubject() {
		t.Error("merge view should prefix subjects with the folder")
	}
}

func TestHandleMergeResult_ErrorFallsBackToStatus(t *testing.T) {
	m := mergedInboxModel(t)
	res, _ := m.handleMergeResult(mergeResultMsg{title: "Bounces", err: errors.New("boom")})
	mm := res.(*Model)
	if !mm.isError || mm.offTabFolder != "" {
		t.Errorf("error should set status and stay in folder; isError=%v offTab=%q", mm.isError, mm.offTabFolder)
	}
}
```

Add imports `errors`, `github.com/sspaeti/neomd/internal/merge` to `model_test.go` (`path/filepath` is already imported). The inbox key handler is `func (m Model) updateInbox(msg tea.KeyMsg) (tea.Model, tea.Cmd)` (value receiver, returns `m` by value, so the test asserts `res.(Model)`). `newInboxList` exists (`grep -n "^func newInboxList" internal/ui/*.go`) and takes `(width, height int, sentFolder, draftFolder string)`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestTargetEmails_Expands|TestMarkKey_Toggles|TestApplySenderRules|TestHandleMergeResult' 2>&1 | head -5`
Expected: compile errors (`merges` field, `selectedItem`, `mergeResultMsg` undefined).

- [ ] **Step 3: Implement**

`internal/ui/inbox.go`, after `selectedEmail`:

```go
// selectedItem returns the highlighted list item (with merge info), or false.
func selectedItem(l list.Model) (emailItem, bool) {
	item, ok := l.SelectedItem().(emailItem)
	return item, ok
}
```

`internal/ui/model.go`:

1. Import `"github.com/sspaeti/neomd/internal/merge"`.
2. Struct, after the `contacts *contacts.Store` field:
   ```go
   	// merges holds user-defined merged threads (title → Message-IDs + optional
   	// sender rule), persisted at cfg.MergesFile. Nil-safe.
   	merges *merge.Store
   	// pendingUnmerge is a merge title awaiting y/n before :unmerge dissolves it.
   	pendingUnmerge string
   ```
3. `New()`, after `compose.contacts = cs`:
   ```go
   	ms, err := merge.Load(cfg.MergesFile)
   	if err != nil {
   		notice = "merges.toml: " + err.Error()
   	}
   ```
   and add `merges: ms,` to the returned `Model{...}` literal.
4. `targetEmails()` — insert before the final `if e := selectedEmail(m.inbox)`:
   ```go
   	if it, ok := selectedItem(m.inbox); ok && it.merge != nil {
   		return append([]imap.Email(nil), it.merge.members...)
   	}
   ```
5. `emailsLoadedMsg` handler — after `m.harvestContacts(msg.emails)`:
   ```go
   	m.applySenderRules(msg.emails)
   ```
6. New helpers, placed after `targetEmails()`:
   ```go
   // applySenderRules adds every email whose sender matches a merge's sender
   // rule to that merge (by Message-ID) and persists the store when anything
   // changed. Returns the number of emails added.
   func (m *Model) applySenderRules(emails []imap.Email) int {
   	if m.merges == nil {
   		return 0
   	}
   	added := 0
   	for _, e := range emails {
   		if e.MessageID == "" {
   			continue
   		}
   		if _, already := m.merges.TitleOf(e.MessageID); already {
   			continue
   		}
   		if title, ok := m.merges.MatchSender(e.From); ok {
   			added += m.merges.Add(title, e.MessageID)
   		}
   	}
   	if added > 0 {
   		store := m.merges
   		safeGo(func() { _ = store.Save() })
   	}
   	return added
   }

   // inMergeView reports whether the list currently shows an opened merge.
   func (m Model) inMergeView() bool {
   	return strings.HasPrefix(m.offTabFolder, "Merged: ")
   }
   ```
7. `enter`/`l` (line 3405) — replace the case body with:
   ```go
   	case "enter", "l":
   		if it, ok := selectedItem(m.inbox); ok && it.merge != nil {
   			m.loading = true
   			return m, tea.Batch(m.spinner.Tick, m.fetchMergeCmd(it.merge.title, m.merges.IDs(it.merge.title), it.merge.members))
   		}
   		e := selectedEmail(m.inbox)
   		if e == nil {
   			return m, nil
   		}
   		m.loading = true
   		return m, tea.Batch(m.spinner.Tick, m.fetchBodyCmd(e))
   ```
8. `T` (line 3460) — insert the same three-line merge check at the top of the case, before `e := selectedEmail(m.inbox)`. Do NOT touch the reader's `T` at line 4064.
9. `m` (line 3476) — replace the toggle with:
   ```go
   	case "m": // mark/unmark current email for batch, advance cursor
   		it, ok := selectedItem(m.inbox)
   		if !ok {
   			break
   		}
   		if it.merge != nil {
   			all := true
   			for _, mem := range it.merge.members {
   				all = all && m.markedUIDs[mem.UID]
   			}
   			for _, mem := range it.merge.members {
   				if all {
   					delete(m.markedUIDs, mem.UID)
   				} else {
   					m.markedUIDs[mem.UID] = true
   				}
   			}
   		} else if m.markedUIDs[it.email.UID] {
   			delete(m.markedUIDs, it.email.UID)
   		} else {
   			m.markedUIDs[it.email.UID] = true
   		}
   		next := m.inbox.Index() + 1
   		if next < len(m.inbox.Items()) {
   			m.inbox.Select(next)
   		}
   		return m, m.applyFilter()
   ```
10. `shouldPrefixFolderInSubject()` — add `m.inMergeView()` to the true branch:
    ```go
    func (m Model) shouldPrefixFolderInSubject() bool {
    	if m.inMergeView() {
    		return true
    	}
    	switch m.offTabFolder {
    	case "Search", "Everything", "Thread", "Sender":
    		return true
    	default:
    		return false
    	}
    }
    ```
11. `applyFilter()` last lines:
    ```go
    	noThread := len(m.folders) > 0 && m.activeFolder() == m.cfg.Folders.Sent
    	var titleOf func(string) (string, bool)
    	if m.merges != nil && !m.inMergeView() {
    		titleOf = m.merges.TitleOf
    	}
    	return setEmails(&m.inbox, filtered, m.markedUIDs, m.spyPixelKeys, m.shouldPrefixFolderInSubject(), m.sortField, m.sortReverse, noThread, titleOf)
    ```
    `activeFolder()` indexes `m.folders[m.activeFolderI]` — it is guarded by `len(m.folders) > 0` here already, so the test model with no folders is safe.

`internal/ui/search.go`, append:

```go
// mergeResultMsg carries the members of an opened user-merged thread.
type mergeResultMsg struct {
	title  string
	emails []imap.Email
	err    error
}

// fetchMergeCmd fetches every stored member of the merge (plus direct
// replies) across the same folders the T conversation view searches.
// fallback (the members visible in the current list) is shown when the
// server returns nothing, so the view never opens empty.
func (m Model) fetchMergeCmd(title string, ids []string, fallback []imap.Email) tea.Cmd {
	cli := m.imapCli()
	f := m.cfg.Folders
	folders := []string{f.Inbox, f.Sent, f.Archive, f.Waiting, f.Someday, f.Scheduled}
	if f.Work != "" {
		folders = append(folders, f.Work)
	}
	cur := m.activeFolder()
	found := false
	for _, fo := range folders {
		if fo == cur {
			found = true
			break
		}
	}
	if !found && cur != "" {
		folders = append(folders, cur)
	}
	return func() tea.Msg {
		emails, err := cli.SearchByMessageIDs(nil, folders, ids)
		if err == nil && len(emails) == 0 {
			emails = fallback
		}
		return mergeResultMsg{title: title, emails: emails, err: err}
	}
}

// handleMergeResult displays the opened merge as an off-tab view.
func (m *Model) handleMergeResult(msg mergeResultMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	m.imapSearchResults = false
	if msg.err != nil {
		m.status = "Merge: " + msg.err.Error()
		m.isError = true
		return m, nil
	}
	m.offTabFolder = "Merged: " + msg.title
	m.emails = msg.emails
	m.markedUIDs = make(map[uint32]bool)
	m.filterActive = false
	m.filterText = ""
	m.status = fmt.Sprintf("Merged %q — %d email(s). esc to close · :unmerge removes the cursor email.", msg.title, len(msg.emails))
	return m, m.sortEmails()
}
```

Wire the message in `Update()` next to `case conversationResultMsg:` (line 2550):

```go
	case mergeResultMsg:
		return m.handleMergeResult(msg)
```

(Match the exact call shape used for `conversationResultMsg` on the line below it — if that case does `return m.handleConversationResult(msg)` on a value receiver copy, do the same.)

`fetchMergeCmd` calls `m.activeFolder()`, which needs `m.folders` non-empty; in the TUI it always is. `m.imapCli()` is also only valid in the TUI — the tests above never invoke `fetchMergeCmd`.

- [ ] **Step 4: Run the full suite**

Run: `go build ./... && go vet ./internal/ui/ && go test ./internal/ui/ 2>&1 | tail -5`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/model.go internal/ui/search.go internal/ui/inbox.go internal/ui/model_test.go
git commit -m "ui: open, mark and act on merged rows; sender rules on load

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: Commands `:merge`, `:merge-sender`, `:unmerge` with arguments and title completion

**Files:**
- Modify: `internal/ui/cmdline.go` (`neomdCmd` line 15, registry, `matchCmds` line 286, `viewCmdLine` line 318)
- Modify: `internal/ui/model.go` (cmd `enter` at line 2954, `tab`/`ctrl+n` at 2992, `right` at 2987, `y` at 3233, `n` at 3261)
- Test: `internal/ui/cmdline_test.go`

**Interfaces:**
- Consumes: `m.merges`, `targetEmails()`, `selectedItem`, `inMergeView()`, `applySenderRules`, `normalizedSender(from string) string` (exists at model.go:1293).
- Produces:
  ```go
  // neomdCmd gains: runArgs func(m *Model, args string) (tea.Model, tea.Cmd)  // used instead of run when set
  func splitCmdInput(input string) (word, args string)
  func (m Model) titleCompletions(text string) []string   // ":merge Bo" → ["merge Bounces"]
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/ui/cmdline_test.go` (imports `strings`, `testing`, `imap` exist; add `path/filepath`, `time`, `github.com/sspaeti/neomd/internal/merge`):

```go
func TestSplitCmdInput(t *testing.T) {
	cases := []struct{ in, word, args string }{
		{"merge Bounces from ACME", "merge", "Bounces from ACME"},
		{"merge   spaced  ", "merge", "spaced"},
		{"unmerge", "unmerge", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		w, a := splitCmdInput(c.in)
		if w != c.word || a != c.args {
			t.Errorf("splitCmdInput(%q) = %q,%q want %q,%q", c.in, w, a, c.word, c.args)
		}
	}
}

func TestMatchCmds_IgnoresArguments(t *testing.T) {
	if c := matchCmd("merge Bounces"); c == nil || c.name != "merge" {
		t.Fatalf("matchCmd with args should resolve merge, got %v", c)
	}
	if c := matchCmd("merge-sender X"); c == nil || c.name != "merge-sender" {
		t.Fatalf("got %v", c)
	}
}

func cmdModel(t *testing.T) Model {
	t.Helper()
	s, _ := merge.Load(filepath.Join(t.TempDir(), "merges.toml"))
	m := Model{merges: s, markedUIDs: map[uint32]bool{}, spyPixelKeys: map[string]bool{}, sortField: "date", sortReverse: true}
	m.inbox = newInboxList(100, 10, "", "")
	m.emails = []imap.Email{
		{UID: 1, MessageID: "<b1>", Subject: "Undelivered", From: "Mailer <mailer-daemon@x>", Date: time.Now().Add(-2 * time.Hour), Seen: true},
		{UID: 2, MessageID: "", Subject: "No id", From: "mailer-daemon@x", Date: time.Now().Add(-1 * time.Hour), Seen: true},
		{UID: 3, MessageID: "<k>", Subject: "Keep", From: "k@x", Date: time.Now(), Seen: true},
	}
	m.applyFilter()
	return m
}

func runCmd(t *testing.T, m Model, input string) Model {
	t.Helper()
	word, args := splitCmdInput(input)
	c := matchCmd(word)
	if c == nil {
		t.Fatalf("no command for %q", input)
	}
	var res tea.Model
	if c.runArgs != nil {
		res, _ = c.runArgs(&m, args)
	} else {
		res, _ = c.run(&m)
	}
	switch v := res.(type) {
	case *Model:
		return *v
	case Model:
		return v
	}
	t.Fatalf("unexpected result type %T", res)
	return m
}

func TestMergeCmd_MarkedEmailsSkipsMissingID(t *testing.T) {
	m := cmdModel(t)
	m.markedUIDs[1] = true
	m.markedUIDs[2] = true
	m = runCmd(t, m, "merge My Bounces")
	if title, ok := m.merges.TitleOf("<b1>"); !ok || title != "My Bounces" {
		t.Errorf("<b1> not merged: %q %v", title, ok)
	}
	if !strings.Contains(m.status, "1 skipped") {
		t.Errorf("status should report the skipped email without Message-ID, got %q", m.status)
	}
	if len(m.markedUIDs) != 0 {
		t.Error("marks should be cleared after :merge")
	}
	if n := len(m.inbox.Items()); n != 3 { // uid1 collapsed alone, uid2, uid3
		t.Errorf("items after merge = %d, want 3", n)
	}
}

func TestMergeCmd_RequiresTitle(t *testing.T) {
	m := cmdModel(t)
	m = runCmd(t, m, "merge")
	if !m.isError || !strings.Contains(m.status, "usage") {
		t.Errorf("empty title should be a usage error, got %q", m.status)
	}
}

func TestMergeSenderCmd_StoresRuleAndAppliesToLoaded(t *testing.T) {
	m := cmdModel(t)
	m.inbox.Select(2) // uid 1 (oldest, sorted last)
	if e := selectedEmail(m.inbox); e == nil || e.UID != 1 {
		t.Fatalf("cursor not on uid 1: %+v", e)
	}
	m = runCmd(t, m, "merge-sender Bounces")
	if _, ok := m.merges.MatchSender("MAILER-DAEMON@x"); !ok {
		t.Error("sender rule not stored")
	}
	if title, ok := m.merges.TitleOf("<b1>"); !ok || title != "Bounces" {
		t.Errorf("cursor email not merged: %q %v", title, ok)
	}
}

func TestUnmergeCmd_OnCollapsedRowAsksThenDissolves(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>")
	m.applyFilter()
	m.inbox.Select(2)
	if it, ok := selectedItem(m.inbox); !ok || it.merge == nil {
		t.Fatalf("cursor should be on the collapsed row, got %+v", it)
	}
	m = runCmd(t, m, "unmerge")
	if m.pendingUnmerge != "Bounces" || !strings.Contains(m.status, "y/n") {
		t.Fatalf("expected y/n prompt, status=%q pending=%q", m.status, m.pendingUnmerge)
	}
	if _, ok := m.merges.TitleOf("<b1>"); !ok {
		t.Error("must not dissolve before confirmation")
	}
}

func TestUnmergeCmd_InsideMergeViewRemovesCursor(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>", "<zz>")
	m.offTabFolder = "Merged: Bounces"
	m.emails = m.emails[:1]
	m.applyFilter()
	m.inbox.Select(0)
	m = runCmd(t, m, "unmerge")
	if _, ok := m.merges.TitleOf("<b1>"); ok {
		t.Error("<b1> should be removed from the merge")
	}
	if _, ok := m.merges.TitleOf("<zz>"); !ok {
		t.Error("other member must stay")
	}
	if n := len(m.inbox.Items()); n != 0 {
		t.Errorf("removed email should leave the view, got %d items", n)
	}
}

func TestUnmergeCmd_OutsideMergeIsError(t *testing.T) {
	m := cmdModel(t)
	m.inbox.Select(0)
	m = runCmd(t, m, "unmerge")
	if !m.isError {
		t.Errorf("unmerge on a plain row should error, got %q", m.status)
	}
}

func TestTitleCompletions(t *testing.T) {
	m := cmdModel(t)
	m.merges.Add("Bounces", "<b1>")
	m.merges.Add("Newsletters", "<n1>")
	if got := m.titleCompletions("merge Bo"); len(got) != 1 || got[0] != "merge Bounces" {
		t.Errorf("got %v", got)
	}
	if got := m.titleCompletions("merge-sender "); len(got) != 2 {
		t.Errorf("empty prefix should list all titles, got %v", got)
	}
	if got := m.titleCompletions("reload x"); got != nil {
		t.Errorf("non-merge commands get no title completion, got %v", got)
	}
}
```

Add `tea "github.com/charmbracelet/bubbletea"` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/ui/ -run 'TestSplitCmdInput|TestMatchCmds_IgnoresArguments|TestMergeCmd|TestMergeSenderCmd|TestUnmergeCmd|TestTitleCompletions' 2>&1 | head -5`
Expected: compile errors (`splitCmdInput`, `runArgs` undefined).

- [ ] **Step 3: Implement**

`internal/ui/cmdline.go`:

1. Struct:
   ```go
   type neomdCmd struct {
   	name    string   // full name, e.g. "screen-all"
   	aliases []string // short forms accepted, e.g. ["sa", "screen-a"]
   	desc    string
   	// run is called when the command is executed; m is the current model.
   	run func(m *Model) (tea.Model, tea.Cmd)
   	// runArgs, when set, is used instead of run and receives everything
   	// typed after the command word (trimmed), e.g. ":merge My Title".
   	runArgs func(m *Model, args string) (tea.Model, tea.Cmd)
   }
   ```
2. Add these three entries to `cmdRegistry` right before the `quit` entry:
   ```go
   		{
   			name:    "merge",
   			aliases: []string{"mg"},
   			desc:    "merge marked/cursor emails into a titled group: :merge <title> (HEY-style merge threads)",
   			runArgs: func(m *Model, args string) (tea.Model, tea.Cmd) {
   				return m.mergeCmd(args, false)
   			},
   		},
   		{
   			name:    "merge-sender",
   			aliases: []string{"mgs"},
   			desc:    "like :merge, plus future mail from the cursor email's sender joins automatically",
   			runArgs: func(m *Model, args string) (tea.Model, tea.Cmd) {
   				return m.mergeCmd(args, true)
   			},
   		},
   		{
   			name:    "unmerge",
   			aliases: []string{"umg"},
   			desc:    "on a ≡ row: dissolve the merge (y/n); inside an opened merge: remove the cursor email",
   			run: func(m *Model) (tea.Model, tea.Cmd) {
   				return m.unmergeCmd()
   			},
   		},
   ```
3. `matchCmds` — match on the first word only. Replace its first line `lower := strings.ToLower(text)` with:
   ```go
   	word, _ := splitCmdInput(text)
   	lower := strings.ToLower(word)
   ```
4. `viewCmdLine` — the ghost completion compares `first.name` against `lower := strings.ToLower(text)`; change that to `lower := strings.ToLower(firstWordOf(text))` where:
   ```go
   // splitCmdInput separates ":merge My Title" into ("merge", "My Title").
   func splitCmdInput(input string) (word, args string) {
   	input = strings.TrimSpace(input)
   	word, args, _ = strings.Cut(input, " ")
   	return word, strings.TrimSpace(args)
   }

   func firstWordOf(text string) string { w, _ := splitCmdInput(text); return w }
   ```
   Also in `viewCmdLine`, the "unknown command" error must not fire when args are present but the word matches: `first := matchCmd(text)` already handles that once `matchCmds` splits. The multi-match menu condition `len(matches) > 1 || text == ""` should not show the menu once the user is typing args: change it to `(len(matches) > 1 && !strings.Contains(strings.TrimSpace(text), " ")) || text == ""`.
5. Command bodies, appended to `cmdline.go`:
   ```go
   // mergeCmd implements :merge / :merge-sender.
   func (m *Model) mergeCmd(title string, withSender bool) (tea.Model, tea.Cmd) {
   	title = strings.TrimSpace(title)
   	if title == "" {
   		m.status = "usage: :merge <title>   (mark emails with m first, or use the cursor email)"
   		m.isError = true
   		return m, nil
   	}
   	targets := m.targetEmails()
   	if len(targets) == 0 {
   		m.status = "No email selected."
   		m.isError = true
   		return m, nil
   	}
   	if withSender {
   		e := selectedEmail(m.inbox)
   		addr := ""
   		if e != nil {
   			addr = normalizedSender(e.From)
   		}
   		if addr == "" {
   			m.status = "Cursor email has no usable From address."
   			m.isError = true
   			return m, nil
   		}
   		m.merges.SetSender(title, addr)
   	}
   	var ids []string
   	skipped := 0
   	for _, e := range targets {
   		if e.MessageID == "" {
   			skipped++
   			continue
   		}
   		ids = append(ids, e.MessageID)
   	}
   	added := m.merges.Add(title, ids...)
   	if withSender {
   		added += m.applySenderRules(m.emails)
   	}
   	if err := m.merges.Save(); err != nil {
   		m.status = "merges.toml: " + err.Error()
   		m.isError = true
   		return m, nil
   	}
   	m.markedUIDs = make(map[uint32]bool)
   	m.isError = false
   	m.status = fmt.Sprintf("Merged %d email(s) into %q", added, title)
   	if skipped > 0 {
   		m.status += fmt.Sprintf(" · %d skipped (no Message-ID)", skipped)
   	}
   	if withSender {
   		m.status += " · sender rule saved"
   	}
   	return m, m.applyFilter()
   }

   // unmergeCmd implements :unmerge.
   func (m *Model) unmergeCmd() (tea.Model, tea.Cmd) {
   	if m.inMergeView() {
   		e := selectedEmail(m.inbox)
   		if e == nil {
   			m.status = "No email selected."
   			m.isError = true
   			return m, nil
   		}
   		if !m.merges.Remove(e.MessageID) {
   			m.status = "Not an explicit member (absorbed reply) — nothing to remove."
   			m.isError = true
   			return m, nil
   		}
   		if err := m.merges.Save(); err != nil {
   			m.status = "merges.toml: " + err.Error()
   			m.isError = true
   			return m, nil
   		}
   		kept := m.emails[:0:0]
   		for _, x := range m.emails {
   			if x.UID != e.UID || x.Folder != e.Folder {
   				kept = append(kept, x)
   			}
   		}
   		m.emails = kept
   		m.isError = false
   		m.status = "Removed from merge."
   		return m, m.applyFilter()
   	}
   	it, ok := selectedItem(m.inbox)
   	if !ok || it.merge == nil {
   		m.status = ":unmerge works on a ≡ merged row or inside an opened merge."
   		m.isError = true
   		return m, nil
   	}
   	m.pendingUnmerge = it.merge.title
   	m.isError = false
   	m.status = fmt.Sprintf("Dissolve merge %q (%d in this folder)? y/n", it.merge.title, len(it.merge.members))
   	return m, nil
   }

   // titleCompletions returns ":merge <title>" candidates for the typed text,
   // or nil when the command is not one that takes a merge title.
   func (m Model) titleCompletions(text string) []string {
   	word, args := splitCmdInput(text)
   	if !strings.Contains(text, " ") {
   		return nil
   	}
   	c := matchCmd(word)
   	if c == nil || (c.name != "merge" && c.name != "merge-sender") {
   		return nil
   	}
   	var out []string
   	for _, t := range m.merges.Titles() {
   		if strings.HasPrefix(strings.ToLower(t), strings.ToLower(args)) {
   			out = append(out, c.name+" "+t)
   		}
   	}
   	return out
   }
   ```

`internal/ui/model.go`, command-mode key handling (line ~2954):

```go
			word, args := splitCmdInput(input)
			if cmd := matchCmd(word); cmd != nil {
				if cmd.runArgs != nil {
					return cmd.runArgs(&m, args)
				}
				result, c := cmd.run(&m)
				return result, c
			}
```

`tab`/`ctrl+n` (line 2992) — try title completion first:

```go
		case "tab", "ctrl+n": // cycle forward through completions
			if titles := m.titleCompletions(m.cmdText); len(titles) > 0 {
				m.cmdText = titles[m.cmdTabI%len(titles)]
				m.cmdTabI++
				break
			}
			matches := matchCmds(m.cmdText)
			if len(matches) > 0 {
				m.cmdText = matches[m.cmdTabI%len(matches)].name
				m.cmdTabI++
			}
```

Note: cycling with `titles[i]` sets `cmdText` to `"merge Bounces"`, and the next tab recomputes completions against the full title — that filters to that one title, so the cycle sticks. To keep cycling, remember the typed prefix: add `cmdTabBase string` to the model; set it to `m.cmdText` when `m.cmdTabI == 0`, and compute `titleCompletions(m.cmdTabBase)` instead. Reset `cmdTabBase = ""` wherever `cmdTabI = 0` is assigned in the command-mode handler.

`y` (line 3233) — first branch:

```go
	case "y":
		if m.pendingUnmerge != "" {
			title := m.pendingUnmerge
			m.pendingUnmerge = ""
			m.merges.Dissolve(title)
			if err := m.merges.Save(); err != nil {
				m.status = "merges.toml: " + err.Error()
				m.isError = true
				return m, nil
			}
			m.status = fmt.Sprintf("Dissolved merge %q.", title)
			return m, m.applyFilter()
		}
```

`n` (line 3261) — first branch:

```go
	case "n":
		if m.pendingUnmerge != "" {
			m.pendingUnmerge = ""
			m.status = "Cancelled."
			return m, nil
		}
```

Also add `m.pendingUnmerge = ""` to the block in `updateInbox` commented `// Clear pending confirmations on any key except y/n` (line ~3058), next to `m.pendingDomainOp = nil`.

- [ ] **Step 4: Run the suite**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | tail -8`
Expected: all `ok`. `TestMatchCmds_EmptyReturnsAll` must still count every registered command (it compares with `len(cmdRegistry)`, so it self-adjusts).

- [ ] **Step 5: Commit**

```bash
git add internal/ui/cmdline.go internal/ui/cmdline_test.go internal/ui/model.go
git commit -m "ui: :merge, :merge-sender, :unmerge commands with title completion

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: Docs, invariants, changelog

**Files:**
- Modify: `internal/ui/keys.go`
- Modify: `AGENTS.md` (after the "Sender view (`V`)" bullet, ~line 137)
- Modify: `CHANGELOG.md` (top)
- Generated: `docs/keybindings.md` via `make docs`

- [ ] **Step 1: keys.go**

In the `"Email actions"` section, after the `"T"` row add:

```go
		{"enter / l / T  (on ≡ merged row)", "open a merged group — every member across folders, newest first"},
```

In the `"Command line"` section, after `":everything  / :ev"` add:

```go
		{":merge <title>  / :mg", "merge marked/cursor emails into a titled group (HEY-style); collapses to one ≡ row"},
		{":merge-sender <title>  / :mgs", "like :merge, plus future mail from the cursor email's sender joins automatically"},
		{":unmerge  / :umg", "on a ≡ row: dissolve the merge (y/n) · inside an opened merge: remove the cursor email"},
```

In the `"Multi-select & Undo"` section, change the `m` row description to `"mark / unmark email (or every member of a ≡ merged row) + advance cursor"`.

- [ ] **Step 2: AGENTS.md**

Add after the Sender view bullet:

```markdown
- **Merged threads (HEY-style)** — `:merge <title>` / `:merge-sender <title>` store
  Message-IDs (never UIDs) in `<config dir>/merges.toml` (`internal/merge`); the list
  collapses members — plus any automatic thread containing a member — into one `≡`
  row placed by its newest member (`collapseMerges`, `internal/ui/thread.go`), rendered
  as `<title> (n)` with `N`/`·` aggregated over members. Enter/`l`/`T` on that row opens
  the members across folders (`SearchByMessageIDs`: Message-ID OR In-Reply-To) in a
  `Merged: <title>` off-tab; `T` on a normal row is unchanged. Bulk keys and `m` expand
  to the members via `targetEmails()`. Sender rules are applied on every folder load
  and persisted. Absorbed replies are display-only until `:merge` is run on the row.
  Tests: `TestCollapseMerges_*`, `TestRenderCollapsedMergeRow`, `TestSetEmails_CollapsesMembers`,
  `TestTargetEmails_ExpandsCollapsedRow`, `TestMarkKey_TogglesAllMembers`,
  `TestApplySenderRules_PersistsMatches`, `TestHandleMergeResult_*`, `TestMergeCmd_*`,
  `TestMergeSenderCmd_*`, `TestUnmergeCmd_*`, `TestTitleCompletions`, `TestMessageIDCriteria`,
  `internal/merge` `TestAddSaveLoad_RoundTrip`.
```

- [ ] **Step 3: CHANGELOG.md**

Insert under `# Changelog`, before the `# 2026-09-14` heading:

```markdown
# 2026-09-18

- **Merge threads (HEY-style) — collapse unrelated emails into one titled row** — recurring mail that shares no reply headers (mailer-daemon bounces, notification floods) can now be merged by hand: mark with `m`, run `:merge <title>`, and the members collapse into a single `≡ <title> (n)` row in every folder they sit in, sorted by the newest member, with `N`/`·` aggregated. Enter, `l`, or `T` on that row opens all members across Inbox/Sent/Archive/Waiting/Someday/Scheduled/Work in a `Merged: <title>` off-tab (`esc` closes) — replies to a merged mail come along, both in the list (the automatic thread is absorbed) and in the view (IMAP search on `In-Reply-To`). `:merge-sender <title>` additionally stores the cursor email's address as a rule so future mail from that sender joins automatically on folder load. `:unmerge` dissolves a merge from its `≡` row (y/n) or removes the cursor email inside an opened merge. Bulk keys (`A`, `x`, `M*`, `n`, screener keys) and `m` act on every member of a collapsed row; undo works as for any multi-move. Merges are stored by Message-ID in `<config dir>/merges.toml` (new `internal/merge` package, `Config.MergesFile`), so they survive folder moves and can live in dotfiles. `T` on a normal email, automatic threading, and the reader are unchanged. New `collapseMerges` (`internal/ui/thread.go`), `SearchByMessageIDs` (`internal/imap/client.go`), commands in `internal/ui/cmdline.go`; colon-commands can now take arguments (`runArgs`). Tests: `TestCollapseMerges_*`, `TestRenderCollapsedMergeRow`, `TestSetEmails_CollapsesMembers`, `TestTargetEmails_ExpandsCollapsedRow`, `TestMarkKey_TogglesAllMembers`, `TestApplySenderRules_PersistsMatches`, `TestHandleMergeResult_*`, `TestMergeCmd_*`, `TestMergeSenderCmd_*`, `TestUnmergeCmd_*`, `TestTitleCompletions`, `TestSplitCmdInput`, `TestMatchCmds_IgnoresArguments`, `TestMessageIDCriteria`, `TestOrCriteria`, `TestLoad_SetsMergesFileNextToConfig`, `internal/merge/store_test.go`
```

- [ ] **Step 4: Regenerate docs and run everything**

Run: `make docs && go test ./... 2>&1 | tail -8 && git status --short`
Expected: tests `ok`; `docs/keybindings.md` (and the synced README/overview) show the new rows.

- [ ] **Step 5: Commit**

```bash
git add internal/ui/keys.go AGENTS.md CHANGELOG.md docs/ README.md
git commit -m "docs: merged threads — keys, invariants, changelog

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 9: Manual smoke test in the demo account

**Files:** none (verification only).

- [ ] **Step 1: Build and run against the demo config**

Run: `make build && make demo`

- [ ] **Step 2: Walk the feature**

1. Mark two unrelated emails with `m`, type `:merge Test group`, Enter. Expect one `≡ Test group (2)` row at the position of the newer one, and a status line reporting 2 merged.
2. Press Enter on the row. Expect a `Merged: Test group` off-tab with both emails, subjects prefixed with their folder. Esc returns to the folder with the row still collapsed.
3. On the row press `A`. Expect both members archived and the row gone; `U` brings them back and the row reappears.
4. `:merge-sender Test group` with the cursor on a third email from a sender that also has other mail in the folder. Expect those to join the row immediately and `merges.toml` under `~/.config/neomd-demo/` to contain the `sender` line.
5. `:unmerge` on the row → `y`. Expect the rows to uncollapse and the entry to be gone from `merges.toml`.
6. `T` on a normal email still opens the subject/participant conversation view.

- [ ] **Step 3: Record the outcome**

If any step fails, fix it in the task that owns that code (add a regression test there), rerun `go test ./...`, and commit with the same trailer.
