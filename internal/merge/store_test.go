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

func TestRemove_ExcludesFromSenderRule(t *testing.T) {
	p := tmpPath(t)
	s, _ := Load(p)
	s.Add("Bounces", "<a@x>", "<b@x>")
	s.SetSender("Bounces", "mailer-daemon@")
	if !s.Remove("<b@x>") {
		t.Fatal("Remove(<b@x>) = false, want true")
	}
	if !s.IsExcluded("<b@x>") {
		t.Error("removed id from a rule-bearing merge should be excluded")
	}
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	s2, err := Load(p)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !s2.IsExcluded("<b@x>") {
		t.Error("exclusion lost across Save/Load")
	}
	// An explicit merge overrides the earlier removal.
	if n := s2.Add("Bounces", "<b@x>"); n != 1 {
		t.Errorf("re-Add returned %d, want 1", n)
	}
	if s2.IsExcluded("<b@x>") {
		t.Error("explicit Add should clear the exclusion")
	}
}

func TestRemove_NoRuleDoesNotExclude(t *testing.T) {
	s, _ := Load(tmpPath(t))
	s.Add("Bounces", "<a@x>", "<b@x>")
	if !s.Remove("<b@x>") {
		t.Fatal("Remove(<b@x>) = false, want true")
	}
	if s.IsExcluded("<b@x>") {
		t.Error("merge without a sender rule must not record exclusions")
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
	if s.IsExcluded("<1>") {
		t.Error("nil IsExcluded = true")
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
