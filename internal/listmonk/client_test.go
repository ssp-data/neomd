package listmonk

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCreateAndSchedule(t *testing.T) {
	var gotCreate campaignRequest
	var gotStatus statusRequest
	var createCalled, statusCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify basic auth
		user, pass, ok := r.BasicAuth()
		if !ok || user != "testuser" || pass != "testtoken" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		switch {
		case r.Method == "POST" && r.URL.Path == "/api/campaigns":
			createCalled = true
			json.NewDecoder(r.Body).Decode(&gotCreate)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"id": 42},
			})

		case r.Method == "PUT" && strings.HasSuffix(r.URL.Path, "/status"):
			statusCalled = true
			json.NewDecoder(r.Body).Decode(&gotStatus)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewClient(Config{
		URL:      srv.URL,
		APIUser:  "testuser",
		APIToken: "testtoken",
	})

	id, err := c.CreateAndSchedule("My Newsletter", "# Hello\n\nWorld", []int{1, 2}, 30*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("got campaign ID %d, want 42", id)
	}
	if !createCalled {
		t.Error("create endpoint not called")
	}
	if !statusCalled {
		t.Error("status endpoint not called")
	}
	if gotCreate.Subject != "My Newsletter" {
		t.Errorf("subject = %q, want %q", gotCreate.Subject, "My Newsletter")
	}
	if gotCreate.ContentType != "markdown" {
		t.Errorf("content_type = %q, want %q", gotCreate.ContentType, "markdown")
	}
	if len(gotCreate.Lists) != 2 || gotCreate.Lists[0] != 1 || gotCreate.Lists[1] != 2 {
		t.Errorf("lists = %v, want [1 2]", gotCreate.Lists)
	}
	if gotCreate.SendAt == "" {
		t.Error("send_at should be set")
	}
	if gotStatus.Status != "scheduled" {
		t.Errorf("status = %q, want %q", gotStatus.Status, "scheduled")
	}
}

func TestCreateAndSchedule_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"bad request"}`, http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewClient(Config{URL: srv.URL, APIUser: "u", APIToken: "t"})
	_, err := c.CreateAndSchedule("Test", "body", []int{1}, 10*time.Minute)
	if err == nil {
		t.Fatal("expected error for bad request")
	}
	if !strings.Contains(err.Error(), "HTTP 400") {
		t.Errorf("error should mention HTTP 400, got: %v", err)
	}
}

func TestResolveListIDs(t *testing.T) {
	triggers := []Trigger{
		{Address: "listmonk-newsletter@ssp.sh", ListIDs: []int{1}},
		{Address: "listmonk-book@ssp.sh", ListIDs: []int{2}},
		{Address: "listmonk@ssp.sh", ListIDs: []int{1, 2}},
	}

	tests := []struct {
		name    string
		to      string
		wantLen int
		wantIDs []int
	}{
		{"newsletter only", "listmonk-newsletter@ssp.sh", 1, []int{1}},
		{"book only", "listmonk-book@ssp.sh", 1, []int{2}},
		{"both lists via single addr", "listmonk@ssp.sh", 2, []int{1, 2}},
		{"no match", "someone@example.com", 0, nil},
		{"case insensitive", "Listmonk-Newsletter@SSP.SH", 1, []int{1}},
		{"with display name", "Newsletter <listmonk-newsletter@ssp.sh>", 1, []int{1}},
		{"multiple to addrs", "listmonk-newsletter@ssp.sh, listmonk-book@ssp.sh", 2, []int{1, 2}},
		{"dedup list IDs", "listmonk@ssp.sh, listmonk-newsletter@ssp.sh", 2, []int{1, 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ids := ResolveListIDs(triggers, tt.to)
			if len(ids) != tt.wantLen {
				t.Errorf("got %d IDs %v, want %d", len(ids), ids, tt.wantLen)
			}
		})
	}
}

func TestIsTriggerAddress(t *testing.T) {
	triggers := []Trigger{
		{Address: "listmonk@ssp.sh", ListIDs: []int{1}},
	}
	if !IsTriggerAddress(triggers, "listmonk@ssp.sh") {
		t.Error("should match trigger address")
	}
	if IsTriggerAddress(triggers, "random@example.com") {
		t.Error("should not match non-trigger address")
	}
}
