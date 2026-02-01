package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/observability"
	"github.com/moriyoshi/s3-router/internal/routing"
)

const (
	// Payload hash for unsigned requests
	PayloadHashUnsigned = "UNSIGNED-PAYLOAD"

	// Payload hash for streaming aws-chunked uploads
	PayloadHashStreaming = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
)

// StreamingPutObjectOptions configures streaming PUT behavior
type StreamingPutObjectOptions struct {
	PayloadHash string // x-amz-content-sha256 header value
}

// StreamingPutObject executes a PUT object request by streaming the body directly to the upstream S3.
// This bypasses the AWS SDK to avoid body buffering.
func StreamingPutObject(
	ctx context.Context,
	req *http.Request,
	bc *backend.BackendClient,
	rc *RequestContext,
	decision *routing.Decision,
) (*http.Response, error) {
	// Get payload hash from headers or use UNSIGNED-PAYLOAD
	payloadHash := rc.Headers.Get("x-amz-content-sha256")
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}

	// Build the upstream URL with endpoint resolution
	finalKey := bc.Prefix + decision.RewrittenKey
	resolvedEndpoint, err := bc.EndpointResolver.ResolveEndpointURL(ctx, backend.EndpointResolverParams{
		Bucket:            bc.Bucket,
		Key:               finalKey,
		Region:            bc.Region,
		UsePathStyle:      bc.UsePathStyle,
		UseFIPS:           bc.UseFIPS,
		UseGlobalEndpoint: bc.UseGlobalEndpoint,
		UseDualStack:      bc.UseDualStack,
		Accelerate:        bc.Accelerate,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve endpoint URL: %w", err)
	}

	logger := observability.GetLoggerFromContext(ctx)
	logger.Debug("endpoint resolved", "resolved_endpoint", resolvedEndpoint.URL.String())

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPut, resolvedEndpoint.URL.String(), req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Apply endpoint headers from resolver
	for k, v := range resolvedEndpoint.Headers {
		upstreamReq.Header[k] = v
	}

	// Copy relevant headers from client request
	copyPutObjectHeaders(upstreamReq, rc)

	// Sign the request
	if err := SignStreamingRequest(ctx, upstreamReq, bc, payloadHash); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	// Execute request
	resp, err := bc.HTTPClient.Do(upstreamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute upstream request: %w", err)
	}

	// Ensure body is properly handled
	// For error responses, we still need to read the body for error parsing
	if resp.StatusCode >= 400 {
		logger := observability.GetLoggerFromContext(ctx)
		return peekS3ErrorResponseResponse(logger, resp)
	}

	return resp, nil
}

