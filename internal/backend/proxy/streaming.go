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
