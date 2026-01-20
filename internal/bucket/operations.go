package bucket

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"strings"

	"github.com/moriyoshi/s3-router/internal/config"
)

// BucketOperationHandler handles bucket-level control-plane operations
type BucketOperationHandler struct {
	cfg *config.Config
}

// Operation types
const (
	OperationCreateBucket            = "CreateBucket"
	OperationDeleteBucket            = "DeleteBucket"
	OperationListBuckets             = "ListBuckets"
	OperationListObjects             = "ListObjects"
	OperationListObjectsV2           = "ListObjectsV2"
	OperationCreateMultipartUpload   = "CreateMultipartUpload"
	OperationUploadPart              = "UploadPart"
	OperationCompleteMultipartUpload = "CompleteMultipartUpload"
	OperationAbortMultipartUpload    = "AbortMultipartUpload"
	OperationListParts               = "ListParts"
	OperationDeleteObjects           = "DeleteObjects"
	OperationGetObjectAcl            = "GetObjectAcl"
	OperationPutObjectAcl            = "PutObjectAcl"
)

// S3 Error structures for XML responses
type S3Error struct {
	XMLName xml.Name `xml:"Error"`
	Code    string   `xml:"Code"`
	Message string   `xml:"Message"`
}

type S3ListBucketsResponse struct {
	XMLName xml.Name `xml:"ListBucketsResponse"`
	Owner   struct {
		ID          string `xml:"ID"`
		DisplayName string `xml:"DisplayName"`
	} `xml:"Owner"`
	Buckets struct {
		Bucket []struct {
			Name         string `xml:"Name"`
			CreationDate string `xml:"CreationDate"`
		} `xml:"Bucket"`
	} `xml:"Buckets"`
}

// S3 ListObjectsV2 response structures
type S3ListObjectsV2Response struct {
	XMLName               xml.Name         `xml:"ListBucketResult"`
	Name                  string           `xml:"Name"`                            // bucket name
	Prefix                string           `xml:"Prefix"`                          // prefix parameter
	Delimiter             string           `xml:"Delimiter,omitempty"`             // delimiter parameter
	MaxKeys               int              `xml:"MaxKeys"`                         // max-keys parameter
	IsTruncated           bool             `xml:"IsTruncated"`                     // true if more results exist
	KeyCount              int              `xml:"KeyCount"`                        // number of keys returned
	ContinuationToken     string           `xml:"ContinuationToken,omitempty"`     // continuation token if any
	NextContinuationToken string           `xml:"NextContinuationToken,omitempty"` // next continuation token
	StartAfter            string           `xml:"StartAfter,omitempty"`            // start-after parameter
	Contents              []S3Object       `xml:"Contents"`                        // objects
	CommonPrefixes        []S3CommonPrefix `xml:"CommonPrefixes,omitempty"`        // common prefixes when delimiter is used
}

// S3 ListObjects v1 response structures (slightly different from v2)
type S3ListObjectsResponse struct {
	XMLName        xml.Name         `xml:"ListBucketResult"`
	XMLNS          string           `xml:"xmlns,attr,omitempty"`
	Name           string           `xml:"Name"`                     // bucket name
	Prefix         string           `xml:"Prefix"`                   // prefix parameter
	Marker         string           `xml:"Marker,omitempty"`         // marker parameter (v1 uses marker instead of start-after)
	NextMarker     string           `xml:"NextMarker,omitempty"`     // next marker for pagination
	Delimiter      string           `xml:"Delimiter,omitempty"`      // delimiter parameter
	MaxKeys        int              `xml:"MaxKeys"`                  // max-keys parameter
	IsTruncated    bool             `xml:"IsTruncated"`              // true if more results exist
	Contents       []S3Object       `xml:"Contents"`                 // objects
	CommonPrefixes []S3CommonPrefix `xml:"CommonPrefixes,omitempty"` // common prefixes when delimiter is used
}

type S3Object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
	Owner        struct {
		ID          string `xml:"ID"`
		DisplayName string `xml:"DisplayName"`
	} `xml:"Owner,omitempty"`
}

type S3CommonPrefix struct {
	Prefix string `xml:"Prefix"`
}

// Multipart upload response structures
type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadID string   `xml:"UploadId"`
}

type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type Part struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
	Size       int64  `xml:"Size"`
}

type ListPartsResult struct {
	XMLName              xml.Name `xml:"ListPartsResult"`
	Bucket               string   `xml:"Bucket"`
	Key                  string   `xml:"Key"`
	UploadID             string   `xml:"UploadId"`
	PartNumberMarker     int      `xml:"PartNumberMarker,omitempty"`
	NextPartNumberMarker int      `xml:"NextPartNumberMarker,omitempty"`
	MaxParts             int      `xml:"MaxParts"`
	IsTruncated          bool     `xml:"IsTruncated"`
	Parts                []Part   `xml:"Part"`
}

// NewBucketOperationHandler creates a new bucket operation handler
func NewBucketOperationHandler(cfg *config.Config) *BucketOperationHandler {
	return &BucketOperationHandler{cfg: cfg}
}

