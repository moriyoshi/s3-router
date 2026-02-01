package proxy

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel"

	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/observability"
	"github.com/moriyoshi/s3-router/internal/routing"
)

const (
	// S3 error codes
	ErrorCodeNoSuchKey = "NoSuchKey"
)

type RequestContext struct {
	Bucket      string
	ObjectKey   string
	Operation   string
	Headers     http.Header
	Body        io.ReadCloser
	Principal   string
	Method      string
	Time        time.Time
	Request     *http.Request       // Add original request for trailer access
	QueryParams map[string][]string // S3 API query parameters (list-type, uploadId, partNumber, etc.)
}

type Executor struct {
	backendMgr *backend.Manager
	matcher    *routing.Matcher
}

func NewExecutor(backendMgr *backend.Manager) *Executor {
	return &Executor{
		backendMgr: backendMgr,
	}
}

// SetMatcher sets the routing matcher for per-key routing operations like DeleteObjects
func (e *Executor) SetMatcher(matcher *routing.Matcher) {
	e.matcher = matcher
}

// S3ErrorResponse represents an S3 error response in XML format
type S3ErrorResponse struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

func hexValue(c byte) byte {
	if c >= '0' && c <= '9' {
		return c - '0'
	}
	if c >= 'A' && c <= 'F' {
		return c - 'A' + 10
	}
	if c >= 'a' && c <= 'f' {
		return c - 'a' + 10
	}
	return 255
}

// conservativePathUnescape unescapes a path
//
// ref. https://github.com/aws/smithy-go/blob/main/encoding/httpbinding/path_replace.go#L82
func conservativePathUnescape(path string) (string, error) {
	r := make([]byte, len(path))
	j := 0
	for i := 0; i < len(path); {
		c := path[i]
		if c == '%' {
			h := byte(255)
			l := byte(255)
			if i+2 < len(path) {
				h = hexValue(path[i+1])
				l = hexValue(path[i+2])
			}
			if h != 255 && l != 255 {
				c = (h << 4) | l
				r[j] = c
				i += 3
				j++
				continue
			}
			r[j] = '%'
			i++
			j++
		} else {
			r[j] = c
			i++
			j++
		}
	}
	return string(r[:j]), nil
}

// awsErrorToHTTPResponse converts an AWS SDK error to an HTTP response with appropriate status code
func awsErrorToHTTPResponse(err error) (*http.Response, error) {
	if err == nil {
		return nil, nil
	}

	statusCode := 0
	errorCode := ""
	message := err.Error()

	// Try to extract error code from AWS error
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		errorCode = apiErr.ErrorCode()
		message = apiErr.ErrorMessage()

		switch errorCode {
		case ErrorCodeNoSuchKey:
			statusCode = http.StatusNotFound
		case "NoSuchBucket":
			statusCode = http.StatusNotFound
		case "NotFound": // HEAD requests for non-existent objects return this
			statusCode = http.StatusNotFound
			errorCode = ErrorCodeNoSuchKey // Normalize to NoSuchKey for consistency
		case "AccessDenied":
			statusCode = http.StatusForbidden
		case "InvalidArgument":
			statusCode = http.StatusBadRequest
		case "MalformedXML":
			statusCode = http.StatusBadRequest
		case "PreconditionFailed":
			statusCode = http.StatusPreconditionFailed
		case "Conflict":
			statusCode = http.StatusConflict
		case "NotModified":
			statusCode = http.StatusNotModified
		}
	}

	// Try to extract HTTP status code from ResponseError if API error code wasn't recognized
	if statusCode == 0 {
		var respErr interface{ HTTPStatusCode() int }
		if errors.As(err, &respErr) {
			httpStatus := respErr.HTTPStatusCode()
			switch httpStatus {
			case http.StatusNotFound:
				statusCode = http.StatusNotFound
				errorCode = ErrorCodeNoSuchKey
			case http.StatusForbidden:
				statusCode = http.StatusForbidden
				errorCode = "AccessDenied"
			case http.StatusPreconditionFailed:
				statusCode = http.StatusPreconditionFailed
				errorCode = "PreconditionFailed"
			case http.StatusNotModified:
				statusCode = http.StatusNotModified
				errorCode = "NotModified"
			}
		}
	}

	if statusCode == 0 {
		return nil, err
	}

	// Build S3 XML error response
	errResp := S3ErrorResponse{
		Code:    errorCode,
		Message: message,
	}
	xmlData, xmlErr := xml.MarshalIndent(errResp, "", " ")
	if xmlErr != nil {
		// Fallback to plain text if XML marshaling fails
		return &http.Response{
			Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
			StatusCode: statusCode,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(errorCode + ": " + message)),
		}, nil
	}

	resp := &http.Response{
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...))),
	}
	resp.Header.Set("Content-Type", "application/xml")
	return resp, nil
}

