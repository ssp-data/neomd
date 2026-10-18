package tags

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestTagsStore_MissingDirEmpty(t *testing.T) {
	s, err := Load(filepath.Join(tmpDir(t), "tags"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := s.All("P"); len(got) != 0 {
		t.Errorf("All = %v, want empty", got)
	}
}

func TestTagsStore_RoundTripPerAccountFiles(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "tags")
	s, _ := Load(dir)
	if n := s.Add("Personal", "Work", "<a@x>", "<b@y>"); n != 2 {
		t.Fatalf("Add returned %d, want 2", n)
	}
	s.Add("Personal", "invoice", "<b@y>")
	if err := s.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Layout: one folder per account, one line-based file per keyword.
	path := filepath.Join(dir, "Personal", "work.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("work.txt: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "<a@x>\n<b@y>") {
		t.Errorf("work.txt = %q, want one Message-ID per line", text)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}

	s2, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	// Keywords are normalized to lowercase on write.
	if got := s2.All("Personal"); len(got) != 2 || got[0] != "invoice" || got[1] != "work" {
		t.Errorf("All(Personal) = %v, want [invoice work]", got)
	}
	if got := s2.KeywordsFor("Personal", "<b@y>"); len(got) != 2 || got[0] != "invoice" || got[1] != "work" {
		t.Errorf("KeywordsFor = %v, want [invoice work]", got)
	}
	if s2.Count("Personal", "work") != 2 || s2.Count("Personal", "invoice") != 1 || s2.Count("Personal", "nope") != 0 {
		t.Errorf("Count mismatch: %d %d %d", s2.Count("Personal", "work"), s2.Count("Personal", "invoice"), s2.Count("Personal", "nope"))
	}

	// Atomic write: no temp files left behind.
	filepath.Walk(dir, func(p string, info os.FileInfo, _ error) error {
		if info != nil && strings.HasSuffix(p, ".tmp") {
			t.Errorf("temp file left behind: %s", p)
		}
		return nil
	})
}

func TestTagsStore_AccountsAreIsolated(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "tags")
	s, _ := Load(dir)
	s.Add("Personal", "work", "<a@x>")
	s.Add("Work", "work", "<w@x>")
	s.Add("Work", "client", "<c@x>")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load(dir)
	if got := s2.All("Personal"); len(got) != 1 || got[0] != "work" {
		t.Errorf("All(Personal) = %v, want [work] only", got)
	}
	if got := s2.All("Work"); len(got) != 2 {
		t.Errorf("All(Work) = %v, want [client work]", got)
	}
	if got := s2.KeywordsFor("Personal", "<w@x>"); len(got) != 0 {
		t.Errorf("Work's id resolved in Personal: %v — restore must stay per-account", got)
	}
	if s2.Count("Personal", "work") != 1 || s2.Count("Work", "work") != 1 {
		t.Errorf("per-account counts wrong: %d %d", s2.Count("Personal", "work"), s2.Count("Work", "work"))
	}
}

func TestTagsStore_UnsafeAccountNameSanitized(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "tags")
	s, _ := Load(dir)
	s.Add("Work Info/2026", "work", "<a@x>")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2, _ := Load(dir)
	if got := s2.KeywordsFor("Work Info/2026", "<a@x>"); len(got) != 1 {
		t.Errorf("sanitized account did not round-trip: %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "Work_Info_2026")); err != nil {
		t.Errorf("expected sanitized folder: %v", err)
	}
}

