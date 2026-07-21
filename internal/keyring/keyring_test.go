package keyring

import (
	"encoding/json"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func TestPasswordRoundTrip(t *testing.T) {
	// Use mock backend for tests
	mock := NewMockBackend()

	account := "test@example.com"
	password := "secret123"

	// Test Set and Get
	key := passwordKey(account)
	err := mock.Set(serviceName, key, password)
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	got, err := mock.Get(serviceName, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got != password {
		t.Errorf("got %q, want %q", got, password)
	}
}

func TestGetNotFound(t *testing.T) {
	mock := NewMockBackend()

	_, err := mock.Get(serviceName, passwordKey("nonexistent"))
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDelete(t *testing.T) {
	mock := NewMockBackend()
	account := "test@example.com"
	key := passwordKey(account)

	// Set then delete
	mock.Set(serviceName, key, "password")
	err := mock.Delete(serviceName, key)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it's gone
	_, err = mock.Get(serviceName, key)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestOAuth2TokenRoundTrip(t *testing.T) {
	mock := NewMockBackend()
	account := "oauth@example.com"

	token := &oauth2.Token{
		AccessToken:  "access_token_123",
		RefreshToken: "refresh_token_456",
		Expiry:       time.Now().Add(time.Hour),
	}

	// Marshal and store
	data, _ := json.Marshal(token)
	key := oauth2Key(account)
	err := mock.Set(serviceName, key, string(data))
	if err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Retrieve and unmarshal
	got, err := mock.Get(serviceName, key)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	var gotToken oauth2.Token
	if err := json.Unmarshal([]byte(got), &gotToken); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if gotToken.AccessToken != token.AccessToken {
		t.Errorf("AccessToken: got %q, want %q", gotToken.AccessToken, token.AccessToken)
	}
	if gotToken.RefreshToken != token.RefreshToken {
		t.Errorf("RefreshToken: got %q, want %q", gotToken.RefreshToken, token.RefreshToken)
	}
}

func TestKeyFormats(t *testing.T) {
	tests := []struct {
		account   string
		wantPass  string
		wantOauth string
	}{
		{
			account:   "Personal",
			wantPass:  "account/Personal/password",
			wantOauth: "account/Personal/oauth2",
		},
		{
			account:   "me@example.com",
			wantPass:  "account/me@example.com/password",
			wantOauth: "account/me@example.com/oauth2",
		},
		{
			account:   "user/name/with/slashes",
			wantPass:  "account/user/name/with/slashes/password",
			wantOauth: "account/user/name/with/slashes/oauth2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.account, func(t *testing.T) {
			gotPass := passwordKey(tt.account)
			if gotPass != tt.wantPass {
				t.Errorf("passwordKey: got %q, want %q", gotPass, tt.wantPass)
			}

			gotOauth := oauth2Key(tt.account)
			if gotOauth != tt.wantOauth {
				t.Errorf("oauth2Key: got %q, want %q", gotOauth, tt.wantOauth)
			}
		})
	}
}

func TestPrepareTokenForStorage_DarwinStripsAccessToken(t *testing.T) {
	tok := &oauth2.Token{
		AccessToken:  "ya29." + string(make([]byte, 2500)),
		RefreshToken: "1//0gRefresh",
		TokenType:    "Bearer",
		Expiry:       time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	}

	got := prepareTokenForStorage(tok, "darwin")
	if got.AccessToken != "" {
		t.Errorf("AccessToken should be stripped on darwin, got len=%d", len(got.AccessToken))
	}
	if got.RefreshToken != tok.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, tok.RefreshToken)
	}
	if got.TokenType != tok.TokenType {
		t.Errorf("TokenType = %q, want %q", got.TokenType, tok.TokenType)
	}
	if !got.Expiry.Equal(tok.Expiry) {
		t.Errorf("Expiry = %v, want %v", got.Expiry, tok.Expiry)
	}

	// Marshaled size must fit comfortably under the 4096-byte security CLI limit
	// (after base64 expansion ~4/3 and ~80 bytes of command wrapping).
	data, _ := json.Marshal(got)
	if len(data) > 1024 {
		t.Errorf("stripped token JSON unexpectedly large: %d bytes", len(data))
	}
}

func TestPrepareTokenForStorage_OtherOSPreservesToken(t *testing.T) {
	tok := &oauth2.Token{
		AccessToken:  "access123",
		RefreshToken: "refresh456",
		TokenType:    "Bearer",
		Expiry:       time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
	}

	for _, goos := range []string{"linux", "windows", "freebsd"} {
		t.Run(goos, func(t *testing.T) {
			got := prepareTokenForStorage(tok, goos)
			if got.AccessToken != tok.AccessToken {
				t.Errorf("AccessToken = %q, want %q (no stripping on %s)", got.AccessToken, tok.AccessToken, goos)
			}
			if got.RefreshToken != tok.RefreshToken {
				t.Errorf("RefreshToken = %q, want %q", got.RefreshToken, tok.RefreshToken)
			}
		})
	}
}

func TestClear(t *testing.T) {
	mock := NewMockBackend()

	// Add some data
	mock.Set(serviceName, "key1", "value1")
	mock.Set(serviceName, "key2", "value2")

	// Clear all
	mock.Clear()

	// Verify all gone
	_, err := mock.Get(serviceName, "key1")
	if err != ErrNotFound {
		t.Errorf("key1: expected ErrNotFound after clear, got %v", err)
	}
	_, err = mock.Get(serviceName, "key2")
	if err != ErrNotFound {
		t.Errorf("key2: expected ErrNotFound after clear, got %v", err)
	}
}
