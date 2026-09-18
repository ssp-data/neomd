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
