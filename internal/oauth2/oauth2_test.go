package oauth2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// --- XOAUTH2 SASL ---

func TestXOAuth2Client_Start(t *testing.T) {
	c := XOAuth2Client("user@example.com", "ya29.token123")
	mech, ir, err := c.Start()
	if err != nil {
		t.Fatal(err)
	}
	if mech != "XOAUTH2" {
		t.Errorf("mechanism = %q, want XOAUTH2", mech)
	}
	want := "user=user@example.com\x01auth=Bearer ya29.token123\x01\x01"
	if string(ir) != want {
		t.Errorf("initial response = %q, want %q", ir, want)
	}
}

func TestXOAuth2Client_Next(t *testing.T) {
	c := XOAuth2Client("u", "t")
	resp, err := c.(*xoauth2Client).Next([]byte("challenge"))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp) != 0 {
		t.Errorf("Next should return empty response, got %q", resp)
	}
}

// --- Token persistence ---

func TestSaveAndLoadToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens", "test.json")

	tok := &oauth2.Token{
		AccessToken:  "access123",
		RefreshToken: "refresh456",
		TokenType:    "Bearer",
		Expiry:       time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := saveToken(path, tok); err != nil {
		t.Fatalf("saveToken: %v", err)
	}

	loaded, err := loadToken(path)
	if err != nil {
		t.Fatalf("loadToken: %v", err)
	}
	if loaded.AccessToken != tok.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, tok.AccessToken)
	}
	if loaded.RefreshToken != tok.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, tok.RefreshToken)
	}
}

func TestSaveToken_FilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")

	tok := &oauth2.Token{AccessToken: "secret"}
	if err := saveToken(path, tok); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Errorf("token file mode = %o, want 0600", mode)
	}
}

func TestSaveToken_DirectoryPermissions(t *testing.T) {
	dir := t.TempDir()
	tokenDir := filepath.Join(dir, "newdir")
	path := filepath.Join(tokenDir, "tok.json")

	tok := &oauth2.Token{AccessToken: "secret"}
	if err := saveToken(path, tok); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(tokenDir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Errorf("token dir mode = %o, want 0700", mode)
	}
}

func TestLoadToken_MissingFile(t *testing.T) {
	_, err := loadToken("/nonexistent/path/token.json")
	if err == nil {
		t.Fatal("expected error for missing token file")
	}
}

func TestLoadToken_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	os.WriteFile(path, []byte("not json"), 0600)

	_, err := loadToken(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- tokenStorage (file-only paths) ---

// When no account name is configured, tokenStorage must go straight to the file.
// This covers the headless/SSH fallback case where keyring is intentionally unused.
func TestTokenStorage_FileOnlyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")
	s := newTokenStorage("", path)

	tok := &oauth2.Token{
		AccessToken:  "access123",
		RefreshToken: "refresh456",
		TokenType:    "Bearer",
		Expiry:       time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := s.Save(tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.AccessToken != tok.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, tok.AccessToken)
	}
	if loaded.RefreshToken != tok.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, tok.RefreshToken)
	}
}

func TestTokenStorage_NoBackendsConfigured(t *testing.T) {
	s := newTokenStorage("", "")
	if _, err := s.Load(); err == nil {
		t.Error("Load with no account and no path should error")
	}
	if err := s.Save(&oauth2.Token{AccessToken: "x"}); err == nil {
		t.Error("Save with no account and no path should error")
	}
}

func TestTokenStorage_LoadMissingFile(t *testing.T) {
	dir := t.TempDir()
	s := newTokenStorage("", filepath.Join(dir, "missing.json"))
	if _, err := s.Load(); err == nil {
		t.Error("expected error loading missing file")
	}
}

// --- Config helpers ---

func TestConfig_RedirectPort(t *testing.T) {
	tests := []struct {
		port int
		want int
	}{
		{0, 8085},    // default
		{9090, 9090}, // explicit
	}
	for _, tt := range tests {
		c := Config{RedirectPort: tt.port}
		if got := c.redirectPort(); got != tt.want {
			t.Errorf("redirectPort(%d) = %d, want %d", tt.port, got, tt.want)
		}
	}
}

func TestConfig_RedirectURL(t *testing.T) {
	c := Config{RedirectPort: 8085}
	want := "http://localhost:8085/callback"
	if got := c.redirectURL(); got != want {
		t.Errorf("redirectURL = %q, want %q", got, want)
	}
}

func TestConfig_Timeouts(t *testing.T) {
	// Defaults
	c := Config{}
	if got := c.discoveryTimeout(); got != 10*time.Second {
		t.Errorf("default discoveryTimeout = %v, want 10s", got)
	}
	if got := c.authFlowTimeout(); got != 5*time.Minute {
		t.Errorf("default authFlowTimeout = %v, want 5m", got)
	}

	// Custom
	c = Config{DiscoveryTimeout: 30 * time.Second, AuthFlowTimeout: 10 * time.Minute}
	if got := c.discoveryTimeout(); got != 30*time.Second {
		t.Errorf("custom discoveryTimeout = %v, want 30s", got)
	}
	if got := c.authFlowTimeout(); got != 10*time.Minute {
		t.Errorf("custom authFlowTimeout = %v, want 10m", got)
	}
}

// --- Endpoint resolution ---

func TestResolve_ManualURLs(t *testing.T) {
	c := Config{
		AuthURL:  "https://auth.example.com/authorize",
		TokenURL: "https://auth.example.com/token",
	}
	auth, tok, err := c.resolve(context.Background(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if auth != c.AuthURL {
		t.Errorf("authURL = %q, want %q", auth, c.AuthURL)
	}
	if tok != c.TokenURL {
		t.Errorf("tokenURL = %q, want %q", tok, c.TokenURL)
	}
}

func TestResolve_NoURLsNoIssuer(t *testing.T) {
	c := Config{}
	_, _, err := c.resolve(context.Background(), 5*time.Second)
	if err == nil {
		t.Fatal("expected error when no URLs and no issuer")
	}
}

func TestDiscoverEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "https://provider.example.com/auth",
			"token_endpoint":         "https://provider.example.com/token",
		})
	}))
	defer srv.Close()

	auth, tok, err := discoverEndpoints(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if auth != "https://provider.example.com/auth" {
		t.Errorf("authURL = %q", auth)
	}
	if tok != "https://provider.example.com/token" {
		t.Errorf("tokenURL = %q", tok)
	}
}

func TestDiscoverEndpoints_MissingFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": "https://provider.example.com/auth",
			// token_endpoint missing
		})
	}))
	defer srv.Close()

	_, _, err := discoverEndpoints(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for missing token_endpoint")
	}
}

func TestDiscoverEndpoints_BadHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, _, err := discoverEndpoints(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
}

// --- Token file security: no sensitive data leaked in error messages ---

func TestTokenErrors_NoTokenLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tok.json")
	// Write a token, then corrupt it
	os.WriteFile(path, []byte(`{"access_token":"SUPER_SECRET_TOKEN"}`), 0600)

	// Corrupt the file
	os.WriteFile(path, []byte("corrupted{"), 0600)
	_, err := loadToken(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if contains(err.Error(), "SUPER_SECRET_TOKEN") {
		t.Error("error message contains token value — credential leak")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
