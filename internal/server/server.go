package server

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/moriyoshi/s3-router/internal/backend/proxy"
	"github.com/moriyoshi/s3-router/internal/bucket"
	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/observability"
	"github.com/moriyoshi/s3-router/internal/routing"
)

const (
	// Auth error codes
	AuthErrorMissingToken       = "MissingAuthenticationToken"
	AuthErrorInvalidAuthHeader  = "InvalidAuthHeader"
	AuthErrorMissingDateHeader  = "MissingDateHeader"
	AuthErrorInvalidDateHeader  = "InvalidDateHeader"
	AuthErrorInvalidAccessKeyId = "InvalidAccessKeyId"
	AuthErrorSignatureMismatch  = "SignatureDoesNotMatch"
	AuthErrorTimeTooSkewed      = "RequestTimeTooSkewed"
)

// HTTPError represents an HTTP error with status code and headers.
type HTTPError struct {
	statusCode int
	message    string
	header     http.Header
}

func (e *HTTPError) StatusCode() int {
	return e.statusCode
}

func (e *HTTPError) Message() string {
	return e.message
}

func (e *HTTPError) Header() http.Header {
	return e.header
}

func (e *HTTPError) Error() string {
	return e.message
}

// NewHTTPError creates a new HTTPError with optional header key-value pairs.
func NewHTTPError(message string, statusCode int, headerValuePairs ...string) *HTTPError {
	header := make(http.Header)
	for i := 0; i < len(headerValuePairs); {
		k := headerValuePairs[i]
		i++
		if i >= len(headerValuePairs) {
			break
		}
		v := headerValuePairs[i]
		i++
		header.Add(k, v)
	}
	return &HTTPError{
		statusCode: statusCode,
		message:    message,
		header:     header,
	}
}

// VirtualHostChecker defines an interface for checking virtual host bucket mappings
type VirtualHostChecker interface {
	// GetBucketMapping returns the bucket name for a host.
	// Returns (bucket, true) if a mapping exists, or ("", false) otherwise.
	// For wildcard matches without explicit mapping, subdomain extraction is used.
	GetBucketMapping(host string) (bucket string, ok bool)
}

// Server handles S3 API requests.
type Server struct {
	Logger               *slog.Logger
	Matcher              *routing.Matcher
	Verifier             *Verifier
	BucketOpsHandler     *bucket.BucketOperationHandler
	ListObjectsV2Handler *bucket.ListObjectsV2Handler
	Executor             *proxy.Executor
	Metrics              *observability.Metrics
	MaxBodySize          int64
	VirtualHostChecker   *config.VirtualHostConfig
}

func (s *Server) HandleS3Request(w http.ResponseWriter, r *http.Request) {
	var err error
	defer func() {
		if err != nil {
			var httpError *HTTPError
			if errors.As(err, &httpError) {
				contextLogger := observability.WithTraceContext(r.Context(), s.Logger)
				if contextLogger == nil {
					contextLogger = s.Logger
				}
				contextLogger.Info("error in the handler", "error", httpError)
				for k, vs := range httpError.Header() {
					w.Header()[k] = vs
				}
				http.Error(w, httpError.Message(), httpError.StatusCode())
			} else {
				contextLogger := observability.WithTraceContext(r.Context(), s.Logger)
				if contextLogger == nil {
					contextLogger = s.Logger
				}
				contextLogger.Error("error in the handler", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}
	}()

	// Enforce max_body_size at ingress
	if s.MaxBodySize > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.MaxBodySize)
	}

	logger := s.Logger.With(
		"proto", r.Proto,
		"method", r.Method,
		"request_uri", r.RequestURI,
		"content_length", r.ContentLength,
		"host", r.Host,
	)

	// Inject trace context into logger
	logger = observability.WithTraceContext(r.Context(), logger)
	if logger == nil {
		logger = s.Logger
	}

	// Parse S3 path - updated to handle bucket-level operations
	bucketName, objectKey := ParseS3PathWithChecker(r, s.VirtualHostChecker)

	logger = logger.With(
		"bucket", bucketName,
		"key", objectKey,
	)

	ok, err := s.handleBucketOperations(logger, w, r, bucketName, objectKey)
	if ok {
		return
	}

	// For non-bucket operations, require a bucket
	if bucketName == "" {
		http.Error(w, "Invalid S3 path", http.StatusBadRequest)
		return
	}

	err = s.handleObjectOperations(logger, w, r, bucketName, objectKey)
}

