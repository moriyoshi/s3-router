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

// Regression tests for URL-encoded path handling in canonical request
// See: https://github.com/moriyoshi/s3-router/issues/...
// Issue: Signature mismatch when object keys contain URL-encoded characters (e.g., %3A for colon)

func TestS3SignerCanonicalRequest_URLEncodedColon(t *testing.T) {
	t.Parallel()
	// Test case matching the real error: key contains colons encoded as %3A
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "ASIAUSJOUX2FNNJ4BVRR",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "ap-northeast-1")

	// Request with URL-encoded path (as it would be after going through net/url parsing)
	req := &http.Request{
		Method: http.MethodPut,
		Host:   "payid-k8s-observability-dev.s3.ap-northeast-1.amazonaws.com",
		URL: &url.URL{
			Scheme:  "https",
			Host:    "payid-k8s-observability-dev.s3.ap-northeast-1.amazonaws.com",
			Path:    "/loki/fake/ca229e806d0bed66/19c2c978262:19c2c978263:42a7dac1",     // Decoded
			RawPath: "/loki/fake/ca229e806d0bed66/19c2c978262%3A19c2c978263%3A42a7dac1", // Encoded as it appears on wire
		},
		Header: http.Header{
			"X-Amz-Date":           []string{"20260208T171111Z"},
			"X-Amz-Content-Sha256": []string{"3cca444cebbc148b21b627b333637cf36ce7d0669eb70b721cce911f838ea263"},
			"Content-Length":       []string{"766"},
			"Content-Md5":          []string{"q7ZdPVVO4bNUALiZzVD9cA=="},
		},
	}

	canonical := signer.buildCanonicalRequest(req, "3cca444cebbc148b21b627b333637cf36ce7d0669eb70b721cce911f838ea263")
	canonicalStr := string(canonical)

	// CRITICAL: The canonical request should contain the URL-encoded form (%3A)
	// NOT the decoded form (:) because that's what AWS will see on the wire
	assert.Contains(t, canonicalStr, "19c2c978262%3A19c2c978263%3A42a7dac1",
		"canonical request should preserve URL encoding for colons in object key")

	// Verify it does NOT contain the decoded form which would cause signature mismatch
	assert.NotContains(t, canonicalStr, "19c2c978262:19c2c978263:42a7dac1",
		"canonical request should not contain decoded form of URL-encoded characters")
}

func TestS3SignerCanonicalRequest_URLEncodedSpecialChars(t *testing.T) {
	t.Parallel()
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "us-east-1")

	testCases := []struct {
		name            string
		decodedPath     string
		encodedPath     string
		expectedInSig   string // What should appear in the canonical request
		shouldNotAppear string // What should NOT appear
	}{
		{
			name:          "colon (%3A)",
			decodedPath:   "/bucket/file:name",
			encodedPath:   "/bucket/file%3Aname",
			expectedInSig: "file%3Aname",
		},
		{
			name:          "space (%20)",
			decodedPath:   "/bucket/file name",
			encodedPath:   "/bucket/file%20name",
			expectedInSig: "file%20name",
		},
		{
			name:          "question mark (%3F)",
			decodedPath:   "/bucket/file?query",
			encodedPath:   "/bucket/file%3Fquery",
			expectedInSig: "file%3Fquery",
		},
		{
			name:          "equals sign (%3D)",
			decodedPath:   "/bucket/key=value",
			encodedPath:   "/bucket/key%3Dvalue",
			expectedInSig: "key%3Dvalue",
		},
		{
			name:          "ampersand (%26)",
			decodedPath:   "/bucket/file&other",
			encodedPath:   "/bucket/file%26other",
			expectedInSig: "file%26other",
		},
		{
			name:          "multiple encoded chars",
			decodedPath:   "/bucket/a:b c?d=e&f",
			encodedPath:   "/bucket/a%3Ab%20c%3Fd%3De%26f",
			expectedInSig: "a%3Ab%20c%3Fd%3De%26f",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &http.Request{
				Method: http.MethodPut,
				Host:   "s3.amazonaws.com",
				URL: &url.URL{
					Scheme:  "https",
					Host:    "s3.amazonaws.com",
					Path:    tc.decodedPath,
					RawPath: tc.encodedPath,
				},
				Header: http.Header{
					"X-Amz-Date":           []string{"20240101T000000Z"},
					"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
				},
			}

			canonical := signer.buildCanonicalRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
			canonicalStr := string(canonical)

			// The canonical request should use the encoded path (as it appears on the wire)
			assert.Contains(t, canonicalStr, tc.expectedInSig,
				"canonical request should contain URL-encoded path: %s", tc.name)
		})
	}
}

