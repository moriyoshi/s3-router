package proxy

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/backend/cred"
	"github.com/moriyoshi/s3-router/internal/config"
)

func TestS3SignerCanonicalRequest(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "us-east-1")

	req := &http.Request{
		Method: http.MethodPut,
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/test-bucket/test-key",
		},
		Header: http.Header{
			"Host":                 []string{"s3.amazonaws.com"},
			"X-Amz-Date":           []string{"20240101T000000Z"},
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			"Content-Type":         []string{"application/octet-stream"},
			"Content-Length":       []string{"100"},
		},
	}

	canonical := signer.buildCanonicalRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")

	// Verify canonical request is not empty and has expected structure
	assert.NotEmpty(t, canonical)
	canonicalStr := string(canonical)
	assert.True(t, strings.Contains(canonicalStr, "PUT"), "canonical request should contain method")
	assert.True(t, strings.Contains(canonicalStr, "/test-bucket/test-key"), "canonical request should contain path")
	assert.True(t, strings.Contains(canonicalStr, "host:s3.amazonaws.com"), "canonical request should contain host header")
	assert.True(t, strings.Contains(canonicalStr, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"), "canonical request should contain payload hash")
}

func TestS3SignerSignRequest(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "us-east-1")

	req := &http.Request{
		Method: http.MethodPut,
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/test-bucket/test-key",
		},
		Header: http.Header{
			"Host":                 []string{"s3.amazonaws.com"},
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			"Content-Type":         []string{"application/octet-stream"},
			"Content-Length":       []string{"100"},
		},
	}

	err := signer.SignRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.NoError(t, err)

	// Verify authorization header was set
	authHeader := req.Header.Get("Authorization")
	assert.NotEmpty(t, authHeader)
	assert.True(t, strings.HasPrefix(authHeader, "AWS4-HMAC-SHA256"))
	assert.Contains(t, authHeader, "Credential=AKIAIOSFODNN7EXAMPLE")
	assert.Contains(t, authHeader, "SignedHeaders=")
	assert.Contains(t, authHeader, "Signature=")

	// Verify x-amz-date was set
	amzDate := req.Header.Get("x-amz-date")
	assert.NotEmpty(t, amzDate)
	assert.Len(t, amzDate, 16) // YYYYMMDDTHHMMSSZ
}

func TestS3SignerWithUnsignedPayload(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "us-east-1")

	req := &http.Request{
		Method: http.MethodPut,
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/test-bucket/test-key",
		},
		Header: http.Header{
			"Host":           []string{"s3.amazonaws.com"},
			"Content-Type":   []string{"application/octet-stream"},
			"Content-Length": []string{"100"},
		},
	}

	err := signer.SignRequest(req, "UNSIGNED-PAYLOAD")
	assert.NoError(t, err)

	// Verify x-amz-content-sha256 is set to UNSIGNED-PAYLOAD
	assert.Equal(t, "UNSIGNED-PAYLOAD", req.Header.Get("x-amz-content-sha256"))

	// Verify authorization header was set
	authHeader := req.Header.Get("Authorization")
	assert.NotEmpty(t, authHeader)
}

func TestS3SignerCanonicalHeaders(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "test",
		SecretAccessKey: "test",
	}, "us-east-1")

	req := &http.Request{
		Host: "s3.amazonaws.com", // Go stores Host in req.Host, not req.Header
		Header: http.Header{
			"Content-Type":   []string{"application/octet-stream"},
			"X-Amz-Date":     []string{"20240101T000000Z"},
			"X-Amz-Meta-Key": []string{"value"},
			"Authorization":  []string{"should-be-skipped"},
		},
	}

	canonical := signer.buildCanonicalHeaders(req)

	// Verify headers are included
	assert.Contains(t, canonical, "content-type:application/octet-stream")
	assert.Contains(t, canonical, "host:s3.amazonaws.com")
	assert.Contains(t, canonical, "x-amz-date:20240101T000000Z")
	assert.Contains(t, canonical, "x-amz-meta-key:value")

	// Verify authorization header is excluded
	assert.NotContains(t, canonical, "authorization")
}

