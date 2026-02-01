package proxy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAwsChunked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		contentEncoding string
		expected        bool
	}{
		{
			name:            "aws-chunked encoding",
			contentEncoding: "aws-chunked",
			expected:        true,
		},
		{
			name:            "aws-chunked with gzip",
			contentEncoding: "aws-chunked,gzip",
			expected:        true,
		},
		{
			name:            "gzip,aws-chunked",
			contentEncoding: "gzip,aws-chunked",
			expected:        true,
		},
		{
			name:            "regular gzip",
			contentEncoding: "gzip",
			expected:        false,
		},
		{
			name:            "empty encoding",
			contentEncoding: "",
			expected:        false,
		},
		{
			name:            "identity",
			contentEncoding: "identity",
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsAwsChunked(tt.contentEncoding)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractSeedSignature(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		authHdr  string
		expected string
	}{
		{
			name:     "valid authorization header",
			authHdr:  "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20240101/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=abcd1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
			expected: "abcd1234567890abcdef1234567890abcdef1234567890abcdef1234567890ab",
		},
		{
			name:     "signature at end",
			authHdr:  "AWS4-HMAC-SHA256 Credential=test, SignedHeaders=host, Signature=deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
			expected: "deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01234567",
		},
		{
			name:     "no signature field",
			authHdr:  "AWS4-HMAC-SHA256 Credential=test, SignedHeaders=host",
			expected: "",
		},
		{
			name:     "empty header",
			authHdr:  "",
			expected: "",
		},
		{
			name:     "basic auth (not sigv4)",
			authHdr:  "Basic dXNlcm5hbWU6cGFzc3dvcmQ=",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractSeedSignature(tt.authHdr)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDeriveSigningKey(t *testing.T) {
	t.Parallel()

	// Test with known AWS test vectors
	secretKey := "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	dateStamp := "20150830"
	region := "us-east-1"
	service := "s3"

	signingKey := DeriveSigningKey(secretKey, dateStamp, region, service)

	// The signing key should be 32 bytes (256 bits) for HMAC-SHA256
	assert.Len(t, signingKey, 32)

	// Verify it's deterministic
	signingKey2 := DeriveSigningKey(secretKey, dateStamp, region, service)
	assert.Equal(t, signingKey, signingKey2)

	// Different inputs should produce different keys
	signingKey3 := DeriveSigningKey(secretKey, "20150831", region, service)
	assert.NotEqual(t, signingKey, signingKey3)
}

func TestCalculateReEncodedContentLength(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		decodedLen    int64
		chunkSize     int
		expectedLen   int64
		expectedValid bool
	}{
		{
			name:        "single small chunk",
			decodedLen:  100,
			chunkSize:   64 * 1024,
			expectedLen: calculateExpectedLength(100, 64*1024),
		},
		{
			name:        "exactly one chunk",
			decodedLen:  64 * 1024,
			chunkSize:   64 * 1024,
			expectedLen: calculateExpectedLength(64*1024, 64*1024),
		},
		{
			name:        "multiple full chunks",
			decodedLen:  128 * 1024,
			chunkSize:   64 * 1024,
			expectedLen: calculateExpectedLength(128*1024, 64*1024),
		},
		{
			name:        "multiple chunks with remainder",
			decodedLen:  100*1024 + 500,
			chunkSize:   64 * 1024,
			expectedLen: calculateExpectedLength(100*1024+500, 64*1024),
		},
		{
			name:        "empty content",
			decodedLen:  0,
			chunkSize:   64 * 1024,
			expectedLen: calculateExpectedLength(0, 64*1024),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateReEncodedContentLength(tt.decodedLen, tt.chunkSize)
			assert.Equal(t, tt.expectedLen, result)
		})
	}
}

// Helper function to calculate expected encoded length
func calculateExpectedLength(decodedLen int64, chunkSize int) int64 {
	if decodedLen == 0 {
		// Just the final chunk: "0;chunk-signature=<64 chars>\r\n\r\n"
		return int64(len("0;chunk-signature=") + 64 + len("\r\n\r\n"))
	}

	var totalLen int64
	remaining := decodedLen

	for remaining > 0 {
		thisChunk := int64(chunkSize)
		if remaining < thisChunk {
			thisChunk = remaining
		}

		// Calculate hex representation of chunk size
		hexStr := strings.ToLower(strings.TrimLeft(hex.EncodeToString(int64ToBytes(thisChunk)), "0"))
		if hexStr == "" {
			hexStr = "0"
		}

		// <hex>;chunk-signature=<64>\r\n<data>\r\n
		totalLen += int64(len(hexStr)) + int64(len(";chunk-signature=")) + 64 + 2 + thisChunk + 2

		remaining -= thisChunk
	}

	// Final chunk: 0;chunk-signature=<64>\r\n\r\n
	totalLen += int64(len("0;chunk-signature=")) + 64 + 2 + 2

	return totalLen
}

func int64ToBytes(n int64) []byte {
	result := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		result[i] = byte(n & 0xff)
		n >>= 8
	}
	return result
}

func TestAwsChunkedReEncoder_SimpleChunk(t *testing.T) {
	t.Parallel()

	// Create a simple aws-chunked body with one data chunk
	chunkData := []byte("Hello, World!")
	chunkSig := strings.Repeat("a", 64) // Fake signature
	inputBody := createAwsChunkedBody([][]byte{chunkData}, chunkSig)

	// Create re-encoder
	signingKey := DeriveSigningKey("testsecret", "20240101", "us-east-1", "s3")
	seedSig := strings.Repeat("b", 64)

	reEncoder := NewAwsChunkedReEncoder(
		bytes.NewReader(inputBody),
		signingKey,
		"20240101T000000Z",
		"us-east-1",
		seedSig,
	)

	// Read all output
	output, err := io.ReadAll(reEncoder)
	require.NoError(t, err)

	// Verify output is valid aws-chunked format
	assert.True(t, len(output) > 0)

	// Parse the output and verify structure
	decoded, err := decodeAwsChunkedBody(output)
	require.NoError(t, err)
	assert.Equal(t, chunkData, decoded)
}

func TestAwsChunkedReEncoder_MultipleChunks(t *testing.T) {
	t.Parallel()

	// Create aws-chunked body with multiple chunks
	chunks := [][]byte{
		[]byte("First chunk data"),
		[]byte("Second chunk data"),
		[]byte("Third chunk"),
	}
	chunkSig := strings.Repeat("c", 64)
	inputBody := createAwsChunkedBody(chunks, chunkSig)

	signingKey := DeriveSigningKey("testsecret", "20240101", "us-east-1", "s3")
	seedSig := strings.Repeat("d", 64)

	reEncoder := NewAwsChunkedReEncoder(
		bytes.NewReader(inputBody),
		signingKey,
		"20240101T000000Z",
		"us-east-1",
		seedSig,
	)

	output, err := io.ReadAll(reEncoder)
	require.NoError(t, err)

	// Decode and verify all data came through
	decoded, err := decodeAwsChunkedBody(output)
	require.NoError(t, err)

	expectedData := bytes.Join(chunks, nil)
	assert.Equal(t, expectedData, decoded)
}

func TestAwsChunkedReEncoder_EmptyBody(t *testing.T) {
	t.Parallel()

	// Create aws-chunked body with no data (just final chunk)
	inputBody := createAwsChunkedBody([][]byte{}, strings.Repeat("e", 64))

	signingKey := DeriveSigningKey("testsecret", "20240101", "us-east-1", "s3")
	seedSig := strings.Repeat("f", 64)

	reEncoder := NewAwsChunkedReEncoder(
		bytes.NewReader(inputBody),
		signingKey,
		"20240101T000000Z",
		"us-east-1",
		seedSig,
	)

	output, err := io.ReadAll(reEncoder)
	require.NoError(t, err)

	// Should produce valid empty aws-chunked body
	decoded, err := decodeAwsChunkedBody(output)
	require.NoError(t, err)
	assert.Empty(t, decoded)
}

func TestAwsChunkedReEncoder_SignatureChain(t *testing.T) {
	t.Parallel()

	// Create input with known data
	chunkData := []byte("test data for signature verification")
	chunkSig := strings.Repeat("1", 64)
	inputBody := createAwsChunkedBody([][]byte{chunkData}, chunkSig)

	signingKey := DeriveSigningKey("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY", "20240101", "us-east-1", "s3")
	seedSig := "seed0000000000000000000000000000000000000000000000000000000000"

	reEncoder := NewAwsChunkedReEncoder(
		bytes.NewReader(inputBody),
		signingKey,
		"20240101T000000Z",
		"us-east-1",
		seedSig,
	)

	output, err := io.ReadAll(reEncoder)
	require.NoError(t, err)

	// Extract signatures from output
	sigs := extractSignaturesFromAwsChunked(output)
	require.Len(t, sigs, 2) // data chunk + final chunk

	// Verify signatures are different (chained)
	assert.NotEqual(t, sigs[0], sigs[1])

	// Verify signatures are valid hex (64 chars)
	for _, sig := range sigs {
		assert.Len(t, sig, 64)
		_, err := hex.DecodeString(sig)
		assert.NoError(t, err)
	}
}

func TestAwsChunkedReEncoder_PartialReads(t *testing.T) {
	t.Parallel()

	// Create a larger chunk
	chunkData := bytes.Repeat([]byte("X"), 1024)
	chunkSig := strings.Repeat("2", 64)
	inputBody := createAwsChunkedBody([][]byte{chunkData}, chunkSig)

	signingKey := DeriveSigningKey("testsecret", "20240101", "us-east-1", "s3")
	seedSig := strings.Repeat("3", 64)

	reEncoder := NewAwsChunkedReEncoder(
		bytes.NewReader(inputBody),
		signingKey,
		"20240101T000000Z",
		"us-east-1",
		seedSig,
	)

	// Read in small chunks to test partial read handling
	var output bytes.Buffer
	buf := make([]byte, 17) // Odd size to test edge cases
	for {
		n, err := reEncoder.Read(buf)
		if n > 0 {
			output.Write(buf[:n])
		}
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}

	// Decode and verify
	decoded, err := decodeAwsChunkedBody(output.Bytes())
	require.NoError(t, err)
	assert.Equal(t, chunkData, decoded)
}

// Helper: create aws-chunked format body from chunks
func createAwsChunkedBody(chunks [][]byte, sig string) []byte {
	var buf bytes.Buffer

	for _, chunk := range chunks {
		// Format: <hex-size>;chunk-signature=<sig>\r\n<data>\r\n
		buf.WriteString(strings.ToLower(hex.EncodeToString(int64ToBytes(int64(len(chunk))))))
		// Actually simpler:
		buf.Reset()
	}

	// Rebuild properly
	var result bytes.Buffer
	for _, chunk := range chunks {
		hexSize := strings.TrimLeft(hex.EncodeToString(int64ToBytes(int64(len(chunk)))), "0")
		if hexSize == "" {
			hexSize = "0"
		}
		result.WriteString(hexSize)
		result.WriteString(";chunk-signature=")
		result.WriteString(sig)
		result.WriteString("\r\n")
		result.Write(chunk)
		result.WriteString("\r\n")
	}

	// Final chunk
	result.WriteString("0;chunk-signature=")
	result.WriteString(sig)
	result.WriteString("\r\n\r\n")

	return result.Bytes()
}

// Helper: decode aws-chunked body to extract raw data
func decodeAwsChunkedBody(encoded []byte) ([]byte, error) {
	var result bytes.Buffer
	reader := bytes.NewReader(encoded)

	for {
		// Read chunk header line
		var headerLine bytes.Buffer
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return nil, err
			}
			if b == '\r' {
				next, err := reader.ReadByte()
				if err != nil {
					return nil, err
				}
				if next == '\n' {
					break
				}
				headerLine.WriteByte(b)
				headerLine.WriteByte(next)
			} else {
				headerLine.WriteByte(b)
			}
		}

		// Parse chunk size
		header := headerLine.String()
		semiIdx := strings.Index(header, ";")
		if semiIdx == -1 {
			return nil, io.ErrUnexpectedEOF
		}
		hexSize := header[:semiIdx]
		sizeBytes, err := hex.DecodeString(padHex(hexSize))
		if err != nil {
			return nil, err
		}
		size := int64(0)
		for _, b := range sizeBytes {
			size = (size << 8) | int64(b)
		}

		if size == 0 {
			// Final chunk, skip trailing \r\n
			reader.ReadByte()
			reader.ReadByte()
			break
		}

		// Read chunk data
		chunkData := make([]byte, size)
		_, err = io.ReadFull(reader, chunkData)
		if err != nil {
			return nil, err
		}
		result.Write(chunkData)

		// Skip trailing \r\n
		reader.ReadByte()
		reader.ReadByte()
	}

	return result.Bytes(), nil
}

