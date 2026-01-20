package bucket

import (
	"regexp"
	"testing"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/observability"
	"github.com/moriyoshi/s3-router/internal/template"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrefixOptimizer_AnalyzeRoute(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name                              string
		pattern                           string
		rewrites                          []config.RewriteRule
		expectedStaticPrefix              string
		expectedCanOptimize               bool
		expectedRequiresFullScan          bool
		expectedRequiresNonEmptyRemainder bool
		expectedHasTrivialRewrite         bool
		expectedRewriteResultPrefix       string
	}{
		{
			name:                              "passthrough pattern without anchor",
			pattern:                           "(.*)",
			rewrites:                          nil,
			expectedStaticPrefix:              "",
			expectedCanOptimize:               false,
			expectedRequiresFullScan:          true,
			expectedRequiresNonEmptyRemainder: false,
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "anchored foo prefix with rest capture (zero or more)",
			pattern:                           "^foo/(?P<rest>.*)",
			rewrites:                          []config.RewriteRule{{Result: template.MustParse("$rest")}},
			expectedStaticPrefix:              "foo/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // .* allows empty
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "anchored bar prefix with numbered capture (zero or more)",
			pattern:                           "^bar/(.*)",
			rewrites:                          []config.RewriteRule{{Result: template.MustParse("$1")}},
			expectedStaticPrefix:              "bar/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // .* allows empty
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "anchored prefix with result prefix",
			pattern:                           "^virtual/(.*)",
			rewrites:                          []config.RewriteRule{{Result: template.MustParse("physical/$1")}},
			expectedStaticPrefix:              "virtual/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // .* allows empty
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "physical/",
		},
		{
			name:                              "no rewrites - passthrough",
			pattern:                           "^data/(.*)",
			rewrites:                          nil,
			expectedStaticPrefix:              "data/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // .* allows empty
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:    "complex rewrite with pattern",
			pattern: "^logs/(.*)",
			rewrites: []config.RewriteRule{
				{
					Pattern: regexp.MustCompile("^special/(.*)"),
					Result:  template.MustParse("SPECIAL/$1"),
				},
			},
			expectedStaticPrefix:              "logs/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // .* allows empty
			expectedHasTrivialRewrite:         false, // Has pattern regex
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "single directory level pattern [^/]* (zero or more)",
			pattern:                           "^users/([^/]*)",
			rewrites:                          []config.RewriteRule{{Result: template.MustParse("$1")}},
			expectedStaticPrefix:              "users/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // [^/]* allows empty
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "single directory level pattern [^/]+ (one or more)",
			pattern:                           "^items/([^/]+)",
			rewrites:                          nil,
			expectedStaticPrefix:              "items/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: true, // [^/]+ requires at least one
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "named capture with [^/]*",
			pattern:                           "^products/(?P<id>[^/]*)",
			rewrites:                          []config.RewriteRule{{Result: template.MustParse("$id")}},
			expectedStaticPrefix:              "products/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // [^/]* allows empty
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "one or more with .+",
			pattern:                           "^required/(.+)",
			rewrites:                          []config.RewriteRule{{Result: template.MustParse("$1")}},
			expectedStaticPrefix:              "required/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: true, // .+ requires at least one
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "named capture with .+",
			pattern:                           "^nonempty/(?P<rest>.+)",
			rewrites:                          []config.RewriteRule{{Result: template.MustParse("$rest")}},
			expectedStaticPrefix:              "nonempty/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: true, // .+ requires at least one
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "complex pattern with intervening wildcard",
			pattern:                           "^foo/bar/([^/]+)/baz/.*",
			rewrites:                          nil,
			expectedStaticPrefix:              "foo/bar/",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false, // .* allows empty after baz/
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "exact match with multiple segments",
			pattern:                           "^foo/bar$",
			rewrites:                          nil,
			expectedStaticPrefix:              "",
			expectedCanOptimize:               false,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: false,
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "complex literal prefix with plus and end anchor",
			pattern:                           "^test.+$",
			rewrites:                          nil,
			expectedStaticPrefix:              "test",
			expectedCanOptimize:               true,
			expectedRequiresFullScan:          false,
			expectedRequiresNonEmptyRemainder: true,
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
		{
			name:                              "unanchored pattern with end anchor",
			pattern:                           "foo/bar$",
			rewrites:                          nil,
			expectedStaticPrefix:              "",
			expectedCanOptimize:               false,
			expectedRequiresFullScan:          true,
			expectedRequiresNonEmptyRemainder: false,
			expectedHasTrivialRewrite:         true,
			expectedRewriteResultPrefix:       "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := config.RouteConfig{
				Path:     regexp.MustCompile(tt.pattern),
				Rewrites: tt.rewrites,
			}

			analysis := optimizer.AnalyzeRoute(route)

			assert.Equal(t, tt.expectedStaticPrefix, analysis.StaticPrefix, "StaticPrefix mismatch")
			assert.Equal(t, tt.expectedCanOptimize, analysis.CanOptimize, "CanOptimize mismatch")
			assert.Equal(t, tt.expectedRequiresFullScan, analysis.RequiresFullScan, "RequiresFullScan mismatch")
			assert.Equal(t, tt.expectedRequiresNonEmptyRemainder, analysis.RequiresNonEmptyRemainder, "RequiresNonEmptyRemainder mismatch")
			assert.Equal(t, tt.expectedHasTrivialRewrite, analysis.HasTrivialRewrite, "HasTrivialRewrite mismatch")
			assert.Equal(t, tt.expectedRewriteResultPrefix, analysis.RewriteResultPrefix, "RewriteResultPrefix mismatch")
		})
	}
}

