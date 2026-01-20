package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Credentials represents a stored access key + secret key pair
type Credentials struct {
	SecretKey string `json:"secret_key"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"created_at,omitempty"`
}

// CredentialsStore manages access to stored credentials
type CredentialsStore interface {
	GetSecret(accessKeyID string) (string, error)
}

// FileCredentialsStore loads credentials from a JSON file
type FileCredentialsStore struct {
	filePath    string
	credentials map[string]Credentials
	mu          sync.RWMutex
}

// NewFileCredentialsStore creates a credential store from a JSON file
func NewFileCredentialsStore(filePath string) (*FileCredentialsStore, error) {
	store := &FileCredentialsStore{
		filePath:    filePath,
		credentials: make(map[string]Credentials),
	}

	if err := store.Reload(); err != nil {
		return nil, err
	}

	return store, nil
}

// Reload reads credentials from file
func (s *FileCredentialsStore) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	var creds map[string]Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return fmt.Errorf("failed to parse credentials file: %w", err)
	}

	s.credentials = creds
	return nil
}

// GetSecret returns the secret key for a given access key ID
func (s *FileCredentialsStore) GetSecret(accessKeyID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cred, exists := s.credentials[accessKeyID]
	if !exists {
		return "", fmt.Errorf("access key not found: %s", accessKeyID)
	}

	if !cred.Enabled {
		return "", fmt.Errorf("access key is disabled: %s", accessKeyID)
	}

	return cred.SecretKey, nil
}

// InMemoryCredentialsStore is a simple in-memory implementation for testing
type InMemoryCredentialsStore struct {
	credentials map[string]string
	mu          sync.RWMutex
}

// NewInMemoryCredentialsStore creates an in-memory credential store
func NewInMemoryCredentialsStore(creds map[string]string) *InMemoryCredentialsStore {
	store := &InMemoryCredentialsStore{
		credentials: make(map[string]string),
	}
	for k, v := range creds {
		store.credentials[k] = v
	}
	return store
}

// GetSecret returns the secret key for a given access key ID
func (s *InMemoryCredentialsStore) GetSecret(accessKeyID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	secret, exists := s.credentials[accessKeyID]
	if !exists {
		return "", fmt.Errorf("access key not found: %s", accessKeyID)
	}

	return secret, nil
}
