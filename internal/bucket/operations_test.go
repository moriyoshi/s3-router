package bucket

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestDetectMultipartOperation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		query    url.Values
		expected string
	}{
		{
			name:     "CreateMultipartUpload with ?uploads",
			method:   http.MethodPost,
			query:    url.Values{"uploads": {""}},
			expected: OperationCreateMultipartUpload,
		},
		{
			name:     "CompleteMultipartUpload with ?uploadId",
			method:   http.MethodPost,
			query:    url.Values{"uploadId": {"test-upload-id"}},
			expected: OperationCompleteMultipartUpload,
		},
		{
			name:     "UploadPart with ?uploadId and ?partNumber",
			method:   http.MethodPut,
			query:    url.Values{"uploadId": {"test-upload-id"}, "partNumber": {"1"}},
			expected: OperationUploadPart,
		},
		{
			name:     "AbortMultipartUpload with ?uploadId",
			method:   http.MethodDelete,
			query:    url.Values{"uploadId": {"test-upload-id"}},
			expected: OperationAbortMultipartUpload,
		},
		{
			name:     "ListParts with ?uploadId and GET",
			method:   http.MethodGet,
			query:    url.Values{"uploadId": {"test-upload-id"}},
			expected: OperationListParts,
		},
		{
			name:     "ListObjectsV2 with ?list-type=2 should not match ListParts",
			method:   http.MethodGet,
			query:    url.Values{"uploadId": {"test-upload-id"}, "list-type": {"2"}},
			expected: "",
		},
		{
			name:     "Regular PUT without uploadId",
			method:   http.MethodPut,
			query:    url.Values{},
			expected: "",
		},
		{
			name:     "DeleteObjects with ?delete",
			method:   http.MethodPost,
			query:    url.Values{"delete": {""}},
			expected: OperationDeleteObjects,
		},
		{
			name:     "GetObjectAcl with ?acl and GET",
			method:   http.MethodGet,
			query:    url.Values{"acl": {""}},
			expected: OperationGetObjectAcl,
		},
		{
			name:     "PutObjectAcl with ?acl and PUT",
			method:   http.MethodPut,
			query:    url.Values{"acl": {""}},
			expected: OperationPutObjectAcl,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DetectMultipartOperation(tt.method, tt.query)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetectBucketOperation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		method    string
		path      string
		query     string
		bucket    string
		objectKey string
		expected  string
	}{
		{
			name:      "ListBuckets on GET /",
			method:    http.MethodGet,
			path:      "/",
			query:     "",
			bucket:    "",
			objectKey: "",
			expected:  OperationListBuckets,
		},
		{
			name:      "ListBuckets on GET / with bucket-region",
			method:    http.MethodGet,
			path:      "/",
			query:     "bucket-region=us-east-1",
			bucket:    "",
			objectKey: "",
			expected:  OperationListBuckets,
		},
		{
			name:      "ListBuckets on GET / with multiple valid params",
			method:    http.MethodGet,
			path:      "/",
			query:     "bucket-region=us-east-1&max-buckets=100&prefix=test",
			bucket:    "",
			objectKey: "",
			expected:  OperationListBuckets,
		},
		{
			name:      "ListBuckets on GET / even with virtual host bucket extracted",
			method:    http.MethodGet,
			path:      "/",
			query:     "bucket-region=us-east-1",
			bucket:    "localhost",
			objectKey: "",
			expected:  OperationListBuckets,
		},
		{
			name:      "Not ListBuckets when list-type=2 present",
			method:    http.MethodGet,
			path:      "/",
			query:     "list-type=2",
			bucket:    "test-bucket",
			objectKey: "",
			expected:  "",
		},
		{
			name:      "CreateBucket on empty objectKey with PUT",
			method:    http.MethodPut,
			path:      "/test-bucket",
			query:     "",
			bucket:    "test-bucket",
			objectKey: "",
			expected:  OperationCreateBucket,
		},
		{
			name:      "DeleteBucket on empty objectKey with DELETE",
			method:    http.MethodDelete,
			path:      "/test-bucket",
			query:     "",
			bucket:    "test-bucket",
			objectKey: "",
			expected:  OperationDeleteBucket,
		},
		{
			name:      "Regular object PUT",
			method:    http.MethodPut,
			path:      "/test-bucket/file.txt",
			query:     "",
			bucket:    "test-bucket",
			objectKey: "file.txt",
			expected:  "",
		},
		{
			name:      "Regular object GET",
			method:    http.MethodGet,
			path:      "/test-bucket/file.txt",
			query:     "",
			bucket:    "test-bucket",
			objectKey: "file.txt",
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urlStr := tt.path
			if tt.query != "" {
				urlStr += "?" + tt.query
			}
			req := httptest.NewRequest(tt.method, urlStr, nil)
			result := DetectBucketOperation(req, tt.bucket, tt.objectKey)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestHandleListBuckets(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Buckets: map[string]config.BucketConfig{
			"bucket1": {Name: "bucket1"},
			"bucket2": {Name: "bucket2"},
		},
	}

	handler := NewBucketOperationHandler(cfg)
	w := httptest.NewRecorder()

	err := handler.HandleListBuckets(w)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))

	body := w.Body.String()
	assert.NotEmpty(t, body)

	// Check that configured buckets are in response
	assert.True(t, contains(body, "bucket1") && contains(body, "bucket2"), "expected bucket names in response")
}

