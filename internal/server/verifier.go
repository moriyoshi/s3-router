package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/signer/v4"

	"github.com/moriyoshi/s3-router/internal/auth"
	"github.com/moriyoshi/s3-router/internal/config"
)

type Verifier struct {
	credStore     auth.CredentialsStore
	timeOffset    time.Duration // Allow clock skew
	defaultRegion string
}

type AuthContext struct {
	Principal       string
	AccessKeyID     string
	SignatureType   string // "header" or "query"
	IsAuthenticated bool
	Timestamp       time.Time
}

// AuthError represents an authentication failure
type AuthError struct {
	Code    string // "InvalidAuthHeader", "SignatureDoesNotMatch", etc.
	Message string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func NewVerifier(cfg *config.Config, credStore auth.CredentialsStore) *Verifier {
	timeOffset := 15 * time.Minute // AWS default tolerance
	if cfg.Auth != nil && cfg.Auth.ClockSkewLeeway != 0 {
		timeOffset = cfg.Auth.ClockSkewLeeway
	}
	defaultRegion := "us-east-1"
	if cfg.Auth != nil && cfg.Auth.DefaultRegion != "" {
		defaultRegion = cfg.Auth.DefaultRegion
	}
	return &Verifier{
		credStore:     credStore,
		timeOffset:    timeOffset,
		defaultRegion: defaultRegion,
	}
}

// VerifyRequest checks SigV4 signature on incoming request and validates it
func (v *Verifier) VerifyRequest(r *http.Request) (*AuthContext, error) {
	auth := &AuthContext{
		Timestamp: time.Now(),
	}

	// Check for Authorization header (SigV4 header style)
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		return v.verifyHeaderSignature(r, authHeader)
	}

	// Check for query-based SigV4 (presigned URLs)
	if r.URL.Query().Get("X-Amz-Signature") != "" {
		return v.verifyQuerySignature(r)
	}

	// No authentication provided
	return auth, &AuthError{
		Code:    "MissingAuthenticationToken",
		Message: "Authorization header or X-Amz-Signature query parameter required",
	}
}

// verifyHeaderSignature verifies Authorization header-based SigV4
func (v *Verifier) verifyHeaderSignature(r *http.Request, authHeader string) (*AuthContext, error) {
	auth := &AuthContext{
		Timestamp:     time.Now(),
		SignatureType: "header",
	}

	// Parse Authorization header
	accessKeyID, signedHeaders, providedSig, err := v.parseAuthHeader(authHeader)
	if err != nil {
		return auth, &AuthError{
			Code:    "InvalidAuthHeader",
			Message: err.Error(),
		}
	}

	auth.AccessKeyID = accessKeyID
	auth.Principal = accessKeyID

	// Get secret key from credential store
	secretKey, err := v.credStore.GetSecret(accessKeyID)
	if err != nil {
		return auth, &AuthError{
			Code:    "InvalidAccessKeyId",
			Message: fmt.Sprintf("Access key '%s' not found", accessKeyID),
		}
	}

	// Extract timestamp from x-amz-date header
	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		return auth, &AuthError{
			Code:    "MissingDateHeader",
			Message: "X-Amz-Date header is required",
		}
	}

	// Parse and validate timestamp
	timestamp, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return auth, &AuthError{
			Code:    "InvalidDateHeader",
			Message: fmt.Sprintf("Invalid X-Amz-Date format: %s", amzDate),
		}
	}

	// Check for replay attacks (timestamp too old/new)
	now := time.Now()
	if timestamp.Before(now.Add(-v.timeOffset)) || timestamp.After(now.Add(v.timeOffset)) {
		return auth, &AuthError{
			Code:    "RequestTimeTooSkewed",
			Message: "Request timestamp expired or too far in future",
		}
	}

	// Extract region from credential scope in Authorization header
	region := v.defaultRegion
	if strings.Contains(authHeader, "Credential=") {
		credPart := strings.Split(authHeader, "Credential=")[1]
		credPart = strings.Split(credPart, ",")[0]
		credParts := strings.Split(credPart, "/")
		if len(credParts) >= 4 {
			region = credParts[2]
		}
	}

	// Compute expected signature
	expectedSig, err := v.computeSignatureWithRegion(r, secretKey, accessKeyID, amzDate, signedHeaders, region)
	if err != nil {
		return auth, &AuthError{
			Code:    "InternalError",
			Message: fmt.Sprintf("Failed to compute signature: %v", err),
		}
	}

	// Compare signatures (constant-time comparison to prevent timing attacks)
	if !hmac.Equal([]byte(providedSig), []byte(expectedSig)) {
		return auth, &AuthError{
			Code:    "SignatureDoesNotMatch",
			Message: "The authorization signature provided is invalid",
		}
	}

	auth.IsAuthenticated = true
	return auth, nil
}