func TestPrefixOptimizer_ComputePhysicalPrefix(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name                   string
		virtualPrefix          string
		staticPrefix           string
		hasTrivialRewrite      bool
		rewriteResultPrefix    string
		expectedPhysicalPrefix string
		expectedOk             bool
	}{
		{
			name:                   "empty virtual prefix",
			virtualPrefix:          "",
			staticPrefix:           "foo/",
			hasTrivialRewrite:      true,
			rewriteResultPrefix:    "",
			expectedPhysicalPrefix: "",
			expectedOk:             true,
		},
		{
			name:                   "passthrough - no static prefix",
			virtualPrefix:          "logs/",
			staticPrefix:           "",
			hasTrivialRewrite:      true,
			rewriteResultPrefix:    "",
			expectedPhysicalPrefix: "logs/",
			expectedOk:             true,
		},
		{
			name:                   "prefix stripping - foo/bar to bar",
			virtualPrefix:          "foo/bar/",
			staticPrefix:           "foo/",
			hasTrivialRewrite:      true,
			rewriteResultPrefix:    "",
			expectedPhysicalPrefix: "bar/",
			expectedOk:             true,
		},
		{
			name:                   "prefix transformation with result prefix",
			virtualPrefix:          "virtual/data/",
			staticPrefix:           "virtual/",
			hasTrivialRewrite:      true,
			rewriteResultPrefix:    "physical/",
			expectedPhysicalPrefix: "physical/data/",
			expectedOk:             true,
		},
		{
			name:                   "virtual prefix matches static prefix exactly",
			virtualPrefix:          "foo/",
			staticPrefix:           "foo/",
			hasTrivialRewrite:      true,
			rewriteResultPrefix:    "",
			expectedPhysicalPrefix: "",
			expectedOk:             true,
		},
		{
			name:                   "virtual prefix doesn't match route",
			virtualPrefix:          "bar/",
			staticPrefix:           "foo/",
			hasTrivialRewrite:      true,
			rewriteResultPrefix:    "",
			expectedPhysicalPrefix: "",
			expectedOk:             false, // route doesn't match this prefix
		},
		{
			name:                   "virtual prefix is prefix of static prefix",
			virtualPrefix:          "fo",
			staticPrefix:           "foo/",
			hasTrivialRewrite:      true,
			rewriteResultPrefix:    "",
			expectedPhysicalPrefix: "",
			expectedOk:             true, // can't optimize but route might match
		},
		{
			name:                   "non-trivial rewrite",
			virtualPrefix:          "logs/",
			staticPrefix:           "logs/",
			hasTrivialRewrite:      false,
			rewriteResultPrefix:    "",
			expectedPhysicalPrefix: "",
			expectedOk:             true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := &PrefixAnalysis{
				StaticPrefix:        tt.staticPrefix,
				HasTrivialRewrite:   tt.hasTrivialRewrite,
				RewriteResultPrefix: tt.rewriteResultPrefix,
			}

			physicalPrefix, ok := optimizer.ComputePhysicalPrefix(tt.virtualPrefix, analysis)

			assert.Equal(t, tt.expectedOk, ok, "ok mismatch")
			assert.Equal(t, tt.expectedPhysicalPrefix, physicalPrefix, "physicalPrefix mismatch")
		})
	}
}