func TestS3SignerShouldSignHeader(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{}, "us-east-1")

	tests := []struct {
		name       string
		header     string
		shouldSign bool
	}{
		{"host header", "host", false}, // will be overridden by req.Host and not propagated
		{"x-amz header", "x-amz-date", true},
		{"x-amz header 2", "x-amz-content-sha256", true},
		{"content-type header", "content-type", true},
		{"content-length header", "content-length", true},
		{"authorization header", "authorization", false},
		{"random header", "user-agent", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := signer.shouldSignHeader(tt.header)
			assert.Equal(t, tt.shouldSign, result)
		})
	}
}

func TestS3SignerGetSignedHeaders(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{}, "us-east-1")

	req := &http.Request{
		Host: "s3.amazonaws.com", // Go stores Host in req.Host, not req.Header
		Header: http.Header{
			"Content-Type":  []string{"application/octet-stream"},
			"X-Amz-Date":    []string{"20240101T000000Z"},
			"Authorization": []string{"should-be-skipped"},
			"User-Agent":    []string{"should-be-skipped"},
		},
	}

	signed := signer.getSignedHeaders(req)

	// Should be sorted alphabetically
	expected := "content-type;host;x-amz-date"
	assert.Equal(t, expected, signed)
}

func TestS3SignerHostFromReqHost(t *testing.T) {
	t.Parallel()
	// This test verifies that the signer correctly uses req.Host for the host header,
	// which is where Go's http package stores the Host (not in req.Header).
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "us-east-1")

	// Create request with Host in req.Host (as Go does), NOT in req.Header
	req := &http.Request{
		Method: http.MethodPut,
		Host:   "my-bucket.s3.us-east-1.amazonaws.com",
		URL: &url.URL{
			Scheme: "https",
			Host:   "my-bucket.s3.us-east-1.amazonaws.com",
			Path:   "/test-key",
		},
		Header: http.Header{
			"X-Amz-Date":           []string{"20240101T000000Z"},
			"X-Amz-Content-Sha256": []string{"UNSIGNED-PAYLOAD"},
			"Content-Type":         []string{"application/octet-stream"},
			"Content-Length":       []string{"100"},
		},
	}

	// Verify canonical headers include host from req.Host
	canonical := signer.buildCanonicalHeaders(req)
	assert.Contains(t, canonical, "host:my-bucket.s3.us-east-1.amazonaws.com",
		"canonical headers should include host from req.Host")

	// Verify signed headers include host
	signed := signer.getSignedHeaders(req)
	assert.Contains(t, signed, "host", "signed headers should include host")

	// Verify the full signing process works
	err := signer.SignRequest(req, "UNSIGNED-PAYLOAD")
	assert.NoError(t, err)

	authHeader := req.Header.Get("Authorization")
	assert.Contains(t, authHeader, "host", "Authorization header SignedHeaders should include host")
}

func TestS3SignerHostFallbackToURLHost(t *testing.T) {
	t.Parallel()
	// Test that we fall back to req.URL.Host when req.Host is empty
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "us-east-1")

	req := &http.Request{
		Method: http.MethodPut,
		Host:   "", // Empty req.Host
		URL: &url.URL{
			Scheme: "https",
			Host:   "fallback-bucket.s3.amazonaws.com",
			Path:   "/test-key",
		},
		Header: http.Header{
			"X-Amz-Date":           []string{"20240101T000000Z"},
			"X-Amz-Content-Sha256": []string{"UNSIGNED-PAYLOAD"},
		},
	}

	canonical := signer.buildCanonicalHeaders(req)
	assert.Contains(t, canonical, "host:fallback-bucket.s3.amazonaws.com",
		"canonical headers should fall back to URL.Host when req.Host is empty")

	signed := signer.getSignedHeaders(req)
	assert.Contains(t, signed, "host", "signed headers should include host even from URL fallback")
}

func TestS3SignerHostNotDuplicated(t *testing.T) {
	t.Parallel()
	// Test that host is not duplicated if somehow present in both req.Host and req.Header
	signer := NewS3Signer(aws.Credentials{}, "us-east-1")

	req := &http.Request{
		Host: "correct-host.s3.amazonaws.com",
		URL: &url.URL{
			Scheme: "https",
			Host:   "correct-host.s3.amazonaws.com",
			Path:   "/test-key",
		},
		Header: http.Header{
			"Host":       []string{"header-host.s3.amazonaws.com"}, // Should be ignored
			"X-Amz-Date": []string{"20240101T000000Z"},
		},
	}

	canonical := signer.buildCanonicalHeaders(req)
	// Should use req.Host, not the one in Header
	assert.Contains(t, canonical, "host:correct-host.s3.amazonaws.com")
	// Should not contain the header version
	assert.NotContains(t, canonical, "header-host")

	signed := signer.getSignedHeaders(req)
	// host should appear only once
	assert.Equal(t, 1, strings.Count(signed, "host"), "host should appear exactly once in signed headers")
}

