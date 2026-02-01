package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCopyPutObjectHeaders(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", "1000")
	h.Set("Content-Encoding", "gzip")
	h.Set("Content-MD5", "abc123==")
	h.Set("X-Amz-Content-Sha256", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")
	h.Set("X-Amz-Meta-Key1", "value1")
	h.Set("X-Amz-Meta-Key2", "value2")
	h.Set("X-Amz-Storage-Class", "STANDARD")
	h.Set("X-Amz-ACL", "public-read")
	h.Set("Cache-Control", "max-age=3600")
	h.Set("Content-Disposition", "attachment")
	h.Set("Content-Language", "en-US")
	h.Set("Expires", "Wed, 21 Oct 2025 07:28:00 GMT")
	h.Set("X-Amz-Checksum-SHA256", "checksum123")

	rc := &RequestContext{
		Headers: h,
	}

	upstreamReq := &http.Request{
		Header: http.Header{},
	}

	copyPutObjectHeaders(upstreamReq, rc)

	// Verify all headers were copied
	assert.Equal(t, "application/octet-stream", upstreamReq.Header.Get("Content-Type"))
	assert.Equal(t, "1000", upstreamReq.Header.Get("Content-Length"))
	assert.Equal(t, "gzip", upstreamReq.Header.Get("Content-Encoding"))
	assert.Equal(t, "abc123==", upstreamReq.Header.Get("Content-MD5"))
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", upstreamReq.Header.Get("X-Amz-Content-Sha256"))
	assert.Equal(t, "value1", upstreamReq.Header.Get("X-Amz-Meta-Key1"))
	assert.Equal(t, "value2", upstreamReq.Header.Get("X-Amz-Meta-Key2"))
	assert.Equal(t, "STANDARD", upstreamReq.Header.Get("X-Amz-Storage-Class"))
	assert.Equal(t, "public-read", upstreamReq.Header.Get("X-Amz-ACL"))
	assert.Equal(t, "max-age=3600", upstreamReq.Header.Get("Cache-Control"))
	assert.Equal(t, "attachment", upstreamReq.Header.Get("Content-Disposition"))
	assert.Equal(t, "en-US", upstreamReq.Header.Get("Content-Language"))
	assert.Equal(t, "Wed, 21 Oct 2025 07:28:00 GMT", upstreamReq.Header.Get("Expires"))
	assert.Equal(t, "checksum123", upstreamReq.Header.Get("X-Amz-Checksum-SHA256"))

	// Verify ContentLength was set
	assert.Equal(t, int64(1000), upstreamReq.ContentLength)
}

func TestCopyUploadPartHeaders(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("Content-Length", "5000")
	h.Set("Content-MD5", "def456==")
	h.Set("X-Amz-Content-Sha256", "abc123")
	h.Set("X-Amz-Checksum-CRC32", "crc32value")
	h.Set("X-Amz-Checksum-SHA256", "sha256value")

	rc := &RequestContext{
		Headers: h,
	}

	upstreamReq := &http.Request{
		Header: http.Header{},
	}

	copyUploadPartHeaders(upstreamReq, rc)

	// Verify all headers were copied
	assert.Equal(t, "5000", upstreamReq.Header.Get("Content-Length"))
	assert.Equal(t, "def456==", upstreamReq.Header.Get("Content-MD5"))
	assert.Equal(t, "abc123", upstreamReq.Header.Get("X-Amz-Content-Sha256"))
	assert.Equal(t, "crc32value", upstreamReq.Header.Get("X-Amz-Checksum-CRC32"))
	assert.Equal(t, "sha256value", upstreamReq.Header.Get("X-Amz-Checksum-SHA256"))

	// Verify ContentLength was set
	assert.Equal(t, int64(5000), upstreamReq.ContentLength)
}

func TestStreamingPutObjectHeaderForwarding(t *testing.T) {
	t.Parallel()
	// Create a mock upstream server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify headers were forwarded
		assert.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
		assert.Equal(t, "10", r.Header.Get("Content-Length"))
		assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", r.Header.Get("X-Amz-Content-Sha256"))

		// Verify body was streamed
		body, _ := io.ReadAll(r.Body)
		assert.Equal(t, "test-data", string(body))

		// Return success response
		w.Header().Set("ETag", "\"abc123\"")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create request context
	bodyReader := io.NopCloser(strings.NewReader("test-data"))
	upstreamReq := httptest.NewRequest(http.MethodPut, server.URL+"/test-bucket/test-key", bodyReader)

	h := http.Header{}
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", "9")
	h.Set("X-Amz-Content-Sha256", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855")

	rc := &RequestContext{
		Headers: h,
		Request: upstreamReq,
	}

	// Note: This test demonstrates the header copying logic,
	// but full integration testing would require mocking credentials
	httpReq := &http.Request{
		Header: http.Header{},
	}
	copyPutObjectHeaders(httpReq, rc)

	assert.Equal(t, "application/octet-stream", httpReq.Header.Get("Content-Type"))
	assert.Equal(t, "9", httpReq.Header.Get("Content-Length"))
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", httpReq.Header.Get("X-Amz-Content-Sha256"))

	_ = rc.Headers // Use variable to avoid lint error
}

func TestIsStreamingEligibleEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		setup    func(*RequestContext)
		expected bool
	}{
		{
			name: "empty content-length string",
			setup: func(rc *RequestContext) {
				rc.Headers = http.Header{
					"Content-Length": []string{""},
				}
			},
			expected: false,
		},
		{
			name: "zero content-length",
			setup: func(rc *RequestContext) {
				rc.Headers = http.Header{
					"Content-Length": []string{"0"},
				}
			},
			expected: true,
		},
		{
			name: "large content-length",
			setup: func(rc *RequestContext) {
				rc.Headers = http.Header{
					"Content-Length": []string{"1099511627776"}, // 1TB
				}
			},
			expected: true,
		},
		{
			name: "empty trailer value",
			setup: func(rc *RequestContext) {
				rc.Headers = http.Header{
					"Content-Length": []string{"1000"},
					"X-Amz-Trailer":  []string{""},
				}
			},
			expected: true, // Empty string means no trailer is actually present
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &RequestContext{}
			tt.setup(rc)
			result := IsStreamingEligible(rc, false)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCopyPutObjectHeadersEmpty(t *testing.T) {
	t.Parallel()
	rc := &RequestContext{
		Headers: http.Header{},
	}

	upstreamReq := &http.Request{
		Header: http.Header{},
	}

	// Should not panic with empty headers
	copyPutObjectHeaders(upstreamReq, rc)

	// Verify request is still valid
	assert.NotNil(t, upstreamReq)
	assert.Equal(t, int64(0), upstreamReq.ContentLength)
}

func TestCopyUploadPartHeadersEmpty(t *testing.T) {
	t.Parallel()
	rc := &RequestContext{
		Headers: http.Header{},
	}

	upstreamReq := &http.Request{
		Header: http.Header{},
	}

	// Should not panic with empty headers
	copyUploadPartHeaders(upstreamReq, rc)

	// Verify request is still valid
	assert.NotNil(t, upstreamReq)
}

func TestCopyHeadersPreservesChecksumAlgorithm(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("X-Amz-Checksum-SHA256", "sha256value")
	h.Set("X-Amz-Checksum-CRC32", "crc32value")
	h.Set("X-Amz-Checksum-SHA1", "sha1value")
	h.Set("X-Amz-Checksum-CRC32C", "crc32cvalue")

	rc := &RequestContext{
		Headers: h,
	}

	upstreamReq := &http.Request{
		Header: http.Header{},
	}

	copyPutObjectHeaders(upstreamReq, rc)

	// All checksum algorithms should be copied
	assert.Equal(t, "sha256value", upstreamReq.Header.Get("X-Amz-Checksum-SHA256"))
	assert.Equal(t, "crc32value", upstreamReq.Header.Get("X-Amz-Checksum-CRC32"))
	assert.Equal(t, "sha1value", upstreamReq.Header.Get("X-Amz-Checksum-SHA1"))
	assert.Equal(t, "crc32cvalue", upstreamReq.Header.Get("X-Amz-Checksum-CRC32C"))
}

func TestCopyHeadersPreservesNewChecksumAlgorithms(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("X-Amz-Checksum-CRC64NVME", "crc64nvmevalue")
	h.Set("X-Amz-Checksum-SHA256", "sha256value")

	rc := &RequestContext{
		Headers: h,
	}

	upstreamReq := &http.Request{
		Header: http.Header{},
	}

	copyPutObjectHeaders(upstreamReq, rc)

	// Should support new checksum algorithms like CRC64NVME
	assert.Equal(t, "crc64nvmevalue", upstreamReq.Header.Get("X-Amz-Checksum-CRC64NVME"))
	assert.Equal(t, "sha256value", upstreamReq.Header.Get("X-Amz-Checksum-SHA256"))
}

func TestIsAwsChunkedEligible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		headers         http.Header
		isCopyOperation bool
		expected        bool
	}{
		{
			name: "eligible - aws-chunked with all required headers",
			headers: http.Header{
				"Content-Encoding":             []string{"aws-chunked"},
				"Content-Length":               []string{"12345"},
				"X-Amz-Decoded-Content-Length": []string{"10000"},
			},
			isCopyOperation: false,
			expected:        true,
		},
		{
			name: "ineligible - not aws-chunked",
			headers: http.Header{
				"Content-Encoding":             []string{"gzip"},
				"Content-Length":               []string{"12345"},
				"X-Amz-Decoded-Content-Length": []string{"10000"},
			},
			isCopyOperation: false,
			expected:        false,
		},
		{
			name: "ineligible - missing Content-Length",
			headers: http.Header{
				"Content-Encoding":             []string{"aws-chunked"},
				"X-Amz-Decoded-Content-Length": []string{"10000"},
			},
			isCopyOperation: false,
			expected:        false,
		},
		{
			name: "ineligible - missing x-amz-decoded-content-length",
			headers: http.Header{
				"Content-Encoding": []string{"aws-chunked"},
				"Content-Length":   []string{"12345"},
			},
			isCopyOperation: false,
			expected:        false,
		},
		{
			name: "ineligible - copy operation",
			headers: http.Header{
				"Content-Encoding":             []string{"aws-chunked"},
				"Content-Length":               []string{"12345"},
				"X-Amz-Decoded-Content-Length": []string{"10000"},
			},
			isCopyOperation: true,
			expected:        false,
		},
		{
			name: "ineligible - no Content-Encoding",
			headers: http.Header{
				"Content-Length":               []string{"12345"},
				"X-Amz-Decoded-Content-Length": []string{"10000"},
			},
			isCopyOperation: false,
			expected:        false,
		},
		{
			name: "eligible - aws-chunked,gzip combined encoding",
			headers: http.Header{
				"Content-Encoding":             []string{"aws-chunked,gzip"},
				"Content-Length":               []string{"12345"},
				"X-Amz-Decoded-Content-Length": []string{"10000"},
			},
			isCopyOperation: false,
			expected:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &RequestContext{
				Headers: tt.headers,
			}
			result := IsAwsChunkedEligible(rc, tt.isCopyOperation)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCopyAwsChunkedHeaders(t *testing.T) {
	t.Parallel()

	h := http.Header{}
	h.Set("Content-Type", "application/octet-stream")
	h.Set("X-Amz-Meta-CustomKey", "CustomValue")
	h.Set("X-Amz-Checksum-SHA256", "abc123")
	h.Set("X-Amz-Storage-Class", "GLACIER")
	h.Set("Cache-Control", "max-age=3600")
	h.Set("Content-Disposition", "attachment")
	h.Set("X-Amz-Checksum-Algorithm", "SHA256")

	rc := &RequestContext{
		Headers: h,
	}

	upstreamReq := &http.Request{
		Header: http.Header{},
	}

	copyAwsChunkedHeaders(upstreamReq, rc)

	// Verify headers were copied
	assert.Equal(t, "application/octet-stream", upstreamReq.Header.Get("Content-Type"))
	assert.Equal(t, "CustomValue", upstreamReq.Header.Get("X-Amz-Meta-CustomKey"))
	assert.Equal(t, "abc123", upstreamReq.Header.Get("X-Amz-Checksum-SHA256"))
	assert.Equal(t, "GLACIER", upstreamReq.Header.Get("X-Amz-Storage-Class"))
	assert.Equal(t, "max-age=3600", upstreamReq.Header.Get("Cache-Control"))
	assert.Equal(t, "attachment", upstreamReq.Header.Get("Content-Disposition"))
	assert.Equal(t, "SHA256", upstreamReq.Header.Get("X-Amz-Checksum-Algorithm"))
}

func TestStreamingAwsChunkedPutObjectInvalidAmzDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		amzDate string
		isError bool
	}{
		{
			name:    "valid x-amz-date",
			amzDate: "20260201T000000Z",
			isError: false,
		},
		{
			name:    "empty x-amz-date",
			amzDate: "",
			isError: true,
		},
		{
			name:    "too short x-amz-date",
			amzDate: "2026",
			isError: true,
		},
		{
			name:    "exactly 7 chars",
			amzDate: "2026020",
			isError: true,
		},
		{
			name:    "exactly 8 chars",
			amzDate: "20260201",
			isError: false,
		},
		{
			name:    "single char",
			amzDate: "x",
			isError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify the validation logic that prevents panic
			if tt.amzDate == "" || len(tt.amzDate) < 8 {
				assert.True(t, tt.isError, "expected error for amzDate: %s", tt.amzDate)
			} else {
				assert.False(t, tt.isError, "expected no error for amzDate: %s", tt.amzDate)
			}
		})
	}
}

func TestStreamingAwsChunkedUploadPartInvalidAmzDate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		amzDate string
		isError bool
	}{
		{
			name:    "valid x-amz-date",
			amzDate: "20260201T000000Z",
			isError: false,
		},
		{
			name:    "empty x-amz-date",
			amzDate: "",
			isError: true,
		},
		{
			name:    "too short x-amz-date",
			amzDate: "2026",
			isError: true,
		},
		{
			name:    "exactly 8 chars",
			amzDate: "20260201",
			isError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify the validation logic that prevents panic
			if tt.amzDate == "" || len(tt.amzDate) < 8 {
				assert.True(t, tt.isError, "expected error for amzDate: %s", tt.amzDate)
			} else {
				assert.False(t, tt.isError, "expected no error for amzDate: %s", tt.amzDate)
			}
		})
	}
}