func (s *Server) handleBucketOperations(logger *slog.Logger, w http.ResponseWriter, r *http.Request, bucketName, objectKey string) (bool, error) {
	// Detect and handle bucket-level control-plane operations early (before routing)
	operation := bucket.DetectBucketOperation(r, bucketName, objectKey)
	if operation == "" {
		return false, nil
	}

	logger.Info("bucket operation intercepted", "operation", operation, "bucket", bucketName)

	var err error
	switch operation {
	case bucket.OperationListBuckets:
		if err = s.BucketOpsHandler.HandleListBuckets(w); err != nil {
			err = fmt.Errorf("failed to handle ListBuckets: %w", err)
		}
	case bucket.OperationCreateBucket:
		if err = s.BucketOpsHandler.HandleCreateBucket(w, bucketName); err != nil {
			err = fmt.Errorf("failed to handle CreateBucket: %w", err)
		}
	case bucket.OperationDeleteBucket:
		if err = s.BucketOpsHandler.HandleDeleteBucket(w, bucketName); err != nil {
			err = fmt.Errorf("failed to handle DeleteBucket: %w", err)
		}
	default:
		return false, nil
	}

	return true, err
}

func (s *Server) handleObjectOperations(logger *slog.Logger, w http.ResponseWriter, r *http.Request, bucketName, objectKey string) error {
	queryParams := r.URL.Query()

	// Check for ListObjects v1 operation (before routing/auth)
	if bucket.DetectListObjects(r, objectKey) {
		logger.Info("list objects v1 operation detected", "bucket", bucketName)
		return s.handleListObjects(logger, w, r, bucketName, objectKey)
	}

	// Check for ListObjectsV2 operation first (before routing/auth)
	if objectKey == "" && bucket.DetectListObjectsV2(r) {
		logger.Info("list objects v2 operation detected", "bucket", bucketName)
		return s.handleListObjectsV2(logger, w, r, bucketName)
	}

	// Detect multipart operations
	multipartOp := bucket.DetectMultipartOperation(r.Method, queryParams)
	if multipartOp != "" {
		logger.Info("multipart operation detected", "operation", multipartOp, "bucket", bucketName, "key", objectKey)
		return s.handleObjectOperation(logger, w, r, bucketName, objectKey, multipartOp)
	}

	// Regular object operations require routing
	return s.handleObjectOperation(logger, w, r, bucketName, objectKey, "")
}