// Execute proxies the request to the backend and returns the response
func (e *Executor) Execute(ctx context.Context, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	tracer := otel.Tracer("github.com/moriyoshi/s3-router/backend/proxy")
	ctx, span := tracer.Start(ctx, "Execute")
	defer span.End()

	// Get backend client
	backendClient, err := e.backendMgr.GetClient(decision.Backend.ID)
	if err != nil {
		return nil, err
	}
	logger := observability.GetLoggerFromContext(ctx)
	if logger != nil {
		logger = backendClient.DecorateLogger(logger)
		ctx = observability.WithLogger(ctx, logger)
	}

	// Route to appropriate S3 operation
	switch strings.ToUpper(rc.Method) {
	case http.MethodGet:
		// Check if this is a ListObjectsV2 operation based on request context
		if rc.Operation == "ListObjectsV2" {
			return e.executeListObjectsV2(ctx, backendClient, rc, decision)
		}
		// Check for ListParts operation
		if rc.Operation == "ListParts" {
			return e.executeListParts(ctx, backendClient, rc, decision)
		}
		// Check for GetObjectAcl operation
		if rc.Operation == "GetObjectAcl" {
			return e.executeGetObjectAcl(ctx, backendClient, rc, decision)
		}
		// Check for GetObjectTagging operation
		if _, hasTagging := rc.QueryParams["tagging"]; hasTagging {
			return e.executeGetObjectTagging(ctx, backendClient, rc, decision)
		}
		return e.executeGetObject(ctx, backendClient, rc, decision)
	case http.MethodHead:
		return e.executeHeadObject(ctx, backendClient, rc, decision)
	case http.MethodPut:
		// Check for PutObjectAcl operation
		if rc.Operation == "PutObjectAcl" {
			return e.executePutObjectAcl(ctx, backendClient, rc, decision)
		}
		// Check for UploadPart operation
		if rc.Operation == "UploadPart" {
			return e.executeUploadPart(ctx, backendClient, rc, decision)
		}
		// Check for PutObjectTagging operation
		if _, hasTagging := rc.QueryParams["tagging"]; hasTagging {
			return e.executePutObjectTagging(ctx, backendClient, rc, decision)
		}
		return e.executePutObject(ctx, backendClient, rc, decision)
	case http.MethodDelete:
		// Check for AbortMultipartUpload operation
		if rc.Operation == "AbortMultipartUpload" {
			return e.executeAbortMultipartUpload(ctx, backendClient, rc, decision)
		}
		// Check for DeleteObjectTagging operation
		if _, hasTagging := rc.QueryParams["tagging"]; hasTagging {
			return e.executeDeleteObjectTagging(ctx, backendClient, rc, decision)
		}
		return e.executeDeleteObject(ctx, backendClient, rc, decision)
	case "POST":
		return e.executePost(ctx, backendClient, rc, decision)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", rc.Method)
	}
}

//nolint:gocyclo
func (e *Executor) executeGetObject(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	// Build GetObject request
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}
	input := &s3.GetObjectInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	}

	// Forward Range header if present
	if rangeHeader := rc.Headers.Get("Range"); rangeHeader != "" {
		input.Range = aws.String(rangeHeader)
	}

	// Forward conditional headers
	if match := rc.Headers.Get("If-Match"); match != "" {
		input.IfMatch = aws.String(match)
	}
	if noneMatch := rc.Headers.Get("If-None-Match"); noneMatch != "" {
		input.IfNoneMatch = aws.String(noneMatch)
	}
	if modifiedSince := rc.Headers.Get("If-Modified-Since"); modifiedSince != "" {
		// Parse HTTP date format
		if t, err := time.Parse(http.TimeFormat, modifiedSince); err == nil {
			input.IfModifiedSince = aws.Time(t)
		}
	}
	if unmodifiedSince := rc.Headers.Get("If-Unmodified-Since"); unmodifiedSince != "" {
		// Parse HTTP date format
		if t, err := time.Parse(http.TimeFormat, unmodifiedSince); err == nil {
			input.IfUnmodifiedSince = aws.Time(t)
		}
	}

	output, err := bc.S3Operations.GetObject(ctx, input)

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Determine response status: 206 for partial content, 200 for full content
	statusCode := http.StatusOK
	statusText := "200 OK"
	if output.ContentRange != nil {
		statusCode = http.StatusPartialContent
		statusText = "206 Partial Content"
	}

	// Convert to HTTP response
	resp := &http.Response{
		Status:     statusText,
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       output.Body,
	}

	// Copy standard response headers
	if output.ContentType != nil {
		resp.Header.Set("Content-Type", *output.ContentType)
	}
	if output.ContentLength != nil {
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", *output.ContentLength))
	}
	if output.ContentRange != nil {
		resp.Header.Set("Content-Range", *output.ContentRange)
	}
	if output.ETag != nil {
		resp.Header.Set("ETag", *output.ETag)
	}
	if output.LastModified != nil {
		resp.Header.Set("Last-Modified", output.LastModified.UTC().Format(http.TimeFormat))
	}
	if output.ContentEncoding != nil {
		resp.Header.Set("Content-Encoding", *output.ContentEncoding)
	}
	if output.CacheControl != nil {
		resp.Header.Set("Cache-Control", *output.CacheControl)
	}
	if output.ContentDisposition != nil {
		resp.Header.Set("Content-Disposition", *output.ContentDisposition)
	}
	if output.ContentLanguage != nil {
		resp.Header.Set("Content-Language", *output.ContentLanguage)
	}
	if output.ExpiresString != nil {
		resp.Header.Set("Expires", *output.ExpiresString)
	}
	if output.VersionId != nil {
		resp.Header.Set("x-amz-version-id", *output.VersionId)
	}
	if output.ServerSideEncryption != "" {
		resp.Header.Set("x-amz-server-side-encryption", string(output.ServerSideEncryption))
	}
	if output.SSEKMSKeyId != nil {
		resp.Header.Set("x-amz-server-side-encryption-aws-kms-key-id", *output.SSEKMSKeyId)
	}
	if output.StorageClass != "" {
		resp.Header.Set("x-amz-storage-class", string(output.StorageClass))
	}

	// Set Accept-Ranges header
	resp.Header.Set("Accept-Ranges", "bytes")

	// Copy metadata headers - use lowercase to match S3 behavior
	// Using direct map access to avoid Go's http.Header canonicalization
	if output.Metadata != nil {
		for k, v := range output.Metadata {
			headerKey := "x-amz-meta-" + strings.ToLower(k)
			resp.Header[headerKey] = []string{v}
		}
	}

	return resp, nil
}