// StreamingUploadPart executes an upload part request by streaming the body directly to the upstream S3.
func StreamingUploadPart(
	ctx context.Context,
	req *http.Request,
	bc *backend.BackendClient,
	rc *RequestContext,
	decision *routing.Decision,
	uploadID string,
	partNumber string,
) (*http.Response, error) {
	// Get payload hash from headers or use UNSIGNED-PAYLOAD
	payloadHash := rc.Headers.Get("x-amz-content-sha256")
	if payloadHash == "" {
		payloadHash = PayloadHashUnsigned
	}

	// Build the upstream URL with endpoint resolution and query parameters
	finalKey := bc.Prefix + decision.RewrittenKey
	resolvedEndpoint, err := bc.EndpointResolver.ResolveEndpointURL(ctx, backend.EndpointResolverParams{
		Bucket:            bc.Bucket,
		Key:               finalKey,
		Region:            bc.Region,
		UsePathStyle:      bc.UsePathStyle,
		UseFIPS:           bc.UseFIPS,
		UseGlobalEndpoint: bc.UseGlobalEndpoint,
		UseDualStack:      bc.UseDualStack,
		Accelerate:        bc.Accelerate,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve endpoint URL: %w", err)
	}

	// Add query parameters
	resolvedEndpoint.URL.RawQuery = fmt.Sprintf("uploadId=%s&partNumber=%s", url.QueryEscape(uploadID), partNumber)

	logger := observability.GetLoggerFromContext(ctx)
	logger.Debug("endpoint resolved", "resolved_endpoint", resolvedEndpoint.URL.String())

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPut, resolvedEndpoint.URL.String(), req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Apply endpoint headers from resolver
	for k, v := range resolvedEndpoint.Headers {
		upstreamReq.Header[k] = v
	}

	// Copy relevant headers from client request
	copyUploadPartHeaders(upstreamReq, rc)

	// Sign the request
	if err := SignStreamingRequest(ctx, upstreamReq, bc, payloadHash); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	// Execute request
	resp, err := bc.HTTPClient.Do(upstreamReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute upstream request: %w", err)
	}

	// Ensure body is properly handled
	if resp.StatusCode >= 400 {
		return peekS3ErrorResponseResponse(logger, resp)
	}

	return resp, nil
}

// copyPutObjectHeaders copies relevant headers from the client request to the upstream request.
func copyPutObjectHeaders(upstreamReq *http.Request, rc *RequestContext) {
	// Content-related headers
	if ct := rc.Headers.Get("Content-Type"); ct != "" {
		upstreamReq.Header.Set("Content-Type", ct)
	}
	if ce := rc.Headers.Get("Content-Encoding"); ce != "" {
		upstreamReq.Header.Set("Content-Encoding", ce)
	}
	if cl := rc.Headers.Get("Content-Length"); cl != "" {
		upstreamReq.Header.Set("Content-Length", cl)
		// Also set ContentLength on the request
		if clInt, err := strconv.ParseInt(cl, 10, 64); err == nil {
			upstreamReq.ContentLength = clInt
		}
	}
	if cmd5 := rc.Headers.Get("Content-MD5"); cmd5 != "" {
		upstreamReq.Header.Set("Content-MD5", cmd5)
	}

	// x-amz-content-sha256 (will be set by signer)
	if csha := rc.Headers.Get("x-amz-content-sha256"); csha != "" {
		upstreamReq.Header.Set("x-amz-content-sha256", csha)
	}

	// User metadata (x-amz-meta-*)
	for key, values := range rc.Headers {
		if len(values) > 0 && len(key) > 11 && strings.EqualFold(key[:11], "X-Amz-Meta-") {
			upstreamReq.Header.Set(key, values[0])
		}
	}

	// Checksum headers (if provided in headers, not trailers)
	// Copy any x-amz-checksum-* headers (CRC32, CRC32C, SHA1, SHA256, CRC64NVME, etc.)
	for key, values := range rc.Headers {
		lowerKey := strings.ToLower(key)
		if strings.HasPrefix(lowerKey, "x-amz-checksum-") {
			if len(values) > 0 {
				upstreamReq.Header.Set(key, values[0])
			}
		}
	}

	// Storage and ACL headers
	if sa := rc.Headers.Get("x-amz-storage-class"); sa != "" {
		upstreamReq.Header.Set("x-amz-storage-class", sa)
	}
	if acl := rc.Headers.Get("x-amz-acl"); acl != "" {
		upstreamReq.Header.Set("x-amz-acl", acl)
	}

	// Cache control and other standard headers
	if cc := rc.Headers.Get("Cache-Control"); cc != "" {
		upstreamReq.Header.Set("Cache-Control", cc)
	}
	if cd := rc.Headers.Get("Content-Disposition"); cd != "" {
		upstreamReq.Header.Set("Content-Disposition", cd)
	}
	if cl := rc.Headers.Get("Content-Language"); cl != "" {
		upstreamReq.Header.Set("Content-Language", cl)
	}
	if exp := rc.Headers.Get("Expires"); exp != "" {
		upstreamReq.Header.Set("Expires", exp)
	}

	// Host header (will be set automatically by http.Client)
}

// copyUploadPartHeaders copies relevant headers for UploadPart requests.
func copyUploadPartHeaders(upstreamReq *http.Request, rc *RequestContext) {
	// Content-related headers
	if cl := rc.Headers.Get("Content-Length"); cl != "" {
		upstreamReq.Header.Set("Content-Length", cl)
		if clInt, err := strconv.ParseInt(cl, 10, 64); err == nil {
			upstreamReq.ContentLength = clInt
		}
	}
	if cmd5 := rc.Headers.Get("Content-MD5"); cmd5 != "" {
		upstreamReq.Header.Set("Content-MD5", cmd5)
	}

	// x-amz-content-sha256 (will be set by signer)
	if csha := rc.Headers.Get("x-amz-content-sha256"); csha != "" {
		upstreamReq.Header.Set("x-amz-content-sha256", csha)
	}

	// Checksum headers
	// Copy any x-amz-checksum-* headers (CRC32, CRC32C, SHA1, SHA256, CRC64NVME, etc.)
	for key, values := range rc.Headers {
		lowerKey := strings.ToLower(key)
		if strings.HasPrefix(lowerKey, "x-amz-checksum-") {
			if len(values) > 0 {
				upstreamReq.Header.Set(key, values[0])
			}
		}
	}
}

// peekS3ErrorResponseResponse reads and parses an S3 error response from the upstream.
func peekS3ErrorResponseResponse(logger *slog.Logger, resp *http.Response) (*http.Response, error) {
	defer func() {
		_ = resp.Body.Close()
	}()
	// Read the body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		// If we can't read the body, return the response as-is with wrapped error
		return nil, err
	}

	// Create a new response with the read body
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	logger.Debug("backend error", "error", string(bodyBytes))

	return resp, err
}

