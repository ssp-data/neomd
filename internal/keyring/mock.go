// Package keyring provides secure storage for passwords and OAuth2 tokens.
// This file contains mock implementations for testing.
package keyring

import (
	"encoding/json"
	"sync"

	"golang.org/x/oauth2"
)

// MockBackend is a memory-based mock keyring for testing.
// Use this in tests by setting the environment variable NEOMD_TEST_KEYRING_MOCK=1.
type MockBackend struct {
	mu    sync.RWMutex
	store map[string]string
}

// NewMockBackend creates a new mock keyring backend.
func NewMockBackend() *MockBackend {
	return &MockBackend{
		store: make(map[string]string),
	}
}

// Set stores a value in the mock keyring.
func (m *MockBackend) Set(service, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store[service+"/"+key] = value
	return nil
}

// Get retrieves a value from the mock keyring.
func (m *MockBackend) Get(service, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	value, ok := m.store[service+"/"+key]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

// Delete removes a value from the mock keyring.
func (m *MockBackend) Delete(service, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.store, service+"/"+key)
	return nil
}

// Clear removes all entries from the mock keyring.
func (m *MockBackend) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.store = make(map[string]string)
}

// MockProvider is the global mock backend instance used in tests.
// Tests can access this to set up pre-existing credentials.
var MockProvider = NewMockBackend()

// IsMockEnabled returns true if the mock backend should be used.
// This is controlled by the NEOMD_TEST_KEYRING_MOCK environment variable.
// In a real implementation, this would check os.Getenv.
func IsMockEnabled() bool {
	// This will be checked by the main implementation
	return false
}

// SetMockPassword is a test helper to pre-populate a password.
func SetMockPassword(accountName, password string) {
	MockProvider.Set(serviceName, passwordKey(accountName), password)
}

// SetMockOAuth2Token is a test helper to pre-populate an OAuth2 token.
func SetMockOAuth2Token(accountName string, token *oauth2.Token) {
	data, _ := json.Marshal(token)
	MockProvider.Set(serviceName, oauth2Key(accountName), string(data))
}