func TestTagsStore_FileFormatComments(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "tags")
	if err := os.MkdirAll(filepath.Join(dir, "P"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "# full-line comment\n<a@x>  # trailing comment\n\n<b@x>\n"
	if err := os.WriteFile(filepath.Join(dir, "P", "hand.txt"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.KeywordsFor("P", "<a@x>"); len(got) != 1 || got[0] != "hand" {
		t.Errorf("comments must be skipped: %v", got)
	}
	if s.Count("P", "hand") != 2 {
		t.Errorf("Count = %d, want 2 (blank line skipped)", s.Count("P", "hand"))
	}
}

func TestTagsStore_AddDedup(t *testing.T) {
	s, _ := Load(filepath.Join(tmpDir(t), "tags"))
	s.Add("P", " Work ", "<a@x>")
	if n := s.Add("P", "WORK", "<a@x>", "", "<c@x>"); n != 1 {
		t.Errorf("second Add returned %d, want 1 (dup + empty skipped)", n)
	}
	if s.Count("P", "work") != 2 {
		t.Errorf("Count = %d, want 2", s.Count("P", "work"))
	}
}

func TestTagsStore_RemoveKeepsKeywordKnown(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "tags")
	s, _ := Load(dir)
	s.Add("P", "work", "<a@x>", "<b@y>")
	if !s.Remove("P", "work", "<a@x>") {
		t.Fatal("Remove = false, want true")
	}
	if s.Remove("P", "work", "<nope>") {
		t.Error("Remove of unknown id = true, want false")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if s.Count("P", "work") != 1 {
		t.Errorf("Count = %d, want 1", s.Count("P", "work"))
	}
	if got := s.All("P"); len(got) != 1 || got[0] != "work" {
		t.Errorf("All = %v, want [work] — an emptied keyword stays known (empty file)", got)
	}
	s2, _ := Load(dir)
	if got := s2.All("P"); len(got) != 1 {
		t.Errorf("empty keyword file lost across Save/Load: %v", got)
	}
}

func TestNormalizeKeyword(t *testing.T) {
	ok := map[string]string{
		"work":      "work",
		"  Work ":   "work",
		"Project-X": "project-x",
		"in_1.2":    "in_1.2",
		"a":         "a",
	}
	for in, want := range ok {
		got, err := NormalizeKeyword(in)
		if err != nil || got != want {
			t.Errorf("NormalizeKeyword(%q) = %q,%v; want %q,nil", in, got, err, want)
		}
	}
	bad := []string{
		"", "   ", "$important", "\\Seen", "$label1",
		"has space", "umlautä", "slash/x", "-leading", ".leading", "_leading",
		"waytoolongkeywordwaytoolongkeywordway", // 37 chars
	}
	for _, in := range bad {
		if got, err := NormalizeKeyword(in); err == nil {
			t.Errorf("NormalizeKeyword(%q) = %q,nil; want error", in, got)
		}
	}
	// The exact reserved-message wording the picker shows.
	_, err := NormalizeKeyword("$HasAttachment")
	if err == nil || err.Error() != "not allowed to use this keyword, it is reserved for other systems" {
		t.Errorf("reserved error = %v, want the reserved-for-other-systems message", err)
	}
}

func TestKeywordListHelpers(t *testing.T) {
	kws := []string{"Work", "invoice"}
	if !Has(kws, "work") || !Has(kws, "INVOICE") || Has(kws, "nope") {
		t.Error("Has must compare case-insensitively")
	}
	got := WithKeyword(kws, "WORK")
	if len(got) != 2 {
		t.Errorf("WithKeyword must not duplicate: %v", got)
	}
	got = WithKeyword(kws, "new")
	if len(got) != 3 || got[2] != "new" {
		t.Errorf("WithKeyword = %v, want appended lowercase new", got)
	}
	got = WithoutKeyword([]string{"work", "INVOICE", "x"}, "invoice")
	if len(got) != 2 || got[0] != "work" || got[1] != "x" {
		t.Errorf("WithoutKeyword = %v, want [work x]", got)
	}
}

func TestNilStoreSafe(t *testing.T) {
	var s *Store
	if n := s.Add("P", "work", "<a>"); n != 0 {
		t.Error("nil Add must be a no-op")
	}
	if s.Remove("P", "work", "<a>") {
		t.Error("nil Remove must be false")
	}
	if s.All("P") != nil || s.KeywordsFor("P", "<a>") != nil || s.Count("P", "work") != 0 {
		t.Error("nil store reads must be empty")
	}
	if err := s.Save(); err != nil {
		t.Errorf("nil Save = %v, want nil", err)
	}
}

func TestTagsStore_EmailAddressKeyVerbatim(t *testing.T) {
	dir := filepath.Join(tmpDir(t), "tags")
	s, _ := Load(dir)
	// The usual per-account key is the bare From address — it must pass into
	// the folder name untouched (valid on Linux and Windows alike).
	key := "markus@ik.me"
	s.Add(key, "work", "<a@x>")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, key, "work.txt")); err != nil {
		t.Fatalf("address must be the folder name verbatim: %v", err)
	}
	s2, _ := Load(dir)
	if got := s2.KeywordsFor(key, "<a@x>"); len(got) != 1 || got[0] != "work" {
		t.Errorf("address-keyed round trip = %v", got)
	}
}
