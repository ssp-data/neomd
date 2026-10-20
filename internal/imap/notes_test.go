package imap

import (
	"context"
	"testing"
	"time"

	"github.com/sspaeti/neomd/internal/notes"
)

// The sidecar round trip: a save creates the folder, FetchNotes reads the
// note back with its UID, a second save replaces the sidecar (old UID gone,
// one note left), delete empties the folder — and the email the note is
// about is never touched.
func TestMem_Notes_SaveFetchReplaceDelete(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "the-book", false)
	ctx := context.Background()
	const folder = "neomd/Notes" // does not exist on the memserver yet

	if got, err := cli.FetchNotes(ctx, folder); err != nil || len(got) != 0 {
		t.Fatalf("FetchNotes on missing folder = %v, %v; want none, nil", got, err)
	}

	n := notes.Note{MessageID: "<the-book@example.com>", Body: "this is the book email", Date: time.Now()}
	uid1, err := cli.SaveNote(ctx, folder, notes.Build(n, "u@example.com", "the-book"), 0)
	if err != nil {
		t.Fatalf("SaveNote: %v", err)
	}
	if uid1 == 0 {
		t.Fatal("SaveNote returned UID 0 (memserver supports UIDPLUS)")
	}
	got, err := cli.FetchNotes(ctx, folder)
	if err != nil || len(got) != 1 {
		t.Fatalf("FetchNotes = %v, %v; want one note", got, err)
	}
	if got[0].MessageID != n.MessageID || got[0].Body != n.Body || got[0].UID != uid1 {
		t.Errorf("note = %+v", got[0])
	}

	// Edit: replaces the sidecar, never accumulates.
	n.Body = "have replied"
	uid2, err := cli.SaveNote(ctx, folder, notes.Build(n, "u@example.com", "the-book"), uid1)
	if err != nil {
		t.Fatalf("SaveNote replace: %v", err)
	}
	got, _ = cli.FetchNotes(ctx, folder)
	if len(got) != 1 || got[0].Body != "have replied" || got[0].UID != uid2 || uid2 == uid1 {
		t.Fatalf("after replace: %+v (uid1=%d uid2=%d)", got, uid1, uid2)
	}

	// The email itself is untouched: same UID, same flags.
	emails, err := cli.FetchHeaders(ctx, "INBOX", 0)
	if err != nil || len(emails) != 1 || emails[0].UID != 1 || emails[0].Seen || len(emails[0].Keywords) != 0 {
		t.Fatalf("INBOX changed: %+v, %v", emails, err)
	}

	if err := cli.DeleteNote(ctx, folder, uid2); err != nil {
		t.Fatalf("DeleteNote: %v", err)
	}
	if got, _ := cli.FetchNotes(ctx, folder); len(got) != 0 {
		t.Fatalf("after delete: %+v", got)
	}
}

// A foreign message in the notes folder (no X-Neomd-Note-For) is ignored.
func TestMem_Notes_ForeignMessageIgnored(t *testing.T) {
	cli, user := startMemIMAP(t)
	if err := user.Create("Notes", nil); err != nil {
		t.Fatal(err)
	}
	seedMessage(t, user, "Notes", "not-a-note", true)
	got, err := cli.FetchNotes(context.Background(), "Notes")
	if err != nil || len(got) != 0 {
		t.Fatalf("FetchNotes = %v, %v; want none", got, err)
	}
}