func TestS3SignerCanonicalQueryString(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{}, "us-east-1")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"single param", "uploadId=abc123", "uploadId=abc123"},
		{"multiple params sorted", "partNumber=2&uploadId=abc123", "partNumber=2&uploadId=abc123"},
		{"params reverse order", "uploadId=abc123&partNumber=2", "partNumber=2&uploadId=abc123"},
		{"empty param value", "uploadId=abc123&tagging", "tagging&uploadId=abc123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := signer.buildCanonicalQueryString(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsStreamingEligible(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rc       *RequestContext
		isCopy   bool
		expected bool
	}{
		{
			name: "eligible - has content-length, no trailers",
			rc: &RequestContext{
				Headers: http.Header{
					"Content-Length": []string{"1000"},
				},
			},
			isCopy:   false,
			expected: true,
		},
		{
			name: "ineligible - no content-length",
			rc: &RequestContext{
				Headers: http.Header{},
			},
			isCopy:   false,
			expected: false,
		},
		{
			name: "ineligible - has trailer",
			rc: &RequestContext{
				Headers: http.Header{
					"Content-Length": []string{"1000"},
					"X-Amz-Trailer":  []string{"x-amz-checksum-sha256"},
				},
			},
			isCopy:   false,
			expected: false,
		},
		{
			name: "ineligible - copy operation",
			rc: &RequestContext{
				Headers: http.Header{
					"Content-Length": []string{"1000"},
				},
			},
			isCopy:   true,
			expected: false,
		},
		{
			name: "empty content-length string",
			rc: &RequestContext{
				Headers: http.Header{
					"Content-Length": []string{""},
				},
			},
			isCopy:   false,
			expected: false,
		},
		{
			name: "zero content-length",
			rc: &RequestContext{
				Headers: http.Header{
					"Content-Length": []string{"0"},
				},
			},
			isCopy:   false,
			expected: true,
		},
		{
			name: "large content-length",
			rc: &RequestContext{
				Headers: http.Header{
					"Content-Length": []string{"1099511627776"}, // 1TB
				},
			},
			expected: true,
		},
		{
			name: "empty trailer value",
			rc: &RequestContext{
				Headers: http.Header{
					"Content-Length": []string{"1000"},
					"X-Amz-Trailer":  []string{""},
				},
			},
			isCopy:   false,
			expected: true, // Empty string means no trailer is actually present
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isStreamingEligible(tt.rc, tt.isCopy)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSignStreamingRequest_UsesBackendRegion(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		region string
	}{
		{"us-east-1", "us-east-1"},
		{"us-west-2", "us-west-2"},
		{"eu-west-1", "eu-west-1"},
		{"ap-southeast-1", "ap-southeast-1"},
	}

	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a backend client with specific region
			bc := &backend.BackendClient{
				BackendConfig: &config.BackendConfig{
					ID:     "test-backend",
					Bucket: "test-bucket",
				},
				Region: tt.region,
				CredsProvider: cred.NewInlineProvider(
					"AKIAIOSFODNN7EXAMPLE",
					"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
					"",
				),
			}

			// Create a request
			req := &http.Request{
				Method: http.MethodPut,
				URL: &url.URL{
					Scheme: "https",
					Host:   "s3.amazonaws.com",
					Path:   "/test-bucket/test-key",
				},
				Header: http.Header{
					"Host":           []string{"s3.amazonaws.com"},
					"Content-Type":   []string{"application/octet-stream"},
					"Content-Length": []string{"100"},
				},
			}
			req = req.WithContext(context.Background())

			// Sign the request using SignStreamingRequest
			err := SignStreamingRequest(ctx, req, bc, "UNSIGNED-PAYLOAD")
			assert.NoError(t, err)

			// Verify the signature was created
			authHeader := req.Header.Get("Authorization")
			assert.NotEmpty(t, authHeader)
			assert.Contains(t, authHeader, "AWS4-HMAC-SHA256")

			// Verify the credential scope includes the correct region
			assert.Contains(t, authHeader, tt.region)
			assert.Contains(t, authHeader, "/s3/aws4_request")
		})
	}
}