func TestHandleCreateBucket(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Buckets: map[string]config.BucketConfig{}}
	handler := NewBucketOperationHandler(cfg)
	w := httptest.NewRecorder()

	err := handler.HandleCreateBucket(w, "test-bucket")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))

	body := w.Body.String()
	assert.True(t, contains(body, "BucketAlreadyExists"), "expected BucketAlreadyExists error code in response")
}

func TestHandleDeleteBucket(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Buckets: map[string]config.BucketConfig{}}
	handler := NewBucketOperationHandler(cfg)
	w := httptest.NewRecorder()

	err := handler.HandleDeleteBucket(w, "test-bucket")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Equal(t, "application/xml", w.Header().Get("Content-Type"))

	body := w.Body.String()
	assert.True(t, contains(body, "BucketNotEmpty"), "expected BucketNotEmpty error code in response")
}

func TestParseS3PathForBucketOp(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		host      string
		path      string
		expBucket string
		expKey    string
	}{
		{
			name:      "virtual-hosted-style bucket only",
			host:      "mybucket.s3.amazonaws.com",
			path:      "/",
			expBucket: "mybucket",
			expKey:    "",
		},
		{
			name:      "virtual-hosted-style with key",
			host:      "mybucket.s3.amazonaws.com",
			path:      "/key.txt",
			expBucket: "mybucket",
			expKey:    "key.txt",
		},
		{
			name:      "path-style bucket only",
			host:      "s3.amazonaws.com",
			path:      "/mybucket",
			expBucket: "mybucket",
			expKey:    "",
		},
		{
			name:      "path-style with key",
			host:      "s3.amazonaws.com",
			path:      "/mybucket/key.txt",
			expBucket: "mybucket",
			expKey:    "key.txt",
		},
		{
			name:      "empty path",
			host:      "s3.amazonaws.com",
			path:      "/",
			expBucket: "",
			expKey:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bucket, key := ParseS3PathForBucketOp(tt.host, tt.path)
			assert.Equal(t, tt.expBucket, bucket)
			assert.Equal(t, tt.expKey, key)
		})
	}
}

func contains(s, substr string) bool {
	for i := 0; i < len(s)-len(substr)+1; i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestDetectListObjects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		method    string
		objectKey string
		query     url.Values
		expected  bool
	}{
		{
			name:      "ListObjects v1 - GET to bucket without list-type",
			method:    http.MethodGet,
			objectKey: "",
			query:     url.Values{},
			expected:  true,
		},
		{
			name:      "ListObjects v1 - GET to bucket with prefix",
			method:    http.MethodGet,
			objectKey: "",
			query:     url.Values{"prefix": {"my-prefix"}},
			expected:  true,
		},
		{
			name:      "ListObjects v1 - GET to bucket with delimiter",
			method:    http.MethodGet,
			objectKey: "",
			query:     url.Values{"delimiter": {"/"}},
			expected:  true,
		},
		{
			name:      "ListObjects v2 - should not match",
			method:    http.MethodGet,
			objectKey: "",
			query:     url.Values{"list-type": {"2"}},
			expected:  false,
		},
		{
			name:      "GET with object key - should not match",
			method:    http.MethodGet,
			objectKey: "file.txt",
			query:     url.Values{},
			expected:  false,
		},
		{
			name:      "PUT - should not match",
			method:    http.MethodPut,
			objectKey: "",
			query:     url.Values{},
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/?"+tt.query.Encode(), nil)
			result := DetectListObjects(req, tt.objectKey)
			assert.Equal(t, tt.expected, result, "DetectListObjects returned unexpected result")
		})
	}
}
