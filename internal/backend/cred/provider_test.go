package cred

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileProvider_ParseCredentialFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		data        []byte
		expectError bool
		expectCreds *CredentialSet
	}{
		{
			name: "valid credential file",
			data: []byte(`{
				"access_key_id": "AKIA123456789",
				"secret_access_key": "secret123",
				"session_token": "token123"
			}`),
			expectCreds: &CredentialSet{
				AccessKeyID:     "AKIA123456789",
				SecretAccessKey: "secret123",
				SessionToken:    "token123",
			},
		},
		{
			name: "valid without session token",
			data: []byte(`{
				"access_key_id": "AKIA123456789",
				"secret_access_key": "secret123"
			}`),
			expectCreds: &CredentialSet{
				AccessKeyID:     "AKIA123456789",
				SecretAccessKey: "secret123",
				SessionToken:    "",
			},
		},
		{
			name:        "missing access key",
			data:        []byte(`{"secret_access_key": "secret123"}`),
			expectError: true,
		},
		{
			name:        "missing secret key",
			data:        []byte(`{"access_key_id": "AKIA123456789"}`),
			expectError: true,
		},
		{
			name:        "invalid JSON",
			data:        []byte(`{invalid json}`),
			expectError: true,
		},
		{
			name:        "empty data",
			data:        []byte(``),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := parseCredentialFile(tt.data)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, creds)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectCreds, creds)
			}
		})
	}
}

func TestNewFileProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		path        string
		expectError bool
	}{
		{
			name: "valid path",
			path: "/tmp/creds.json",
		},
		{
			name:        "empty path",
			path:        "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewFileProvider(tt.path)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, provider)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, provider)
			}
		})
	}
}

func TestNewDefaultProvider(t *testing.T) {
	// DO NOT call t.Parallel() - this test modifies environment variables
	// and must run serially to avoid conflicts with other tests

	ctx := context.Background()

	// Use t.Setenv for proper test isolation - automatically restores after test
	testAccessKey := "AKIAIOSFODNN7EXAMPLE"
	testSecretKey := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	t.Setenv("AWS_ACCESS_KEY_ID", testAccessKey)
	t.Setenv("AWS_SECRET_ACCESS_KEY", testSecretKey)

	provider := NewDefaultProvider()
	assert.NotNil(t, provider)

	// Default provider should retrieve credentials from AWS SDK's default chain
	creds, err := provider.Get(ctx)
	require.NoError(t, err, "expected DefaultProvider to successfully retrieve credentials")
	require.NotNil(t, creds)
	assert.Equal(t, testAccessKey, creds.AccessKeyID)
	assert.Equal(t, testSecretKey, creds.SecretAccessKey)
}

func TestInlineProvider(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name            string
		accessKeyID     string
		secretAccessKey string
		sessionToken    string
		expectError     bool
	}{
		{
			name:            "valid credentials",
			accessKeyID:     "AKIA123456789",
			secretAccessKey: "secret123",
			sessionToken:    "token123",
		},
		{
			name:            "without session token",
			accessKeyID:     "AKIA123456789",
			secretAccessKey: "secret123",
		},
		{
			name:            "missing access key",
			accessKeyID:     "",
			secretAccessKey: "secret123",
			expectError:     true,
		},
		{
			name:            "missing secret key",
			accessKeyID:     "AKIA123456789",
			secretAccessKey: "",
			expectError:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewInlineProvider(tt.accessKeyID, tt.secretAccessKey, tt.sessionToken)
			assert.NotNil(t, provider)

			creds, err := provider.Get(ctx)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, creds)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.accessKeyID, creds.AccessKeyID)
				assert.Equal(t, tt.secretAccessKey, creds.SecretAccessKey)
				assert.Equal(t, tt.sessionToken, creds.SessionToken)
			}
		})
	}
}

func TestNewSecretsManagerProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		secretName  string
		region      string
		expectError bool
	}{
		{
			name:       "valid config",
			secretName: "my-secret",
			region:     "us-east-1",
		},
		{
			name:        "missing secret name",
			secretName:  "",
			region:      "us-east-1",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewSecretsManagerProvider(tt.secretName, tt.region)

			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, provider)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, provider)
			}
		})
	}
}