//nolint:gocyclo
func (e *Executor) executeHeadObject(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}
	input := &s3.HeadObjectInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	}

	// Forward conditional headers
	if match := rc.Headers.Get("If-Match"); match != "" {
		input.IfMatch = aws.String(match)
	}
	if noneMatch := rc.Headers.Get("If-None-Match"); noneMatch != "" {
		input.IfNoneMatch = aws.String(noneMatch)
	}
	if modifiedSince := rc.Headers.Get("If-Modified-Since"); modifiedSince != "" {
		// Parse HTTP date format
		if t, err := time.Parse(http.TimeFormat, modifiedSince); err == nil {
			input.IfModifiedSince = aws.Time(t)
		}
	}
	if unmodifiedSince := rc.Headers.Get("If-Unmodified-Since"); unmodifiedSince != "" {
		// Parse HTTP date format
		if t, err := time.Parse(http.TimeFormat, unmodifiedSince); err == nil {
			input.IfUnmodifiedSince = aws.Time(t)
		}
	}

	output, err := bc.S3Operations.HeadObject(ctx, input)

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	// Copy standard response headers
	if output.ContentType != nil {
		resp.Header.Set("Content-Type", *output.ContentType)
	}
	if output.ContentLength != nil {
		resp.Header.Set("Content-Length", fmt.Sprintf("%d", *output.ContentLength))
	}
	if output.ETag != nil {
		resp.Header.Set("ETag", *output.ETag)
	}
	if output.LastModified != nil {
		resp.Header.Set("Last-Modified", output.LastModified.UTC().Format(http.TimeFormat))
	}
	if output.ContentEncoding != nil {
		resp.Header.Set("Content-Encoding", *output.ContentEncoding)
	}
	if output.CacheControl != nil {
		resp.Header.Set("Cache-Control", *output.CacheControl)
	}
	if output.ContentDisposition != nil {
		resp.Header.Set("Content-Disposition", *output.ContentDisposition)
	}
	if output.ContentLanguage != nil {
		resp.Header.Set("Content-Language", *output.ContentLanguage)
	}
	if output.ExpiresString != nil {
		resp.Header.Set("Expires", *output.ExpiresString)
	}
	if output.VersionId != nil {
		resp.Header.Set("x-amz-version-id", *output.VersionId)
	}
	if output.ServerSideEncryption != "" {
		resp.Header.Set("x-amz-server-side-encryption", string(output.ServerSideEncryption))
	}
	if output.SSEKMSKeyId != nil {
		resp.Header.Set("x-amz-server-side-encryption-aws-kms-key-id", *output.SSEKMSKeyId)
	}
	if output.StorageClass != "" {
		resp.Header.Set("x-amz-storage-class", string(output.StorageClass))
	}

	// Set Accept-Ranges header
	resp.Header.Set("Accept-Ranges", "bytes")

	// Copy metadata headers - use lowercase to match S3 behavior
	// Using direct map access to avoid Go's http.Header canonicalization
	if output.Metadata != nil {
		for k, v := range output.Metadata {
			headerKey := "x-amz-meta-" + strings.ToLower(k)
			resp.Header[headerKey] = []string{v}
		}
	}

	return resp, nil
}

func readRequestBody(r *http.Request) ([]byte, error) {
	var b []byte
	defer func() {
		_ = r.Body.Close()
	}()
	if r.ContentLength >= 0 {
		b = make([]byte, int(r.ContentLength))
		n, err := io.ReadFull(r.Body, b)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		if int64(n) < r.ContentLength {
			return nil, fmt.Errorf("reading request body fell short at offset %d", n)
		}
	} else {
		var err error
		b, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
	}
	return b, nil
}

