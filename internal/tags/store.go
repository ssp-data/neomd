// Package tags persists neomd's local registry of IMAP keyword tags: every
// keyword neomd has ever set, keyed by Message-ID. The IMAP keyword on the
// message is the primary storage (it survives reinstalls and works in other
// clients); this registry is the write-through backup — a provider could
// purge unknown keywords, and the registry is what :tags-restore (Phase B)
// would re-apply from. It also doubles as the list of "known" tags for the
// picker and the chip renderer: foreign keywords ($HasAttachment,
// Thunderbird's $label1-5, …) never chip because they are not in here.
//
// Layout — one directory per account, one line-based file per keyword
// (screener-list style, no human editing expected):
//
//	<config dir>/tags/<account>/<keyword>.txt   # one Message-ID per line
//
// The per-account split is what makes a later restore selective: re-applying
// tags usually targets one account, and the restore must not touch the
// others.
package tags

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// safeAccount maps an account key to a stable folder name: characters
// outside [A-Za-z0-9._@%+-] collapse to "_". The allowed set covers email
// addresses verbatim (they are valid folder names on Linux AND Windows —
// Windows forbids <>:"/\|?* and control chars, none of which an address
// contains, and its reserved device names cannot collide with anything
// carrying an @), so the usual per-account key — the bare From address —
// passes through untouched. Sanitization only catches pathological input.
// Applied identically on write and lookup, so keys round-trip.
func safeAccount(account string) string {
	var b strings.Builder
	for _, r := range account {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-', r == '@', r == '%', r == '+':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// tagFile is one keyword's list of Message-IDs.
type tagFile struct {
	ids   []string
	dirty bool
}

// Store is a concurrency-safe in-memory copy of the tags/ directory tree.
// All methods are safe on a nil receiver.
type Store struct {
	mu      sync.Mutex
	dir     string
	entries map[string]map[string]*tagFile // account → keyword → file
}

// Load reads dir (missing directory = empty store; malformed lines are
// skipped, not fatal — the registry is a backup, never a blocker).
func Load(dir string) (*Store, error) {
	s := &Store{dir: dir, entries: map[string]map[string]*tagFile{}}
	accounts, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	for _, acc := range accounts {
		if !acc.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, acc.Name()))
		if err != nil {
			continue // unreadable account folder: treat as empty
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".txt") {
				continue
			}
			kw := strings.TrimSuffix(f.Name(), ".txt")
			ids, err := readIDList(filepath.Join(dir, acc.Name(), f.Name()))
			if err != nil {
				continue
			}
			if s.entries[acc.Name()] == nil {
				s.entries[acc.Name()] = map[string]*tagFile{}
			}
			s.entries[acc.Name()][kw] = &tagFile{ids: ids}
		}
	}
	return s, nil
}

// readIDList parses one keyword file: one Message-ID per line, "#" starts a
// comment (full-line or trailing) — the screener-list convention.
func readIDList(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id := strings.Fields(line)[0]
		if !strings.HasPrefix(id, "#") {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Save writes every changed keyword file atomically (temp + rename, 0600).
// Unchanged files are not touched.
func (s *Store) Save() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for account, keywords := range s.entries {
		for kw, f := range keywords {
			if !f.dirty {
				continue
			}
			if err := s.writeFile(account, kw, f); err != nil {
				return err
			}
			f.dirty = false
		}
	}
	return nil
}

func (s *Store) writeFile(account, keyword string, f *tagFile) error {
	dir := filepath.Join(s.dir, safeAccount(account))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# neomd tag registry — Message-IDs carrying this keyword\n")
	for _, id := range f.ids {
		b.WriteString(id)
		b.WriteByte('\n')
	}
	tmp := filepath.Join(dir, keyword+".txt.tmp")
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, keyword+".txt"))
}

// file returns the keyword file of account, creating it in memory when
// missing. Caller holds mu.
func (s *Store) file(account, keyword string) *tagFile {
	account = safeAccount(account)
	if s.entries[account] == nil {
		s.entries[account] = map[string]*tagFile{}
	}
	f, ok := s.entries[account][keyword]
	if !ok {
		f = &tagFile{}
		s.entries[account][keyword] = f
	}
	return f
}