// verifyQuerySignature verifies query-string-based SigV4 (presigned URLs)
func (v *Verifier) verifyQuerySignature(r *http.Request) (*AuthContext, error) {
	auth := &AuthContext{
		Timestamp:     time.Now(),
		SignatureType: "query",
	}

	// Extract credential scope: AKIAIOSFODNN7EXAMPLE/20230101/us-east-1/s3/aws4_request
	credentialScope := r.URL.Query().Get("X-Amz-Credential")
	if credentialScope == "" {
		return auth, &AuthError{
			Code:    "InvalidQueryStringParameter",
			Message: "X-Amz-Credential parameter is required",
		}
	}

	parts := strings.Split(credentialScope, "/")
	if len(parts) < 2 {
		return auth, &AuthError{
			Code:    "InvalidQueryStringParameter",
			Message: "Invalid X-Amz-Credential format",
		}
	}

	accessKeyID := parts[0]
	auth.AccessKeyID = accessKeyID
	auth.Principal = accessKeyID

	// Extract region from credential scope
	region := v.defaultRegion
	if len(parts) >= 4 {
		region = parts[2]
	}

	// Get secret key from credential store
	secretKey, err := v.credStore.GetSecret(accessKeyID)
	if err != nil {
		return auth, &AuthError{
			Code:    "InvalidAccessKeyId",
			Message: fmt.Sprintf("Access key '%s' not found", accessKeyID),
		}
	}

	// Extract and validate timestamp
	amzDate := r.URL.Query().Get("X-Amz-Date")
	if amzDate == "" {
		return auth, &AuthError{
			Code:    "InvalidQueryStringParameter",
			Message: "X-Amz-Date query parameter is required",
		}
	}

	timestamp, err := time.Parse("20060102T150405Z", amzDate)
	if err != nil {
		return auth, &AuthError{
			Code:    "InvalidQueryStringParameter",
			Message: fmt.Sprintf("Invalid X-Amz-Date format: %s", amzDate),
		}
	}

	// Check timestamp validity
	now := time.Now()
	if timestamp.Before(now.Add(-v.timeOffset)) || timestamp.After(now.Add(v.timeOffset)) {
		return auth, &AuthError{
			Code:    "RequestTimeTooSkewed",
			Message: "Request timestamp expired or too far in future",
		}
	}

	// For query signatures, build a modified request without the signature
	signedHeaders := r.URL.Query().Get("X-Amz-SignedHeaders")
	if signedHeaders == "" {
		signedHeaders = "host"
	}

	// Compute expected signature
	expectedSig, err := v.computeSignatureWithRegion(r, secretKey, accessKeyID, amzDate, signedHeaders, region)
	if err != nil {
		return auth, &AuthError{
			Code:    "InternalError",
			Message: fmt.Sprintf("Failed to compute signature: %v", err),
		}
	}

	// Get provided signature
	providedSig := r.URL.Query().Get("X-Amz-Signature")

	// Compare signatures
	if !hmac.Equal([]byte(providedSig), []byte(expectedSig)) {
		return auth, &AuthError{
			Code:    "SignatureDoesNotMatch",
			Message: "The authorization signature provided is invalid",
		}
	}

	auth.IsAuthenticated = true
	return auth, nil
}

// parseAuthHeader parses the Authorization header and extracts components
func (v *Verifier) parseAuthHeader(authHeader string) (accessKeyID, signedHeaders, signature string, err error) {
	// Format: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20230101/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=xyz

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || parts[0] != "AWS4-HMAC-SHA256" {
		return "", "", "", fmt.Errorf("invalid authorization scheme")
	}

	params := parts[1]
	for _, param := range strings.Split(params, ",") {
		param = strings.TrimSpace(param)
		if strings.HasPrefix(param, "Credential=") {
			cred := strings.TrimPrefix(param, "Credential=")
			credParts := strings.Split(cred, "/")
			if len(credParts) > 0 {
				accessKeyID = credParts[0]
			}
		} else if strings.HasPrefix(param, "SignedHeaders=") {
			signedHeaders = strings.TrimPrefix(param, "SignedHeaders=")
		} else if strings.HasPrefix(param, "Signature=") {
			signature = strings.TrimPrefix(param, "Signature=")
		}
	}

	if accessKeyID == "" || signedHeaders == "" || signature == "" {
		return "", "", "", fmt.Errorf("missing required authorization components")
	}

	return accessKeyID, signedHeaders, signature, nil
}

