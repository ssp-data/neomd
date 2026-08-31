package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	goIMAP "github.com/sspaeti/neomd/internal/imap"
)

type fakeBodyFetcher struct {
	body      string
	err       error
	reqFolder string
	reqUID    uint32
}

func (f *fakeBodyFetcher) FetchBody(_ context.Context, folder string, uid uint32) (string, string, string, []goIMAP.Attachment, string, goIMAP.SpyPixelInfo, error) {
	f.reqFolder = folder
	f.reqUID = uid
	if f.err != nil {
		return "", "", "", nil, "", goIMAP.SpyPixelInfo{}, f.err
	}
	return f.body, "", "", nil, "", goIMAP.SpyPixelInfo{}, nil
}

func TestParseReadArgs(t *testing.T) {
	opts, err := parseReadArgs([]string{"--folder", "Feed", "--uid", "42"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opts.folder != "Feed" || opts.uid != 42 {
		t.Errorf("opts = %+v", opts)
	}
	for _, bad := range [][]string{
		{"--uid", "42"},                    // missing folder
		{"--folder", "Feed"},               // missing uid
		{"--folder", "Feed", "--uid", "x"}, // bad uid
		{"--folder", "Feed", "--uid", "0"}, // zero uid
	} {
		if _, err := parseReadArgs(bad); err == nil {
			t.Errorf("parseReadArgs(%v): expected error, got nil", bad)
		}
	}
}

func TestRunRead_JSONShape(t *testing.T) {
	fetcher := &fakeBodyFetcher{body: "# Hello\n\nSome **markdown** body."}
	var buf bytes.Buffer
	code := runRead(context.Background(), testFolders(), fetcher,
		[]string{"--folder", "papertrail", "--uid", "42"}, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if fetcher.reqFolder != "HEY/Paper Trail" || fetcher.reqUID != 42 {
		t.Errorf("fetched (%q, %d), want (HEY/Paper Trail, 42)", fetcher.reqFolder, fetcher.reqUID)
	}
	var out struct {
		OK        bool   `json:"ok"`
		Folder    string `json:"folder"`
		UID       uint32 `json:"uid"`
		Body      string `json:"body"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if !out.OK || out.Folder != "PaperTrail" || out.UID != 42 {
		t.Errorf("output = %+v", out)
	}
	if out.Body != "# Hello\n\nSome **markdown** body." {
		t.Errorf("body = %q", out.Body)
	}
	if out.Truncated {
		t.Error("truncated = true for small body")
	}
}

func TestRunRead_TruncatesLongBody(t *testing.T) {
	fetcher := &fakeBodyFetcher{body: strings.Repeat("a", 100)}
	var buf bytes.Buffer
	runRead(context.Background(), testFolders(), fetcher,
		[]string{"--folder", "Inbox", "--uid", "1", "--max-bytes", "50"}, &buf)
	var out struct {
		OK        bool   `json:"ok"`
		Body      string `json:"body"`
		Truncated bool   `json:"truncated"`
	}
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, buf.String())
	}
	if !out.Truncated {
		t.Error("truncated = false, want true")
	}
	if len(out.Body) > 50 {
		t.Errorf("body length = %d, want <= 50", len(out.Body))
	}
}

func TestRunRead_FetchErrorJSON(t *testing.T) {
	fetcher := &fakeBodyFetcher{err: errors.New("gone")}
	var buf bytes.Buffer
	code := runRead(context.Background(), testFolders(), fetcher,
		[]string{"--folder", "Inbox", "--uid", "9"}, &buf)
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

func TestRunRead_UnknownFolderErrorJSON(t *testing.T) {
	fetcher := &fakeBodyFetcher{}
	var buf bytes.Buffer
	code := runRead(context.Background(), testFolders(), fetcher,
		[]string{"--folder", "Bogus", "--uid", "9"}, &buf)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if fetcher.reqUID != 0 {
		t.Error("fetch attempted despite unknown folder")
	}
	var out struct {
		OK bool `json:"ok"`
	}
	json.Unmarshal(buf.Bytes(), &out)
	if out.OK {
		t.Error("ok = true, want false")
	}
}
