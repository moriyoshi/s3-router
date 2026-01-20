package cred

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/moriyoshi/s3-router/internal/config"
)

func TestNewProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      *config.CredentialsConfig
		expectError bool
	}{
		{
			name: "default provider",
			config: &config.CredentialsConfig{
				Type: "default",
			},
		},
		{
			name: "file provider",
			config: &config.CredentialsConfig{
				Type: "file",
				Path: "/tmp/creds.json",
			},
		},
		{
			name: "file provider missing path",
			config: &config.CredentialsConfig{
				Type: "file",
			},
			expectError: true,
		},
		{
			name: "aws-secrets-manager provider",
			config: &config.CredentialsConfig{
				Type:       "aws-secrets-manager",
				SecretName: "my-secret",
				Region:     "us-east-1",
			},
		},
		{
			name: "aws-secrets-manager missing secret name",
			config: &config.CredentialsConfig{
				Type:   "aws-secrets-manager",
				Region: "us-east-1",
			},
			expectError: true,
		},
		{
			name: "inline provider",
			config: &config.CredentialsConfig{
				Type:            "inline",
				AccessKeyID:     "AKIA123456789",
				SecretAccessKey: "secret123",
			},
		},
		{
			name:        "unknown provider type",
			config:      &config.CredentialsConfig{Type: "unknown"},
			expectError: true,
		},
		{
			name:        "nil config",
			config:      nil,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewProvider(tt.config)

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

func TestNewProvider_WithAssumeRole(t *testing.T) {
	t.Parallel()

	config := &config.CredentialsConfig{
		Type:   "file",
		Path:   "/tmp/creds.json",
		Region: "us-east-1",
		AssumeRole: &config.CredentialsAssumeRole{
			RoleARN:     "arn:aws:iam::123456789:role/MyRole",
			SessionName: "test-session",
			Duration:    1800,
		},
	}

	provider, err := NewProvider(config)
	require.NoError(t, err)
	assert.NotNil(t, provider)

	// Verify it's an AssumeRoleProvider wrapping a FileProvider
	assumeRoleProvider, ok := provider.(*AssumeRoleProvider)
	assert.True(t, ok, "expected AssumeRoleProvider, got %T", provider)

	// Verify the base provider is a FileProvider
	if assumeRoleProvider != nil {
		_, ok := assumeRoleProvider.baseProvider.(*FileProvider)
		assert.True(t, ok, "expected base provider to be FileProvider")
	}
}
