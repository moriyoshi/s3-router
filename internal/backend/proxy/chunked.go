// Package proxy provides AWS chunked encoding/decoding support for streaming uploads.
//
// AWS chunked encoding format:
//
//	<chunk-size-hex>;chunk-signature=<sig>\r\n
//	<chunk-data>\r\n
//	...
//	0;chunk-signature=<sig>\r\n
//	<trailers>\r\n
//	\r\n
//
// The AwsChunkedReEncoder reads aws-chunked data signed with client credentials,
// extracts the raw payload, re-signs each chunk with backend credentials, and
// outputs properly formatted aws-chunked data for the backend.
package proxy

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// IsAwsChunked checks if the Content-Encoding header indicates aws-chunked encoding.
func IsAwsChunked(contentEncoding string) bool {
	return strings.Contains(strings.ToLower(contentEncoding), "aws-chunked")
}

// AwsChunkedReEncoder reads aws-chunked data from a source, decodes it,
// re-signs each chunk with backend credentials, and outputs valid aws-chunked
// data for the backend. This enables true streaming without buffering.
type AwsChunkedReEncoder struct {
	src             *bufio.Reader // Source aws-chunked stream
	signingKey      []byte        // Backend signing key (kSigning)
	amzDate         string        // e.g., "20260201T123456Z"
	credentialScope string        // e.g., "20260201/us-east-1/s3/aws4_request"
	prevSig         string        // Previous chunk signature (starts with seed)

	outputBuf bytes.Buffer // Buffered re-encoded output
	chunkSize int          // Size of chunks to emit (64KB default)
	rawBuf    []byte       // Buffer for accumulating raw data
	done      bool         // True when final chunk has been written

	// State for reading from source
	srcRemaining int64 // Bytes remaining in current source chunk
	srcEOF       bool  // True when source has no more chunks
}

// NewAwsChunkedReEncoder creates a re-encoder that reads aws-chunked data from src,
// decodes it, and re-encodes with new signatures using the provided signing key.
//
// Parameters:
//   - src: The source aws-chunked encoded stream (from client)
//   - signingKey: The pre-computed kSigning key for the backend (result of deriveKey)
//   - amzDate: The X-Amz-Date header value (e.g., "20260201T123456Z")
//   - region: AWS region for credential scope
//   - seedSignature: The signature from the initial request (used as first prevSig)
func NewAwsChunkedReEncoder(
	src io.Reader,
	signingKey []byte,
	amzDate string,
	region string,
	seedSignature string,
) *AwsChunkedReEncoder {
	dateStamp := amzDate[:8] // Extract YYYYMMDD
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, region)

	return &AwsChunkedReEncoder{
		src:             bufio.NewReaderSize(src, 128*1024), // 128KB buffer
		signingKey:      signingKey,
		amzDate:         amzDate,
		credentialScope: credentialScope,
		prevSig:         seedSignature,
		chunkSize:       64 * 1024, // 64KB chunks
		rawBuf:          make([]byte, 0, 64*1024),
		done:            false,
		srcRemaining:    0,
		srcEOF:          false,
	}
}

// Read implements io.Reader, returning re-encoded aws-chunked data.
func (r *AwsChunkedReEncoder) Read(p []byte) (int, error) {
	// If we have buffered output, return it first
	if r.outputBuf.Len() > 0 {
		return r.outputBuf.Read(p)
	}

	if r.done {
		return 0, io.EOF
	}

	// Read raw data from source and accumulate until we have a full chunk
	for len(r.rawBuf) < r.chunkSize && !r.srcEOF {
		if err := r.readFromSource(); err != nil {
			if err == io.EOF {
				r.srcEOF = true
				break
			}
			return 0, err
		}
	}

	// Emit a chunk with the accumulated data
	if len(r.rawBuf) > 0 {
		// Take up to chunkSize bytes
		emitSize := len(r.rawBuf)
		if emitSize > r.chunkSize {
			emitSize = r.chunkSize
		}

		chunkData := r.rawBuf[:emitSize]
		r.rawBuf = r.rawBuf[emitSize:]

		// Sign and write the chunk
		if err := r.writeChunk(chunkData); err != nil {
			return 0, err
		}
	}

	// If source is exhausted and no more raw data, write final chunk
	if r.srcEOF && len(r.rawBuf) == 0 && !r.done {
		if err := r.writeFinalChunk(); err != nil {
			return 0, err
		}
		r.done = true
	}

	// Return buffered output
	if r.outputBuf.Len() > 0 {
		return r.outputBuf.Read(p)
	}

	if r.done {
		return 0, io.EOF
	}

	return 0, nil
}