// IsStreamingEligible determines if a request can use the streaming path.
func IsStreamingEligible(rc *RequestContext, isCopyOperation bool) bool {
	// Must have Content-Length (no chunked encoding)
	if rc.Headers.Get("Content-Length") == "" {
		return false
	}

	// Must not have trailer-based checksums
	if trailers := rc.Headers.Get("x-amz-trailer"); trailers != "" {
		return false
	}

	// Copy operations must use existing path
	if isCopyOperation {
		return false
	}

	return true
}

// IsAwsChunkedEligible determines if a request uses aws-chunked encoding and can be handled.
// AWS chunked requests are identified by either:
//  1. Content-Encoding containing "aws-chunked", OR
//  2. x-amz-content-sha256 being "STREAMING-AWS4-HMAC-SHA256-PAYLOAD" (or similar streaming values)
//     with x-amz-decoded-content-length present
func IsAwsChunkedEligible(rc *RequestContext, isCopyOperation bool) bool {
	// Check for aws-chunked via Content-Encoding header
	contentEncoding := rc.Headers.Get("Content-Encoding")
	hasAwsChunkedEncoding := IsAwsChunked(contentEncoding)

	// Check for aws-chunked via x-amz-content-sha256 streaming payload hash
	contentSha256 := rc.Headers.Get("x-amz-content-sha256")
	hasStreamingPayload := strings.HasPrefix(contentSha256, "STREAMING-")

	// Must be aws-chunked via either method
	if !hasAwsChunkedEncoding && !hasStreamingPayload {
		return false
	}

	// Must have Content-Length (the total encoded size)
	if rc.Headers.Get("Content-Length") == "" {
		return false
	}

	// Must have x-amz-decoded-content-length (the actual payload size)
	if rc.Headers.Get("x-amz-decoded-content-length") == "" {
		return false
	}

	// Copy operations must use existing path
	if isCopyOperation {
		return false
	}

	return true
}

