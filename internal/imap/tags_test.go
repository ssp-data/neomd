package imap

// Keyword-tagging feature tests against the in-memory IMAP server: STORE
// add/remove round trip, case-insensitivity, FETCH parsing and MOVE keeping
// the keyword. No network (see memserver_test.go for the TLS setup).

import (
	"context"
	"fmt"
	"testing"
)

func keywordsOfUID(t *testing.T, cli *Client, folder string, uid uint32) []string {
	t.Helper()
	emails, err := cli.FetchHeaders(context.Background(), folder, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range emails {
		if e.UID == uid {
			return e.Keywords
		}
	}
	return nil
}

func TestMem_AddRemoveKeyword_RoundTrip(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "tags-rt", false)

	if err := cli.AddKeyword(context.Background(), "INBOX", 1, "work"); err != nil {
		t.Fatalf("AddKeyword: %v", err)
	}
	got := keywordsOfUID(t, cli, "INBOX", 1)
	if len(got) != 1 || got[0] != "work" {
		t.Fatalf("keywords after add = %v, want [work]", got)
	}
	// System flags must not leak into Keywords.
	seedMessage(t, user, "INBOX", "tags-seen", true)
	if err := cli.MarkSeen(context.Background(), "INBOX", 2); err != nil {
		t.Fatal(err)
	}
	if got := keywordsOfUID(t, cli, "INBOX", 2); len(got) != 0 {
		t.Errorf("seen-only message reports keywords %v, want none", got)
	}

	if err := cli.RemoveKeyword(context.Background(), "INBOX", 1, "work"); err != nil {
		t.Fatalf("RemoveKeyword: %v", err)
	}
	if got := keywordsOfUID(t, cli, "INBOX", 1); len(got) != 0 {
		t.Errorf("keywords after remove = %v, want none", got)
	}
}

func TestMem_KeywordCaseInsensitive(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "tags-case", false)

	// neomd normalizes to lowercase on write, even when handed mixed case.
	if err := cli.AddKeyword(context.Background(), "INBOX", 1, "ProJect-X"); err != nil {
		t.Fatalf("AddKeyword: %v", err)
	}
	got := keywordsOfUID(t, cli, "INBOX", 1)
	if len(got) != 1 || got[0] != "project-x" {
		t.Fatalf("keywords = %v, want lowercase [project-x]", got)
	}
	// Removal is case-insensitive too (a server may have normalized the case).
	if err := cli.RemoveKeyword(context.Background(), "INBOX", 1, "PROJECT-X"); err != nil {
		t.Fatalf("RemoveKeyword: %v", err)
	}
	if got := keywordsOfUID(t, cli, "INBOX", 1); len(got) != 0 {
		t.Errorf("keywords after case-mismatched remove = %v, want none", got)
	}
}

func TestMem_FetchHeaders_ParsesKeywords(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "tags-parse", true)
	if err := cli.AddKeyword(context.Background(), "INBOX", 1, "work"); err != nil {
		t.Fatal(err)
	}
	if err := cli.AddKeyword(context.Background(), "INBOX", 1, "invoice"); err != nil {
		t.Fatal(err)
	}

	emails, err := cli.FetchHeaders(context.Background(), "INBOX", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(emails) != 1 {
		t.Fatalf("got %d emails, want 1", len(emails))
	}
	e := emails[0]
	if !e.Seen {
		t.Error("\\Seen lost while keywords present")
	}
	// Keywords are parsed sorted — server flag order is map-random, so the
	// parser normalizes for deterministic downstream state.
	if fmt.Sprint(e.Keywords) != "[invoice work]" {
		t.Errorf("Keywords = %v, want sorted [invoice work]", e.Keywords)
	}
}

func TestMem_FetchHeadersByUID_ParsesKeywords(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "ToScreen", "tags-byuid", false)
	if err := cli.AddKeyword(context.Background(), "ToScreen", 1, "work"); err != nil {
		t.Fatal(err)
	}
	emails, err := cli.FetchHeadersByUID(context.Background(), "ToScreen", []uint32{1})
	if err != nil {
		t.Fatal(err)
	}
	if len(emails) != 1 || fmt.Sprint(emails[0].Keywords) != "[work]" {
		t.Errorf("FetchHeadersByUID Keywords = %+v, want [work]", emails)
	}
}

func TestMem_MovePreservesKeyword(t *testing.T) {
	cli, user := startMemIMAP(t)
	seedMessage(t, user, "INBOX", "tags-move", true)
	if err := cli.AddKeyword(context.Background(), "INBOX", 1, "work"); err != nil {
		t.Fatal(err)
	}

	destUID, err := cli.MoveMessage(context.Background(), "INBOX", 1, "Archive")
	if err != nil {
		t.Fatalf("MoveMessage: %v", err)
	}
	if destUID == 0 {
		t.Fatal("no destination UID returned")
	}
	got := keywordsOfUID(t, cli, "Archive", destUID)
	if len(got) != 1 || got[0] != "work" {
		t.Errorf("keywords after move = %v, want [work] — tags must survive folder moves", got)
	}
	if !cliKeywordsSeen(t, cli, "Archive", destUID) {
		t.Error("\\Seen lost across the move")
	}
}

func cliKeywordsSeen(t *testing.T, cli *Client, folder string, uid uint32) bool {
	t.Helper()
	emails, err := cli.FetchHeaders(context.Background(), folder, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range emails {
		if e.UID == uid {
			return e.Seen
		}
	}
	return false
}

func TestMem_StoreKeyword_MissingMailboxErrors(t *testing.T) {
	cli, _ := startMemIMAP(t)
	if err := cli.AddKeyword(context.Background(), "NoSuchBox", 1, "work"); err == nil {
		t.Error("expected SELECT error for missing mailbox")
	}
	// The failed SELECT must leave the connection usable (selection cleared).
	if cli.selectedMailbox != "" {
		t.Errorf("selectedMailbox = %q after failed SELECT, want empty", cli.selectedMailbox)
	}
}