func TestPrefixOptimizer_CanSkipRoute(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name          string
		virtualPrefix string
		staticPrefix  string
		canOptimize   bool
		expectedSkip  bool
	}{
		{
			name:          "matching prefix - should not skip",
			virtualPrefix: "foo/bar/",
			staticPrefix:  "foo/",
			canOptimize:   true,
			expectedSkip:  false,
		},
		{
			name:          "non-matching prefix - should skip",
			virtualPrefix: "bar/baz/",
			staticPrefix:  "foo/",
			canOptimize:   true,
			expectedSkip:  true,
		},
		{
			name:          "empty virtual prefix - should not skip",
			virtualPrefix: "",
			staticPrefix:  "foo/",
			canOptimize:   true,
			expectedSkip:  false,
		},
		{
			name:          "empty static prefix - should not skip",
			virtualPrefix: "foo/",
			staticPrefix:  "",
			canOptimize:   true,
			expectedSkip:  false,
		},
		{
			name:          "can't optimize - should not skip",
			virtualPrefix: "bar/",
			staticPrefix:  "foo/",
			canOptimize:   false,
			expectedSkip:  false,
		},
		{
			name:          "virtual is prefix of static - should not skip",
			virtualPrefix: "fo",
			staticPrefix:  "foo/",
			canOptimize:   true,
			expectedSkip:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := &PrefixAnalysis{
				StaticPrefix:     tt.staticPrefix,
				CanOptimize:      tt.canOptimize,
				RequiresFullScan: !tt.canOptimize,
			}
			route := config.RouteConfig{}

			canSkip := optimizer.CanSkipRoute(tt.virtualPrefix, route, analysis)

			assert.Equal(t, tt.expectedSkip, canSkip, "CanSkipRoute mismatch")
		})
	}
}

func TestPrefixOptimizer_GetOptimizedBackendPrefix(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name          string
		virtualPrefix string
		backendPrefix string
		analysis      *PrefixAnalysis
		expected      string
	}{
		{
			name:          "aligned virtual prefix uses virtual prefix",
			virtualPrefix: "logs/2024/",
			backendPrefix: "backend/",
			analysis: &PrefixAnalysis{
				StaticPrefix: "logs/",
				CanOptimize:  true,
			},
			expected: "backend/logs/2024/",
		},
		{
			name:          "non-aligned virtual prefix falls back to static prefix",
			virtualPrefix: "data/",
			backendPrefix: "backend/",
			analysis: &PrefixAnalysis{
				StaticPrefix: "logs/",
				CanOptimize:  true,
			},
			expected: "backend/logs/",
		},
		{
			name:          "no optimization uses virtual prefix",
			virtualPrefix: "data/",
			backendPrefix: "backend/",
			analysis: &PrefixAnalysis{
				StaticPrefix: "",
				CanOptimize:  false,
			},
			expected: "backend/data/",
		},
		{
			name:          "empty virtual prefix uses static prefix",
			virtualPrefix: "",
			backendPrefix: "backend/",
			analysis: &PrefixAnalysis{
				StaticPrefix: "logs/",
				CanOptimize:  true,
			},
			expected: "backend/logs/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := optimizer.GetOptimizedBackendPrefix(tt.virtualPrefix, tt.backendPrefix, tt.analysis)
			assert.Equal(t, tt.expected, result, "GetOptimizedBackendPrefix mismatch")
		})
	}
}

func TestPrefixOptimizer_TransformVirtualPrefix(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name          string
		virtualPrefix string
		analysis      *PrefixAnalysis
		expected      string
	}{
		{
			name:          "empty virtual prefix returns static prefix",
			virtualPrefix: "",
			analysis: &PrefixAnalysis{
				StaticPrefix: "logs/",
				CanOptimize:  true,
			},
			expected: "logs/",
		},
		{
			name:          "matching virtual prefix returns virtual prefix",
			virtualPrefix: "logs/2024/",
			analysis: &PrefixAnalysis{
				StaticPrefix: "logs/",
				CanOptimize:  true,
			},
			expected: "logs/2024/",
		},
		{
			name:          "optimizable non-matching prefix is concatenated",
			virtualPrefix: "data/",
			analysis: &PrefixAnalysis{
				StaticPrefix: "logs/",
				CanOptimize:  true,
			},
			expected: "logs/data/",
		},
		{
			name:          "non-optimizable falls back to static prefix",
			virtualPrefix: "data/",
			analysis: &PrefixAnalysis{
				StaticPrefix: "logs/",
				CanOptimize:  false,
			},
			expected: "logs/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := config.RouteConfig{}
			result := optimizer.TransformVirtualPrefix(tt.virtualPrefix, route, tt.analysis)
			assert.Equal(t, tt.expected, result, "TransformVirtualPrefix mismatch")
		})
	}
}