// DetectBucketOperation detects if a request is a bucket-level operation
// Returns operation name and whether it's a bucket operation
func DetectBucketOperation(r *http.Request, bucket, objectKey string) string {
	// ListBuckets: GET / with only ListBuckets-specific query parameters
	// Valid params: bucket-region, continuation-token, max-buckets, prefix
	// This must be checked regardless of virtual host settings
	if r.Method == http.MethodGet && r.URL.Path == "/" {
		if isListBucketsQuery(r.URL.Query()) {
			return OperationListBuckets
		}
	}

	// CreateBucket: PUT with no object key
	if objectKey == "" && r.Method == http.MethodPut {
		return OperationCreateBucket
	}

	// DeleteBucket: DELETE with no object key
	if objectKey == "" && r.Method == http.MethodDelete {
		return OperationDeleteBucket
	}

	return ""
}

// listBucketsValidParams contains valid query parameters for ListBuckets operation
var listBucketsValidParams = map[string]bool{
	"bucket-region":      true,
	"continuation-token": true,
	"max-buckets":        true,
	"prefix":             true,
}

// isListBucketsQuery checks if the query parameters are valid for ListBuckets
func isListBucketsQuery(query url.Values) bool {
	for key := range query {
		if !listBucketsValidParams[key] {
			return false
		}
	}
	return true
}

// DetectListObjectsV2 detects if a request is a ListObjectsV2 operation
// Returns true if the request has list-type=2 query parameter
func DetectListObjectsV2(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}

	queryParams := r.URL.Query()
	listType := queryParams.Get("list-type")
	return listType == "2"
}

// DetectMultipartOperation detects multipart-related S3 operations
// Returns the operation name or empty string if not a multipart operation
func DetectMultipartOperation(method string, queryParams url.Values) string {
	uploadID := queryParams.Get("uploadId")

	switch method {
	case http.MethodPost:
		// POST with ?delete = DeleteObjects (batch delete)
		if queryParams.Has("delete") {
			return OperationDeleteObjects
		}
		// POST with no uploadId = CreateMultipartUpload (has ?uploads parameter)
		if uploadID == "" && queryParams.Has("uploads") {
			return OperationCreateMultipartUpload
		}
		// POST with uploadId = CompleteMultipartUpload
		if uploadID != "" {
			return OperationCompleteMultipartUpload
		}
	case http.MethodPut:
		// PUT with ?acl = PutObjectAcl
		if queryParams.Has("acl") {
			return OperationPutObjectAcl
		}
		// PUT with uploadId and partNumber = UploadPart
		if uploadID != "" && queryParams.Get("partNumber") != "" {
			return OperationUploadPart
		}
	case http.MethodDelete:
		// DELETE with uploadId = AbortMultipartUpload
		if uploadID != "" {
			return OperationAbortMultipartUpload
		}
	case http.MethodGet:
		// GET with ?acl = GetObjectAcl
		if queryParams.Has("acl") {
			return OperationGetObjectAcl
		}
		// GET with uploadId (and no list-type=2) = ListParts
		if uploadID != "" && queryParams.Get("list-type") == "" {
			return OperationListParts
		}
	}

	return ""
}

// ListObjectsV2Params extracts ListObjectsV2 parameters from the request
type ListObjectsV2Params struct {
	Prefix            string
	Delimiter         string
	MaxKeys           int
	StartAfter        string
	ContinuationToken string
}

// ListObjectsParams extracts ListObjects (v1) parameters from the request
type ListObjectsParams struct {
	Prefix    string
	Delimiter string
	MaxKeys   int
	Marker    string // v1 uses marker instead of start-after/continuation-token
}

// ParseListObjectsV2Params parses ListObjectsV2 parameters from URL query
func ParseListObjectsV2Params(query url.Values) ListObjectsV2Params {
	params := ListObjectsV2Params{
		MaxKeys: 1000, // AWS default
	}

	if prefix := query.Get("prefix"); prefix != "" {
		params.Prefix = prefix
	}

	if delimiter := query.Get("delimiter"); delimiter != "" {
		params.Delimiter = delimiter
	}

	if maxKeys := query.Get("max-keys"); maxKeys != "" {
		if parsed, err := parseMaxKeys(maxKeys); err == nil {
			params.MaxKeys = parsed
		}
	}

	if startAfter := query.Get("start-after"); startAfter != "" {
		params.StartAfter = startAfter
	}

	if contToken := query.Get("continuation-token"); contToken != "" {
		params.ContinuationToken = contToken
	}

	// Pagination parameters are captured above and will be handled in buildResponse

	return params
}

