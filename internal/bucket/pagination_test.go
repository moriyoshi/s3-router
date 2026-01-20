package bucket

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/moriyoshi/s3-router/internal/observability"
	"github.com/stretchr/testify/assert"
)

// TestPaginationLogic tests the pagination filtering logic in isolation
func TestPaginationLogic(t *testing.T) {
	t.Parallel()
	// Create test objects
	objects := []VirtualObject{
		{VirtualKey: "file01.txt", Size: 100, LastModified: time.Now()},
		{VirtualKey: "file02.txt", Size: 200, LastModified: time.Now()},
		{VirtualKey: "file03.txt", Size: 300, LastModified: time.Now()},
		{VirtualKey: "file04.txt", Size: 400, LastModified: time.Now()},
		{VirtualKey: "file05.txt", Size: 500, LastModified: time.Now()},
		{VirtualKey: "file06.txt", Size: 600, LastModified: time.Now()},
		{VirtualKey: "file07.txt", Size: 700, LastModified: time.Now()},
		{VirtualKey: "file08.txt", Size: 800, LastModified: time.Now()},
		{VirtualKey: "file09.txt", Size: 900, LastModified: time.Now()},
		{VirtualKey: "file10.txt", Size: 1000, LastModified: time.Now()},
	}

	// Create handler
	logger := observability.NewLogger("test")
	handler := &ListObjectsV2Handler{logger: logger}

	testCases := []struct {
		name          string
		params        ListObjectsV2Params
		expectedKeys  []string
		expectedTrunc bool
		expectedNext  string
	}{
		{
			name: "no_pagination",
			params: ListObjectsV2Params{
				MaxKeys: 1000,
			},
			expectedKeys: []string{
				"file01.txt", "file02.txt", "file03.txt", "file04.txt", "file05.txt",
				"file06.txt", "file07.txt", "file08.txt", "file09.txt", "file10.txt",
			},
			expectedTrunc: false,
			expectedNext:  "",
		},
		{
			name: "max_keys_limit",
			params: ListObjectsV2Params{
				MaxKeys: 3,
			},
			expectedKeys:  []string{"file01.txt", "file02.txt", "file03.txt"},
			expectedTrunc: true,
			expectedNext:  "file03.txt",
		},
		{
			name: "start_after",
			params: ListObjectsV2Params{
				StartAfter: "file05.txt",
				MaxKeys:    1000,
			},
			expectedKeys: []string{
				"file06.txt", "file07.txt", "file08.txt", "file09.txt", "file10.txt",
			},
			expectedTrunc: false,
			expectedNext:  "",
		},
		{
			name: "start_after_with_limit",
			params: ListObjectsV2Params{
				StartAfter: "file03.txt",
				MaxKeys:    2,
			},
			expectedKeys:  []string{"file04.txt", "file05.txt"},
			expectedTrunc: true,
			expectedNext:  "file05.txt",
		},
		{
			name: "continuation_token",
			params: ListObjectsV2Params{
				ContinuationToken: "file05.txt",
				MaxKeys:           3,
			},
			expectedKeys:  []string{"file06.txt", "file07.txt", "file08.txt"},
			expectedTrunc: true,
			expectedNext:  "file08.txt",
		},
		{
			name: "continuation_token_overrides_start_after",
			params: ListObjectsV2Params{
				StartAfter:        "file01.txt",
				ContinuationToken: "file07.txt",
				MaxKeys:           2,
			},
			expectedKeys:  []string{"file08.txt", "file09.txt"},
			expectedTrunc: true,
			expectedNext:  "file09.txt",
		},
		{
			name: "start_after_beyond_all_objects",
			params: ListObjectsV2Params{
				StartAfter: "file99.txt",
				MaxKeys:    1000,
			},
			expectedKeys:  []string{},
			expectedTrunc: false,
			expectedNext:  "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			response, err := handler.buildResponse("test-bucket", tc.params, objects)
			assert.NoError(t, err)

			// Check returned keys
			var actualKeys []string
			for _, obj := range response.Contents {
				actualKeys = append(actualKeys, obj.Key)
			}

			assert.Equal(t, len(tc.expectedKeys), len(actualKeys), "expected %d keys, got %d: %v vs %v",
				len(tc.expectedKeys), len(actualKeys), tc.expectedKeys, actualKeys)

			for i, expectedKey := range tc.expectedKeys {
				if i < len(actualKeys) {
					assert.Equal(t, expectedKey, actualKeys[i])
				}
			}

			// Check truncation
			assert.Equal(t, tc.expectedTrunc, response.IsTruncated)

			// Check next continuation token
			assert.Equal(t, tc.expectedNext, response.NextContinuationToken)

			// Check key count
			assert.Equal(t, len(actualKeys), response.KeyCount)
		})
	}
}