func TestAnalyzeRewrites(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name                        string
		rewrites                    []config.RewriteRule
		expectedHasTrivialRewrite   bool
		expectedRewriteResultPrefix string
	}{
		{
			name:                        "no rewrites - passthrough",
			rewrites:                    nil,
			expectedHasTrivialRewrite:   true,
			expectedRewriteResultPrefix: "",
		},
		{
			name:                        "empty rewrites slice",
			rewrites:                    []config.RewriteRule{},
			expectedHasTrivialRewrite:   true,
			expectedRewriteResultPrefix: "",
		},
		{
			name:                        "single capture $rest",
			rewrites:                    []config.RewriteRule{{Result: template.MustParse("$rest")}},
			expectedHasTrivialRewrite:   true,
			expectedRewriteResultPrefix: "",
		},
		{
			name:                        "single capture $1",
			rewrites:                    []config.RewriteRule{{Result: template.MustParse("$1")}},
			expectedHasTrivialRewrite:   true,
			expectedRewriteResultPrefix: "",
		},
		{
			name:                        "prefix with capture",
			rewrites:                    []config.RewriteRule{{Result: template.MustParse("PREFIX/$1")}},
			expectedHasTrivialRewrite:   true,
			expectedRewriteResultPrefix: "PREFIX/",
		},
		{
			name:                        "prefix with named capture",
			rewrites:                    []config.RewriteRule{{Result: template.MustParse("data/$rest")}},
			expectedHasTrivialRewrite:   true,
			expectedRewriteResultPrefix: "data/",
		},
		{
			name:                        "conditional placeholder default",
			rewrites:                    []config.RewriteRule{{Result: template.MustParse("${name:-default}")}},
			expectedHasTrivialRewrite:   false,
			expectedRewriteResultPrefix: "",
		},
		{
			name: "rewrite with pattern regex",
			rewrites: []config.RewriteRule{{
				Pattern: regexp.MustCompile("^special/(.*)"),
				Result:  template.MustParse("SPECIAL/$1"),
			}},
			expectedHasTrivialRewrite:   false,
			expectedRewriteResultPrefix: "",
		},
		{
			name: "multiple rewrites",
			rewrites: []config.RewriteRule{
				{Result: template.MustParse("$1")},
				{Result: template.MustParse("$2")},
			},
			expectedHasTrivialRewrite:   false,
			expectedRewriteResultPrefix: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := &PrefixAnalysis{}

			optimizer.analyzeRewrites(analysis, tt.rewrites)

			assert.Equal(t, tt.expectedHasTrivialRewrite, analysis.HasTrivialRewrite, "HasTrivialRewrite mismatch")
			assert.Equal(t, tt.expectedRewriteResultPrefix, analysis.RewriteResultPrefix, "RewriteResultPrefix mismatch")
		})
	}
}

func TestFilterByVirtualPrefix(t *testing.T) {
	t.Parallel()
	// Create a minimal handler for testing with a real logger
	logger := observability.NewLogger("error")
	handler := &ListObjectsV2Handler{
		logger: logger,
	}

	objects := []VirtualObject{
		{VirtualKey: "logs/2024/01/01.log"},
		{VirtualKey: "logs/2024/01/02.log"},
		{VirtualKey: "logs/2024/02/01.log"},
		{VirtualKey: "data/file1.csv"},
		{VirtualKey: "data/file2.csv"},
		{VirtualKey: "backup/data.tar.gz"},
	}

	tests := []struct {
		name         string
		prefix       string
		expectedKeys []string
	}{
		{
			name:   "empty prefix returns all",
			prefix: "",
			expectedKeys: []string{
				"logs/2024/01/01.log",
				"logs/2024/01/02.log",
				"logs/2024/02/01.log",
				"data/file1.csv",
				"data/file2.csv",
				"backup/data.tar.gz",
			},
		},
		{
			name:   "logs prefix",
			prefix: "logs/",
			expectedKeys: []string{
				"logs/2024/01/01.log",
				"logs/2024/01/02.log",
				"logs/2024/02/01.log",
			},
		},
		{
			name:   "logs/2024/01 prefix",
			prefix: "logs/2024/01/",
			expectedKeys: []string{
				"logs/2024/01/01.log",
				"logs/2024/01/02.log",
			},
		},
		{
			name:         "nonexistent prefix",
			prefix:       "nonexistent/",
			expectedKeys: []string{},
		},
		{
			name:   "data prefix",
			prefix: "data/",
			expectedKeys: []string{
				"data/file1.csv",
				"data/file2.csv",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := handler.filterByVirtualPrefix(objects, tt.prefix)

			require.Len(t, filtered, len(tt.expectedKeys), "filtered count mismatch")

			actualKeys := make([]string, len(filtered))
			for i, obj := range filtered {
				actualKeys[i] = obj.VirtualKey
			}

			assert.Equal(t, tt.expectedKeys, actualKeys, "filtered keys mismatch")
		})
	}
}