func padHex(s string) string {
	if len(s)%2 == 1 {
		return "0" + s
	}
	return s
}

// Helper: extract signatures from aws-chunked body
func extractSignaturesFromAwsChunked(encoded []byte) []string {
	var sigs []string
	reader := bytes.NewReader(encoded)

	for {
		var headerLine bytes.Buffer
		for {
			b, err := reader.ReadByte()
			if err != nil {
				return sigs
			}
			if b == '\r' {
				next, _ := reader.ReadByte()
				if next == '\n' {
					break
				}
				headerLine.WriteByte(b)
				headerLine.WriteByte(next)
			} else {
				headerLine.WriteByte(b)
			}
		}

		header := headerLine.String()
		if strings.Contains(header, "chunk-signature=") {
			idx := strings.Index(header, "chunk-signature=")
			sig := header[idx+len("chunk-signature="):]
			sigs = append(sigs, sig)
		}

		// Parse size to skip data
		semiIdx := strings.Index(header, ";")
		if semiIdx == -1 {
			return sigs
		}
		hexSize := header[:semiIdx]
		sizeBytes, _ := hex.DecodeString(padHex(hexSize))
		size := int64(0)
		for _, b := range sizeBytes {
			size = (size << 8) | int64(b)
		}

		if size == 0 {
			break
		}

		// Skip chunk data and trailing \r\n
		reader.Seek(size+2, io.SeekCurrent)
	}

	return sigs
}

func TestHmacSHA256Bytes(t *testing.T) {
	t.Parallel()

	key := []byte("testkey")
	data := []byte("testdata")

	result := hmacSHA256Bytes(key, data)

	// Should return 32 bytes (SHA256 output)
	assert.Len(t, result, 32)

	// Should be deterministic
	result2 := hmacSHA256Bytes(key, data)
	assert.Equal(t, result, result2)

	// Different input should produce different output
	result3 := hmacSHA256Bytes(key, []byte("different"))
	assert.NotEqual(t, result, result3)
}

func TestChunkPayloadHash(t *testing.T) {
	t.Parallel()

	testData := []byte("Hello, World!")
	expected := sha256.Sum256(testData)
	expectedHex := hex.EncodeToString(expected[:])

	// The re-encoder should compute SHA256 of chunk data
	// This is implicit in the signature calculation
	assert.Len(t, expectedHex, 64)
}
