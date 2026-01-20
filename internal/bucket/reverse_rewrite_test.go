package bucket

import (
	"regexp"
	"testing"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/template"
	"github.com/stretchr/testify/assert"
)

func TestReverseRewrite(t *testing.T) {
	t.Parallel()
	// Create a minimal handler for testing
	handler := &ListObjectsV2Handler{}

	tests := []struct {
		name        string
		physicalKey string
		pathPattern string
		rewrites    []config.RewriteRule
		expected    string
	}{
		{
			name:        "simple named capture - $rest",
			physicalKey: "bar/baz",
			pathPattern: "^foo/(?P<rest>.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$rest")},
			},
			expected: "foo/bar/baz",
		},
		{
			name:        "simple numbered capture - $1",
			physicalKey: "document.txt",
			pathPattern: "^files/(.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$1")},
			},
			expected: "files/document.txt",
		},
		{
			name:        "prefix replacement - SPECIAL/$1",
			physicalKey: "SPECIAL/content",
			pathPattern: "^bar/special/(.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("SPECIAL/$1")},
			},
			expected: "bar/special/content",
		},
		{
			name:        "prefix replacement with named capture",
			physicalKey: "ARCHIVED/old-file.txt",
			pathPattern: "^archive/(?P<file>.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("ARCHIVED/$file")},
			},
			expected: "archive/old-file.txt",
		},
		{
			name:        "deep path static prefix",
			physicalKey: "image.png",
			pathPattern: "^assets/images/(.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$1")},
			},
			expected: "assets/images/image.png",
		},
		{
			name:        "no rewrites - passthrough",
			physicalKey: "some/path/file.txt",
			pathPattern: "^data/(.*)",
			rewrites:    []config.RewriteRule{},
			expected:    "some/path/file.txt",
		},
		{
			name:        "suffix after capture - $1/data",
			physicalKey: "prefix-value/data",
			pathPattern: "^logs/(?P<id>.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$id/data")},
			},
			expected: "logs/prefix-value",
		},
		// Multiple capture tests
		{
			name:        "multiple numbered captures - $1/$2",
			physicalKey: "user123/doc456",
			pathPattern: "^data/([^/]+)/([^/]+)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$1/$2")},
			},
			expected: "data/user123/doc456",
		},
		{
			name:        "multiple named captures - $category/$file",
			physicalKey: "images/photo.jpg",
			pathPattern: "^assets/(?P<category>[^/]+)/(?P<file>.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$category/$file")},
			},
			expected: "assets/images/photo.jpg",
		},
		{
			name:        "multiple captures with prefix - PREFIX/$1/$2",
			physicalKey: "PREFIX/abc/xyz",
			pathPattern: "^items/([^/]+)/([^/]+)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("PREFIX/$1/$2")},
			},
			expected: "items/abc/xyz",
		},
		{
			name:        "multiple captures reordered - $2/$1",
			physicalKey: "second/first",
			pathPattern: "^swap/([^/]+)/([^/]+)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$2/$1")},
			},
			expected: "swap/second/first",
		},
		{
			name:        "multiple named captures with static infix - $user/files/$doc",
			physicalKey: "john/files/report.pdf",
			pathPattern: "^storage/(?P<user>[^/]+)/(?P<doc>.*)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$user/files/$doc")},
			},
			expected: "storage/john/files/report.pdf",
		},
		{
			name:        "three captures - $1/$2/$3",
			physicalKey: "a/b/c",
			pathPattern: "^root/([^/]+)/([^/]+)/([^/]+)",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$1/$2/$3")},
			},
			expected: "root/a/b/c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := config.RouteConfig{
				Path:     regexp.MustCompile(tt.pathPattern),
				Rewrites: tt.rewrites,
			}

			result := handler.reverseRewrite(tt.physicalKey, route)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractStaticPrefixFromRegex(t *testing.T) {
	t.Parallel()
	tests := []struct {
		pattern  string
		expected string
	}{
		{"^foo/(?P<rest>.*)", "foo/"},
		{"^bar/special/(.*)", "bar/special/"},
		{"^assets/images/(.*)", "assets/images/"},
		{"^(.*)", ""},
		{"^data-files/(.*)", "data-files/"},
		{"^logs_archive/(.*)", "logs_archive/"},
		{"foo/bar/(.*)", "foo/bar/"},   // no anchor
		{"^foo/[^/]+/(.*)", "foo/"},    // stops at [
		{"^users/\\d+/(.*)", "users/"}, // stops at \
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			result := extractStaticPrefixFromRegex(tt.pattern)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsSingleCaptureVariable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		result   string
		expected bool
	}{
		{"$rest", true},
		{"$1", true},
		{"$name", true},
		{"$file_name", true},
		{"SPECIAL/$1", false},
		{"$1/suffix", false},
		{"prefix/$rest/suffix", false},
		{"", false},
		{"$", false},
		{"plain-text", false},
	}

	for _, tt := range tests {
		t.Run(tt.result, func(t *testing.T) {
			tmpl := template.MustParse(tt.result)
			analysis := tmpl.Analysis()
			// A single capture variable has: no prefix, exactly one placeholder, no tail
			result := analysis.Prefix == "" && len(analysis.Rest) == 1 && analysis.Rest[0].Tail == ""
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractPrefixBeforeCapture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		result   string
		expected string
	}{
		{"SPECIAL/$1", "SPECIAL/"},
		{"PREFIX/$rest", "PREFIX/"},
		{"data/archive/$file", "data/archive/"},
		{"$1", ""},        // no prefix
		{"$rest", ""},     // no prefix
		{"$1/suffix", ""}, // has suffix, not just prefix
		{"plain-text", ""},
	}

	for _, tt := range tests {
		t.Run(tt.result, func(t *testing.T) {
			result := extractPrefixBeforeCapture(tt.result)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractSuffixAfterCapture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		result   string
		expected string
	}{
		{"$1/data", "/data"},
		{"$rest.bak", ".bak"},
		{"$file_suffix", ""}, // no suffix, just variable
		{"$1", ""},           // no suffix
		{"PREFIX/$1", ""},    // no suffix
		{"$id/logs/file.txt", "/logs/file.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.result, func(t *testing.T) {
			result := extractSuffixAfterCapture(tt.result)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsOnlyCaptureVariables(t *testing.T) {
	t.Parallel()
	tests := []struct {
		result   string
		expected bool
	}{
		{"$1", true},
		{"$rest", true},
		{"$1/$2", true},
		{"$category/$file", true},
		{"$1/$2/$3", true},
		{"$a/$b/$c/$d", true},
		{"PREFIX/$1", false},
		{"$1/suffix", false},
		{"prefix/$1/suffix", false},
		{"", false},
		{"plain-text", false},
		{"$", false},
	}

	for _, tt := range tests {
		t.Run(tt.result, func(t *testing.T) {
			tmpl := template.MustParse(tt.result)
			analysis := tmpl.Analysis()
			// Only capture variables: has placeholders, no prefix, no tail after last
			result := len(analysis.Rest) > 0 && analysis.Prefix == "" && analysis.Rest[len(analysis.Rest)-1].Tail == ""
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractPrefixBeforeFirstCapture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		result   string
		expected string
	}{
		{"PREFIX/$1", "PREFIX/"},
		{"PREFIX/$1/$2", "PREFIX/"},
		{"data/archive/$file/$name", "data/archive/"},
		{"$1", ""},
		{"$1/$2", ""},
		{"", ""},
		// New placeholder syntax with defaults
		{"PREFIX/${name:-default}", "PREFIX/"},
		{"PREFIX/${1:-fallback}", "PREFIX/"},
		{"data/${path:-root}/file", "data/"},
		{"${name:-default}", ""},
	}

	for _, tt := range tests {
		t.Run(tt.result, func(t *testing.T) {
			tmpl := template.MustParse(tt.result)
			analysis := tmpl.Analysis()
			assert.Equal(t, tt.expected, analysis.Prefix)
		})
	}
}

func TestExtractSuffixAfterLastCapture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		result   string
		expected string
	}{
		{"$1/data", "/data"},
		{"$1/$2/logs", "/logs"},
		{"$rest.bak", ".bak"},
		{"$1", ""},
		{"$1/$2", ""},
		{"PREFIX/$1", ""},
		// New placeholder syntax with defaults
		{"${name:-default}/logs", "/logs"},
		{"${1:-fallback}/data", "/data"},
		{"PREFIX/${path:-root}/file", "/file"},
		{"${name:-default}", ""},
	}

	for _, tt := range tests {
		t.Run(tt.result, func(t *testing.T) {
			tmpl := template.MustParse(tt.result)
			analysis := tmpl.Analysis()
			var result string
			if len(analysis.Rest) > 0 {
				result = analysis.Rest[len(analysis.Rest)-1].Tail
			}
			assert.Equal(t, tt.expected, result)
		})
	}
}