func TestTokenizePattern(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		pattern          string
		expectedSegments []string
	}{
		{
			name:             "simple literal path",
			pattern:          "^foo/bar/baz",
			expectedSegments: []string{"foo", "bar", "baz"},
		},
		{
			name:             "with regex group",
			pattern:          "^foo/(?P<rest>.*)",
			expectedSegments: []string{"foo", "(?P<rest>.*)"},
		},
		{
			name:             "single level wildcard",
			pattern:          "^foo/([^/]+)/bar",
			expectedSegments: []string{"foo", "([^/]+)", "bar"},
		},
		{
			name:             "complex pattern",
			pattern:          "^foo/bar/([^/]+)/baz/.*",
			expectedSegments: []string{"foo", "bar", "([^/]+)", "baz", ".*"},
		},
		{
			name:             "with trailing anchor",
			pattern:          "^products/([^/]*)$",
			expectedSegments: []string{"products", "([^/]*)"},
		},
		{
			name:             "multi-level at start",
			pattern:          "^([^/]+)/data/(.*)",
			expectedSegments: []string{"([^/]+)", "data", "(.*)"},
		},
		{
			name:             "multi-level only",
			pattern:          "^(.*)",
			expectedSegments: []string{"(.*)"},
		},
		{
			name:             "empty segments ignored",
			pattern:          "^foo//bar",
			expectedSegments: []string{"foo", "bar"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenizePattern(tt.pattern)
			assert.Equal(t, tt.expectedSegments, result, "segments mismatch")
		})
	}
}

func TestClassifySegment(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		segment           string
		expectedType      SegmentType
		expectedOptional  bool
		expectedOptional2 bool
	}{
		{
			name:             "literal foo",
			segment:          "foo",
			expectedType:     SegmentLiteral,
			expectedOptional: false,
		},
		{
			name:             "literal with numbers",
			segment:          "data123",
			expectedType:     SegmentLiteral,
			expectedOptional: false,
		},
		{
			name:              "multiLevel .*",
			segment:           ".*",
			expectedType:      SegmentMultiLevel,
			expectedOptional2: true,
		},
		{
			name:              "multiLevel .+",
			segment:           ".+",
			expectedType:      SegmentMultiLevel,
			expectedOptional2: false,
		},
		{
			name:              "multiLevel (.*)",
			segment:           "(.*)",
			expectedType:      SegmentMultiLevel,
			expectedOptional2: true,
		},
		{
			name:              "multiLevel (.+)",
			segment:           "(.+)",
			expectedType:      SegmentMultiLevel,
			expectedOptional2: false,
		},
		{
			name:              "multiLevel named (?P<rest>.*)",
			segment:           "(?P<rest>.*)",
			expectedType:      SegmentMultiLevel,
			expectedOptional2: true,
		},
		{
			name:             "singleLevel [^/]*",
			segment:          "[^/]*",
			expectedType:     SegmentSingleLevel,
			expectedOptional: true,
		},
		{
			name:             "singleLevel [^/]+",
			segment:          "[^/]+",
			expectedType:     SegmentSingleLevel,
			expectedOptional: false,
		},
		{
			name:             "singleLevel ([^/]*)",
			segment:          "([^/]*)",
			expectedType:     SegmentSingleLevel,
			expectedOptional: true,
		},
		{
			name:             "singleLevel ([^/]+)",
			segment:          "([^/]+)",
			expectedType:     SegmentSingleLevel,
			expectedOptional: false,
		},
		{
			name:             "singleLevel named (?P<id>[^/]*)",
			segment:          "(?P<id>[^/]*)",
			expectedType:     SegmentSingleLevel,
			expectedOptional: true,
		},
		{
			name:             "singleLevel named (?P<id>[^/]+)",
			segment:          "(?P<id>[^/]+)",
			expectedType:     SegmentSingleLevel,
			expectedOptional: false,
		},
		{
			name:             "complex pattern",
			segment:          "[a-z]+",
			expectedType:     SegmentComplex,
			expectedOptional: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifySegment(tt.segment)
			assert.Equal(t, tt.expectedType, result.Type, "Type mismatch")
			if tt.expectedType == SegmentSingleLevel {
				assert.Equal(t, tt.expectedOptional, result.Optional, "Optional mismatch")
			}
			if tt.expectedType == SegmentMultiLevel {
				assert.Equal(t, tt.expectedOptional2, result.Optional2, "Optional2 mismatch")
			}
		})
	}
}