// TestFindStartIndex tests the binary search logic for pagination
func TestFindStartIndex(t *testing.T) {
	t.Parallel()
	// Create test objects
	objects := []VirtualObject{
		{VirtualKey: "a.txt"},
		{VirtualKey: "b.txt"},
		{VirtualKey: "c.txt"},
		{VirtualKey: "d.txt"},
		{VirtualKey: "e.txt"},
	}

	logger := observability.NewLogger("test")
	handler := &ListObjectsV2Handler{logger: logger}

	testCases := []struct {
		name          string
		startAfterKey string
		expectedIndex int
	}{
		{name: "before_first", startAfterKey: "0.txt", expectedIndex: 0},
		{name: "exact_match_first", startAfterKey: "a.txt", expectedIndex: 1},
		{name: "between_first_second", startAfterKey: "a1.txt", expectedIndex: 1},
		{name: "exact_match_middle", startAfterKey: "c.txt", expectedIndex: 3},
		{name: "between_middle", startAfterKey: "c1.txt", expectedIndex: 3},
		{name: "exact_match_last", startAfterKey: "e.txt", expectedIndex: 5},
		{name: "after_last", startAfterKey: "z.txt", expectedIndex: 5},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			index := handler.findStartIndex(objects, tc.startAfterKey)
			assert.Equal(t, tc.expectedIndex, index, "expected index %d, got %d for key %q",
				tc.expectedIndex, index, tc.startAfterKey)
		})
	}
}

// TestPaginationIntegration tests pagination with a mock HTTP server
func TestPaginationIntegration(t *testing.T) {
	t.Parallel()
	// This test would require a more complex setup with actual backend mocks
	// For now, we'll test the URL parameter parsing

	testCases := []struct {
		name     string
		query    string
		expected ListObjectsV2Params
	}{
		{
			name:  "basic_pagination",
			query: "list-type=2&max-keys=5&continuation-token=file05.txt",
			expected: ListObjectsV2Params{
				MaxKeys:           5,
				ContinuationToken: "file05.txt",
			},
		},
		{
			name:  "start_after",
			query: "list-type=2&start-after=file03.txt&max-keys=10",
			expected: ListObjectsV2Params{
				MaxKeys:    10,
				StartAfter: "file03.txt",
			},
		},
		{
			name:  "with_prefix",
			query: "list-type=2&prefix=logs/&max-keys=100",
			expected: ListObjectsV2Params{
				Prefix:  "logs/",
				MaxKeys: 100,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			values, err := url.ParseQuery(tc.query)
			assert.NoError(t, err)

			params := ParseListObjectsV2Params(values)

			assert.Equal(t, tc.expected.MaxKeys, params.MaxKeys)
			assert.Equal(t, tc.expected.ContinuationToken, params.ContinuationToken)
			assert.Equal(t, tc.expected.StartAfter, params.StartAfter)
			assert.Equal(t, tc.expected.Prefix, params.Prefix)
		})
	}
}

// TestMaxKeysValidation tests the max-keys parameter validation
func TestMaxKeysValidation(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input    string
		expected int
	}{
		{"1", 1},
		{"500", 500},
		{"1000", 1000},
		{"1001", 1000}, // Should cap at 1000
		{"0", 0},
		{"-1", 1000},  // Invalid, use default
		{"abc", 1000}, // Invalid, use default
		{"", 1000},    // Empty, use default
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("input_%s", tc.input), func(t *testing.T) {
			result, err := parseMaxKeys(tc.input)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result, "expected %d, got %d for input %q", tc.expected, result, tc.input)
		})
	}
}