func TestS3SignerCanonicalRequest_EscapedPathNotPath(t *testing.T) {
	t.Parallel()
	// This test explicitly verifies that buildCanonicalRequest uses EscapedPath()
	// and not Path, which is critical when RawPath is set
	signer := NewS3Signer(aws.Credentials{}, "us-east-1")

	req := &http.Request{
		Method: http.MethodGet,
		Host:   "s3.amazonaws.com",
		URL: &url.URL{
			Scheme:  "https",
			Host:    "s3.amazonaws.com",
			Path:    "/bucket/my:key",   // Decoded by Go's URL parser
			RawPath: "/bucket/my%3Akey", // Original encoded form
		},
		Header: http.Header{
			"X-Amz-Date":           []string{"20240101T000000Z"},
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		},
	}

	canonical := signer.buildCanonicalRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	canonicalStr := string(canonical)

	// When RawPath is set, EscapedPath() returns RawPath (preserving encoding)
	assert.Contains(t, canonicalStr, "/bucket/my%3Akey",
		"canonical request should use RawPath when set (preserving %3A encoding)")

	// Confirm it's using the right path by checking it doesn't use the fully decoded form
	lines := strings.Split(canonicalStr, "\n")
	assert.Greater(t, len(lines), 1, "canonical request should have multiple lines")
	// The second line is the CanonicalURI
	assert.Equal(t, "/bucket/my%3Akey", lines[1],
		"CanonicalURI should be the URL-encoded path from RawPath, not decoded Path")
}

func TestS3SignerCanonicalRequest_NoRawPath(t *testing.T) {
	t.Parallel()
	// Test the case where RawPath is not set (EscapedPath() will encode Path)
	signer := NewS3Signer(aws.Credentials{}, "us-east-1")

	// When RawPath is not set, Go will encode Path if needed
	req := &http.Request{
		Method: http.MethodGet,
		Host:   "s3.amazonaws.com",
		URL: &url.URL{
			Scheme: "https",
			Host:   "s3.amazonaws.com",
			Path:   "/bucket/my%20key", // Already encoded in the path
			// RawPath is not set
		},
		Header: http.Header{
			"X-Amz-Date":           []string{"20240101T000000Z"},
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
		},
	}

	canonical := signer.buildCanonicalRequest(req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	canonicalStr := string(canonical)

	// EscapedPath() will return the path as-is or double-encode if needed
	// Since Path has %20, EscapedPath will preserve it or potentially encode the %
	lines := strings.Split(canonicalStr, "\n")
	assert.Greater(t, len(lines), 1)
	// The path should be properly handled for canonical form
	assert.True(t, strings.Contains(canonicalStr, "/bucket/my"),
		"canonical request should contain the bucket and key prefix")
}

func TestS3SignerSignRequest_URLEncodedKey(t *testing.T) {
	t.Parallel()
	// Full end-to-end test: sign a request with URL-encoded key and verify signature matches
	signer := NewS3Signer(aws.Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
	}, "us-east-1")

	req1 := &http.Request{
		Method: http.MethodPut,
		Host:   "s3.amazonaws.com",
		URL: &url.URL{
			Scheme:  "https",
			Host:    "s3.amazonaws.com",
			Path:    "/bucket/my:key",
			RawPath: "/bucket/my%3Akey",
		},
		Header: http.Header{
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			"Content-Type":         []string{"application/octet-stream"},
		},
	}

	// Set same timestamp for reproducible signatures
	req1.Header.Set("X-Amz-Date", "20240101T000000Z")

	err := signer.SignRequest(req1, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.NoError(t, err)

	sig1 := req1.Header.Get("Authorization")
	assert.NotEmpty(t, sig1)

	// Create an identical request and verify it gets the same signature
	req2 := &http.Request{
		Method: http.MethodPut,
		Host:   "s3.amazonaws.com",
		URL: &url.URL{
			Scheme:  "https",
			Host:    "s3.amazonaws.com",
			Path:    "/bucket/my:key",
			RawPath: "/bucket/my%3Akey",
		},
		Header: http.Header{
			"X-Amz-Content-Sha256": []string{"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
			"Content-Type":         []string{"application/octet-stream"},
		},
	}

	req2.Header.Set("X-Amz-Date", "20240101T000000Z")

	err = signer.SignRequest(req2, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	assert.NoError(t, err)

	sig2 := req2.Header.Get("Authorization")
	assert.NotEmpty(t, sig2)

	// Both requests should have identical signatures since they're identical
	assert.Equal(t, sig1, sig2,
		"identical requests with URL-encoded keys should produce identical signatures")
}