func TestS3SignerSessionTokenPassedToUpstream(t *testing.T) {
	t.Parallel()
	// Test that session token is properly included in request headers
	sessionToken := "AQoDYXdzEJr..ExampleSessionToken...Y7iI="
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    sessionToken,
	}, "us-east-1")

	req := &http.Request{
		Method: http.MethodPut,
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/test-bucket/test-key",
		},
		Header: http.Header{
			"Host":                 []string{"s3.amazonaws.com"},
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			"Content-Type":         []string{"application/octet-stream"},
			"Content-Length":       []string{"100"},
		},
	}

	err := signer.SignRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.NoError(t, err)

	// Verify x-amz-security-token header is set
	token := req.Header.Get("x-amz-security-token")
	assert.Equal(t, sessionToken, token)
}

func TestS3SignerSessionTokenIncludedInCanonicalRequest(t *testing.T) {
	t.Parallel()
	// Test that session token is included in the canonical request for signature calculation
	sessionToken := "AQoDYXdzEJr..ExampleSessionToken...Y7iI="
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    sessionToken,
	}, "us-east-1")

	req := &http.Request{
		Method: http.MethodPut,
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/test-bucket/test-key",
		},
		Header: http.Header{
			"Host":                 []string{"s3.amazonaws.com"},
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			"Content-Type":         []string{"application/octet-stream"},
		},
	}

	err := signer.SignRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.NoError(t, err)

	// Verify x-amz-security-token is in signed headers
	authHeader := req.Header.Get("Authorization")
	assert.Contains(t, authHeader, "x-amz-security-token", "session token should be in signed headers")

	// Verify it's in the canonical headers when building the canonical request
	// by checking that the canonical request includes the security token header
	canonicalRequest := signer.buildCanonicalRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.Contains(t, string(canonicalRequest), "x-amz-security-token:"+sessionToken,
		"session token should be in canonical request")
}

func TestS3SignerNoSessionTokenWhenEmpty(t *testing.T) {
	t.Parallel()
	// Test that when session token is empty, x-amz-security-token is still set but empty
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		SessionToken:    "", // No session token
	}, "us-east-1")

	req := &http.Request{
		Method: http.MethodPut,
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/test-bucket/test-key",
		},
		Header: http.Header{
			"Host":                 []string{"s3.amazonaws.com"},
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			"Content-Type":         []string{"application/octet-stream"},
		},
	}

	err := signer.SignRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.NoError(t, err)

	// x-amz-security-token header should be set (even if empty)
	token := req.Header.Get("x-amz-security-token")
	assert.Equal(t, "", token)
}

func TestSignStreamingRequest_WithSessionToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// Test that SignStreamingRequest properly passes session token to upstream
	sessionToken := "AQoDYXdzEJr..ExampleSessionToken...Y7iI="

	bc := &backend.BackendClient{
		BackendConfig: &config.BackendConfig{
			ID:     "test-backend",
			Bucket: "test-bucket",
		},
		Region: "us-east-1",
		CredsProvider: cred.NewInlineProvider(
			"AKIAIOSFODNN7EXAMPLE",
			"wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			sessionToken,
		),
	}

	req := &http.Request{
		Method: http.MethodPut,
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/test-bucket/test-key",
		},
		Header: http.Header{
			"Host":           []string{"s3.amazonaws.com"},
			"Content-Type":   []string{"application/octet-stream"},
			"Content-Length": []string{"100"},
		},
	}
	req = req.WithContext(context.Background())

	err := SignStreamingRequest(ctx, req, bc, "UNSIGNED-PAYLOAD")
	assert.NoError(t, err)

	// Verify the session token is in the request
	token := req.Header.Get("x-amz-security-token")
	assert.Equal(t, sessionToken, token)

	// Verify the signature was created
	authHeader := req.Header.Get("Authorization")
	assert.NotEmpty(t, authHeader)
	assert.Contains(t, authHeader, "AWS4-HMAC-SHA256")
}