//nolint:gocyclo
func (e *Executor) executePutObject(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	// Check if this is a copy operation
	isCopyOperation := rc.Headers.Get("x-amz-copy-source") != ""
	if isCopyOperation {
		return e.executeCopyObject(ctx, bc, rc, decision, rc.Headers.Get("x-amz-copy-source"))
	}

	// Check if we can use aws-chunked streaming path (decode + re-encode with backend creds)
	if IsAwsChunkedEligible(rc, isCopyOperation) {
		return StreamingAwsChunkedPutObject(ctx, rc.Request, bc, rc, decision)
	}

	// Check if we can use regular streaming path
	if IsStreamingEligible(rc, isCopyOperation) {
		return StreamingPutObject(ctx, rc.Request, bc, rc, decision)
	}

	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	// Check for checksum trailers - if present, we must buffer the entire body to read trailers
	// This is required to extract checksum values from chunked encoding trailers
	trailerChecksumType := strings.ToLower(rc.Headers.Get("x-amz-trailer"))
	if trailerChecksumType != "" {
		// Checksum trailers require buffering the entire request body
		// Memory usage = Content-Length (or full body size for streamed requests)
		// For large uploads, consider recommending clients send checksums in headers instead
		b, err := readRequestBody(rc.Request)
		if err != nil {
			return nil, err
		}

		// Build PutObjectInput with buffered body
		input := &s3.PutObjectInput{
			Bucket: aws.String(bc.Bucket),
			Key:    aws.String(finalKey),
			Body:   bytes.NewReader(b),
		}

		// Pass through content-related headers exactly as received
		if contentType := rc.Headers.Get("Content-Type"); contentType != "" {
			input.ContentType = aws.String(contentType)
		}
		if contentEncoding := rc.Headers.Get("Content-Encoding"); contentEncoding != "" {
			input.ContentEncoding = aws.String(contentEncoding)
		}
		if contentMD5 := rc.Headers.Get("Content-MD5"); contentMD5 != "" {
			input.ContentMD5 = aws.String(contentMD5)
		}

		// Extract metadata from x-amz-meta-* headers
		metadata := make(map[string]string)
		for key, values := range rc.Headers {
			if len(values) > 0 && len(key) > 11 && strings.EqualFold(key[:11], "X-Amz-Meta-") {
				metaKey := key[11:] // Remove "X-Amz-Meta-" prefix
				metadata[metaKey] = values[0]
			}
		}
		if len(metadata) > 0 {
			input.Metadata = metadata
		}

		// Handle checksum trailers - extract from trailers
		trailer := rc.Request.Trailer
		switch trailerChecksumType {
		case "x-amz-checksum-sha256":
			h := trailer.Get("x-amz-checksum-sha256")
			if h == "" {
				return nil, fmt.Errorf("unexpected payload; x-amz-checksum-sha256 is not available")
			}
			input.ChecksumAlgorithm = types.ChecksumAlgorithmSha256
			input.ChecksumSHA256 = aws.String(h)
		case "x-amz-checksum-sha1":
			h := trailer.Get("x-amz-checksum-sha1")
			if h == "" {
				return nil, fmt.Errorf("unexpected payload; x-amz-checksum-sha1 is not available")
			}
			input.ChecksumAlgorithm = types.ChecksumAlgorithmSha1
			input.ChecksumSHA1 = aws.String(h)
		case "x-amz-checksum-crc32":
			h := trailer.Get("x-amz-checksum-crc32")
			if h == "" {
				return nil, fmt.Errorf("unexpected payload; x-amz-checksum-crc32 is not available")
			}
			input.ChecksumAlgorithm = types.ChecksumAlgorithmCrc32
			input.ChecksumCRC32 = aws.String(h)
		case "x-amz-checksum-crc32c":
			h := trailer.Get("x-amz-checksum-crc32c")
			if h == "" {
				return nil, fmt.Errorf("unexpected payload; x-amz-checksum-crc32c is not available")
			}
			input.ChecksumAlgorithm = types.ChecksumAlgorithmCrc32c
			input.ChecksumCRC32C = aws.String(h)
		case "x-amz-checksum-crc64nvme":
			h := trailer.Get("x-amz-checksum-crc64nvme")
			if h == "" {
				return nil, fmt.Errorf("unexpected payload; x-amz-checksum-crc64nvme is not available")
			}
			input.ChecksumAlgorithm = types.ChecksumAlgorithmCrc64nvme
			input.ChecksumCRC64NVME = aws.String(h)
		default:
			// do nothing
		}

		// Use the PutObject operation
		output, err := bc.S3Operations.PutObject(ctx, input)
		if err != nil {
			return awsErrorToHTTPResponse(err)
		}

		resp := &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}

		if output.ETag != nil {
			resp.Header.Set("ETag", *output.ETag)
		}
		if output.VersionId != nil {
			resp.Header.Set("x-amz-version-id", *output.VersionId)
		}

		return resp, nil
	}

	// No checksum trailers - buffer the body and wrap in bytes.Reader
	// AWS SDK v2 requires a seekable body to compute payload hash for signing
	b, err := readRequestBody(rc.Request)
	if err != nil {
		return nil, err
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
		Body:   bytes.NewReader(b),
	}

	// Pass through content-related headers exactly as received
	if contentType := rc.Headers.Get("Content-Type"); contentType != "" {
		input.ContentType = aws.String(contentType)
	}
	if contentEncoding := rc.Headers.Get("Content-Encoding"); contentEncoding != "" {
		input.ContentEncoding = aws.String(contentEncoding)
	}
	if contentMD5 := rc.Headers.Get("Content-MD5"); contentMD5 != "" {
		input.ContentMD5 = aws.String(contentMD5)
	}
	if contentLength := rc.Headers.Get("Content-Length"); contentLength != "" {
		if cl, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			input.ContentLength = aws.Int64(cl)
		}
	}

	// Extract metadata from x-amz-meta-* headers
	metadata := make(map[string]string)
	for key, values := range rc.Headers {
		if len(values) > 0 && len(key) > 11 && strings.EqualFold(key[:11], "X-Amz-Meta-") {
			metaKey := key[11:] // Remove "X-Amz-Meta-" prefix
			metadata[metaKey] = values[0]
		}
	}
	if len(metadata) > 0 {
		input.Metadata = metadata
	}

	// Use the PutObject operation
	output, err := bc.S3Operations.PutObject(ctx, input)

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}

	if output.ETag != nil {
		resp.Header.Set("ETag", *output.ETag)
	}

	return resp, nil
}

func (e *Executor) executeDeleteObject(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}
	_, err = bc.S3Operations.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	})

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "204 No Content",
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	return resp, nil
}

func (e *Executor) executeDeleteObjectTagging(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey := bc.Prefix + decision.RewrittenKey
	_, err := bc.S3Operations.DeleteObjectTagging(ctx, &s3.DeleteObjectTaggingInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	})

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "204 No Content",
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	return resp, nil
}

func (e *Executor) executeGetObjectTagging(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey := bc.Prefix + decision.RewrittenKey
	output, err := bc.S3Operations.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	})

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Build XML response
	type Tag struct {
		Key   string `xml:"Key"`
		Value string `xml:"Value"`
	}
	type Tagging struct {
		XMLName xml.Name `xml:"Tagging"`
		Xmlns   string   `xml:"xmlns,attr"`
		TagSet  struct {
			Tags []Tag `xml:"Tag"`
		} `xml:"TagSet"`
	}

	tagging := Tagging{
		Xmlns: "http://s3.amazonaws.com/doc/2006-03-01/",
	}
	for _, t := range output.TagSet {
		tagging.TagSet.Tags = append(tagging.TagSet.Tags, Tag{
			Key:   *t.Key,
			Value: *t.Value,
		})
	}

	xmlData, err := xml.Marshal(tagging)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tagging response: %w", err)
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(xml.Header + string(xmlData))),
	}
	resp.Header.Set("Content-Type", "application/xml")

	return resp, nil
}