// readFromSource reads and decodes data from the source aws-chunked stream.
func (r *AwsChunkedReEncoder) readFromSource() error {
	// If we have remaining bytes in the current chunk, read them
	if r.srcRemaining > 0 {
		toRead := r.srcRemaining
		if toRead > int64(r.chunkSize) {
			toRead = int64(r.chunkSize)
		}

		buf := make([]byte, toRead)
		n, err := io.ReadFull(r.src, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("failed to read chunk data: %w", err)
		}

		r.rawBuf = append(r.rawBuf, buf[:n]...)
		r.srcRemaining -= int64(n)

		// If we've finished this chunk, read trailing CRLF
		if r.srcRemaining == 0 {
			crlf := make([]byte, 2)
			if _, err := io.ReadFull(r.src, crlf); err != nil && err != io.EOF {
				return fmt.Errorf("failed to read chunk trailing CRLF: %w", err)
			}
		}

		return nil
	}

	// Read next chunk header
	headerLine, err := r.src.ReadBytes('\n')
	if err != nil {
		return err
	}

	// Remove trailing \r\n
	headerLine = bytes.TrimSuffix(headerLine, []byte("\r\n"))
	headerLine = bytes.TrimSuffix(headerLine, []byte("\n"))

	// Parse chunk size: <hex>;chunk-signature=<sig>
	parts := strings.SplitN(string(headerLine), ";", 2)
	sizeHex := strings.TrimSpace(parts[0])

	chunkSize, err := strconv.ParseInt(sizeHex, 16, 64)
	if err != nil {
		return fmt.Errorf("invalid chunk size %q: %w", sizeHex, err)
	}

	// If chunk size is 0, we've reached the end
	if chunkSize == 0 {
		// Skip trailers until empty line
		for {
			line, err := r.src.ReadBytes('\n')
			if err != nil && err != io.EOF {
				return fmt.Errorf("failed to read trailers: %w", err)
			}
			line = bytes.TrimSuffix(line, []byte("\r\n"))
			line = bytes.TrimSuffix(line, []byte("\n"))
			if len(line) == 0 || err == io.EOF {
				break
			}
		}
		return io.EOF
	}

	r.srcRemaining = chunkSize
	return r.readFromSource() // Recurse to read the actual data
}

// writeChunk writes a signed chunk to the output buffer.
func (r *AwsChunkedReEncoder) writeChunk(data []byte) error {
	// Compute chunk signature
	sig := r.signChunk(data)
	r.prevSig = sig

	// Write chunk header
	header := fmt.Sprintf("%x;chunk-signature=%s\r\n", len(data), sig)
	r.outputBuf.WriteString(header)

	// Write chunk data
	r.outputBuf.Write(data)

	// Write trailing CRLF
	r.outputBuf.WriteString("\r\n")

	return nil
}

// writeFinalChunk writes the final (zero-size) chunk.
func (r *AwsChunkedReEncoder) writeFinalChunk() error {
	// Sign empty chunk
	sig := r.signChunk([]byte{})

	// Write final chunk header
	header := fmt.Sprintf("0;chunk-signature=%s\r\n", sig)
	r.outputBuf.WriteString(header)

	// Write final CRLF (no trailers for now)
	r.outputBuf.WriteString("\r\n")

	return nil
}

