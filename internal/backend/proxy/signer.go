package proxy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/backend/cred"
)

// S3Signer implements AWS SigV4 signing for S3 requests.
// It signs requests using a pre-computed payload hash (from the client).
type S3Signer struct {
	credentials aws.Credentials
	region      string
}

// NewS3Signer creates a new S3 request signer.
func NewS3Signer(credentials aws.Credentials, region string) *S3Signer {
	return &S3Signer{
		credentials: credentials,
		region:      region,
	}
}

// SignRequest signs an HTTP request using AWS SigV4.
// The payload hash must be provided in the x-amz-content-sha256 header
// or will be computed if not present.
func (s *S3Signer) SignRequest(req *http.Request, payloadHash string) error {
	// Set timestamp if not already set
	if req.Header.Get("x-amz-date") == "" {
		req.Header.Set("x-amz-date", time.Now().UTC().Format("20060102T150405Z"))
	}

	// Set or validate payload hash
	if payloadHash == "" {
		payloadHash = req.Header.Get("x-amz-content-sha256")
	}
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-security-token", s.credentials.SessionToken)

	// Create canonical request
	canonicalRequest := s.buildCanonicalRequest(req, payloadHash)

	// Create string to sign
	amzDate := req.Header.Get("x-amz-date")
	datestamp := amzDate[:8] // YYYYMMDD
	credentialScope := fmt.Sprintf("%s/%s/s3/aws4_request", datestamp, s.region)

	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%x", amzDate, credentialScope, sha256.Sum256(canonicalRequest))

	// Derive signing key
	var kDate []byte
	{
		b := make([]byte, 4+len(s.credentials.SecretAccessKey))
		copy(b[:4], "AWS4")
		copy(b[4:], s.credentials.SecretAccessKey)
		kDate = hmacSHA256(b, datestamp)
	}
	kRegion := hmacSHA256(kDate, s.region)
	kService := hmacSHA256(kRegion, "s3")
	kSigning := hmacSHA256(kService, "aws4_request")

	// Calculate signature
	signature := hmacSHA256(kSigning, stringToSign)

	// Build authorization header
	signedHeaders := s.getSignedHeaders(req)
	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%x",
		s.credentials.AccessKeyID, credentialScope, signedHeaders, signature)

	req.Header.Set("Authorization", authHeader)

	return nil
}

// buildCanonicalRequest constructs the canonical request string for SigV4.
func (s *S3Signer) buildCanonicalRequest(req *http.Request, payloadHash string) []byte {
	var buf bytes.Buffer

	// Method
	buf.WriteString(req.Method)
	buf.WriteString("\n")

	// CanonicalURI
	uri := req.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	buf.WriteString(uri)
	buf.WriteString("\n")

	// CanonicalQueryString
	if req.URL.RawQuery != "" {
		buf.WriteString(s.buildCanonicalQueryString(req.URL.RawQuery))
	}
	buf.WriteString("\n")

	// CanonicalHeaders
	buf.WriteString(s.buildCanonicalHeaders(req))
	buf.WriteString("\n")

	// SignedHeaders
	buf.WriteString(s.getSignedHeaders(req))
	buf.WriteString("\n")

	// Payload hash
	buf.WriteString(payloadHash)

	return buf.Bytes()
}

// buildCanonicalHeaders constructs the canonical headers string.
func (s *S3Signer) buildCanonicalHeaders(req *http.Request) string {
	var headers []string

	// Add the host header from req.Host (Go stores Host separately, not in req.Header)
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if host != "" {
		headers = append(headers, "host:"+host)
	}

	for name, values := range req.Header {
		lowerName := strings.ToLower(name)
		// Only include headers that should be signed
		if !s.shouldSignHeader(lowerName) {
			continue
		}
		for _, value := range values {
			// Trim whitespace
			value = strings.TrimSpace(value)
			headers = append(headers, lowerName+":"+value)
		}
	}

	// Sort headers
	sort.Strings(headers)

	var buf bytes.Buffer
	for _, header := range headers {
		buf.WriteString(header)
		buf.WriteString("\n")
	}

	return buf.String()
}

// shouldSignHeader determines if a header should be included in the signature.
func (s *S3Signer) shouldSignHeader(name string) bool {
	// Skip authorization header and host (already added above)
	if name == "authorization" || name == "host" {
		return false
	}
	// Skip headers whose values are regenerated
	if name == "x-amz-signature" {
		return false
	}

	// Always sign x-amz-* headers
	if strings.HasPrefix(name, "x-amz-") {
		return true
	}

	// Sign content-* headers
	if strings.HasPrefix(name, "content-") {
		return true
	}

	return false
}

// getSignedHeaders returns the list of signed header names.
func (s *S3Signer) getSignedHeaders(req *http.Request) string {
	var headers []string

	// Always include host (Go stores Host separately in req.Host, not in req.Header)
	host := req.Host
	if host == "" && req.URL != nil {
		host = req.URL.Host
	}
	if host != "" {
		headers = append(headers, "host")
	}

	for name := range req.Header {
		lowerName := strings.ToLower(name)
		// Skip authorization and host (already added above)
		if lowerName == "authorization" || lowerName == "host" {
			continue
		}
		if s.shouldSignHeader(lowerName) {
			headers = append(headers, lowerName)
		}
	}

	sort.Strings(headers)
	return strings.Join(headers, ";")
}

// buildCanonicalQueryString constructs the canonical query string.
func (s *S3Signer) buildCanonicalQueryString(rawQuery string) string {
	// Parse and canonicalize query string
	params := make(map[string]string)
	pairs := strings.Split(rawQuery, "&")
	for _, pair := range pairs {
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, "=", 2)
		if len(parts) == 2 {
			params[parts[0]] = parts[1]
		} else {
			params[parts[0]] = ""
		}
	}

	// Sort by key
	var keys []string
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	for i, k := range keys {
		if i > 0 {
			buf.WriteString("&")
		}
		buf.WriteString(k)
		if params[k] != "" {
			buf.WriteString("=")
			buf.WriteString(params[k])
		}
	}

	return buf.String()
}

// SignStreamingRequest is a helper to sign a request for streaming operations.
// It extracts credentials and region from the backend client and signs the request.
func SignStreamingRequest(ctx context.Context, req *http.Request, bc *backend.BackendClient, payloadHash string) error {
	// Get credentials from the backend client
	creds, err := cred.ToAWSCredentialsProvider(bc.CredsProvider).Retrieve(ctx)
	if err != nil {
		return fmt.Errorf("failed to retrieve credentials: %w", err)
	}
	signer := NewS3Signer(creds, bc.Region)
	return signer.SignRequest(req, payloadHash)
}

// SignStreamingRequestWithCredentials signs a request and returns the credentials used.
// This is needed for streaming uploads where we need the same credentials for both
// request signing and chunk re-encoding.
func SignStreamingRequestWithCredentials(ctx context.Context, req *http.Request, bc *backend.BackendClient, payloadHash string) (aws.Credentials, error) {
	// Get credentials from the backend client
	creds, err := cred.ToAWSCredentialsProvider(bc.CredsProvider).Retrieve(ctx)
	if err != nil {
		return aws.Credentials{}, fmt.Errorf("failed to retrieve credentials: %w", err)
	}
	signer := NewS3Signer(creds, bc.Region)
	if err := signer.SignRequest(req, payloadHash); err != nil {
		return aws.Credentials{}, err
	}
	return creds, nil
}

// Helper functions

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}