func (e *Executor) executePutObjectTagging(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	// Parse the request body for tagging XML
	body, err := readRequestBody(rc.Request)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	type Tag struct {
		Key   string `xml:"Key"`
		Value string `xml:"Value"`
	}
	type TagSet struct {
		Tags []Tag `xml:"Tag"`
	}
	type Tagging struct {
		TagSet TagSet `xml:"TagSet"`
	}

	var tagging Tagging
	if err := xml.Unmarshal(body, &tagging); err != nil {
		return nil, fmt.Errorf("failed to parse tagging XML: %w", err)
	}

	// Convert to AWS SDK types
	var tagSet []types.Tag
	for _, t := range tagging.TagSet.Tags {
		tagSet = append(tagSet, types.Tag{
			Key:   aws.String(t.Key),
			Value: aws.String(t.Value),
		})
	}

	_, err = bc.S3Operations.PutObjectTagging(ctx, &s3.PutObjectTaggingInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
		Tagging: &types.Tagging{
			TagSet: tagSet,
		},
	})

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	return resp, nil
}

func (e *Executor) executePost(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	// Handle batch delete
	if rc.Operation == "DeleteObjects" {
		return e.executeDeleteObjects(ctx, bc, rc, decision)
	}
	// Handle multipart operations
	if rc.Operation == "CreateMultipartUpload" {
		return e.executeCreateMultipartUpload(ctx, bc, rc, decision)
	}
	if rc.Operation == "CompleteMultipartUpload" {
		return e.executeCompleteMultipartUpload(ctx, bc, rc, decision)
	}
	return nil, fmt.Errorf("POST operations not supported")
}

func (e *Executor) executeListObjectsV2(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	// For basic implementation, this is a placeholder
	// In Phase 2, we'll implement full routing logic with prefix optimization
	return nil, fmt.Errorf("ListObjectsV2 operations not yet fully implemented")
}

func (e *Executor) executeCreateMultipartUpload(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	input := &s3.CreateMultipartUploadInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	}

	// Pass through content-related headers
	if contentType := rc.Headers.Get("Content-Type"); contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	output, err := bc.S3Operations.CreateMultipartUpload(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Build response with virtual bucket and original key
	respBody := struct {
		XMLName  string `xml:"InitiateMultipartUploadResult"`
		Bucket   string `xml:"Bucket"`
		Key      string `xml:"Key"`
		UploadID string `xml:"UploadId"`
	}{
		Bucket:   rc.Bucket,
		Key:      rc.ObjectKey,
		UploadID: *output.UploadId,
	}

	xmlData, err := xml.MarshalIndent(respBody, "", " ")
	if err != nil {
		return nil, err
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...))),
	}
	resp.Header.Set("Content-Type", "application/xml")

	return resp, nil
}

func (e *Executor) executeUploadPart(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	defer func() {
		_ = rc.Body.Close()
	}()

	uploadID := ""
	partNumber := ""

	// Extract upload ID and part number from query parameters
	if rc.QueryParams != nil {
		if uploadIDArr, ok := rc.QueryParams["uploadId"]; ok && len(uploadIDArr) > 0 {
			uploadID = uploadIDArr[0]
		}
		if partNumArr, ok := rc.QueryParams["partNumber"]; ok && len(partNumArr) > 0 {
			partNumber = partNumArr[0]
		}
	}

	if uploadID == "" || partNumber == "" {
		return nil, fmt.Errorf("missing uploadId or partNumber query parameters")
	}

	// Check if we can use aws-chunked streaming path (decode + re-encode with backend creds)
	if IsAwsChunkedEligible(rc, false) {
		return StreamingAwsChunkedUploadPart(ctx, rc.Request, bc, rc, decision, uploadID, partNumber)
	}

	// Check if we can use regular streaming path
	if IsStreamingEligible(rc, false) {
		return StreamingUploadPart(ctx, rc.Request, bc, rc, decision, uploadID, partNumber)
	}

	// Fallback to buffered path using AWS SDK
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	var partNum int32
	_, _ = fmt.Sscanf(partNumber, "%d", &partNum)

	// Stream body directly (no buffering via io.ReadAll)
	input := &s3.UploadPartInput{
		Bucket:     aws.String(bc.Bucket),
		Key:        aws.String(finalKey),
		UploadId:   aws.String(uploadID),
		PartNumber: aws.Int32(partNum),
		Body:       rc.Body, // Stream body directly
	}

	// Pass through content-related headers
	if contentLength := rc.Headers.Get("Content-Length"); contentLength != "" {
		if cl, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			input.ContentLength = aws.Int64(cl)
		}
	}

	output, err := bc.S3Operations.UploadPart(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	if output.ETag != nil {
		resp.Header.Set("ETag", *output.ETag)
	}

	return resp, nil
}

