package auth

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestInMemoryCredentialsStore(t *testing.T) {
	t.Parallel()
	creds := map[string]string{
		"AKIAIOSFODNN7EXAMPLE":  "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		"AKIA2222222222EXAMPLE": "secret2",
	}

	store := NewInMemoryCredentialsStore(creds)

	tests := []struct {
		name         string
		accessKey    string
		expectErr    bool
		expectSecret string
	}{
		{
			name:         "valid access key",
			accessKey:    "AKIAIOSFODNN7EXAMPLE",
			expectErr:    false,
			expectSecret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
		{
			name:         "another valid key",
			accessKey:    "AKIA2222222222EXAMPLE",
			expectErr:    false,
			expectSecret: "secret2",
		},
		{
			name:      "non-existent key",
			accessKey: "AKIAINVALIDKEYEXAMPLE",
			expectErr: true,
		},
		{
			name:      "empty key",
			accessKey: "",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := store.GetSecret(tt.accessKey)
			if tt.expectErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.expectErr && secret != tt.expectSecret {
				t.Errorf("expected %q, got %q", tt.expectSecret, secret)
			}
		})
	}
}

func TestFileCredentialsStore(t *testing.T) {
	t.Parallel()
	// Create temporary credentials file
	tmpFile, err := os.CreateTemp("", "credentials*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Write test credentials
	creds := map[string]Credentials{
		"AKIAIOSFODNN7EXAMPLE": {
			SecretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			Enabled:   true,
			CreatedAt: time.Now().Format(time.RFC3339),
		},
		"AKIADISABLEDKEYEXMP": {
			SecretKey: "secret_for_disabled_key",
			Enabled:   false,
		},
	}

	data, err := json.MarshalIndent(creds, "", "  ")
	assert.NoError(t, err)

	_, err = tmpFile.Write(data)
	assert.NoError(t, err)
	tmpFile.Close()

	// Create store
	store, err := NewFileCredentialsStore(tmpFile.Name())
	assert.NoError(t, err)

	tests := []struct {
		name         string
		accessKey    string
		expectErr    bool
		expectSecret string
	}{
		{
			name:         "valid enabled key",
			accessKey:    "AKIAIOSFODNN7EXAMPLE",
			expectErr:    false,
			expectSecret: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		},
		{
			name:      "disabled key",
			accessKey: "AKIADISABLEDKEYEXMP",
			expectErr: true,
		},
		{
			name:      "non-existent key",
			accessKey: "AKIAINVALIDKEYEXAMPLE",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := store.GetSecret(tt.accessKey)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectSecret, secret)
			}
		})
	}
}

func TestFileCredentialsStore_Reload(t *testing.T) {
	t.Parallel()
	tmpFile, err := os.CreateTemp("", "credentials*.json")
	assert.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	// Initial credentials
	creds := map[string]Credentials{
		"AKIAIOSFODNN7EXAMPLE": {
			SecretKey: "secret1",
			Enabled:   true,
		},
	}
	data, _ := json.Marshal(creds)
	tmpFile.Write(data)
	tmpFile.Close()

	store, err := NewFileCredentialsStore(tmpFile.Name())
	assert.NoError(t, err)

	// Verify initial load
	secret, err := store.GetSecret("AKIAIOSFODNN7EXAMPLE")
	assert.NoError(t, err)
	assert.Equal(t, "secret1", secret)

	// Update file with new credentials
	creds2 := map[string]Credentials{
		"AKIANEWKEYEXAMPLE": {
			SecretKey: "newsecret",
			Enabled:   true,
		},
	}
	data2, _ := json.Marshal(creds2)
	os.WriteFile(tmpFile.Name(), data2, 0644)

	// Reload
	err = store.Reload()
	assert.NoError(t, err)

	// Verify old key no longer accessible
	_, err = store.GetSecret("AKIAIOSFODNN7EXAMPLE")
	assert.Error(t, err)

	// Verify new key is accessible
	secret, err = store.GetSecret("AKIANEWKEYEXAMPLE")
	assert.NoError(t, err)
	assert.Equal(t, "newsecret", secret)
}