// signChunk computes the signature for a chunk.
// String to sign format:
//
//	AWS4-HMAC-SHA256-PAYLOAD\n
//	<amz-date>\n
//	<credential-scope>\n
//	<previous-signature>\n
//	<chunk-payload-hash>
func (r *AwsChunkedReEncoder) signChunk(data []byte) string {
	// Hash the chunk data
	hash := sha256.Sum256(data)
	chunkHash := hex.EncodeToString(hash[:])

	// Build string to sign
	stringToSign := fmt.Sprintf(
		"AWS4-HMAC-SHA256-PAYLOAD\n%s\n%s\n%s\n%s",
		r.amzDate,
		r.credentialScope,
		r.prevSig,
		chunkHash,
	)

	// Sign with the signing key
	mac := hmac.New(sha256.New, r.signingKey)
	mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil))
}

// deriveSigningKeyForChunk derives the signing key for AWS SigV4.
// This is kSigning = HMAC(HMAC(HMAC(HMAC("AWS4"+secret, date), region), service), "aws4_request")
func DeriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256Bytes([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256Bytes(kDate, []byte(region))
	kService := hmacSHA256Bytes(kRegion, []byte(service))
	kSigning := hmacSHA256Bytes(kService, []byte("aws4_request"))
	return kSigning
}

// hmacSHA256Bytes computes HMAC-SHA256 (local to avoid conflict with signer.go).
func hmacSHA256Bytes(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// ExtractSeedSignature extracts the signature from an Authorization header.
// The Authorization header format is:
// AWS4-HMAC-SHA256 Credential=.../..., SignedHeaders=..., Signature=<sig>
func ExtractSeedSignature(authHeader string) string {
	// Find "Signature=" in the header
	const prefix = "Signature="
	idx := strings.Index(authHeader, prefix)
	if idx < 0 {
		return ""
	}

	sig := authHeader[idx+len(prefix):]

	// Signature ends at comma or end of string
	if commaIdx := strings.Index(sig, ","); commaIdx >= 0 {
		sig = sig[:commaIdx]
	}

	return strings.TrimSpace(sig)
}

// CalculateReEncodedContentLength calculates the content length of the re-encoded
// aws-chunked body given the decoded content length.
// Each chunk format: "<hex-size>;chunk-signature=<64-char-sig>\r\n<data>\r\n"
// Final chunk: "0;chunk-signature=<64-char-sig>\r\n\r\n"
func CalculateReEncodedContentLength(decodedLength int64, chunkSize int) int64 {
	if decodedLength == 0 {
		// Just final chunk: "0;chunk-signature=<64>\r\n\r\n"
		return int64(1 + 17 + 64 + 2 + 2) // "0" + ";chunk-signature=" + sig + "\r\n" + "\r\n"
	}

	numFullChunks := decodedLength / int64(chunkSize)
	lastChunkSize := decodedLength % int64(chunkSize)

	var totalLen int64

	// Each full chunk
	for i := int64(0); i < numFullChunks; i++ {
		// Header: "<hex>;chunk-signature=<sig>\r\n"
		hexLen := len(fmt.Sprintf("%x", chunkSize))
		totalLen += int64(hexLen) + 17 + 64 + 2 // hex + ";chunk-signature=" + sig + "\r\n"
		// Data + trailing CRLF
		totalLen += int64(chunkSize) + 2
	}

	// Last partial chunk (if any)
	if lastChunkSize > 0 {
		hexLen := len(fmt.Sprintf("%x", lastChunkSize))
		totalLen += int64(hexLen) + 17 + 64 + 2
		totalLen += lastChunkSize + 2
	}

	// Final chunk: "0;chunk-signature=<64>\r\n\r\n"
	totalLen += 1 + 17 + 64 + 2 + 2

	return totalLen
}