// StreamingAwsChunkedPutObject handles aws-chunked PUT requests by decoding the incoming
// aws-chunked body and re-encoding it with backend credentials. This maintains true
// streaming without buffering the entire body.
func StreamingAwsChunkedPutObject(
	ctx context.Context,
	req *http.Request,
	bc *backend.BackendClient,
	rc *RequestContext,
	decision *routing.Decision,
) (*http.Response, error) {
	logger := observability.GetLoggerFromContext(ctx)

	// Extract seed signature from client request
	authHeader := rc.Headers.Get("Authorization")
	seedSignature := ExtractSeedSignature(authHeader)
	if seedSignature == "" {
		return nil, fmt.Errorf("missing seed signature in Authorization header")
	}

	// Get decoded content length
	decodedContentLength := rc.Headers.Get("x-amz-decoded-content-length")
	decodedLen, err := strconv.ParseInt(decodedContentLength, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid x-amz-decoded-content-length: %w", err)
	}

	// Get current timestamp for signing
	amzDate := rc.Headers.Get("x-amz-date")
	if amzDate == "" || len(amzDate) < 8 {
		return nil, fmt.Errorf("invalid x-amz-date header: must be at least 8 characters")
	}

	// Derive signing key for backend credentials
	dateStamp := amzDate[:8]
	signingKey := DeriveSigningKey(bc.Credentials.SecretAccessKey, dateStamp, bc.Region, "s3")

	// Create re-encoder
	reEncoder := NewAwsChunkedReEncoder(
		rc.Body,
		signingKey,
		amzDate,
		bc.Region,
		seedSignature,
	)

	// Calculate the re-encoded content length
	reEncodedContentLength := CalculateReEncodedContentLength(decodedLen, 64*1024)

	// Build the upstream URL using endpoint resolver
	finalKey := bc.Prefix + decision.RewrittenKey
	resolvedEndpoint, err := bc.EndpointResolver.ResolveEndpointURL(ctx, backend.EndpointResolverParams{
		Bucket:            bc.Bucket,
		Key:               finalKey,
		Region:            bc.Region,
		UsePathStyle:      bc.UsePathStyle,
		UseFIPS:           bc.UseFIPS,
		UseGlobalEndpoint: bc.UseGlobalEndpoint,
		UseDualStack:      bc.UseDualStack,
		Accelerate:        bc.Accelerate,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve endpoint: %w", err)
	}

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPut, resolvedEndpoint.URL.String(), reEncoder)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Apply endpoint headers
	for k, v := range resolvedEndpoint.Headers {
		upstreamReq.Header[k] = v
	}

	// Set required headers
	upstreamReq.Header.Set("Content-Encoding", "aws-chunked")
	upstreamReq.Header.Set("x-amz-decoded-content-length", decodedContentLength)
	upstreamReq.Header.Set("Content-Length", strconv.FormatInt(reEncodedContentLength, 10))
	upstreamReq.ContentLength = reEncodedContentLength

	// Copy through relevant headers
	copyAwsChunkedHeaders(upstreamReq, rc)

	// Sign the request with streaming payload hash
	if err := SignStreamingRequest(ctx, upstreamReq, bc, PayloadHashStreaming); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	logger.Debug("streaming aws-chunked PUT",
		"bucket", bc.Bucket,
		"key", finalKey,
		"decoded_length", decodedLen,
		"encoded_length", reEncodedContentLength,
	)

	// Execute request
	resp, err := bc.HTTPClient.Do(upstreamReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}

	return resp, nil
}