func TestAnalyzePatternStructure(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name                     string
		pattern                  string
		expectedStaticPrefixLen  int
		expectedHasStaticPrefix  bool
		expectedFirstSegmentType SegmentType
		expectedSegmentCount     int
	}{
		{
			name:                     "all literals",
			pattern:                  "^foo/bar/baz",
			expectedStaticPrefixLen:  3,
			expectedHasStaticPrefix:  true,
			expectedFirstSegmentType: SegmentLiteral,
			expectedSegmentCount:     3,
		},
		{
			name:                     "literals then wildcard",
			pattern:                  "^foo/bar/([^/]+)/baz/.*",
			expectedStaticPrefixLen:  2,
			expectedHasStaticPrefix:  true,
			expectedFirstSegmentType: SegmentLiteral,
			expectedSegmentCount:     5,
		},
		{
			name:                     "starts with wildcard",
			pattern:                  "^([^/]+)/data/(.*)",
			expectedStaticPrefixLen:  0,
			expectedHasStaticPrefix:  false,
			expectedFirstSegmentType: SegmentSingleLevel,
			expectedSegmentCount:     3,
		},
		{
			name:                     "single literal then multi-level",
			pattern:                  "^products/(.*)",
			expectedStaticPrefixLen:  1,
			expectedHasStaticPrefix:  true,
			expectedFirstSegmentType: SegmentLiteral,
			expectedSegmentCount:     2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			structure := optimizer.analyzePatternStructure(tt.pattern)

			assert.Equal(t, tt.expectedStaticPrefixLen, structure.StaticPrefixLen, "StaticPrefixLen mismatch")
			assert.Equal(t, tt.expectedHasStaticPrefix, structure.HasStaticPrefix, "HasStaticPrefix mismatch")
			assert.Equal(t, tt.expectedSegmentCount, len(structure.Segments), "SegmentCount mismatch")
			if len(structure.Segments) > 0 {
				assert.Equal(t, tt.expectedFirstSegmentType, structure.Segments[0].Type, "FirstSegmentType mismatch")
			}
		})
	}
}

func TestGetStaticPrefixFromStructure(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	tests := []struct {
		name           string
		pattern        string
		expectedPrefix string
	}{
		{
			name:           "simple path",
			pattern:        "^foo/bar",
			expectedPrefix: "foo/bar/",
		},
		{
			name:           "path then wildcard",
			pattern:        "^foo/bar/([^/]+)",
			expectedPrefix: "foo/bar/",
		},
		{
			name:           "single segment",
			pattern:        "^products/(.*)",
			expectedPrefix: "products/",
		},
		{
			name:           "starts with wildcard",
			pattern:        "^([^/]+)/data",
			expectedPrefix: "",
		},
		{
			name:           "no static prefix",
			pattern:        "^(.*)",
			expectedPrefix: "",
		},
		{
			name:           "complex segment with literal prefix",
			pattern:        "^test.*",
			expectedPrefix: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			structure := optimizer.analyzePatternStructure(tt.pattern)
			result := optimizer.GetStaticPrefixFromStructure(structure)
			assert.Equal(t, tt.expectedPrefix, result, "prefix mismatch")
		})
	}
}