func (e *Executor) executeCompleteMultipartUpload(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	defer func() {
		_ = rc.Body.Close()
	}()

	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	uploadID := ""

	// Extract upload ID from query parameters
	if rc.QueryParams != nil {
		if uploadIDArr, ok := rc.QueryParams["uploadId"]; ok && len(uploadIDArr) > 0 {
			uploadID = uploadIDArr[0]
		}
	}

	if uploadID == "" {
		return nil, fmt.Errorf("missing uploadId query parameter")
	}

	// Parse request body for part ETags - with bounded buffering (1MB limit)
	const maxXMLSize = 1 * 1024 * 1024 // 1MB limit for XML request
	limitedReader := io.LimitReader(rc.Body, maxXMLSize)
	b, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, err
	}

	// Parse CompleteMultipartUpload XML request
	type CompletePart struct {
		PartNumber int32  `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	}
	type CompleteRequest struct {
		Parts []CompletePart `xml:"Part"`
	}

	var completeReq CompleteRequest
	if err := xml.Unmarshal(b, &completeReq); err != nil {
		return nil, fmt.Errorf("failed to parse CompleteMultipartUpload request: %w", err)
	}

	// Build CompletedMultipartUpload for SDK
	completedParts := make([]types.CompletedPart, len(completeReq.Parts))
	for i, part := range completeReq.Parts {
		completedParts[i] = types.CompletedPart{
			PartNumber: aws.Int32(part.PartNumber),
			ETag:       aws.String(part.ETag),
		}
	}

	input := &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(bc.Bucket),
		Key:             aws.String(finalKey),
		UploadId:        aws.String(uploadID),
		MultipartUpload: &types.CompletedMultipartUpload{Parts: completedParts},
	}

	output, err := bc.S3Operations.CompleteMultipartUpload(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Build response with virtual bucket and original key
	respBody := struct {
		XMLName  string `xml:"CompleteMultipartUploadResult"`
		Location string `xml:"Location"`
		Bucket   string `xml:"Bucket"`
		Key      string `xml:"Key"`
		ETag     string `xml:"ETag"`
	}{
		Location: fmt.Sprintf("https://%s.s3.amazonaws.com/%s", rc.Bucket, rc.ObjectKey),
		Bucket:   rc.Bucket,
		Key:      rc.ObjectKey,
		ETag:     *output.ETag,
	}

	xmlData, err := xml.MarshalIndent(respBody, "", " ")
	if err != nil {
		return nil, err
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...))),
	}
	resp.Header.Set("Content-Type", "application/xml")

	return resp, nil
}

func (e *Executor) executeAbortMultipartUpload(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	uploadID := ""

	// Extract upload ID from query parameters
	if rc.QueryParams != nil {
		if uploadIDArr, ok := rc.QueryParams["uploadId"]; ok && len(uploadIDArr) > 0 {
			uploadID = uploadIDArr[0]
		}
	}

	if uploadID == "" {
		return nil, fmt.Errorf("missing uploadId query parameter")
	}

	_, err = bc.S3Operations.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(bc.Bucket),
		Key:      aws.String(finalKey),
		UploadId: aws.String(uploadID),
	})

	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "204 No Content",
		StatusCode: http.StatusNoContent,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	return resp, nil
}

func (e *Executor) executeListParts(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}
	uploadID := ""
	maxParts := int32(1000)

	// Extract upload ID from query parameters
	if rc.QueryParams != nil {
		if uploadIDArr, ok := rc.QueryParams["uploadId"]; ok && len(uploadIDArr) > 0 {
			uploadID = uploadIDArr[0]
		}
		if maxPartsArr, ok := rc.QueryParams["max-parts"]; ok && len(maxPartsArr) > 0 {
			var mp int
			_, _ = fmt.Sscanf(maxPartsArr[0], "%d", &mp)
			if mp > 0 && mp <= 1000 {
				maxParts = int32(mp)
			}
		}
	}

	if uploadID == "" {
		return nil, fmt.Errorf("missing uploadId query parameter")
	}

	input := &s3.ListPartsInput{
		Bucket:   aws.String(bc.Bucket),
		Key:      aws.String(finalKey),
		UploadId: aws.String(uploadID),
		MaxParts: aws.Int32(maxParts),
	}

	output, err := bc.S3Operations.ListParts(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Build response
	parts := make([]struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
		Size       int64  `xml:"Size"`
	}, len(output.Parts))

	for i, p := range output.Parts {
		parts[i].PartNumber = int(*p.PartNumber)
		parts[i].ETag = *p.ETag
		parts[i].Size = *p.Size
	}

	respBody := struct {
		XMLName     string `xml:"ListPartsResult"`
		Bucket      string `xml:"Bucket"`
		Key         string `xml:"Key"`
		UploadID    string `xml:"UploadId"`
		MaxParts    int    `xml:"MaxParts"`
		IsTruncated bool   `xml:"IsTruncated"`
		Parts       []struct {
			PartNumber int    `xml:"PartNumber"`
			ETag       string `xml:"ETag"`
			Size       int64  `xml:"Size"`
		} `xml:"Part"`
	}{
		Bucket:      rc.Bucket,
		Key:         rc.ObjectKey,
		UploadID:    uploadID,
		MaxParts:    int(maxParts),
		IsTruncated: output.IsTruncated != nil && *output.IsTruncated,
		Parts:       parts,
	}

	xmlData, err := xml.MarshalIndent(respBody, "", " ")
	if err != nil {
		return nil, err
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...))),
	}
	resp.Header.Set("Content-Type", "application/xml")

	return resp, nil
}

func (e *Executor) executeDeleteObjects(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	defer func() {
		_ = rc.Body.Close()
	}()

	// Parse the delete request body
	bodyBytes, err := io.ReadAll(rc.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	// Parse XML to get list of keys to delete
	type DeleteRequest struct {
		Object []struct {
			Key string `xml:"Key"`
		} `xml:"Object"`
	}

	var deleteReq DeleteRequest
	if err := xml.Unmarshal(bodyBytes, &deleteReq); err != nil {
		return nil, fmt.Errorf("failed to parse delete request: %w", err)
	}

	if len(deleteReq.Object) == 0 {
		// Return empty delete response
		resp := &http.Response{
			Status:     "200 OK",
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`<?xml version="1.0" encoding="UTF-8"?><DeleteResult></DeleteResult>`)),
		}
		resp.Header.Set("Content-Type", "application/xml")
		return resp, nil
	}

	// If no matcher available, fall back to single-backend deletion (backward compatibility)
	if e.matcher == nil {
		return e.executeDeleteObjectsSingleBackend(ctx, bc, rc, decision, deleteReq)
	}

	// Route each key individually and group by backend
	type keyMapping struct {
		virtualKey   string // Original key from client
		rewrittenKey string // After routing/rewriting
		backendID    string // Target backend
		physicalKey  string // With backend prefix
	}

	keysByBackend := make(map[string][]keyMapping)

	for _, obj := range deleteReq.Object {
		// Route this specific key
		keyDecision, err := e.matcher.Match(ctx, rc.Bucket, obj.Key, http.MethodDelete, rc.Headers)
		if err != nil {
			// If routing fails, use the original decision as fallback
			keyDecision = decision
		}

		backendClient, err := e.backendMgr.GetClient(keyDecision.Backend.ID)
		if err != nil {
			// Skip keys for unavailable backends or use original decision
			keyDecision = decision
			backendClient = bc
		}

		mapping := keyMapping{
			virtualKey:   obj.Key,
			rewrittenKey: keyDecision.RewrittenKey,
			backendID:    keyDecision.Backend.ID,
			physicalKey:  backendClient.Prefix + keyDecision.RewrittenKey,
		}
		keysByBackend[keyDecision.Backend.ID] = append(keysByBackend[keyDecision.Backend.ID], mapping)
	}

	// Execute deletes per-backend and collect results
	type DeletedObject struct {
		Key string `xml:"Key"`
	}

	allDeleted := []DeletedObject{}

	for backendID, keys := range keysByBackend {
		backendClient, err := e.backendMgr.GetClient(backendID)
		if err != nil {
			continue
		}

		// Build delete request for this backend
		var objectIds []types.ObjectIdentifier
		for _, mapping := range keys {
			objectIds = append(objectIds, types.ObjectIdentifier{
				Key: aws.String(mapping.physicalKey),
			})
		}

		input := &s3.DeleteObjectsInput{
			Bucket: aws.String(backendClient.Bucket),
			Delete: &types.Delete{
				Objects: objectIds,
			},
		}

		output, err := backendClient.S3Operations.DeleteObjects(ctx, input)
		if err != nil {
			// Log but continue with other backends
			continue
		}

		// Map deleted keys back to virtual keys
		// Build a map from physical key to virtual key for this backend
		physicalToVirtual := make(map[string]string)
		for _, mapping := range keys {
			physicalToVirtual[mapping.physicalKey] = mapping.virtualKey
		}

		for _, d := range output.Deleted {
			virtualKey := physicalToVirtual[*d.Key]
			if virtualKey == "" {
				// Shouldn't happen, but use the physical key as fallback
				virtualKey = *d.Key
			}
			allDeleted = append(allDeleted, DeletedObject{Key: virtualKey})
		}
	}

	// Build response with deleted keys (virtual keys only)
	type DeleteResultResponse struct {
		XMLName string          `xml:"DeleteResult"`
		Deleted []DeletedObject `xml:"Deleted"`
	}

	respBody := DeleteResultResponse{
		Deleted: allDeleted,
	}

	xmlData, err := xml.MarshalIndent(respBody, "", " ")
	if err != nil {
		return nil, err
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...))),
	}
	resp.Header.Set("Content-Type", "application/xml")

	return resp, nil
}

// executeDeleteObjectsSingleBackend is the fallback for single-backend deletion
func (e *Executor) executeDeleteObjectsSingleBackend(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision, deleteReq struct {
	Object []struct {
		Key string `xml:"Key"`
	} `xml:"Object"`
}) (*http.Response, error) {
	// Build DeleteObjects input
	var objectIds []types.ObjectIdentifier
	for _, obj := range deleteReq.Object {
		finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
		if err != nil {
			return nil, fmt.Errorf("malformed key: %w", err)
		}
		// For backward compatibility: if RewrittenKey is empty, just apply prefix to obj.Key
		if decision.RewrittenKey == "" {
			finalKey = bc.Prefix + obj.Key
		}
		objectIds = append(objectIds, types.ObjectIdentifier{
			Key: aws.String(finalKey),
		})
	}

	// Call DeleteObjects
	input := &s3.DeleteObjectsInput{
		Bucket: aws.String(bc.Bucket),
		Delete: &types.Delete{
			Objects: objectIds,
		},
	}

	output, err := bc.S3Operations.DeleteObjects(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Build response with deleted keys
	type DeletedObject struct {
		Key string `xml:"Key"`
	}

	type DeleteResultResponse struct {
		XMLName string          `xml:"DeleteResult"`
		Deleted []DeletedObject `xml:"Deleted"`
	}

	deleted := make([]DeletedObject, len(output.Deleted))
	for i, d := range output.Deleted {
		// Strip backend prefix from response key
		key := *d.Key
		key = strings.TrimPrefix(key, bc.Prefix)
		deleted[i].Key = key
	}

	respBody := DeleteResultResponse{
		Deleted: deleted,
	}

	xmlData, err := xml.MarshalIndent(respBody, "", " ")
	if err != nil {
		return nil, err
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...))),
	}
	resp.Header.Set("Content-Type", "application/xml")

	return resp, nil
}

//nolint:gocyclo
func (e *Executor) executeCopyObject(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision, copySource string) (*http.Response, error) {
	finalDestKey := bc.Prefix + decision.RewrittenKey

	// Parse the copy source (format: /bucket/key or bucket/key)
	sourceParts := strings.TrimPrefix(copySource, "/")
	parts := strings.SplitN(sourceParts, "/", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid x-amz-copy-source format: %s", copySource)
	}

	sourceBucket := parts[0]
	sourceKey := parts[1]

	// Route the source key if matcher is available
	var finalCopySource string
	if e.matcher != nil {
		// Route the source key independently
		sourceDecision, err := e.matcher.Match(ctx, sourceBucket, sourceKey, http.MethodGet, rc.Headers)
		if err != nil {
			// If routing fails, use original source (may fail at backend)
			finalCopySource = copySource
		} else {
			// Get the backend for the source
			sourceBCClient, err := e.backendMgr.GetClient(sourceDecision.Backend.ID)
			if err != nil {
				// Backend not found, use original source
				finalCopySource = copySource
			} else {
				// Check if source and destination are on the same backend
				if sourceDecision.Backend.ID != decision.Backend.ID {
					// Cross-backend copy not supported; return error
					return awsErrorToHTTPResponse(fmt.Errorf(
						"copy source and destination must be in the same backend (source: %s, dest: %s)",
						sourceDecision.Backend.ID, decision.Backend.ID))
				}

				// Rewrite source to physical key
				finalSourceKey := sourceBCClient.Prefix + sourceDecision.RewrittenKey
				finalCopySource = fmt.Sprintf("/%s/%s", sourceBCClient.Bucket, finalSourceKey)
			}
		}
	} else {
		// No matcher; use original copy source
		finalCopySource = copySource
	}

	// Build CopyObject input with rewritten source
	input := &s3.CopyObjectInput{
		Bucket:     aws.String(bc.Bucket),
		CopySource: aws.String(finalCopySource),
		Key:        aws.String(finalDestKey),
	}

	// Pass through optional headers for copy
	if directives := rc.Headers.Get("x-amz-metadata-directive"); directives != "" {
		if strings.EqualFold(directives, "COPY") {
			input.MetadataDirective = types.MetadataDirectiveCopy
		} else if strings.EqualFold(directives, "REPLACE") {
			input.MetadataDirective = types.MetadataDirectiveReplace
		}
	}

	if tagDirective := rc.Headers.Get("x-amz-tagging-directive"); tagDirective != "" {
		if strings.EqualFold(tagDirective, "COPY") {
			input.TaggingDirective = types.TaggingDirectiveCopy
		} else if strings.EqualFold(tagDirective, "REPLACE") {
			input.TaggingDirective = types.TaggingDirectiveReplace
		}
	}

	// Extract metadata from x-amz-meta-* headers
	metadata := make(map[string]string)
	for key, values := range rc.Headers {
		if len(values) > 0 && len(key) > 11 && strings.EqualFold(key[:11], "X-Amz-Meta-") {
			metaKey := key[11:] // Remove "X-Amz-Meta-" prefix
			metadata[metaKey] = values[0]
		}
	}
	if len(metadata) > 0 {
		input.Metadata = metadata
	}

	// Pass through content type if replacing metadata
	if contentType := rc.Headers.Get("Content-Type"); contentType != "" {
		input.ContentType = aws.String(contentType)
	}

	output, err := bc.S3Operations.CopyObject(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Build response with virtual key (strip backend prefix and rewrite)
	respBody := struct {
		XMLName      string `xml:"CopyObjectResult"`
		ETag         string `xml:"ETag"`
		LastModified string `xml:"LastModified"`
	}{
		ETag: *output.CopyObjectResult.ETag,
	}

	if output.CopyObjectResult.LastModified != nil {
		respBody.LastModified = output.CopyObjectResult.LastModified.UTC().Format(time.RFC3339)
	}

	xmlData, err := xml.MarshalIndent(respBody, "", " ")
	if err != nil {
		return nil, err
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...))),
	}
	resp.Header.Set("Content-Type", "application/xml")

	return resp, nil
}

func (e *Executor) executeGetObjectAcl(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	// Build GetObjectAcl input
	input := &s3.GetObjectAclInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	}

	output, err := bc.S3Operations.GetObjectAcl(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	// Build response
	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
	}

	resp.Header.Set("Content-Type", "application/xml")

	// Build ACL XML response with proper namespace
	type Grantee struct {
		XMLName     xml.Name `xml:"Grantee"`
		XMLNS       string   `xml:"xmlns:xsi,attr"`
		Type        string   `xml:"xsi:type,attr"`
		ID          string   `xml:"ID,omitempty"`
		DisplayName string   `xml:"DisplayName,omitempty"`
	}

	type Grant struct {
		Grantee    Grantee `xml:"Grantee"`
		Permission string  `xml:"Permission"`
	}

	type ACLResponse struct {
		XMLName xml.Name `xml:"AccessControlPolicy"`
		XMLNS   string   `xml:"xmlns,attr"`
		Owner   struct {
			ID          string `xml:"ID"`
			DisplayName string `xml:"DisplayName"`
		} `xml:"Owner"`
		AccessControlList struct {
			Grants []Grant `xml:"Grant"`
		} `xml:"AccessControlList"`
	}

	aclResp := ACLResponse{
		XMLNS: "http://s3.amazonaws.com/doc/2006-03-01/",
	}

	if output.Owner != nil {
		if output.Owner.ID != nil {
			aclResp.Owner.ID = *output.Owner.ID
		}
		if output.Owner.DisplayName != nil {
			aclResp.Owner.DisplayName = *output.Owner.DisplayName
		}
	}

	// Convert grants
	for _, g := range output.Grants {
		grant := Grant{}
		if g.Grantee != nil {
			grant.Grantee = Grantee{
				XMLNS: "http://www.w3.org/2001/XMLSchema-instance",
				Type:  string(g.Grantee.Type),
			}
			if g.Grantee.ID != nil {
				grant.Grantee.ID = *g.Grantee.ID
			}
			if g.Grantee.DisplayName != nil {
				grant.Grantee.DisplayName = *g.Grantee.DisplayName
			}
		}
		grant.Permission = string(g.Permission)
		aclResp.AccessControlList.Grants = append(aclResp.AccessControlList.Grants, grant)
	}

	xmlData, err := xml.MarshalIndent(aclResp, "", " ")
	if err != nil {
		return nil, err
	}

	resp.Body = io.NopCloser(bytes.NewReader(append([]byte(xml.Header), xmlData...)))
	return resp, nil
}

func (e *Executor) executePutObjectAcl(ctx context.Context, bc *backend.BackendClient, rc *RequestContext, decision *routing.Decision) (*http.Response, error) {
	finalKey, err := conservativePathUnescape(bc.Prefix + decision.RewrittenKey)
	if err != nil {
		return nil, fmt.Errorf("malformed key: %w", err)
	}

	// Build PutObjectAcl input
	input := &s3.PutObjectAclInput{
		Bucket: aws.String(bc.Bucket),
		Key:    aws.String(finalKey),
	}

	// Check for canned ACL (x-amz-acl header)
	if aclHeader := rc.Headers.Get("x-amz-acl"); aclHeader != "" {
		input.ACL = types.ObjectCannedACL(aclHeader)
	}

	_, err = bc.S3Operations.PutObjectAcl(ctx, input)
	if err != nil {
		return awsErrorToHTTPResponse(err)
	}

	resp := &http.Response{
		Status:     "200 OK",
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}

	return resp, nil
}
