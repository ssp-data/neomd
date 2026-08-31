package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	goIMAP "github.com/sspaeti/neomd/internal/imap"
)

type fakeScreener struct {
	calls []string
	err   error
}

func (f *fakeScreener) Approve(from string) error        { return f.record("approve", from) }
func (f *fakeScreener) Block(from string) error          { return f.record("block", from) }
func (f *fakeScreener) MarkFeed(from string) error       { return f.record("feed", from) }
func (f *fakeScreener) MarkPaperTrail(from string) error { return f.record("paper", from) }
func (f *fakeScreener) record(op, from string) error {
	f.calls = append(f.calls, op+":"+from)
	return f.err
}

type move struct {
	src string
	uid uint32
	dst string
}

type fakeScreenIMAP struct {
	toScreen []goIMAP.Email
	moves    []move
	moveErr  error
}

func (f *fakeScreenIMAP) SearchUIDs(_ context.Context, folder string) ([]uint32, error) {
	uids := make([]uint32, 0, len(f.toScreen))
	for _, e := range f.toScreen {
		if e.Folder == folder {
			uids = append(uids, e.UID)
		}
	}
	return uids, nil
}

func (f *fakeScreenIMAP) FetchHeadersByUID(_ context.Context, folder string, uids []uint32) ([]goIMAP.Email, error) {
	var out []goIMAP.Email
	for _, e := range f.toScreen {
		for _, u := range uids {
			if e.UID == u && e.Folder == folder {
				out = append(out, e)
			}
		}
	}
	return out, nil
}

func (f *fakeScreenIMAP) MoveMessage(_ context.Context, src string, uid uint32, dst string) (uint32, error) {
	if f.moveErr != nil {
		return 0, f.moveErr
	}
	f.moves = append(f.moves, move{src, uid, dst})
	return uid, nil
}

func TestParseScreenArgs(t *testing.T) {
	opts, err := parseScreenArgs([]string{"--from", "jane@example.com", "--action", "in"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.from != "jane@example.com" || opts.action != "in" {
		t.Errorf("opts = %+v", opts)
	}
	for _, bad := range [][]string{
		{"--action", "in"},                           // missing from
		{"--from", "a@b.c"},                          // missing action
		{"--from", "a@b.c", "--action", "yolo"},      // bad action
		{"--from", "a@b.c", "--action", "in", "--x"}, // unknown flag
	} {
		if _, err := parseScreenArgs(bad); err == nil {
			t.Errorf("parseScreenArgs(%v): expected error, got nil", bad)
		}
	}
}

func screenTestEmails() []goIMAP.Email {
	return []goIMAP.Email{
		{UID: 1, From: "Jane <jane@example.com>", Folder: "ToScreen"},
		{UID: 2, From: "bob@example.com", Folder: "ToScreen"},
		{UID: 3, From: "JANE@example.com", Folder: "ToScreen"}, // same sender, different case/format
	}
}

func TestRunScreen_ApproveMovesAllFromSender(t *testing.T) {
	sc := &fakeScreener{}
	cli := &fakeScreenIMAP{toScreen: screenTestEmails()}
	var buf bytes.Buffer
	code := runScreen(context.Background(), testFolders(), sc, cli,
		[]string{"--from", "Jane <jane@example.com>", "--action", "in"}, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(sc.calls) != 1 || sc.calls[0] != "approve:Jane <jane@example.com>" {
		t.Errorf("screener calls = %v", sc.calls)
	}
	if len(cli.moves) != 2 {
		t.Fatalf("moves = %v, want 2 (uid 1 and 3)", cli.moves)
	}
	for _, m := range cli.moves {
		if m.src != "ToScreen" || m.dst != "INBOX" {
			t.Errorf("move %+v, want ToScreen → INBOX", m)
		}
	}
	var out struct {
		OK    bool `json:"ok"`
		Moved int  `json:"moved"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if !out.OK || out.Moved != 2 {
		t.Errorf("output = %+v, want ok=true moved=2", out)
	}
}

func TestRunScreen_ActionDestinations(t *testing.T) {
	cases := []struct {
		action string
		screen string
		dst    string
	}{
		{"in", "approve", "INBOX"},
		{"out", "block", "ScreenedOutFolder"},
		{"feed", "feed", "Feed"},
		{"paper", "paper", "HEY/Paper Trail"},
	}
	folders := testFolders()
	folders.ScreenedOut = "ScreenedOutFolder"
	for _, c := range cases {
		sc := &fakeScreener{}
		cli := &fakeScreenIMAP{toScreen: []goIMAP.Email{
			{UID: 9, From: "x@y.z", Folder: "ToScreen"},
		}}
		var buf bytes.Buffer
		runScreen(context.Background(), folders, sc, cli,
			[]string{"--from", "x@y.z", "--action", c.action}, &buf)
		if len(sc.calls) != 1 || sc.calls[0] != c.screen+":x@y.z" {
			t.Errorf("action %s: screener calls = %v, want %s", c.action, sc.calls, c.screen)
		}
		if len(cli.moves) != 1 || cli.moves[0].dst != c.dst {
			t.Errorf("action %s: moves = %v, want dst %s", c.action, cli.moves, c.dst)
		}
	}
}

func TestRunScreen_ScreenerErrorJSON(t *testing.T) {
	sc := &fakeScreener{err: errors.New("disk full")}
	cli := &fakeScreenIMAP{toScreen: screenTestEmails()}
	var buf bytes.Buffer
	code := runScreen(context.Background(), testFolders(), sc, cli,
		[]string{"--from", "jane@example.com", "--action", "in"}, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(cli.moves) != 0 {
		t.Errorf("moved %v despite screener error — list update must come first", cli.moves)
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.Unmarshal(buf.Bytes(), &out)
	if out.OK || out.Error == "" {
		t.Errorf("output = %+v, want ok=false with error", out)
	}
}

func TestRunScreen_MoveErrorJSON(t *testing.T) {
	sc := &fakeScreener{}
	cli := &fakeScreenIMAP{toScreen: screenTestEmails(), moveErr: errors.New("MOVE failed")}
	var buf bytes.Buffer
	code := runScreen(context.Background(), testFolders(), sc, cli,
		[]string{"--from", "jane@example.com", "--action", "in"}, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var out struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.Unmarshal(buf.Bytes(), &out)
	if out.OK || out.Error == "" {
		t.Errorf("output = %+v, want ok=false with error", out)
	}
}

func TestRunScreen_RefusesTrashDestination(t *testing.T) {
	folders := testFolders()
	folders.Trash = "Trash"
	folders.ScreenedOut = "Trash" // misconfiguration: screener destination = Trash
	sc := &fakeScreener{}
	cli := &fakeScreenIMAP{toScreen: screenTestEmails()}
	var buf bytes.Buffer
	code := runScreen(context.Background(), folders, sc, cli,
		[]string{"--from", "jane@example.com", "--action", "out"}, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(sc.calls) != 0 || len(cli.moves) != 0 {
		t.Errorf("acted despite unsafe config: calls=%v moves=%v", sc.calls, cli.moves)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	json.Unmarshal(buf.Bytes(), &out)
	if out.OK {
		t.Error("ok = true, want false for Trash destination")
	}
}
