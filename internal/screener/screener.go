// Package screener classifies email senders into inbox categories,
// mirroring the HEY-style screener used in the neomutt setup.
// It reads/writes the same plain-text allowlist files used by the
// existing notmuch_screening.sh and initial_screening.sh scripts.
package screener

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Category is the inbox bucket for a sender.
type Category int

const (
	CategoryToScreen    Category = iota // unknown — awaiting decision
	CategoryInbox                       // approved sender
	CategoryScreenedOut                 // blocked (known human/company)
	CategoryFeed                        // newsletter / feed
	CategoryPaperTrail                  // receipts / notifications
	CategorySpam                        // actual spam — never needs review
)

func (c Category) String() string {
	switch c {
	case CategoryInbox:
		return "Inbox"
	case CategoryScreenedOut:
		return "ScreenedOut"
	case CategoryFeed:
		return "Feed"
	case CategoryPaperTrail:
		return "PaperTrail"
	case CategorySpam:
		return "Spam"
	default:
		return "ToScreen"
	}
}

// Config maps each category to its list file path.
type Config struct {
	ScreenedIn  string
	ScreenedOut string
	Feed        string
	PaperTrail  string
	Spam        string
}

// Screener holds loaded allowlists in memory for fast classification.
type Screener struct {
	cfg         Config
	screenedIn  map[string]bool
	screenedOut map[string]bool
	feed        map[string]bool
	paperTrail  map[string]bool
	spam        map[string]bool
}

// Snapshot is a point-in-time copy of all screener list files and in-memory sets.
// It is used to roll back a failed screener operation.
type Snapshot struct {
	ScreenedIn  map[string]bool
	ScreenedOut map[string]bool
	Feed        map[string]bool
	PaperTrail  map[string]bool
	Spam        map[string]bool
}

// New loads all lists from the paths in cfg.
// Missing files are silently skipped (treated as empty).
func New(cfg Config) (*Screener, error) {
	s := &Screener{
		cfg:         cfg,
		screenedIn:  make(map[string]bool),
		screenedOut: make(map[string]bool),
		feed:        make(map[string]bool),
		paperTrail:  make(map[string]bool),
		spam:        make(map[string]bool),
	}
	for path, m := range map[string]map[string]bool{
		cfg.ScreenedIn:  s.screenedIn,
		cfg.ScreenedOut: s.screenedOut,
		cfg.Feed:        s.feed,
		cfg.PaperTrail:  s.paperTrail,
		cfg.Spam:        s.spam,
	} {
		if err := loadList(path, m); err != nil {
			return nil, fmt.Errorf("load screener list %s: %w", path, err)
		}
	}
	return s, nil
}

// IsEmpty returns true when all screener lists are empty (no senders classified yet).
// This typically means neomd is running for the first time or lists were cleared.
func (s *Screener) IsEmpty() bool {
	return len(s.screenedIn) == 0 && len(s.screenedOut) == 0 &&
		len(s.feed) == 0 && len(s.paperTrail) == 0 && len(s.spam) == 0
}

// AllAddresses returns a deduplicated slice of all known email addresses
// from screened_in, feed, and papertrail lists. Useful for autocomplete.
// Excludes screened_out and spam since you wouldn't want to email those.
func (s *Screener) AllAddresses() []string {
	seen := make(map[string]bool)
	var addrs []string
	for _, m := range []map[string]bool{s.screenedIn, s.feed, s.paperTrail} {
		for addr := range m {
			if !seen[addr] {
				seen[addr] = true
				addrs = append(addrs, addr)
			}
		}
	}
	return addrs
}

// Classify returns the category for a given "from" email address.
// The address is normalised to lowercase before matching.
func (s *Screener) Classify(from string) Category {
	addr := normalise(from)
	switch {
	case s.spam[addr]:
		return CategorySpam // spam wins over everything
	case s.screenedOut[addr]:
		return CategoryScreenedOut // hard block
	case s.feed[addr]:
		return CategoryFeed // specific routing beats generic approval
	case s.paperTrail[addr]:
		return CategoryPaperTrail
	case s.screenedIn[addr]:
		return CategoryInbox // trusted but no specific folder → stay in Inbox
	default:
		return CategoryToScreen
	}
}

// ClassifyDebug is like Classify but also returns the normalised address used
// for the lookup, for diagnostic purposes.
func (s *Screener) ClassifyDebug(from string) (Category, string) {
	addr := normalise(from)
	return s.Classify(from), addr
}

// Approve adds addr to screened_in.txt and removes it from screened_out/spam.
func (s *Screener) Approve(from string) error {
	_ = s.removeFromList(s.cfg.ScreenedOut, s.screenedOut, from)
	_ = s.removeFromList(s.cfg.Spam, s.spam, from)
	return s.addToList(s.cfg.ScreenedIn, s.screenedIn, from)
}

