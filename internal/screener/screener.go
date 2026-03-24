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
	CategoryToScreen  Category = iota // unknown — awaiting decision
	CategoryInbox                     // approved sender
	CategoryScreenedOut               // blocked
	CategoryFeed                      // newsletter / feed
	CategoryPaperTrail                // receipts / notifications
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
}

// Screener holds loaded allowlists in memory for fast classification.
type Screener struct {
	cfg         Config
	screenedIn  map[string]bool
	screenedOut map[string]bool
	feed        map[string]bool
	paperTrail  map[string]bool
}

// New loads all four lists from the paths in cfg.
// Missing files are silently skipped (treated as empty).
func New(cfg Config) (*Screener, error) {
	s := &Screener{
		cfg:         cfg,
		screenedIn:  make(map[string]bool),
		screenedOut: make(map[string]bool),
		feed:        make(map[string]bool),
		paperTrail:  make(map[string]bool),
	}
	for path, m := range map[string]map[string]bool{
		cfg.ScreenedIn:  s.screenedIn,
		cfg.ScreenedOut: s.screenedOut,
		cfg.Feed:        s.feed,
		cfg.PaperTrail:  s.paperTrail,
	} {
		if err := loadList(path, m); err != nil {
			return nil, fmt.Errorf("load screener list %s: %w", path, err)
		}
	}
	return s, nil
}

// Classify returns the category for a given "from" email address.
// The address is normalised to lowercase before matching.
func (s *Screener) Classify(from string) Category {
	addr := normalise(from)
	switch {
	case s.screenedIn[addr]:
		return CategoryInbox
	case s.screenedOut[addr]:
		return CategoryScreenedOut
	case s.feed[addr]:
		return CategoryFeed
	case s.paperTrail[addr]:
		return CategoryPaperTrail
	default:
		return CategoryToScreen
	}
}

// Approve adds addr to screened_in.txt and updates the in-memory set.
func (s *Screener) Approve(from string) error {
	return s.addToList(s.cfg.ScreenedIn, s.screenedIn, from)
}

// Block adds addr to screened_out.txt and updates the in-memory set.
func (s *Screener) Block(from string) error {
	return s.addToList(s.cfg.ScreenedOut, s.screenedOut, from)
}

// MarkFeed adds addr to feed.txt and updates the in-memory set.
func (s *Screener) MarkFeed(from string) error {
	return s.addToList(s.cfg.Feed, s.feed, from)
}

// MarkPaperTrail adds addr to papertrail.txt and updates the in-memory set.
func (s *Screener) MarkPaperTrail(from string) error {
	return s.addToList(s.cfg.PaperTrail, s.paperTrail, from)
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
		m[strings.ToLower(line)] = true
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