//nolint:gocyclo
func (s *Server) handleObjectOperation(logger *slog.Logger, w http.ResponseWriter, r *http.Request, bucketName, objectKey, operation string) error {
	// Verify authentication for other operations
	authCtx, err := s.Verifier.VerifyRequest(r)
	if err != nil {
		logger.Warn("auth verification failed", "error", err)
		// Enforce strict authentication - reject all unauthenticated requests
		if authErr, ok := err.(*AuthError); ok {
			switch authErr.Code {
			case AuthErrorMissingToken:
				return NewHTTPError("Missing Authorization header", http.StatusUnauthorized, "WWW-Authenticate", `AWS4-HMAC-SHA256`)
			case AuthErrorInvalidAuthHeader, AuthErrorMissingDateHeader, AuthErrorInvalidDateHeader:
				return NewHTTPError("Invalid authentication header", http.StatusBadRequest)
			case AuthErrorInvalidAccessKeyId:
				return NewHTTPError("The AWS Access Key Id you provided does not exist in our records", http.StatusForbidden)
			case AuthErrorSignatureMismatch:
				return NewHTTPError("The authorization signature provided is invalid", http.StatusForbidden)
			case AuthErrorTimeTooSkewed:
				return NewHTTPError("Request timestamp too far from current time", http.StatusForbidden)
			default:
				return NewHTTPError("Authentication failed", http.StatusForbidden)
			}
		} else {
			return NewHTTPError("Authentication failed", http.StatusForbidden)
		}
	}

	if !authCtx.IsAuthenticated {
		return NewHTTPError("Missing Authorization header", http.StatusUnauthorized, "WWW-Authenticate", `AWS4-HMAC-SHA256`)
	}

	// Find matching route (include headers for header-based routing conditions)
	decision, err := s.Matcher.Match(r.Context(), bucketName, objectKey, r.Method, r.Header)
	if err != nil {
		logger.Warn("no matching route")
		return NewHTTPError("Not found", http.StatusNotFound)
	}

	logger = logger.With(
		"backend_id", decision.Backend.ID,
		"rewritten_key", decision.RewrittenKey,
	)
	if operation != "" {
		logger = logger.With("operation", operation)
	}
	ctx := observability.WithLogger(r.Context(), logger)
	s.Metrics.RoutingDecisions.WithLabelValues(bucketName, decision.Backend.ID).Inc()

	start := time.Now()

	// Create request context
	rc := &proxy.RequestContext{
		Bucket:      bucketName,
		ObjectKey:   objectKey,
		Operation:   operation,
		Headers:     r.Header,
		Body:        r.Body,
		Principal:   authCtx.Principal,
		Method:      r.Method,
		Time:        start,
		Request:     r,             // Add original request for trailer access
		QueryParams: r.URL.Query(), // Pass query parameters for multipart operations
	}

	// Execute proxy
	logger.Info("executing proxy")
	resp, err := s.Executor.Execute(ctx, rc, decision)
	duration := time.Since(start)

	if err != nil {
		logger.Error("proxy execution failed",
			"error", err,
			"duration", duration.Milliseconds(),
		)
		s.Metrics.BackendErrors.WithLabelValues(decision.Backend.ID, "execution_error").Inc()
		return errors.Join(NewHTTPError(fmt.Sprintf("Backend error: %v", err), http.StatusBadGateway), err)
	}

	// Write response
	s.Metrics.RequestTotal.WithLabelValues(r.Method, decision.Backend.ID, fmt.Sprintf("%d", resp.StatusCode)).Inc()
	s.Metrics.RequestLatency.WithLabelValues(r.Method, decision.Backend.ID).Observe(duration.Seconds())

	// Pre-check response size before writing headers
	// This prevents silent truncation by validating Content-Length upfront
	if resp.Body != nil && resp.StatusCode == http.StatusOK {
		if contentLengthStr := resp.Header.Get("Content-Length"); contentLengthStr != "" {
			var contentLength int64
			if _, err := fmt.Sscanf(contentLengthStr, "%d", &contentLength); err == nil {
				if contentLength > s.MaxBodySize {
					w.WriteHeader(413) // Payload Too Large
					errMsg := fmt.Sprintf("Object size %d exceeds maximum allowed size %d", contentLength, s.MaxBodySize)
					logger.Warn("response size exceeds limit",
						"content_length", contentLength,
						"max_body_size", s.MaxBodySize,
					)
					_, _ = w.Write([]byte(errMsg + "\n"))
					_ = resp.Body.Close()
					return fmt.Errorf("response size exceeds limit: %d > %d", contentLength, s.MaxBodySize)
				}
			}
		}
	}

	for k, v := range resp.Header {
		// Use direct map access to avoid header name canonicalization for metadata headers
		w.Header()[k] = v
	}

	w.WriteHeader(resp.StatusCode)
	if resp.Body != nil {
		defer func() {
			_ = resp.Body.Close()
		}()

		// Copy response body with size limit (for streaming responses without Content-Length)
		limitedBody := io.LimitReader(resp.Body, s.MaxBodySize+1)
		written, err := io.Copy(w, limitedBody)
		if err != nil {
			return fmt.Errorf("failed to copy response body: %w", err)
		}
		if written > s.MaxBodySize {
			return fmt.Errorf("response body exceeds maximum allowed size: %d > %d", written, s.MaxBodySize)
		}
	}

	return nil
}

