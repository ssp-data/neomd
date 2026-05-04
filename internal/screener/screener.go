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
	Notify      string // optional: addresses (or @domain) to fire desktop notifications for
}

// Screener holds loaded allowlists in memory for fast classification.
type Screener struct {
	cfg         Config
	screenedIn  map[string]bool
	screenedOut map[string]bool
	feed        map[string]bool
	paperTrail  map[string]bool
	spam        map[string]bool
	notify      map[string]bool
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
		notify:      make(map[string]bool),
	}
	for path, m := range map[string]map[string]bool{
		cfg.ScreenedIn:  s.screenedIn,
		cfg.ScreenedOut: s.screenedOut,
		cfg.Feed:        s.feed,
		cfg.PaperTrail:  s.paperTrail,
		cfg.Spam:        s.spam,
		cfg.Notify:      s.notify,
	} {
		if path == "" {
			continue
		}
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
//
// List entries can be either an exact email ("john@ssp.sh") or a domain
// prefixed with "@" ("@ssp.sh") that matches any address at that domain.
// Exact matches always win over domain matches: an exact entry in any list
// is consulted before the @-domain entries in the same priority pass, but
// crucially the per-list priority itself (spam > out > feed > papertrail >
// in) is preserved across both passes by iterating the lists in order twice.
func (s *Screener) Classify(from string) Category {
	addr := normalise(from)
	switch {
	case s.spam[addr]:
		return CategorySpam
	case s.screenedOut[addr]:
		return CategoryScreenedOut
	case s.feed[addr]:
		return CategoryFeed
	case s.paperTrail[addr]:
		return CategoryPaperTrail
	case s.screenedIn[addr]:
		return CategoryInbox
	}
	// No exact match — try @domain entries in the same priority order so a
	// per-address override remains stronger than a domain rule.
	switch {
	case domainMatch(addr, s.spam):
		return CategorySpam
	case domainMatch(addr, s.screenedOut):
		return CategoryScreenedOut
	case domainMatch(addr, s.feed):
		return CategoryFeed
	case domainMatch(addr, s.paperTrail):
		return CategoryPaperTrail
	case domainMatch(addr, s.screenedIn):
		return CategoryInbox
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

// ShouldNotify reports whether a desktop notification should fire for this
// sender. True when from (or its domain via "@domain.tld") is in notify.txt.
// Independent of the screening Category: notify and screening are orthogonal.
func (s *Screener) ShouldNotify(from string) bool {
	if len(s.notify) == 0 {
		return false
	}
	return matchAddr(normalise(from), s.notify)
}

// matchAddr returns true if addr is in set, either as an exact email or as
// a "@domain" entry covering its domain. addr must already be normalised.
func matchAddr(addr string, set map[string]bool) bool {
	if set[addr] {
		return true
	}
	return domainMatch(addr, set)
}

// AddNotify appends a sender (or "@domain" entry) to notify.txt.  No-op when
// the entry is already present or when no notify list is configured.
// Independent of the screening category — does not touch screened_in /
// screened_out / feed / papertrail / spam.
func (s *Screener) AddNotify(from string) error {
	if s.cfg.Notify == "" {
		return fmt.Errorf("notify list path not configured (set [screener].notify in config.toml)")
	}
	return s.addToList(s.cfg.Notify, s.notify, from)
}

// RemoveNotify deletes a sender (or "@domain" entry) from notify.txt.
func (s *Screener) RemoveNotify(from string) error {
	if s.cfg.Notify == "" {
		return nil
	}
	return s.removeFromList(s.cfg.Notify, s.notify, from)
}

// domainMatch returns true only via the "@domain" form (skipping the exact
// check). Used by Classify so that the priority loop can be split into an
// exact-match pass first, then a domain-match pass.
func domainMatch(addr string, set map[string]bool) bool {
	if i := strings.IndexByte(addr, '@'); i >= 0 {
		return set[addr[i:]]
	}
	return false
}

// Approve adds addr to screened_in.txt and removes it from all conflicting lists.
func (s *Screener) Approve(from string) error {
	snap := s.Snapshot()
	if err := s.removeFromList(s.cfg.ScreenedOut, s.screenedOut, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Feed, s.feed, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.PaperTrail, s.paperTrail, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Spam, s.spam, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.addToList(s.cfg.ScreenedIn, s.screenedIn, from); err != nil {
		s.Restore(snap)
		return err
	}
	return nil
}

// Block adds addr to screened_out.txt and removes it from all conflicting lists.
func (s *Screener) Block(from string) error {
	snap := s.Snapshot()
	if err := s.removeFromList(s.cfg.ScreenedIn, s.screenedIn, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Feed, s.feed, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.PaperTrail, s.paperTrail, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Spam, s.spam, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.addToList(s.cfg.ScreenedOut, s.screenedOut, from); err != nil {
		s.Restore(snap)
		return err
	}
	return nil
}

// MarkSpam adds addr to spam.txt and removes it from all conflicting lists.
func (s *Screener) MarkSpam(from string) error {
	snap := s.Snapshot()
	if err := s.removeFromList(s.cfg.ScreenedIn, s.screenedIn, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.ScreenedOut, s.screenedOut, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Feed, s.feed, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.PaperTrail, s.paperTrail, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.addToList(s.cfg.Spam, s.spam, from); err != nil {
		s.Restore(snap)
		return err
	}
	return nil
}

// MarkFeed adds addr to feed.txt and removes it from all conflicting lists.
func (s *Screener) MarkFeed(from string) error {
	snap := s.Snapshot()
	if err := s.removeFromList(s.cfg.ScreenedIn, s.screenedIn, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.ScreenedOut, s.screenedOut, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.PaperTrail, s.paperTrail, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Spam, s.spam, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.addToList(s.cfg.Feed, s.feed, from); err != nil {
		s.Restore(snap)
		return err
	}
	return nil
}

// MarkPaperTrail adds addr to papertrail.txt and removes it from all conflicting lists.
func (s *Screener) MarkPaperTrail(from string) error {
	snap := s.Snapshot()
	if err := s.removeFromList(s.cfg.ScreenedIn, s.screenedIn, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.ScreenedOut, s.screenedOut, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Feed, s.feed, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.removeFromList(s.cfg.Spam, s.spam, from); err != nil {
		s.Restore(snap)
		return err
	}
	if err := s.addToList(s.cfg.PaperTrail, s.paperTrail, from); err != nil {
		s.Restore(snap)
		return err
	}
	return nil
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

// loadList reads a one-address-per-line file into a set.  Full-line and
// inline "# comment" suffixes are stripped — `addr@example.com  # note`
// stores `addr@example.com`, matching the documented examples.
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
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = strings.TrimSpace(line[:i])
			if line == "" {
				continue
			}
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