// Add puts ids under account/keyword (created if missing) and returns how
// many were new. Keywords are normalized to lowercase; empty ids and
// duplicates are skipped. A keyword stays "known" (All lists it) even when
// its last id is removed — the empty file remains.
func (s *Store) Add(account, keyword string, ids ...string) int {
	if s == nil {
		return 0
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw == "" {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.file(account, kw)
	have := make(map[string]bool, len(f.ids))
	for _, id := range f.ids {
		have[id] = true
	}
	added := 0
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || have[id] {
			continue
		}
		have[id] = true
		f.ids = append(f.ids, id)
		added++
	}
	if added > 0 {
		f.dirty = true
	}
	return added
}

// Remove drops ids from account/keyword. It reports whether anything was
// removed. The keyword itself stays registered (its file remains, possibly
// empty) so it stays listed in the picker.
func (s *Store) Remove(account, keyword string, ids ...string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.entries[safeAccount(account)][keyword]
	if !ok {
		return false
	}
	drop := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id = strings.TrimSpace(id); id != "" {
			drop[id] = true
		}
	}
	removed := false
	kept := f.ids[:0:0]
	for _, id := range f.ids {
		if drop[id] {
			removed = true
			continue
		}
		kept = append(kept, id)
	}
	if removed {
		f.ids = kept
		f.dirty = true
	}
	return removed
}

// KeywordsFor returns the registered keywords for one Message-ID within
// account, sorted. The registry is the backup, not the source of truth —
// callers merge this with the server-side keywords on the email itself.
func (s *Store) KeywordsFor(account, id string) []string {
	if s == nil || id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for kw, f := range s.entries[safeAccount(account)] {
		for _, have := range f.ids {
			if have == id {
				out = append(out, kw)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// All returns every registered keyword of account, sorted (picker list +
// chip filter). Other accounts' keywords are invisible — restoring or
// browsing tags is always a per-account concern.
func (s *Store) All(account string) []string {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	acc := s.entries[safeAccount(account)]
	out := make([]string, 0, len(acc))
	for kw := range acc {
		out = append(out, kw)
	}
	sort.Strings(out)
	return out
}

// Count returns how many Message-IDs carry account/keyword (0 for unknown).
func (s *Store) Count(account, keyword string) int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.entries[safeAccount(account)][keyword]; ok {
		return len(f.ids)
	}
	return 0
}

// maxKeywordLen is the atom-charset length cap: [a-z0-9][a-z0-9-_.]{0,31}.
const maxKeywordLen = 32

// ReservedKeywords documents keywords other systems own. The enforceable rule
// is the prefix: EVERY keyword starting with $ or \ is reserved (system flags
// use \, other MUAs' conventions use $) — this list is documentation, not the
// gate.
var ReservedKeywords = []string{
	"$Forwarded", "$MDNSent", "$Junk", "$NotJunk", "$Phishing", "$Important", // go-imap / common MUA conventions
	"$HasAttachment", "$HasNoAttachment", // observed live on Infomaniak
	"$label1", "$label2", "$label3", "$label4", "$label5", // Thunderbird
}

// NormalizeKeyword validates and normalizes user input for a tag: lowercased,
// IMAP atom-safe ([a-z0-9][a-z0-9-_.]{0,31} after lowering), and never
// reserved ($ or \ prefix — those belong to system flags and other MUAs).
func NormalizeKeyword(input string) (string, error) {
	kw := strings.ToLower(strings.TrimSpace(input))
	if kw == "" {
		return "", fmt.Errorf("keyword must not be empty")
	}
	if strings.HasPrefix(kw, "$") || strings.HasPrefix(kw, "\\") {
		return "", fmt.Errorf("not allowed to use this keyword, it is reserved for other systems")
	}
	if len(kw) > maxKeywordLen {
		return "", fmt.Errorf("keyword too long (max %d characters)", maxKeywordLen)
	}
	for i, r := range kw {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
			(i > 0 && (r == '-' || r == '_' || r == '.'))
		if !ok {
			return "", fmt.Errorf("keyword may only contain letters, digits, - _ . (got %q)", string(r))
		}
	}
	return kw, nil
}

// Has reports whether keywords contains kw (IMAP flags compare
// case-insensitively — servers may normalize the case on write).
func Has(keywords []string, kw string) bool {
	for _, k := range keywords {
		if strings.EqualFold(k, kw) {
			return true
		}
	}
	return false
}

// WithKeyword returns keywords with kw appended (lowercased, deduplicated) —
// the optimistic local flip for a tag being added. Returns the input slice
// unchanged when it is already there.
func WithKeyword(keywords []string, kw string) []string {
	if Has(keywords, kw) {
		return keywords
	}
	return append(keywords, strings.ToLower(kw))
}

// WithoutKeyword returns keywords minus kw (case-insensitive) — the optimistic
// local flip for a tag being removed, and the error-revert helper.
func WithoutKeyword(keywords []string, kw string) []string {
	out := keywords[:0:0]
	for _, k := range keywords {
		if !strings.EqualFold(k, kw) {
			out = append(out, k)
		}
	}
	return out
}
