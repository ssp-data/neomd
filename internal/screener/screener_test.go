package screener

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// TestClassify — table-driven, uses pre-populated in-memory maps
// ---------------------------------------------------------------------------

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		screener *Screener
		from     string
		want     Category
	}{
		{
			name: "spam wins over all other lists",
			screener: &Screener{
				screenedIn:  map[string]bool{"x@example.com": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{"x@example.com": true},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{"x@example.com": true},
			},
			from: "x@example.com",
			want: CategorySpam,
		},
		{
			name: "screened out beats feed/papertrail/screened in",
			screener: &Screener{
				screenedIn:  map[string]bool{"a@example.com": true},
				screenedOut: map[string]bool{"a@example.com": true},
				feed:        map[string]bool{"a@example.com": true},
				paperTrail:  map[string]bool{"a@example.com": true},
				spam:        map[string]bool{},
			},
			from: "a@example.com",
			want: CategoryScreenedOut,
		},
		{
			name: "feed beats papertrail and screened in",
			screener: &Screener{
				screenedIn:  map[string]bool{"b@example.com": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{"b@example.com": true},
				paperTrail:  map[string]bool{"b@example.com": true},
				spam:        map[string]bool{},
			},
			from: "b@example.com",
			want: CategoryFeed,
		},
		{
			name: "papertrail beats screened in",
			screener: &Screener{
				screenedIn:  map[string]bool{"c@example.com": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{"c@example.com": true},
				spam:        map[string]bool{},
			},
			from: "c@example.com",
			want: CategoryPaperTrail,
		},
		{
			name: "screened in returns CategoryInbox",
			screener: &Screener{
				screenedIn:  map[string]bool{"d@example.com": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "d@example.com",
			want: CategoryInbox,
		},
		{
			name: "unknown returns CategoryToScreen",
			screener: &Screener{
				screenedIn:  map[string]bool{},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "nobody@example.com",
			want: CategoryToScreen,
		},
		{
			name: "normalizes case",
			screener: &Screener{
				screenedIn:  map[string]bool{"user@example.com": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "USER@EXAMPLE.COM",
			want: CategoryInbox,
		},
		{
			name: "normalizes angle brackets",
			screener: &Screener{
				screenedIn:  map[string]bool{},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{"user@ex.com": true},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "Name <user@ex.com>",
			want: CategoryFeed,
		},
		{
			name: "empty from returns ToScreen",
			screener: &Screener{
				screenedIn:  map[string]bool{},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "",
			want: CategoryToScreen,
		},
		{
			name: "@domain in screened_in matches any address at that domain",
			screener: &Screener{
				screenedIn:  map[string]bool{"@ssp.sh": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "anyone@ssp.sh",
			want: CategoryInbox,
		},
		{
			name: "@domain in screened_out blocks any address at that domain",
			screener: &Screener{
				screenedIn:  map[string]bool{},
				screenedOut: map[string]bool{"@spammy.io": true},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "Promo Bot <promo@spammy.io>",
			want: CategoryScreenedOut,
		},
		{
			name: "exact email beats @domain in different lists",
			screener: &Screener{
				// Exact john@ssp.sh is blocked, but @ssp.sh is approved overall.
				screenedIn:  map[string]bool{"@ssp.sh": true},
				screenedOut: map[string]bool{"john@ssp.sh": true},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "john@ssp.sh",
			want: CategoryScreenedOut,
		},
		{
			name: "domain rule does not match different domain",
			screener: &Screener{
				screenedIn:  map[string]bool{"@ssp.sh": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "alice@example.com",
			want: CategoryToScreen,
		},
		{
			name: "@domain entry is case-insensitive on the domain part",
			screener: &Screener{
				screenedIn:  map[string]bool{"@example.com": true},
				screenedOut: map[string]bool{},
				feed:        map[string]bool{},
				paperTrail:  map[string]bool{},
				spam:        map[string]bool{},
			},
			from: "User@EXAMPLE.com",
			want: CategoryInbox,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.screener.Classify(tt.from)
			if got != tt.want {
				t.Errorf("Classify(%q) = %v, want %v", tt.from, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// TestFileOperations — uses t.TempDir() for isolation
// ---------------------------------------------------------------------------

func TestFileOperations(t *testing.T) {
	makeCfg := func(dir string) Config {
		return Config{
			ScreenedIn:  filepath.Join(dir, "screened_in.txt"),
			ScreenedOut: filepath.Join(dir, "screened_out.txt"),
			Feed:        filepath.Join(dir, "feed.txt"),
			PaperTrail:  filepath.Join(dir, "papertrail.txt"),
			Spam:        filepath.Join(dir, "spam.txt"),
		}
	}

	t.Run("New with missing files returns no error", func(t *testing.T) {
		dir := t.TempDir()
		s, err := New(makeCfg(dir))
		if err != nil {
			t.Fatalf("New() returned error: %v", err)
		}
		if !s.IsEmpty() {
			t.Error("expected IsEmpty() = true for fresh screener")
		}
	})

	t.Run("New skips comment lines and blank lines", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		content := "# this is a comment\n\nalice@example.com\n  \n# another comment\nbob@example.com\n"
		if err := os.WriteFile(cfg.ScreenedIn, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := New(cfg)
		if err != nil {
			t.Fatalf("New() returned error: %v", err)
		}
		if s.Classify("alice@example.com") != CategoryInbox {
			t.Error("alice should be in Inbox")
		}
		if s.Classify("bob@example.com") != CategoryInbox {
			t.Error("bob should be in Inbox")
		}
		if !s.IsEmpty() == true {
			// 2 entries loaded
		}
	})

	t.Run("New strips inline # comments after entries", func(t *testing.T) {
		// The documented example showed `@ssp.sh # everyone at ssp.sh …` —
		// loadList must strip the inline comment so the entry actually matches.
		dir := t.TempDir()
		cfg := makeCfg(dir)
		content := "@ssp.sh # everyone at ssp.sh is approved\nalice@example.com    # personal contact\n#bob@example.com # full-line still works\n"
		if err := os.WriteFile(cfg.ScreenedIn, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if s.Classify("anyone@ssp.sh") != CategoryInbox {
			t.Error("@ssp.sh entry with inline comment should match anyone@ssp.sh")
		}
		if s.Classify("alice@example.com") != CategoryInbox {
			t.Error("alice@example.com with trailing comment should match")
		}
		if s.Classify("bob@example.com") != CategoryToScreen {
			t.Error("bob (full-line commented out) should NOT match")
		}
	})

	t.Run("Approve adds to screened_in removes from screened_out and spam", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		// Pre-populate screened_out and spam files
		os.WriteFile(cfg.ScreenedOut, []byte("victim@example.com\n"), 0600)
		os.WriteFile(cfg.Spam, []byte("victim@example.com\n"), 0600)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if s.Classify("victim@example.com") != CategorySpam {
			t.Fatal("should start as spam")
		}
		if err := s.Approve("victim@example.com"); err != nil {
			t.Fatalf("Approve: %v", err)
		}
		if got := s.Classify("victim@example.com"); got != CategoryInbox {
			t.Errorf("after Approve got %v, want Inbox", got)
		}
		// Verify removed from files
		if data, _ := os.ReadFile(cfg.ScreenedOut); len(data) != 0 {
			t.Errorf("screened_out should be empty, got %q", data)
		}
		if data, _ := os.ReadFile(cfg.Spam); len(data) != 0 {
			t.Errorf("spam should be empty, got %q", data)
		}
	})

	t.Run("Block adds to screened_out removes from screened_in", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		os.WriteFile(cfg.ScreenedIn, []byte("annoying@example.com\n"), 0600)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Block("annoying@example.com"); err != nil {
			t.Fatalf("Block: %v", err)
		}
		if got := s.Classify("annoying@example.com"); got != CategoryScreenedOut {
			t.Errorf("after Block got %v, want ScreenedOut", got)
		}
		if data, _ := os.ReadFile(cfg.ScreenedIn); len(data) != 0 {
			t.Errorf("screened_in should be empty, got %q", data)
		}
	})

	t.Run("MarkFeed persists across reload", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkFeed("news@example.com"); err != nil {
			t.Fatal(err)
		}
		// Reload from files
		s2, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got := s2.Classify("news@example.com"); got != CategoryFeed {
			t.Errorf("reloaded Classify = %v, want Feed", got)
		}
	})

	t.Run("MarkPaperTrail persists across reload", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkPaperTrail("receipts@shop.com"); err != nil {
			t.Fatal(err)
		}
		s2, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got := s2.Classify("receipts@shop.com"); got != CategoryPaperTrail {
			t.Errorf("reloaded Classify = %v, want PaperTrail", got)
		}
	})

	t.Run("MarkSpam removes from screened_in and screened_out", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		os.WriteFile(cfg.ScreenedIn, []byte("bad@example.com\n"), 0600)
		os.WriteFile(cfg.ScreenedOut, []byte("bad@example.com\n"), 0600)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkSpam("bad@example.com"); err != nil {
			t.Fatal(err)
		}
		if got := s.Classify("bad@example.com"); got != CategorySpam {
			t.Errorf("after MarkSpam got %v, want Spam", got)
		}
		if data, _ := os.ReadFile(cfg.ScreenedIn); len(data) != 0 {
			t.Errorf("screened_in should be empty, got %q", data)
		}
		if data, _ := os.ReadFile(cfg.ScreenedOut); len(data) != 0 {
			t.Errorf("screened_out should be empty, got %q", data)
		}
	})

	t.Run("IsEmpty true when no entries false after add", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !s.IsEmpty() {
			t.Error("should be empty initially")
		}
		s.Approve("someone@example.com")
		if s.IsEmpty() {
			t.Error("should not be empty after Approve")
		}
	})

	t.Run("AllAddresses deduplicates across lists", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		// same address in screened_in and feed
		os.WriteFile(cfg.ScreenedIn, []byte("dup@example.com\nunique1@example.com\n"), 0600)
		os.WriteFile(cfg.Feed, []byte("dup@example.com\nunique2@example.com\n"), 0600)
		os.WriteFile(cfg.PaperTrail, []byte("dup@example.com\nunique3@example.com\n"), 0600)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		addrs := s.AllAddresses()
		sort.Strings(addrs)
		want := []string{"dup@example.com", "unique1@example.com", "unique2@example.com", "unique3@example.com"}
		sort.Strings(want)
		if len(addrs) != len(want) {
			t.Fatalf("AllAddresses len = %d, want %d; got %v", len(addrs), len(want), addrs)
		}
		for i := range want {
			if addrs[i] != want[i] {
				t.Errorf("AllAddresses[%d] = %q, want %q", i, addrs[i], want[i])
			}
		}
	})

	t.Run("Snapshot and Restore roll back mutations", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Approve("undo@example.com"); err != nil {
			t.Fatal(err)
		}
		snap := s.Snapshot()
		if err := s.Block("undo@example.com"); err != nil {
			t.Fatal(err)
		}
		if got := s.Classify("undo@example.com"); got != CategoryScreenedOut {
			t.Fatalf("after Block got %v, want ScreenedOut", got)
		}
		if err := s.Restore(snap); err != nil {
			t.Fatal(err)
		}
		if got := s.Classify("undo@example.com"); got != CategoryInbox {
			t.Fatalf("after Restore got %v, want Inbox", got)
		}
		data, err := os.ReadFile(cfg.ScreenedIn)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "undo@example.com\n" {
			t.Fatalf("screened_in contents = %q, want restored entry", data)
		}
	})
}

// ---------------------------------------------------------------------------
// TestShouldNotify — notify list is independent of categories
// ---------------------------------------------------------------------------

func TestShouldNotify(t *testing.T) {
	t.Run("empty list returns false", func(t *testing.T) {
		s := &Screener{notify: map[string]bool{}}
		if s.ShouldNotify("anyone@example.com") {
			t.Error("ShouldNotify should be false when notify is empty")
		}
	})

	t.Run("exact email match", func(t *testing.T) {
		s := &Screener{notify: map[string]bool{"vip@example.com": true}}
		if !s.ShouldNotify("vip@example.com") {
			t.Error("expected exact match")
		}
		if s.ShouldNotify("other@example.com") {
			t.Error("non-listed address should not notify")
		}
	})

	t.Run("@domain match notifies any address at that domain", func(t *testing.T) {
		s := &Screener{notify: map[string]bool{"@ssp.sh": true}}
		if !s.ShouldNotify("anyone@ssp.sh") {
			t.Error("expected domain match")
		}
		if s.ShouldNotify("anyone@other.tld") {
			t.Error("different domain should not notify")
		}
	})

	t.Run("normalises display name and case", func(t *testing.T) {
		s := &Screener{notify: map[string]bool{"vip@example.com": true}}
		if !s.ShouldNotify("VIP <VIP@Example.com>") {
			t.Error("expected normalised match")
		}
	})

	t.Run("loads from notify.txt via New", func(t *testing.T) {
		dir := t.TempDir()
		notifyPath := filepath.Join(dir, "notify.txt")
		os.WriteFile(notifyPath, []byte("@important.org\nboss@work.com\n"), 0600)
		s, err := New(Config{
			ScreenedIn:  filepath.Join(dir, "in.txt"),
			ScreenedOut: filepath.Join(dir, "out.txt"),
			Feed:        filepath.Join(dir, "feed.txt"),
			PaperTrail:  filepath.Join(dir, "pt.txt"),
			Spam:        filepath.Join(dir, "spam.txt"),
			Notify:      notifyPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !s.ShouldNotify("alice@important.org") {
			t.Error("@important.org domain entry should match")
		}
		if !s.ShouldNotify("boss@work.com") {
			t.Error("boss@work.com exact entry should match")
		}
		if s.ShouldNotify("nobody@nowhere.com") {
			t.Error("unrelated address should not notify")
		}
	})

	t.Run("AddNotify appends and persists, RemoveNotify clears", func(t *testing.T) {
		dir := t.TempDir()
		notifyPath := filepath.Join(dir, "notify.txt")
		s, err := New(Config{
			ScreenedIn:  filepath.Join(dir, "in.txt"),
			ScreenedOut: filepath.Join(dir, "out.txt"),
			Feed:        filepath.Join(dir, "feed.txt"),
			PaperTrail:  filepath.Join(dir, "pt.txt"),
			Spam:        filepath.Join(dir, "spam.txt"),
			Notify:      notifyPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.AddNotify("alice@example.com"); err != nil {
			t.Fatalf("AddNotify exact: %v", err)
		}
		if err := s.AddNotify("@important.org"); err != nil {
			t.Fatalf("AddNotify domain: %v", err)
		}
		// Re-add is no-op (no duplicate).
		if err := s.AddNotify("alice@example.com"); err != nil {
			t.Fatalf("AddNotify duplicate: %v", err)
		}
		if !s.ShouldNotify("alice@example.com") {
			t.Error("alice should notify")
		}
		if !s.ShouldNotify("anyone@important.org") {
			t.Error("@important.org should match anyone@important.org")
		}
		// Reload from disk to confirm persistence.
		s2, err := New(s.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if !s2.ShouldNotify("alice@example.com") || !s2.ShouldNotify("bob@important.org") {
			t.Error("reload lost entries")
		}
		// Remove.
		if err := s.RemoveNotify("alice@example.com"); err != nil {
			t.Fatal(err)
		}
		if s.ShouldNotify("alice@example.com") {
			t.Error("removed entry should not match")
		}
	})

	t.Run("AddNotify errors when path not configured", func(t *testing.T) {
		s := &Screener{notify: map[string]bool{}, cfg: Config{}}
		if err := s.AddNotify("x@example.com"); err == nil {
			t.Error("expected error when notify path empty")
		}
	})

	t.Run("ShouldNotify is false when Notify path is omitted from Config (regression)", func(t *testing.T) {
		// Regression: cmd/neomd/main.go used to construct screener.Config without
		// the Notify field, so the in-memory notify set stayed empty even when
		// notify.txt had entries. Result: ShouldNotify returned false silently.
		dir := t.TempDir()
		notifyPath := filepath.Join(dir, "notify.txt")
		os.WriteFile(notifyPath, []byte("vip@example.com\n"), 0600)
		// Build a screener WITHOUT passing Notify — mimics the broken main.go
		// before the fix.
		s, err := New(Config{
			ScreenedIn:  filepath.Join(dir, "in.txt"),
			ScreenedOut: filepath.Join(dir, "out.txt"),
			Feed:        filepath.Join(dir, "feed.txt"),
			PaperTrail:  filepath.Join(dir, "pt.txt"),
			Spam:        filepath.Join(dir, "spam.txt"),
			// Notify intentionally unset
		})
		if err != nil {
			t.Fatal(err)
		}
		if s.ShouldNotify("vip@example.com") {
			t.Fatal("ShouldNotify should be false when Notify path is omitted")
		}
		// Now pass Notify — ShouldNotify must return true.
		s2, err := New(Config{
			ScreenedIn:  filepath.Join(dir, "in.txt"),
			ScreenedOut: filepath.Join(dir, "out.txt"),
			Feed:        filepath.Join(dir, "feed.txt"),
			PaperTrail:  filepath.Join(dir, "pt.txt"),
			Spam:        filepath.Join(dir, "spam.txt"),
			Notify:      notifyPath,
		})
		if err != nil {
			t.Fatal(err)
		}
		if !s2.ShouldNotify("vip@example.com") {
			t.Fatal("ShouldNotify should return true when Notify path is wired up")
		}
	})

	t.Run("Notify path empty leaves the list empty", func(t *testing.T) {
		dir := t.TempDir()
		s, err := New(Config{
			ScreenedIn:  filepath.Join(dir, "in.txt"),
			ScreenedOut: filepath.Join(dir, "out.txt"),
			Feed:        filepath.Join(dir, "feed.txt"),
			PaperTrail:  filepath.Join(dir, "pt.txt"),
			Spam:        filepath.Join(dir, "spam.txt"),
			// Notify intentionally unset
		})
		if err != nil {
			t.Fatal(err)
		}
		if s.ShouldNotify("anyone@anywhere.com") {
			t.Error("ShouldNotify should be false when no notify list configured")
		}
	})
}

// ---------------------------------------------------------------------------
// Security tests — file permissions
// ---------------------------------------------------------------------------

func TestFilePermissions(t *testing.T) {
	t.Run("appendLine creates files with mode 0600", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "new_list.txt")

		if err := appendLine(path, "test@example.com"); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("appendLine file perm = %04o, want 0600", perm)
		}
	})

	t.Run("removeFromList rewrites with mode 0600", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "rewrite.txt")
		// Write initial file with 0600 (the mode screener itself would use)
		os.WriteFile(path, []byte("keep@example.com\nremove@example.com\n"), 0600)

		s := &Screener{
			cfg:         Config{ScreenedIn: path},
			screenedIn:  map[string]bool{"keep@example.com": true, "remove@example.com": true},
			screenedOut: map[string]bool{},
			feed:        map[string]bool{},
			paperTrail:  map[string]bool{},
			spam:        map[string]bool{},
		}
		if err := s.removeFromList(path, s.screenedIn, "remove@example.com"); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0600 {
			t.Errorf("removeFromList file perm = %04o, want 0600", perm)
		}
	})
}

// ---------------------------------------------------------------------------
// Cross-list cleanup regression test
// ---------------------------------------------------------------------------

func TestCrossListCleanup_Reclassification(t *testing.T) {
	makeCfg := func(dir string) Config {
		return Config{
			ScreenedIn:  filepath.Join(dir, "screened_in.txt"),
			ScreenedOut: filepath.Join(dir, "screened_out.txt"),
			Feed:        filepath.Join(dir, "feed.txt"),
			PaperTrail:  filepath.Join(dir, "papertrail.txt"),
			Spam:        filepath.Join(dir, "spam.txt"),
		}
	}

	verifyOnlyInList := func(t *testing.T, s *Screener, cfg Config, addr string, expectedCat Category) {
		t.Helper()
		// Verify in-memory classification
		if got := s.Classify(addr); got != expectedCat {
			t.Errorf("Classify(%q) = %v, want %v", addr, got, expectedCat)
		}

		// Verify on-disk: email should exist in ONLY the expected file
		files := map[Category]string{
			CategoryInbox:       cfg.ScreenedIn,
			CategoryScreenedOut: cfg.ScreenedOut,
			CategoryFeed:        cfg.Feed,
			CategoryPaperTrail:  cfg.PaperTrail,
			CategorySpam:        cfg.Spam,
		}

		for cat, path := range files {
			data, err := os.ReadFile(path)
			if err != nil && !os.IsNotExist(err) {
				t.Fatalf("ReadFile(%s): %v", path, err)
			}
			contains := false
			if len(data) > 0 {
				for _, line := range strings.Split(string(data), "\n") {
					if normalise(line) == normalise(addr) {
						contains = true
						break
					}
				}
			}

			if cat == expectedCat {
				if !contains {
					t.Errorf("%s should contain %q but doesn't; file contents: %q", path, addr, data)
				}
			} else {
				if contains {
					t.Errorf("%s should NOT contain %q but does; file contents: %q", path, addr, data)
				}
			}
		}
	}

	t.Run("Feed to ScreenedOut removes from feed.txt", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		addr := "reclassify@example.com"

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}

		// Start: mark as Feed
		if err := s.MarkFeed(addr); err != nil {
			t.Fatalf("MarkFeed: %v", err)
		}
		verifyOnlyInList(t, s, cfg, addr, CategoryFeed)

		// Reclassify: mark as ScreenedOut
		if err := s.Block(addr); err != nil {
			t.Fatalf("Block: %v", err)
		}
		verifyOnlyInList(t, s, cfg, addr, CategoryScreenedOut)
	})

	t.Run("PaperTrail to Feed removes from papertrail.txt", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		addr := "newsletter@example.com"

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}

		// Start: mark as PaperTrail
		if err := s.MarkPaperTrail(addr); err != nil {
			t.Fatalf("MarkPaperTrail: %v", err)
		}
		verifyOnlyInList(t, s, cfg, addr, CategoryPaperTrail)

		// Reclassify: mark as Feed
		if err := s.MarkFeed(addr); err != nil {
			t.Fatalf("MarkFeed: %v", err)
		}
		verifyOnlyInList(t, s, cfg, addr, CategoryFeed)
	})

	t.Run("Full reclassification chain", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		addr := "chain@example.com"

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}

		// Chain: Inbox → Feed → PaperTrail → ScreenedOut → Spam → Inbox
		steps := []struct {
			fn  func(string) error
			cat Category
		}{
			{s.Approve, CategoryInbox},
			{s.MarkFeed, CategoryFeed},
			{s.MarkPaperTrail, CategoryPaperTrail},
			{s.Block, CategoryScreenedOut},
			{s.MarkSpam, CategorySpam},
			{s.Approve, CategoryInbox},
		}

		for i, step := range steps {
			if err := step.fn(addr); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			verifyOnlyInList(t, s, cfg, addr, step.cat)
		}
	})

	t.Run("Reclassification persists after reload", func(t *testing.T) {
		dir := t.TempDir()
		cfg := makeCfg(dir)
		addr := "persist@example.com"

		s, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}

		// Start in Feed
		if err := s.MarkFeed(addr); err != nil {
			t.Fatal(err)
		}

		// Reclassify to ScreenedOut
		if err := s.Block(addr); err != nil {
			t.Fatal(err)
		}

		// Reload from disk
		s2, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}

		// Verify cleanup persisted
		verifyOnlyInList(t, s2, cfg, addr, CategoryScreenedOut)
	})
}
