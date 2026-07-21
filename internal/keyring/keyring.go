// Package keyring provides secure storage for passwords and OAuth2 tokens
// using the OS keyring (macOS Keychain, Linux Secret Service, Windows Credential Manager).
package keyring

import (
	"encoding/json"
	"fmt"
	"runtime"

	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

const serviceName = "neomd"

// passwordKey returns the keyring key for an account password.
func passwordKey(accountName string) string {
	return fmt.Sprintf("account/%s/password", accountName)
}

// oauth2Key returns the keyring key for an OAuth2 token.
func oauth2Key(accountName string) string {
	return fmt.Sprintf("account/%s/oauth2", accountName)
}

// SetPassword stores a password in the OS keyring.
func SetPassword(accountName, password string) error {
	return keyring.Set(serviceName, passwordKey(accountName), password)
}

// GetPassword retrieves a password from the OS keyring.
// Returns ErrNotFound if no password exists for this account.
func GetPassword(accountName string) (string, error) {
	password, err := keyring.Get(serviceName, passwordKey(accountName))
	if err == keyring.ErrNotFound {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("keyring get password: %w", err)
	}
	return password, nil
}

// DeletePassword removes a password from the OS keyring.
func DeletePassword(accountName string) error {
	err := keyring.Delete(serviceName, passwordKey(accountName))
	if err == keyring.ErrNotFound {
		return nil // Already deleted is fine
	}
	return err
}

// clientSecretKey returns the keyring key for an OAuth2 client secret.
func clientSecretKey(accountName string) string {
	return fmt.Sprintf("account/%s/oauth2_client_secret", accountName)
}

// SetClientSecret stores an OAuth2 client secret in the OS keyring.
func SetClientSecret(accountName, secret string) error {
	return keyring.Set(serviceName, clientSecretKey(accountName), secret)
}

// GetClientSecret retrieves an OAuth2 client secret from the OS keyring.
// Returns ErrNotFound if no entry exists for this account.
func GetClientSecret(accountName string) (string, error) {
	secret, err := keyring.Get(serviceName, clientSecretKey(accountName))
	if err == keyring.ErrNotFound {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("keyring get client secret: %w", err)
	}
	return secret, nil
}

// DeleteClientSecret removes an OAuth2 client secret from the OS keyring.
func DeleteClientSecret(accountName string) error {
	err := keyring.Delete(serviceName, clientSecretKey(accountName))
	if err == keyring.ErrNotFound {
		return nil
	}
	return err
}

// SetOAuth2Token stores an OAuth2 token in the OS keyring as JSON.
//
// On macOS, only the refresh-relevant fields (RefreshToken, TokenType,
// Expiry) are persisted; the access_token is dropped. Google access tokens
// can exceed 2 KB which, once base64-encoded and wrapped in the `security`
// CLI command zalando/go-keyring uses on macOS, breaches its 4096-byte
// command-length limit. With AccessToken empty, oauth2.Token.Valid() returns
// false, so the library refreshes on first use using the stored
// RefreshToken. Other platforms store the full token unchanged.
func SetOAuth2Token(accountName string, token *oauth2.Token) error {
	data, err := json.Marshal(prepareTokenForStorage(token, runtime.GOOS))
	if err != nil {
		return fmt.Errorf("marshal oauth2 token: %w", err)
	}
	return keyring.Set(serviceName, oauth2Key(accountName), string(data))
}

// prepareTokenForStorage returns the token shape to persist for a given OS.
// On darwin, AccessToken is dropped to stay under zalando/go-keyring's
// 4096-byte `security` command-length limit. Other platforms keep the token
// unchanged.
func prepareTokenForStorage(token *oauth2.Token, goos string) *oauth2.Token {
	if goos != "darwin" {
		return token
	}
	return &oauth2.Token{
		TokenType:    token.TokenType,
		RefreshToken: token.RefreshToken,
		Expiry:       token.Expiry,
	}
}

// GetOAuth2Token retrieves an OAuth2 token from the OS keyring.
// Returns ErrNotFound if no token exists for this account.
func GetOAuth2Token(accountName string) (*oauth2.Token, error) {
	data, err := keyring.Get(serviceName, oauth2Key(accountName))
	if err == keyring.ErrNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("keyring get oauth2 token: %w", err)
	}

	var token oauth2.Token
	if err := json.Unmarshal([]byte(data), &token); err != nil {
		return nil, fmt.Errorf("unmarshal oauth2 token: %w", err)
	}
	return &token, nil
}

// DeleteOAuth2Token removes an OAuth2 token from the OS keyring.
func DeleteOAuth2Token(accountName string) error {
	err := keyring.Delete(serviceName, oauth2Key(accountName))
	if err == keyring.ErrNotFound {
		return nil // Already deleted is fine
	}
	return err
}

// ErrNotFound is returned when a keyring entry doesn't exist.
var ErrNotFound = fmt.Errorf("keyring: not found")