// computeSignatureWithRegion computes the SigV4 signature for a request with a specified region
func (v *Verifier) computeSignatureWithRegion(r *http.Request, secretKey, accessKeyID, amzDate, signedHeaders, region string) (string, error) {
	// Compute payload hash
	var payloadHash string
	if r.Header.Get("X-Amz-Content-Sha256") != "" {
		payloadHash = r.Header.Get("X-Amz-Content-Sha256")
	} else {
		// Read body for hashing (preserve it for later use)
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read request body: %w", err)
		}
		r.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))

		hash := sha256.Sum256(bodyBytes)
		payloadHash = hex.EncodeToString(hash[:])
	}

	// Add payload hash to headers for signature computation
	r.Header.Set("X-Amz-Content-Sha256", payloadHash)

	// Create canonical request
	canonical := v.createCanonicalRequest(r, signedHeaders, payloadHash)

	// Create string to sign
	dateStamp := amzDate[:8] // YYYYMMDD
	service := "s3"

	canonicalHash := sha256.Sum256([]byte(canonical))
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s/%s/%s/aws4_request\n%s",
		amzDate,
		dateStamp,
		region,
		service,
		hex.EncodeToString(canonicalHash[:]))

	// Compute signature
	signingKey := GetSigningKey(secretKey, dateStamp, region, service)
	h := hmac.New(sha256.New, signingKey)
	h.Write([]byte(stringToSign))
	signature := hex.EncodeToString(h.Sum(nil))

	return signature, nil
}

// createCanonicalRequest creates the canonical request for SigV4
func (v *Verifier) createCanonicalRequest(r *http.Request, signedHeaders, payloadHash string) string {
	// Method
	canonical := r.Method + "\n"

	// Canonical URI - use EscapedPath() for proper percent-encoding
	uri := r.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	canonical += uri + "\n"

	// Canonical query string (excluding X-Amz-Signature)
	// Use RFC3986 encoding where spaces are %20, not +
	query := r.URL.Query()
	query.Del("X-Amz-Signature")
	queryStr := v.encodeQueryStringRFC3986(query)
	canonical += queryStr + "\n"

	// Canonical headers
	headersList := strings.Split(signedHeaders, ";")
	for _, header := range headersList {
		header = strings.ToLower(strings.TrimSpace(header))
		value := r.Header.Get(header)
		if value == "" && header == "host" {
			value = r.Host
			if value == "" {
				value = r.Header.Get("Host")
			}
		}
		canonical += fmt.Sprintf("%s:%s\n", header, strings.TrimSpace(value))
	}

	canonical += "\n"
	canonical += signedHeaders + "\n"
	canonical += payloadHash

	return canonical
}

// encodeQueryStringRFC3986 encodes query parameters using RFC3986 (space as %20, not +)
// For multi-valued parameters, all values are included in sorted order
func (v *Verifier) encodeQueryStringRFC3986(query url.Values) string {
	if len(query) == 0 {
		return ""
	}

	// Sort keys for consistent ordering
	keys := make([]string, 0, len(query))
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build query string with RFC3986 encoding
	// For multi-valued parameters, create key=value pairs for each value, sorted by value
	var pairs []string
	for _, k := range keys {
		encodedKey := v.encodeRFC3986(k)
		// Sort values for this key to ensure consistent canonical form
		values := make([]string, len(query[k]))
		copy(values, query[k])
		sort.Strings(values)

		for _, val := range values {
			pair := encodedKey + "=" + v.encodeRFC3986(val)
			pairs = append(pairs, pair)
		}
	}

	return strings.Join(pairs, "&")
}

// encodeRFC3986 encodes a string using RFC3986 rules (spaces as %20, not +)
func (v *Verifier) encodeRFC3986(s string) string {
	var result strings.Builder
	for _, r := range s {
		// RFC3986: unreserved = ALPHA / DIGIT / "-" / "." / "_" / "~"
		// Everything else should be percent-encoded
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '.' || r == '_' || r == '~' {
			result.WriteRune(r)
		} else {
			// Percent-encode as UTF-8 bytes
			for _, b := range []byte(string(r)) {
				fmt.Fprintf(&result, "%%%02X", b)
			}
		}
	}
	return result.String()
}

// SignRequest signs an outgoing request to the backend using backend credentials
func (v *Verifier) SignRequest(r *http.Request, backendCreds config.BackendConfig) error {
	if backendCreds.Credentials == nil {
		// No credentials to sign with; use unsigned request
		return nil
	}

	// Create signer and sign request
	signer := v4.NewSigner()

	awsCreds := aws.Credentials{
		AccessKeyID:     backendCreds.Credentials.AccessKeyID,
		SecretAccessKey: backendCreds.Credentials.SecretAccessKey,
		SessionToken:    backendCreds.Credentials.SessionToken,
	}

	err := signer.SignHTTP(
		context.Background(),
		awsCreds,
		r,
		"",
		"s3",
		"us-east-1",
		time.Now(),
	)

	return err
}

// GetSigningKey computes the SigV4 signing key
func GetSigningKey(secretAccessKey, dateStamp, region, service string) []byte {
	kDate := hmac.New(sha256.New, []byte("AWS4"+secretAccessKey))
	kDate.Write([]byte(dateStamp))

	kRegion := hmac.New(sha256.New, kDate.Sum(nil))
	kRegion.Write([]byte(region))

	kService := hmac.New(sha256.New, kRegion.Sum(nil))
	kService.Write([]byte(service))

	kSigning := hmac.New(sha256.New, kService.Sum(nil))
	kSigning.Write([]byte("aws4_request"))

	return kSigning.Sum(nil)
}
