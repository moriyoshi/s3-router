package server

import (
	"net/http"
	"testing"

	"github.com/moriyoshi/s3-router/internal/auth"
	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestVerifier_MissingAuthentication(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{
			"backend1": {
				ID:     "backend1",
				Bucket: "test-bucket",
			},
		},
	}

	credStore := auth.NewInMemoryCredentialsStore(map[string]string{
		"AKIAIOSFODNN7EXAMPLE": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})

	v := NewVerifier(cfg, credStore)

	// Request with no authentication
	req, _ := http.NewRequest("GET", "http://example.com/object", nil)
	auth, err := v.VerifyRequest(req)

	assert.Error(t, err)
	assert.False(t, auth.IsAuthenticated)

	// Check error type
	authErr, ok := err.(*AuthError)
	assert.True(t, ok, "expected AuthError type")
	assert.Equal(t, "MissingAuthenticationToken", authErr.Code)
}

func TestVerifier_InvalidAuthHeader(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{},
	}
	credStore := auth.NewInMemoryCredentialsStore(map[string]string{})
	v := NewVerifier(cfg, credStore)

	tests := []struct {
		name       string
		authHeader string
		expectCode string
	}{
		{
			name:       "invalid scheme",
			authHeader: "Bearer token123",
			expectCode: "InvalidAuthHeader",
		},
		{
			name:       "malformed header",
			authHeader: "AWS4-HMAC-SHA256",
			expectCode: "InvalidAuthHeader",
		},
		{
			name:       "missing components",
			authHeader: "AWS4-HMAC-SHA256 Credential=AKIA123",
			expectCode: "InvalidAuthHeader",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "http://example.com/object", nil)
			req.Header.Set("Authorization", tt.authHeader)
			_, err := v.VerifyRequest(req)

			assert.Error(t, err)
			authErr, ok := err.(*AuthError)
			assert.True(t, ok)
			assert.Equal(t, tt.expectCode, authErr.Code)
		})
	}
}

func TestVerifier_InvalidAccessKey(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{},
	}
	credStore := auth.NewInMemoryCredentialsStore(map[string]string{
		"AKIAIOSFODNN7EXAMPLE": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	v := NewVerifier(cfg, credStore)

	req, _ := http.NewRequest("GET", "http://example.com/object", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAINVALIDKEY/20230101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=sig123")
	req.Header.Set("X-Amz-Date", "20230101T000000Z")

	_, err := v.VerifyRequest(req)

	assert.Error(t, err)
	authErr, ok := err.(*AuthError)
	assert.True(t, ok)
	assert.Equal(t, "InvalidAccessKeyId", authErr.Code)
}

func TestVerifier_MissingDateHeader(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{},
	}
	credStore := auth.NewInMemoryCredentialsStore(map[string]string{
		"AKIAIOSFODNN7EXAMPLE": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	v := NewVerifier(cfg, credStore)

	req, _ := http.NewRequest("GET", "http://example.com/object", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20230101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=sig123")
	// Missing X-Amz-Date

	_, err := v.VerifyRequest(req)

	assert.Error(t, err)
	authErr, ok := err.(*AuthError)
	assert.True(t, ok)
	assert.Equal(t, "MissingDateHeader", authErr.Code)
}

func TestVerifier_InvalidDateFormat(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{},
	}
	credStore := auth.NewInMemoryCredentialsStore(map[string]string{
		"AKIAIOSFODNN7EXAMPLE": "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	})
	v := NewVerifier(cfg, credStore)

	req, _ := http.NewRequest("GET", "http://example.com/object", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20230101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=sig123")
	req.Header.Set("X-Amz-Date", "invalid-date")

	_, err := v.VerifyRequest(req)

	assert.Error(t, err)
	authErr, ok := err.(*AuthError)
	assert.True(t, ok)
	assert.Equal(t, "InvalidDateHeader", authErr.Code)
}

func TestVerifier_ParseAuthHeader(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	credStore := auth.NewInMemoryCredentialsStore(map[string]string{})
	v := NewVerifier(cfg, credStore)

	tests := []struct {
		name                string
		authHeader          string
		expectAccessKey     string
		expectSignedHeaders string
		expectSignature     string
		expectErr           bool
	}{
		{
			name:                "valid header",
			authHeader:          "AWS4-HMAC-SHA256 Credential=AKIA123/20230101/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc123",
			expectAccessKey:     "AKIA123",
			expectSignedHeaders: "host;x-amz-date",
			expectSignature:     "abc123",
			expectErr:           false,
		},
		{
			name:       "missing credential",
			authHeader: "AWS4-HMAC-SHA256 SignedHeaders=host, Signature=sig",
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accessKey, signedHeaders, signature, err := v.parseAuthHeader(tt.authHeader)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectAccessKey, accessKey)
				assert.Equal(t, tt.expectSignedHeaders, signedHeaders)
				assert.Equal(t, tt.expectSignature, signature)
			}
		})
	}
}

func TestVerifier_DisabledCredential(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{},
	}
	credStore := auth.NewInMemoryCredentialsStore(map[string]string{})
	v := NewVerifier(cfg, credStore)

	req, _ := http.NewRequest("GET", "http://example.com/object", nil)
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIADISABLED/20230101/us-east-1/s3/aws4_request, SignedHeaders=host, Signature=sig")
	req.Header.Set("X-Amz-Date", "20230101T000000Z")

	_, err := v.VerifyRequest(req)

	assert.Error(t, err)
	authErr, ok := err.(*AuthError)
	assert.True(t, ok)
	assert.Equal(t, "InvalidAccessKeyId", authErr.Code)
}

func TestGetSigningKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		secretAccessKey string
		dateStamp       string
		region          string
		service         string
		expectKeyLength int
	}{
		{
			name:            "standard AWS credentials",
			secretAccessKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
			dateStamp:       "20230101",
			region:          "us-east-1",
			service:         "s3",
			expectKeyLength: 32, // SHA256 produces 32 bytes
		},
		{
			name:            "empty credentials",
			secretAccessKey: "",
			dateStamp:       "",
			region:          "",
			service:         "",
			expectKeyLength: 32,
		},
		{
			name:            "different region",
			secretAccessKey: "secret",
			dateStamp:       "20240101",
			region:          "eu-west-1",
			service:         "s3",
			expectKeyLength: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := GetSigningKey(tt.secretAccessKey, tt.dateStamp, tt.region, tt.service)

			assert.NotNil(t, key)
			assert.Equal(t, tt.expectKeyLength, len(key))

			// Verify deterministic output
			key2 := GetSigningKey(tt.secretAccessKey, tt.dateStamp, tt.region, tt.service)
			assert.True(t, bytesEqual(key, key2), "signing key is not deterministic")
		})
	}
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
