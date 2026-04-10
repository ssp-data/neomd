package screener

import (
	"os"
	"path/filepath"
	"sort"
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