func (s *Server) handleListObjects(logger *slog.Logger, w http.ResponseWriter, r *http.Request, bucketName, objectKey string) error {
	// Parse ListObjects (v1) parameters
	params := bucket.ParseListObjectsParams(r.URL.Query())

	logger = logger.With(
		"operation", "ListObjects",
		"prefix", params.Prefix,
		"max_keys", params.MaxKeys,
		"delimiter", params.Delimiter,
		"marker", params.Marker,
	)

	// Verify authentication (ListObjects requires auth)
	authCtx, err := s.Verifier.VerifyRequest(r)
	if err != nil {
		logger.Warn("auth verification failed", "error", err)
		if authErr, ok := err.(*AuthError); ok {
			switch authErr.Code {
			case AuthErrorMissingToken:
				return NewHTTPError("Missing Authorization header", http.StatusUnauthorized, "WWW-Authenticate", `AWS4-HMAC-SHA256`)
			case AuthErrorInvalidAuthHeader, AuthErrorMissingDateHeader, AuthErrorInvalidDateHeader:
				return NewHTTPError("Invalid authentication header", http.StatusBadRequest)
			case AuthErrorInvalidAccessKeyId:
				return NewHTTPError("The AWS Access Key Id you provided does not exist in our records", http.StatusForbidden)
			case AuthErrorSignatureMismatch:
				return NewHTTPError("The authorization signature provided is invalid", http.StatusForbidden)
			case AuthErrorTimeTooSkewed:
				return NewHTTPError("Request timestamp too far from current time", http.StatusForbidden)
			default:
				return NewHTTPError("Authentication failed", http.StatusForbidden)
			}
		} else {
			return NewHTTPError("Authentication failed", http.StatusForbidden)
		}
	}

	if !authCtx.IsAuthenticated {
		return NewHTTPError("Missing Authorization header", http.StatusUnauthorized, "WWW-Authenticate", `AWS4-HMAC-SHA256`)
	}

	// Reuse ListObjectsV2 handler with v1 params converted to v2
	logger.Info("handling ListObjects v1 via virtual bucket handler", "bucket", bucketName)

	ctx := observability.WithLogger(r.Context(), logger)
	v2Params := params.ToV2Params()
	v2Response, err := s.ListObjectsV2Handler.HandleListObjectsV2(ctx, bucketName, v2Params)
	if err != nil {
		logger.Error("ListObjects handler failed", "error", err)
		return NewHTTPError(fmt.Sprintf("Failed to list objects: %v", err), http.StatusInternalServerError)
	}

	// Convert v2 response to v1 format
	v1Response := bucket.S3ListObjectsResponse{
		XMLNS:          "http://s3.amazonaws.com/doc/2006-03-01/",
		Name:           v2Response.Name,
		Prefix:         v2Response.Prefix,
		Marker:         params.Marker,
		Delimiter:      v2Response.Delimiter,
		MaxKeys:        v2Response.MaxKeys,
		IsTruncated:    v2Response.IsTruncated,
		Contents:       v2Response.Contents,
		CommonPrefixes: v2Response.CommonPrefixes,
	}

	// Set NextMarker if truncated (last key in result)
	if v2Response.IsTruncated && len(v2Response.Contents) > 0 {
		v1Response.NextMarker = v2Response.Contents[len(v2Response.Contents)-1].Key
	}

	// Marshal to XML
	xmlData, err := xml.MarshalIndent(v1Response, "", " ")
	if err != nil {
		return fmt.Errorf("failed to marshal ListObjects response: %w", err)
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(append([]byte(xml.Header), xmlData...))
	return err
}

func (s *Server) handleListObjectsV2(logger *slog.Logger, w http.ResponseWriter, r *http.Request, bucketName string) error {
	// Parse ListObjectsV2 parameters
	params := bucket.ParseListObjectsV2Params(r.URL.Query())

	logger = logger.With(
		"operation", "ListObjectsV2",
		"prefix", params.Prefix,
		"max_keys", params.MaxKeys,
		"delimiter", params.Delimiter,
	)

	// Verify authentication (ListObjectsV2 requires auth)
	authCtx, err := s.Verifier.VerifyRequest(r)
	if err != nil {
		logger.Warn("auth verification failed", "error", err)
		if authErr, ok := err.(*AuthError); ok {
			switch authErr.Code {
			case AuthErrorMissingToken:
				return NewHTTPError("Missing Authorization header", http.StatusUnauthorized, "WWW-Authenticate", `AWS4-HMAC-SHA256`)
			case AuthErrorInvalidAuthHeader, AuthErrorMissingDateHeader, AuthErrorInvalidDateHeader:
				return NewHTTPError("Invalid authentication header", http.StatusBadRequest)
			case AuthErrorInvalidAccessKeyId:
				return NewHTTPError("The AWS Access Key Id you provided does not exist in our records", http.StatusForbidden)
			case AuthErrorSignatureMismatch:
				return NewHTTPError("The authorization signature provided is invalid", http.StatusForbidden)
			case AuthErrorTimeTooSkewed:
				return NewHTTPError("Request timestamp too far from current time", http.StatusForbidden)
			default:
				return NewHTTPError("Authentication failed", http.StatusForbidden)
			}
		} else {
			return NewHTTPError("Authentication failed", http.StatusForbidden)
		}
	}

	if !authCtx.IsAuthenticated {
		return NewHTTPError("Missing Authorization header", http.StatusUnauthorized, "WWW-Authenticate", `AWS4-HMAC-SHA256`)
	}

	// Use the ListObjectsV2 handler to process the request
	logger.Info("handling ListObjectsV2 via virtual bucket handler", "bucket", bucketName)

	ctx := observability.WithLogger(r.Context(), logger)
	response, err := s.ListObjectsV2Handler.HandleListObjectsV2(ctx, bucketName, params)
	if err != nil {
		logger.Error("ListObjectsV2 handler failed", "error", err)
		return NewHTTPError(fmt.Sprintf("Failed to list objects: %v", err), http.StatusInternalServerError)
	}

	// Marshal to XML
	xmlData, err := xml.MarshalIndent(response, "", " ")
	if err != nil {
		return fmt.Errorf("failed to marshal ListObjectsV2 response: %w", err)
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(append([]byte(xml.Header), xmlData...))
	return err
}

// ParseS3PathWithChecker extracts bucket and object key from the request using the VirtualHostChecker.
// This supports explicit bucket name mappings configured in virtual_hosts.
func ParseS3PathWithChecker(r *http.Request, checker *config.VirtualHostConfig) (bucket, objectKey string) {
	host := r.Host
	path := r.URL.EscapedPath()

	// Check if there's an explicit bucket mapping for this host (virtual-host style)
	if checker != nil {
		if mappedBucket, ok := checker.GetBucketMapping(host); ok {
			bucket = mappedBucket
			// Extract object key from path
			if len(path) > 1 {
				objectKey = path[1:] // Skip leading /, then decode
			}
			return
		}
	}

	// Path-style: bucket is in the URL path
	parts := parsePathStyle(path)
	if len(parts) > 0 {
		bucket, _ = url.PathUnescape(parts[0])
		if len(parts) > 1 {
			objectKey = parts[1]
		}
		return
	}

	return
}

func parsePathStyle(path string) []string {
	if path == "/" || len(path) <= 1 {
		return nil
	}

	parts := make([]string, 0)
	p := path[1:] // Skip leading /

	if idx := len(p) - 1; idx >= 0 {
		// Find first slash to split bucket and key
		for i := 0; i < len(p); i++ {
			if p[i] == '/' {
				parts = append(parts, p[:i])
				if i+1 < len(p) {
					parts = append(parts, p[i+1:])
				}
				return parts
			}
		}
		parts = append(parts, p)
	}

	return parts
}