// Block adds addr to screened_out.txt and removes it from screened_in/spam.
func (s *Screener) Block(from string) error {
	_ = s.removeFromList(s.cfg.ScreenedIn, s.screenedIn, from)
	_ = s.removeFromList(s.cfg.Spam, s.spam, from)
	return s.addToList(s.cfg.ScreenedOut, s.screenedOut, from)
}

// MarkSpam adds addr to spam.txt and removes it from screened_in/screened_out.
func (s *Screener) MarkSpam(from string) error {
	_ = s.removeFromList(s.cfg.ScreenedIn, s.screenedIn, from)
	_ = s.removeFromList(s.cfg.ScreenedOut, s.screenedOut, from)
	return s.addToList(s.cfg.Spam, s.spam, from)
}

// MarkFeed adds addr to feed.txt and updates the in-memory set.
func (s *Screener) MarkFeed(from string) error {
	return s.addToList(s.cfg.Feed, s.feed, from)
}

// MarkPaperTrail adds addr to papertrail.txt and updates the in-memory set.
func (s *Screener) MarkPaperTrail(from string) error {
	return s.addToList(s.cfg.PaperTrail, s.paperTrail, from)
}

func cloneSet(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Snapshot captures the current screener state so a caller can roll back.
func (s *Screener) Snapshot() Snapshot {
	return Snapshot{
		ScreenedIn:  cloneSet(s.screenedIn),
		ScreenedOut: cloneSet(s.screenedOut),
		Feed:        cloneSet(s.feed),
		PaperTrail:  cloneSet(s.paperTrail),
		Spam:        cloneSet(s.spam),
	}
}

func writeSet(path string, m map[string]bool) error {
	lines := make([]string, 0, len(m))
	for addr := range m {
		lines = append(lines, addr)
	}
	content := ""
	if len(lines) > 0 {
		content = strings.Join(lines, "\n") + "\n"
	}
	return os.WriteFile(path, []byte(content), 0600)
}

// Restore rewrites all screener list files and in-memory sets from a snapshot.
func (s *Screener) Restore(snapshot Snapshot) error {
	if err := writeSet(s.cfg.ScreenedIn, snapshot.ScreenedIn); err != nil {
		return err
	}
	if err := writeSet(s.cfg.ScreenedOut, snapshot.ScreenedOut); err != nil {
		return err
	}
	if err := writeSet(s.cfg.Feed, snapshot.Feed); err != nil {
		return err
	}
	if err := writeSet(s.cfg.PaperTrail, snapshot.PaperTrail); err != nil {
		return err
	}
	if err := writeSet(s.cfg.Spam, snapshot.Spam); err != nil {
		return err
	}
	s.screenedIn = cloneSet(snapshot.ScreenedIn)
	s.screenedOut = cloneSet(snapshot.ScreenedOut)
	s.feed = cloneSet(snapshot.Feed)
	s.paperTrail = cloneSet(snapshot.PaperTrail)
	s.spam = cloneSet(snapshot.Spam)
	return nil
}

// removeFromList deletes addr from the file and in-memory set if present.
func (s *Screener) removeFromList(path string, m map[string]bool, from string) error {
	addr := normalise(from)
	if !m[addr] {
		return nil // not in list, nothing to do
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		delete(m, addr)
		return nil
	}
	if err != nil {
		return err
	}
	var keep []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if normalise(line) != addr {
			keep = append(keep, line)
		}
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return err
	}
	content := ""
	if len(keep) > 0 {
		content = strings.Join(keep, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	delete(m, addr)
	return nil
}

func (s *Screener) addToList(path string, m map[string]bool, from string) error {
	addr := normalise(from)
	if m[addr] {
		return nil // already present
	}
	if err := appendLine(path, addr); err != nil {
		return err
	}
	m[addr] = true
	return nil
}

// loadList reads a one-address-per-line file into a set.
func loadList(path string, m map[string]bool) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m[normalise(line)] = true
	}
	return sc.Err()
}

// appendLine appends a single line to path, creating the file if needed.
func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, line)
	return err
}

// normalise extracts the email address from a From header value and
// lowercases it.  Handles "Name <addr>" and bare "addr" forms.
func normalise(from string) string {
	from = strings.TrimSpace(from)
	if i := strings.IndexByte(from, '<'); i >= 0 {
		j := strings.IndexByte(from, '>')
		if j > i {
			from = from[i+1 : j]
		}
	}
	return strings.ToLower(strings.TrimSpace(from))
}