// TestPatternMatchingComprehensive - comprehensive table-driven test for prefix optimizer pattern analysis
// Tests regex pattern safety, optimization decisions, URL encoding, anchor behavior, and edge cases
func TestPatternMatchingComprehensive(t *testing.T) {
	t.Parallel()
	optimizer := NewPrefixOptimizer()

	testCases := []struct {
		name            string
		pattern         string
		testInputs      []string
		expectedMatches []bool
		shouldOptimize  bool
		expectedPrefix  string
		notes           string
	}{
		// Basic common routing patterns
		{
			name:            "Common user ID pattern",
			pattern:         "^users/\\d+/.*",
			testInputs:      []string{"users/123/profile", "users/456/settings", "users/abc/profile"},
			expectedMatches: []bool{true, true, false},
			shouldOptimize:  true,
			expectedPrefix:  "users/",
			notes:           "Common routing pattern with numeric IDs",
		},
		{
			name:            "Static asset path",
			pattern:         "^assets/images/.*",
			testInputs:      []string{"assets/images/logo.png", "assets/js/app.js", "assets/images/icon.svg"},
			expectedMatches: []bool{true, false, true},
			shouldOptimize:  true,
			expectedPrefix:  "assets/images/",
			notes:           "Static asset routing with deep prefix",
		},

		// Regex anchor behavior
		{
			name:            "Go $ anchor behavior - exact match",
			pattern:         "test$",
			testInputs:      []string{"test", "test\n", "test\r\n", "testing"},
			expectedMatches: []bool{true, false, false, false},
			shouldOptimize:  false,
			expectedPrefix:  "",
			notes:           "Go regex $ only matches at true end, not before newlines",
		},
		{
			name:            "Pattern with ^ and $ anchors",
			pattern:         "^logs$",
			testInputs:      []string{"logs", "logs\n", "logs\r\n", "mylogs", "logs.txt"},
			expectedMatches: []bool{true, false, false, false, false},
			shouldOptimize:  false,
			expectedPrefix:  "",
			notes:           "Exact match pattern - no prefix optimization possible",
		},

		// URL encoding scenarios
		{
			name:            "LF in object key via URL encoding",
			pattern:         "^test.*",
			testInputs:      []string{"test\nkey", "test\r\nkey", "test/key"},
			expectedMatches: []bool{true, true, true},
			shouldOptimize:  true,
			expectedPrefix:  "test",
			notes:           "URL-encoded LF should be handled correctly",
		},
		{
			name:            "CRLF in object key via URL encoding",
			pattern:         "^test.*",
			testInputs:      []string{"test\r\nkey", "test\nkey"},
			expectedMatches: []bool{true, true},
			shouldOptimize:  true,
			expectedPrefix:  "test",
			notes:           "URL-encoded CRLF should be handled correctly",
		},

		// Safe optimization patterns
		{
			name:            "Safe eat-all pattern",
			pattern:         "^logs/.*",
			testInputs:      []string{"logs/app.log", "logs/app\n.log", "logs/\napp.log"},
			expectedMatches: []bool{true, true, true},
			shouldOptimize:  true,
			expectedPrefix:  "logs/",
			notes:           "Pattern with .* eat-all is safe for optimization",
		},
		{
			name:            "Safe eat-some pattern",
			pattern:         "^data/.+",
			testInputs:      []string{"data/file.txt", "data/\n", "data/"},
			expectedMatches: []bool{true, false, false},
			shouldOptimize:  true,
			expectedPrefix:  "data/",
			notes:           "Pattern with .+ eat-some is safe for optimization",
		},
		{
			name:            "End-anchored pattern with constraints",
			pattern:         "^logs/[^/]+$",
			testInputs:      []string{"logs/app.log", "logs/app/sub.log", "logs/\n"},
			expectedMatches: []bool{true, false, true},
			shouldOptimize:  true,
			expectedPrefix:  "logs/",
			notes:           "End-anchored pattern is safe even with constraints",
		},

		// Unsafe optimization patterns
		{
			name:            "Unsafe pattern with constraints after prefix",
			pattern:         "^users/.*foo",
			testInputs:      []string{"users/john_foo", "users/jane_bar", "users/foo"},
			expectedMatches: []bool{true, false, true},
			shouldOptimize:  false,
			expectedPrefix:  "",
			notes:           "Pattern has constraints after prefix - not safe to optimize",
		},
		{
			name:            "Complex unsafe pattern",
			pattern:         "^assets/[^/]+/bar",
			testInputs:      []string{"assets/css/bar", "assets/js/foo", "assets/css/bar.txt"},
			expectedMatches: []bool{true, false, true},
			shouldOptimize:  false,
			expectedPrefix:  "",
			notes:           "Specific requirements after prefix make it unsafe",
		},

		// No anchor patterns
		{
			name:            "Pattern without start anchor",
			pattern:         "users/.*",
			testInputs:      []string{"users/john", "data/users/john", "myusers/john"},
			expectedMatches: []bool{true, true, true},
			shouldOptimize:  false,
			expectedPrefix:  "",
			notes:           "No ^ anchor means pattern can match anywhere - not optimizable",
		},

		// Newline-specific patterns
		{
			name:            "Pattern excluding newlines",
			pattern:         "^data/[^\n]+$",
			testInputs:      []string{"data/file.txt", "data/file\n.txt", "data/\nfile.txt"},
			expectedMatches: []bool{true, false, false},
			shouldOptimize:  true,
			expectedPrefix:  "data/",
			notes:           "Pattern explicitly excludes newlines but is end-anchored",
		},
		{
			name:            "Pattern requiring newline ending",
			pattern:         "^temp.*\\n$",
			testInputs:      []string{"temp.txt\n", "temp.txt", "temporary\n", "temp\nfile\n"},
			expectedMatches: []bool{true, false, true, false},
			shouldOptimize:  true,
			expectedPrefix:  "temp",
			notes:           "Pattern requires newline ending - safe with end anchor",
		},

		// Edge cases from previous issues
		{
			name:            "Complex named groups pattern",
			pattern:         "^files/(?P<category>[^/]+)/.*",
			testInputs:      []string{"files/docs/readme.txt", "files/docs/sub/file.txt", "files/"},
			expectedMatches: []bool{true, true, false},
			shouldOptimize:  true,
			expectedPrefix:  "files/",
			notes:           "Named groups with eat-all ending are safe",
		},
		{
			name:            "Alternation pattern at start",
			pattern:         "^(temp|cache)/.*",
			testInputs:      []string{"temp/file.txt", "cache/data.txt", "temporary/file.txt"},
			expectedMatches: []bool{true, true, false},
			shouldOptimize:  false,
			expectedPrefix:  "",
			notes:           "Alternation at start prevents static prefix extraction",
		},
		{
			name:            "Log file pattern without prefix",
			pattern:         "^.*\\.log$",
			testInputs:      []string{"app.log", "data/app.log", "app.txt"},
			expectedMatches: []bool{true, true, false},
			shouldOptimize:  false,
			expectedPrefix:  "",
			notes:           "No static prefix available - requires full scan",
		},
		{
			name:            "Log file pattern WITH prefix",
			pattern:         "^logs/.*\\.log$",
			testInputs:      []string{"logs/app.log", "logs/debug.log", "logs/app.txt"},
			expectedMatches: []bool{true, true, false},
			shouldOptimize:  true,
			expectedPrefix:  "logs/",
			notes:           "Pattern with prefix and suffix constraint - should be optimizable",
		},
		{
			name:            "JSON file pattern with prefix",
			pattern:         "^data/.*\\.json$",
			testInputs:      []string{"data/config.json", "data/temp.xml", "data/nested/data.json"},
			expectedMatches: []bool{true, false, true},
			shouldOptimize:  true,
			expectedPrefix:  "data/",
			notes:           "End-anchored pattern with file extension and prefix",
		},
		{
			name:            "Backup file pattern with prefix",
			pattern:         "^backups/.*\\.bak$",
			testInputs:      []string{"backups/db.bak", "backups/config.bak", "backups/temp.tmp"},
			expectedMatches: []bool{true, true, false},
			shouldOptimize:  true,
			expectedPrefix:  "backups/",
			notes:           "Another example of prefix + suffix pattern optimization",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test regex compilation
			regex, err := regexp.Compile(tc.pattern)
			assert.NoError(t, err)

			// Test pattern matching against inputs
			for i, input := range tc.testInputs {
				matches := regex.MatchString(input)
				assert.Equal(t, tc.expectedMatches[i], matches, "match mismatch for input %q: expected %v, got %v",
					input, tc.expectedMatches[i], matches)
			}

			// Test prefix optimization analysis
			route := config.RouteConfig{
				Path: regex,
			}
			analysis := optimizer.AnalyzeRoute(route)

			assert.Equal(t, tc.shouldOptimize, analysis.CanOptimize, "optimization mismatch: expected CanOptimize=%v, got %v",
				tc.shouldOptimize, analysis.CanOptimize)

			assert.Equal(t, tc.expectedPrefix, analysis.StaticPrefix, "prefix mismatch: expected %q, got %q",
				tc.expectedPrefix, analysis.StaticPrefix)

			// Log details for debugging
			t.Logf("Pattern: %q", tc.pattern)
			t.Logf("CanOptimize: %v, StaticPrefix: %q", analysis.CanOptimize, analysis.StaticPrefix)
			t.Logf("Notes: %s", tc.notes)
		})
	}
}