// ParseListObjectsParams parses ListObjects (v1) parameters from URL query
func ParseListObjectsParams(query url.Values) ListObjectsParams {
	params := ListObjectsParams{
		MaxKeys: 1000, // AWS default
	}

	if prefix := query.Get("prefix"); prefix != "" {
		params.Prefix = prefix
	}

	if delimiter := query.Get("delimiter"); delimiter != "" {
		params.Delimiter = delimiter
	}

	if maxKeys := query.Get("max-keys"); maxKeys != "" {
		if parsed, err := parseMaxKeys(maxKeys); err == nil {
			params.MaxKeys = parsed
		}
	}

	if marker := query.Get("marker"); marker != "" {
		params.Marker = marker
	}

	return params
}

// ToV2Params converts ListObjects v1 params to v2 params for processing
func (p ListObjectsParams) ToV2Params() ListObjectsV2Params {
	return ListObjectsV2Params{
		Prefix:            p.Prefix,
		Delimiter:         p.Delimiter,
		MaxKeys:           p.MaxKeys,
		StartAfter:        p.Marker, // v1 marker is similar to v2 start-after
		ContinuationToken: "",
	}
}

// parseMaxKeys parses max-keys parameter with validation
func parseMaxKeys(maxKeysStr string) (int, error) {
	// AWS allows 0-1000 for max-keys
	maxKeys := 1000 // default
	if maxKeysStr != "" {
		parsed := 0
		for _, r := range maxKeysStr {
			if r < '0' || r > '9' {
				return 1000, nil // invalid, use default
			}
			parsed = parsed*10 + int(r-'0')
			if parsed > 1000 {
				return 1000, nil // cap at AWS limit
			}
		}
		if parsed >= 0 {
			maxKeys = parsed
		}
	}
	return maxKeys, nil
}

// HandleListBuckets synthesizes a ListBuckets response from configured buckets
func (h *BucketOperationHandler) HandleListBuckets(w http.ResponseWriter) error {
	response := S3ListBucketsResponse{}
	response.Owner.ID = "router"
	response.Owner.DisplayName = "S3 Router"

	// Add configured buckets to response
	buckets := make([]struct {
		Name         string `xml:"Name"`
		CreationDate string `xml:"CreationDate"`
	}, len(h.cfg.Buckets))

	var i int
	for _, bcfg := range h.cfg.Buckets {
		buckets[i].Name = bcfg.Name
		buckets[i].CreationDate = "2024-01-01T00:00:00Z" // Synthetic creation date
		i++
	}
	response.Buckets.Bucket = buckets

	// Marshal to XML
	xmlData, err := xml.MarshalIndent(response, "", " ")
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(append([]byte(xml.Header), xmlData...))
	return err
}

// HandleCreateBucket rejects CreateBucket with a policy error
func (h *BucketOperationHandler) HandleCreateBucket(w http.ResponseWriter, bucketName string) error {
	errResp := S3Error{
		Code:    "BucketAlreadyExists",
		Message: "The requested bucket name is not available. Bucket lifecycle is managed declaratively.",
	}

	xmlData, err := xml.Marshal(errResp)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusConflict)
	_, err = w.Write(append([]byte(xml.Header), xmlData...))
	return err
}

// HandleDeleteBucket rejects DeleteBucket with a policy error
func (h *BucketOperationHandler) HandleDeleteBucket(w http.ResponseWriter, bucketName string) error {
	errResp := S3Error{
		Code:    "BucketNotEmpty",
		Message: "The bucket you tried to delete is managed declaratively and cannot be deleted through the S3 API.",
	}

	xmlData, err := xml.Marshal(errResp)
	if err != nil {
		return err
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusForbidden)
	_, err = w.Write(append([]byte(xml.Header), xmlData...))
	return err
}

// ParseS3PathForBucketOp parses S3 path to extract bucket, considering both styles
// Returns bucket, objectKey, isPathStyle
func ParseS3PathForBucketOp(host, path string) (bucket, objectKey string) {
	// Try virtual-hosted-style first (bucket.s3.xxx)
	if strings.Contains(host, ".s3") {
		parts := strings.Split(host, ".")
		if len(parts) > 0 {
			bucket = parts[0]
			if path != "/" && len(path) > 1 {
				objectKey = path[1:]
			}
			return
		}
	}

	// Try path-style (s3.amazonaws.com/bucket/key)
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return
	}

	parts := strings.SplitN(path, "/", 2)
	if len(parts) > 0 {
		bucket = parts[0]
		if len(parts) > 1 {
			objectKey = parts[1]
		}
	}

	return
}

// IsEmptyPath checks if the path represents an empty object key (bucket-level operation)
func IsEmptyPath(objectKey string) bool {
	return objectKey == "" || objectKey == "/"
}

// DetectListObjects detects if a request is a ListObjects v1 operation (without list-type=2)
// Returns true if: it's a GET to the bucket (no object key) and doesn't have list-type=2
func DetectListObjects(r *http.Request, objectKey string) bool {
	if r.Method != http.MethodGet {
		return false
	}

	// Only bucket-level requests (no object key)
	if objectKey != "" {
		return false
	}

	queryParams := r.URL.Query()
	// Only if NOT ListObjects v2
	listType := queryParams.Get("list-type")
	return listType != "2"
}