// StreamingAwsChunkedUploadPart handles aws-chunked UploadPart requests.
func StreamingAwsChunkedUploadPart(
	ctx context.Context,
	req *http.Request,
	bc *backend.BackendClient,
	rc *RequestContext,
	decision *routing.Decision,
	uploadID string,
	partNumber string,
) (*http.Response, error) {
	logger := observability.GetLoggerFromContext(ctx)

	// Extract seed signature from client request
	authHeader := rc.Headers.Get("Authorization")
	seedSignature := ExtractSeedSignature(authHeader)
	if seedSignature == "" {
		return nil, fmt.Errorf("missing seed signature in Authorization header")
	}

	// Get decoded content length
	decodedContentLength := rc.Headers.Get("x-amz-decoded-content-length")
	decodedLen, err := strconv.ParseInt(decodedContentLength, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid x-amz-decoded-content-length: %w", err)
	}

	// Get current timestamp for signing
	amzDate := rc.Headers.Get("x-amz-date")
	if amzDate == "" || len(amzDate) < 8 {
		return nil, fmt.Errorf("invalid x-amz-date header: must be at least 8 characters")
	}

	// Derive signing key for backend credentials
	dateStamp := amzDate[:8]
	signingKey := DeriveSigningKey(bc.Credentials.SecretAccessKey, dateStamp, bc.Region, "s3")

	// Create re-encoder
	reEncoder := NewAwsChunkedReEncoder(
		rc.Body,
		signingKey,
		amzDate,
		bc.Region,
		seedSignature,
	)

	// Calculate the re-encoded content length
	reEncodedContentLength := CalculateReEncodedContentLength(decodedLen, 64*1024)

	// Build the upstream URL using endpoint resolver
	finalKey := bc.Prefix + decision.RewrittenKey
	resolvedEndpoint, err := bc.EndpointResolver.ResolveEndpointURL(ctx, backend.EndpointResolverParams{
		Bucket:            bc.Bucket,
		Key:               finalKey,
		Region:            bc.Region,
		UsePathStyle:      bc.UsePathStyle,
		UseFIPS:           bc.UseFIPS,
		UseGlobalEndpoint: bc.UseGlobalEndpoint,
		UseDualStack:      bc.UseDualStack,
		Accelerate:        bc.Accelerate,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to resolve endpoint: %w", err)
	}

	// Add query parameters for UploadPart
	resolvedEndpoint.URL.RawQuery = fmt.Sprintf("uploadId=%s&partNumber=%s", url.QueryEscape(uploadID), partNumber)

	// Create upstream request
	upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPut, resolvedEndpoint.URL.String(), reEncoder)
	if err != nil {
		return nil, fmt.Errorf("failed to create upstream request: %w", err)
	}

	// Apply endpoint headers
	for k, v := range resolvedEndpoint.Headers {
		upstreamReq.Header[k] = v
	}

	// Set required headers
	upstreamReq.Header.Set("Content-Encoding", "aws-chunked")
	upstreamReq.Header.Set("x-amz-decoded-content-length", decodedContentLength)
	upstreamReq.Header.Set("Content-Length", strconv.FormatInt(reEncodedContentLength, 10))
	upstreamReq.ContentLength = reEncodedContentLength

	// Sign the request with streaming payload hash
	if err := SignStreamingRequest(ctx, upstreamReq, bc, PayloadHashStreaming); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	logger.Debug("streaming aws-chunked UploadPart",
		"bucket", bc.Bucket,
		"key", finalKey,
		"part_number", partNumber,
		"decoded_length", decodedLen,
		"encoded_length", reEncodedContentLength,
	)

	// Execute request
	resp, err := bc.HTTPClient.Do(upstreamReq)
	if err != nil {
		return nil, fmt.Errorf("upstream request failed: %w", err)
	}

	return resp, nil
}

// copyAwsChunkedHeaders copies relevant headers for aws-chunked requests.
func copyAwsChunkedHeaders(dst *http.Request, rc *RequestContext) {
	// Content-Type
	if ct := rc.Headers.Get("Content-Type"); ct != "" {
		dst.Header.Set("Content-Type", ct)
	}

	// User metadata (x-amz-meta-*)
	for key, values := range rc.Headers {
		if len(values) > 0 && len(key) > 11 && strings.EqualFold(key[:11], "X-Amz-Meta-") {
			dst.Header.Set(key, values[0])
		}
	}

	// Checksum headers (x-amz-checksum-*)
	for key, values := range rc.Headers {
		if len(values) > 0 && len(key) > 15 && strings.EqualFold(key[:15], "X-Amz-Checksum-") {
			dst.Header.Set(key, values[0])
		}
	}

	// Checksum algorithm
	if ca := rc.Headers.Get("x-amz-checksum-algorithm"); ca != "" {
		dst.Header.Set("x-amz-checksum-algorithm", ca)
	}

	// Storage class and ACL
	if sa := rc.Headers.Get("x-amz-storage-class"); sa != "" {
		dst.Header.Set("x-amz-storage-class", sa)
	}
	if acl := rc.Headers.Get("x-amz-acl"); acl != "" {
		dst.Header.Set("x-amz-acl", acl)
	}

	// Cache and content disposition
	if cc := rc.Headers.Get("Cache-Control"); cc != "" {
		dst.Header.Set("Cache-Control", cc)
	}
	if cd := rc.Headers.Get("Content-Disposition"); cd != "" {
		dst.Header.Set("Content-Disposition", cd)
	}
}
