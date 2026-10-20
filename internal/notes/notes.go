// Package notes holds neomd's private per-email notes (HEY "thread notes"):
// a free-text Markdown comment attached to one email, visible only to you.
//
// Storage is two-layered, like tags (internal/tags):
//
//   - The server copy is a small "sidecar" message in a dedicated IMAP folder
//     ([notes] folder, default "neomd/Notes"): a text/markdown message whose
//     X-Neomd-Note-For header carries the Message-ID of the email it belongs
//     to. The email itself is NEVER modified (no re-APPEND, no new UID, no
//     flag change) — only the sidecar is replaced on edit. This works on
//     every IMAP server (Gmail, Dovecot, Proton Bridge, …), unlike the
//     ANNOTATE extension (RFC 5257) that only Cyrus implements, and it
//     syncs between machines like any other mail.
//   - The local copy is one file per note under
//     <config dir>/notes/<account>/<message-id>.md, written through on every
//     save so the inbox can render the ✎ snippet before the first server
//     fetch lands and notes survive a provider that drops the folder.
//
// File format (self-describing, so the directory can be re-read without the
// server):
//
//	Message-ID: <id@host>
//	UID: 42                       # sidecar UID in the notes folder (0 = unknown)
//	Date: 2026-10-20T10:00:00Z
//
//	note body (Markdown, any number of lines)
package notes

import (
	"bufio"
	"bytes"
	"fmt"
	"mime"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// HeaderFor is the sidecar header naming the email a note belongs to.
const HeaderFor = "X-Neomd-Note-For"

// Note is one private comment on one email.
type Note struct {
	MessageID string    // the email's Message-ID (with angle brackets, as parsed by IMAP)
	UID       uint32    // sidecar UID in the notes folder; 0 = not on the server (yet)
	Body      string    // Markdown text, trailing whitespace trimmed
	Date      time.Time // last edit
}

// FirstLine is the one-line inbox snippet: the first non-empty line, with
// Markdown list/heading markers stripped.
func (n Note) FirstLine() string {
	for _, l := range strings.Split(n.Body, "\n") {
		l = strings.TrimSpace(l)
		l = strings.TrimLeft(l, "#>-* ")
		if l != "" {
			return l
		}
	}
	return ""
}

// Build renders the sidecar message for a note: a minimal RFC 5322 message
// with the note body as text/markdown (UTF-8, 8bit). from is the account
// address (so the folder is self-explanatory in another client), subject the
// email's subject (prefixed "Note: ").
func Build(n Note, from, subject string) []byte {
	var b bytes.Buffer
	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + from + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", "Note: "+subject) + "\r\n")
	b.WriteString("Date: " + n.Date.UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + sidecarMessageID(n, from) + "\r\n")
	b.WriteString(HeaderFor + ": " + n.MessageID + "\r\n")
	b.WriteString("X-Neomd-Note: 1\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/markdown; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(n.Body, "\n", "\r\n"))
	b.WriteString("\r\n")
	return b.Bytes()
}

func sidecarMessageID(n Note, from string) string {
	host := "neomd.local"
	if i := strings.LastIndex(from, "@"); i >= 0 {
		host = strings.Trim(from[i+1:], "<> ")
	}
	return fmt.Sprintf("<note-%d-%x@%s>", n.Date.UnixNano(), hashID(n.MessageID), host)
}

func hashID(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

// Parse reads a sidecar message back: the X-Neomd-Note-For header and the
// body (CRLF normalised, trailing whitespace trimmed). ok is false when the
// header is missing — not a neomd note.
func Parse(raw []byte) (n Note, ok bool) {
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(raw)))
	hdr, err := r.ReadMIMEHeader()
	if err != nil && len(hdr) == 0 {
		return Note{}, false
	}
	n.MessageID = strings.TrimSpace(hdr.Get(HeaderFor))
	if n.MessageID == "" {
		return Note{}, false
	}
	if d, err := mail(hdr.Get("Date")); err == nil {
		n.Date = d
	}
	rest, _ := readAll(r)
	n.Body = normaliseBody(rest)
	return n, true
}

func mail(date string) (time.Time, error) {
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(date)); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable date %q", date)
}

func readAll(r *textproto.Reader) (string, error) {
	var b strings.Builder
	for {
		line, err := r.R.ReadString('\n')
		b.WriteString(line)
		if err != nil {
			return b.String(), nil
		}
	}
}

func normaliseBody(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.TrimRight(s, " \t\n")
}

// ── local store ──────────────────────────────────────────────────────────

// Store is the local write-through copy of every note, per account, plus the
// in-memory map the inbox row renderer reads. Safe for concurrent use: the
// UI goroutine reads snippets while a background save writes files.
type Store struct {
	dir string
	mu  sync.RWMutex
	// notes[account][messageID]
	notes map[string]map[string]Note
	dirty map[string]map[string]bool // files needing a write (true) or removal (false)
}

