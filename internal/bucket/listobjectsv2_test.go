package bucket

import (
	"encoding/xml"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDetectListObjectsV2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		method   string
		query    string
		expected bool
	}{
		{
			name:     "valid list objects v2",
			method:   "GET",
			query:    "list-type=2",
			expected: true,
		},
		{
			name:     "valid list objects v2 with other params",
			method:   "GET",
			query:    "list-type=2&prefix=logs/&max-keys=100",
			expected: true,
		},
		{
			name:     "invalid method",
			method:   "POST",
			query:    "list-type=2",
			expected: false,
		},
		{
			name:     "missing list-type",
			method:   "GET",
			query:    "prefix=logs/",
			expected: false,
		},
		{
			name:     "wrong list-type",
			method:   "GET",
			query:    "list-type=1",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/?"+tt.query, nil)
			result := DetectListObjectsV2(req)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseListObjectsV2Params(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		query    string
		expected ListObjectsV2Params
	}{
		{
			name:  "basic params",
			query: "prefix=logs/&delimiter=/&max-keys=100",
			expected: ListObjectsV2Params{
				Prefix:    "logs/",
				Delimiter: "/",
				MaxKeys:   100,
			},
		},
		{
			name:  "with pagination",
			query: "prefix=data/&start-after=data/2023/&continuation-token=abc123",
			expected: ListObjectsV2Params{
				Prefix:            "data/",
				MaxKeys:           1000, // default
				StartAfter:        "data/2023/",
				ContinuationToken: "abc123",
			},
		},
		{
			name:  "max-keys edge cases",
			query: "max-keys=0",
			expected: ListObjectsV2Params{
				MaxKeys: 0,
			},
		},
		{
			name:  "invalid max-keys uses default",
			query: "max-keys=invalid",
			expected: ListObjectsV2Params{
				MaxKeys: 1000,
			},
		},
		{
			name:  "max-keys over limit",
			query: "max-keys=2000",
			expected: ListObjectsV2Params{
				MaxKeys: 1000, // capped
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, _ := url.ParseQuery(tt.query)
			result := ParseListObjectsV2Params(query)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestListObjectsV2Response(t *testing.T) {
	t.Parallel()
	response := S3ListObjectsV2Response{
		Name:        "test-bucket",
		Prefix:      "logs/",
		Delimiter:   "/",
		MaxKeys:     100,
		IsTruncated: false,
		KeyCount:    1,
		Contents: []S3Object{
			{
				Key:          "logs/2024/01/test.log",
				LastModified: "2024-01-01T12:00:00.000Z",
				ETag:         "\"abc123\"",
				Size:         1024,
				StorageClass: "STANDARD",
			},
		},
	}

	xmlData, err := xml.MarshalIndent(response, "", " ")
	assert.NoError(t, err)

	xmlString := string(xmlData)

	// Check for required elements
	assert.Contains(t, xmlString, "<Name>test-bucket</Name>", "XML missing bucket name")
	assert.Contains(t, xmlString, "<Prefix>logs/</Prefix>", "XML missing prefix")
	assert.Contains(t, xmlString, "<Key>logs/2024/01/test.log</Key>", "XML missing object key")
}
