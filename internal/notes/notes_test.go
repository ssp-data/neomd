package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotes_BuildParseRoundTrip(t *testing.T) {
	n := Note{MessageID: "<abc@example.com>", Body: "have replied\n\n- waiting on invoice\nümlaut", Date: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
	raw := Build(n, "me@example.com", "Re: Invoice")
	if !strings.Contains(string(raw), HeaderFor+": <abc@example.com>\r\n") {
		t.Fatalf("missing %s header:\n%s", HeaderFor, raw)
	}
	if strings.Contains(string(raw), "\n\n") {
		t.Errorf("bare LF in wire format")
	}
	got, ok := Parse(raw)
	if !ok {
		t.Fatal("Parse: not a note")
	}
	if got.MessageID != n.MessageID {
		t.Errorf("MessageID = %q", got.MessageID)
	}
	if got.Body != n.Body {
		t.Errorf("Body = %q, want %q", got.Body, n.Body)
	}
	if !got.Date.Equal(n.Date) {
		t.Errorf("Date = %v, want %v", got.Date, n.Date)
	}
}

func TestNotes_ParseRejectsForeignMessage(t *testing.T) {
	if _, ok := Parse([]byte("From: x@y\r\nSubject: hi\r\n\r\nbody\r\n")); ok {
		t.Fatal("a message without X-Neomd-Note-For must not parse as a note")
	}
}

func TestNotes_FirstLineSkipsBlankAndMarkers(t *testing.T) {
	n := Note{Body: "\n\n## have replied  \nsecond"}
	if got := n.FirstLine(); got != "have replied" {
		t.Errorf("FirstLine = %q", got)
	}
	if (Note{}).FirstLine() != "" {
		t.Error("empty note must have empty snippet")
	}
}

func TestNotesStore_RoundTripPerAccountFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	s.Set("me@x.com", Note{MessageID: "<a/b@x>", UID: 7, Body: "this is the book email", Date: when})
	s.Set("other@y.com", Note{MessageID: "<a/b@x>", Body: "different account, same id"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	// Account dirs are the verbatim address; the Message-ID is sanitised.
	if _, err := os.Stat(filepath.Join(dir, "me@x.com", "a_b@x.md")); err != nil {
		t.Fatalf("note file: %v", err)
	}
	s2, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	n, ok := s2.Get("me@x.com", "<a/b@x>")
	if !ok || n.Body != "this is the book email" || n.UID != 7 || !n.Date.Equal(when) {
		t.Errorf("reloaded note = %+v, ok=%v", n, ok)
	}
	if n2, _ := s2.Get("other@y.com", "<a/b@x>"); n2.Body != "different account, same id" {
		t.Errorf("accounts not isolated: %+v", n2)
	}
	if s2.Snippet("me@x.com", "<nope>") != "" {
		t.Error("snippet for unknown id must be empty")
	}
}

func TestNotesStore_DeleteRemovesFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	s, _ := Load(dir)
	s.Set("me@x.com", Note{MessageID: "<id@x>", Body: "x"})
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s.Delete("me@x.com", "<id@x>")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "me@x.com", "id@x.md")); !os.IsNotExist(err) {
		t.Fatalf("file still there: %v", err)
	}
	if _, ok := s.Get("me@x.com", "<id@x>"); ok {
		t.Error("deleted note still in memory")
	}
}

func TestNotesStore_MergeServerWinsKeepsLocalOnly(t *testing.T) {
	s, _ := Load(filepath.Join(t.TempDir(), "notes"))
	s.Set("me@x.com", Note{MessageID: "<same@x>", Body: "old local"})
	s.Set("me@x.com", Note{MessageID: "<local-only@x>", Body: "offline note"})
	s.Merge("me@x.com", []Note{
		{MessageID: "<same@x>", UID: 3, Body: "server version"},
		{MessageID: "<new@x>", UID: 4, Body: "from another machine"},
	})
	if n, _ := s.Get("me@x.com", "<same@x>"); n.Body != "server version" || n.UID != 3 {
		t.Errorf("server must win: %+v", n)
	}
	if n, ok := s.Get("me@x.com", "<local-only@x>"); !ok || n.Body != "offline note" {
		t.Errorf("local-only note lost: %+v", n)
	}
	if n, ok := s.Get("me@x.com", "<new@x>"); !ok || n.Body != "from another machine" {
		t.Errorf("new server note missing: %+v", n)
	}
	if len(s.All("me@x.com")) != 3 {
		t.Errorf("All = %d", len(s.All("me@x.com")))
	}
}

func TestNotesStore_NilSafe(t *testing.T) {
	var s *Store
	s.Set("a", Note{MessageID: "<x>"})
	s.Delete("a", "<x>")
	if s.Snippet("a", "<x>") != "" || s.Save() != nil || s.All("a") != nil {
		t.Error("nil store must be inert")
	}
}