// Load reads every note file under dir (one folder per account). A missing
// dir is an empty store; files that fail to parse are skipped.
func Load(dir string) (*Store, error) {
	s := &Store{dir: dir, notes: map[string]map[string]Note{}, dirty: map[string]map[string]bool{}}
	accounts, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, err
	}
	for _, a := range accounts {
		if !a.IsDir() {
			continue
		}
		files, err := os.ReadDir(filepath.Join(dir, a.Name()))
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, a.Name(), f.Name()))
			if err != nil {
				continue
			}
			n, ok := parseFile(raw)
			if !ok {
				continue
			}
			s.put(a.Name(), n)
		}
	}
	return s, nil
}

func (s *Store) put(account string, n Note) {
	if s.notes[account] == nil {
		s.notes[account] = map[string]Note{}
	}
	s.notes[account][n.MessageID] = n
}

// Get returns the note for messageID in account (ok = false when none).
func (s *Store) Get(account, messageID string) (Note, bool) {
	if s == nil {
		return Note{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	n, ok := s.notes[account][messageID]
	return n, ok
}

// Snippet is Get(...).FirstLine() — "" when there is no note. The inbox row
// renderer calls this for every row, so it must stay cheap (one map lookup).
func (s *Store) Snippet(account, messageID string) string {
	n, ok := s.Get(account, messageID)
	if !ok {
		return ""
	}
	return n.FirstLine()
}

// Set stores n for account in memory and marks its file for writing (Save).
func (s *Store) Set(account string, n Note) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.put(account, n)
	s.markDirty(account, n.MessageID, true)
}

// Delete forgets the note for messageID (memory now, file on Save).
func (s *Store) Delete(account, messageID string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.notes[account], messageID)
	s.markDirty(account, messageID, false)
}

func (s *Store) markDirty(account, messageID string, write bool) {
	if s.dirty[account] == nil {
		s.dirty[account] = map[string]bool{}
	}
	s.dirty[account][messageID] = write
}

// All returns a copy of every note of account.
func (s *Store) All(account string) []Note {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Note, 0, len(s.notes[account]))
	for _, n := range s.notes[account] {
		out = append(out, n)
	}
	return out
}

// Merge replaces account's notes with the server's set (server wins per
// Message-ID), keeping local-only notes that the server does not know yet
// (they were saved while offline or before the first sync). Every merged
// note is marked for writing so the files mirror the server.
func (s *Store) Merge(account string, server []Note) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range server {
		local, ok := s.notes[account][n.MessageID]
		if ok && local.Body == n.Body && local.UID == n.UID {
			continue // identical: no file write
		}
		s.put(account, n)
		s.markDirty(account, n.MessageID, true)
	}
}

// Save writes every changed file atomically (temp + rename, 0600) and removes
// the files of deleted notes.
func (s *Store) Save() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for account, ids := range s.dirty {
		dir := filepath.Join(s.dir, safeName(account))
		for id, write := range ids {
			path := filepath.Join(dir, safeName(id)+".md")
			if !write {
				if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
					return err
				}
				continue
			}
			n, ok := s.notes[account][id]
			if !ok {
				continue
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return err
			}
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, formatFile(n), 0o600); err != nil {
				return err
			}
			if err := os.Rename(tmp, path); err != nil {
				return err
			}
		}
		delete(s.dirty, account)
	}
	return nil
}

func formatFile(n Note) []byte {
	var b strings.Builder
	b.WriteString("Message-ID: " + n.MessageID + "\n")
	b.WriteString("UID: " + strconv.FormatUint(uint64(n.UID), 10) + "\n")
	b.WriteString("Date: " + n.Date.UTC().Format(time.RFC3339) + "\n")
	b.WriteString("\n")
	b.WriteString(n.Body)
	b.WriteString("\n")
	return []byte(b.String())
}

func parseFile(raw []byte) (Note, bool) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	head, body, found := strings.Cut(text, "\n\n")
	if !found {
		head = text
	}
	var n Note
	for _, l := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "message-id":
			n.MessageID = v
		case "uid":
			u, _ := strconv.ParseUint(v, 10, 32)
			n.UID = uint32(u)
		case "date":
			n.Date, _ = time.Parse(time.RFC3339, v)
		}
	}
	if n.MessageID == "" {
		return Note{}, false
	}
	n.Body = normaliseBody(body)
	return n, true
}

// safeName maps an account key or Message-ID to a file name: characters
// outside [A-Za-z0-9._@%+-] collapse to "_" and the angle brackets of a
// Message-ID are dropped. Email addresses pass through verbatim.
func safeName(s string) string {
	s = strings.Trim(s, "<>")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '@', r == '%', r == '+', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}
