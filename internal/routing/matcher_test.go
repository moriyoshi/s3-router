package routing

import (
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/template"
)

func TestMatcher_Match(t *testing.T) {
	t.Parallel()
	backend1Config := &config.BackendConfig{
		ID:     "backend1",
		Bucket: "bucket1",
		Prefix: "prefix1/",
	}
	cfg := &config.Config{
		Buckets: map[string]config.BucketConfig{
			"test-bucket": {
				Name: "test-bucket",
				Routes: []config.RouteConfig{
					{
						Path:    regexp.MustCompile("^foo/(?P<rest>.*)"),
						Backend: backend1Config,
						Rewrites: []config.RewriteRule{
							{
								Result: template.MustParse("$rest"),
							},
						},
					},
					{
						Path:    regexp.MustCompile("^bar/special/(.*)$"),
						Backend: backend1Config,
						Rewrites: []config.RewriteRule{
							{
								Result: template.MustParse("SPECIAL/$1"),
							},
						},
					},
				},
			},
		},
	}

	matcher, err := NewMatcher(cfg, 100)
	assert.NoError(t, err)

	tests := []struct {
		name          string
		bucket        string
		objectKey     string
		method        string
		expectErr     bool
		expectKey     string
		expectBackend string
	}{
		{
			name:          "match foo route",
			bucket:        "test-bucket",
			objectKey:     "foo/bar/baz",
			method:        "GET",
			expectErr:     false,
			expectKey:     "bar/baz",
			expectBackend: "backend1",
		},
		{
			name:          "match special bar route",
			bucket:        "test-bucket",
			objectKey:     "bar/special/content",
			method:        "GET",
			expectErr:     false,
			expectKey:     "SPECIAL/content",
			expectBackend: "backend1",
		},
		{
			name:      "no match",
			bucket:    "test-bucket",
			objectKey: "unknown/path",
			method:    "GET",
			expectErr: true,
		},
		{
			name:      "bucket not found",
			bucket:    "unknown-bucket",
			objectKey: "foo/bar",
			method:    "GET",
			expectErr: true,
		},
	}

	ctx := context.Background()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decision, err := matcher.Match(ctx, tt.bucket, tt.objectKey, tt.method, nil)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectKey, decision.RewrittenKey)
				assert.Equal(t, tt.expectBackend, decision.Backend.ID)
			}
		})
	}
}

func TestMatcher_Cache(t *testing.T) {
	t.Parallel()
	backend1Config := &config.BackendConfig{
		ID:     "backend1",
		Bucket: "bucket1",
		Prefix: "prefix1/",
	}
	cfg := &config.Config{
		Buckets: map[string]config.BucketConfig{
			"test-bucket": {
				Name: "test-bucket",
				Routes: []config.RouteConfig{
					{
						Path:     regexp.MustCompile("^foo/(.*)"),
						Backend:  backend1Config,
						Rewrites: []config.RewriteRule{},
					},
				},
			},
		},
	}

	matcher, err := NewMatcher(cfg, 100)
	assert.NoError(t, err)

	ctx := context.Background()
	// First call - should cache
	decision1, err := matcher.Match(ctx, "test-bucket", "foo/bar", "GET", nil)
	assert.NoError(t, err)

	// Second call - should use cache
	decision2, err := matcher.Match(ctx, "test-bucket", "foo/bar", "GET", nil)
	assert.NoError(t, err)

	// Both should be the same
	assert.Equal(t, decision1.Backend.ID, decision2.Backend.ID)
}
