package bucket

import (
	"regexp"
	"testing"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/template"
)

// BenchmarkPrefixOptimizer benchmarks the prefix optimization analysis
func BenchmarkPrefixOptimizer(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	// Test patterns of varying complexity
	patterns := []string{
		"^simple/path/.*",
		"^users/(?P<userid>[^/]+)/.*",
		"^complex/(?P<type>user|admin)/(?P<id>\\d+)/files/.*",
		"^(media|assets)/(?P<category>[^/]+)/(?P<filename>[^/]+)\\.(jpg|png|gif)$",
		"^.*\\.log$", // Requires full scan
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, pattern := range patterns {
			// Simulate a route for analysis
			analysis := optimizer.analyzePattern(pattern, nil)
			_ = analysis // Prevent optimization
		}
	}
}

// BenchmarkPrefixOptimizerCached benchmarks prefix optimization with caching
func BenchmarkPrefixOptimizerCached(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	routes := []config.RouteConfig{
		{
			Path:     regexp.MustCompile("^simple/path/.*"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$0")}},
		},
		{
			Path:     regexp.MustCompile("^users/(?P<userid>[^/]+)/.*"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("users/$userid")}},
		},
		{
			Path:     regexp.MustCompile(`^complex/(?P<type>user|admin)/(?P<id>\d+)/files/.*`),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$type/$id")}},
		},
	}

	// Warm up the cache
	for _, route := range routes {
		optimizer.AnalyzeRoute(route)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, route := range routes {
			analysis := optimizer.AnalyzeRoute(route)
			_ = analysis
		}
	}
}

// BenchmarkCanSkipRoute benchmarks the route skipping optimization
func BenchmarkCanSkipRoute(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	routes := []config.RouteConfig{
		{
			Path:     regexp.MustCompile("^users/(?P<userid>[^/]+)/.*"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$userid")}},
		},
		{
			Path:     regexp.MustCompile("^assets/.*"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$0")}},
		},
		{
			Path:     regexp.MustCompile(`^logs/.*\.log$`),
			Rewrites: nil,
		},
	}

	// Precompute analyses
	analyses := make([]*PrefixAnalysis, len(routes))
	for i, route := range routes {
		analyses[i] = optimizer.AnalyzeRoute(route)
	}

	prefixes := []string{
		"users/abc123/",
		"assets/images/",
		"logs/2024/",
		"other/path/",
		"",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, prefix := range prefixes {
			for j, route := range routes {
				skip := optimizer.CanSkipRoute(prefix, route, analyses[j])
				_ = skip
			}
		}
	}
}

// BenchmarkComputePhysicalPrefix benchmarks virtual to physical prefix transformation
func BenchmarkComputePhysicalPrefix(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	testCases := []struct {
		route         config.RouteConfig
		virtualPrefix string
	}{
		{
			route: config.RouteConfig{
				Path:     regexp.MustCompile("^foo/(?P<rest>.*)"),
				Rewrites: []config.RewriteRule{{Result: template.MustParse("$rest")}},
			},
			virtualPrefix: "foo/bar/baz/",
		},
		{
			route: config.RouteConfig{
				Path:     regexp.MustCompile("^virtual/(.*)"),
				Rewrites: []config.RewriteRule{{Result: template.MustParse("physical/$1")}},
			},
			virtualPrefix: "virtual/data/",
		},
		{
			route: config.RouteConfig{
				Path:     regexp.MustCompile("^data/(.*)"),
				Rewrites: nil,
			},
			virtualPrefix: "data/archive/2024/",
		},
	}

	// Precompute analyses
	analyses := make([]*PrefixAnalysis, len(testCases))
	for i, tc := range testCases {
		analyses[i] = optimizer.AnalyzeRoute(tc.route)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for j, tc := range testCases {
			physicalPrefix, ok := optimizer.ComputePhysicalPrefix(tc.virtualPrefix, analyses[j])
			_, _ = physicalPrefix, ok
		}
	}
}

// BenchmarkPatternTokenization benchmarks the pattern tokenization step
func BenchmarkPatternTokenization(b *testing.B) {
	patterns := []string{
		"^simple/path/.*",
		"^users/(?P<userid>[^/]+)/files/(?P<filename>[^/]+)$",
		"^complex/(?P<type>user|admin)/(?P<id>\\d+)/data/(.*)$",
		"^media/(?P<category>[^/]+)/(?P<subcategory>[^/]+)/(?P<filename>[^/]+)\\.(jpg|png|gif)$",
		"^a/b/c/d/e/f/g/h/i/j/.*",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, pattern := range patterns {
			segments := tokenizePattern(pattern)
			_ = segments
		}
	}
}

// BenchmarkSegmentClassification benchmarks classifying regex segments
func BenchmarkSegmentClassification(b *testing.B) {
	segments := []string{
		"simple",
		"(?P<userid>[^/]+)",
		"(?P<rest>.*)",
		"[^/]+",
		".*",
		".+",
		"(user|admin)",
		"\\d+",
		"(?P<filename>[^/]+)\\.(jpg|png|gif)",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, seg := range segments {
			ps := classifySegment(seg)
			_ = ps
		}
	}
}

// BenchmarkPrefixOptimizerParallel benchmarks concurrent access to the optimizer
func BenchmarkPrefixOptimizerParallel(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	routes := []config.RouteConfig{
		{
			Path:     regexp.MustCompile("^users/(?P<userid>[^/]+)/.*"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$userid")}},
		},
		{
			Path:     regexp.MustCompile("^assets/(.*)"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$1")}},
		},
		{
			Path:     regexp.MustCompile(`^logs/(?P<date>[^/]+)/.*\.log$`),
			Rewrites: nil,
		},
		{
			Path:     regexp.MustCompile("^data/(?P<type>[^/]+)/(?P<id>[^/]+)/.*"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$type/$id")}},
		},
	}

	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			route := routes[i%len(routes)]
			analysis := optimizer.AnalyzeRoute(route)
			_ = analysis
			i++
		}
	})
}

// BenchmarkGetOptimizedBackendPrefix benchmarks computing optimized backend prefixes
func BenchmarkGetOptimizedBackendPrefix(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	route := config.RouteConfig{
		Path:     regexp.MustCompile("^data/(?P<rest>.*)"),
		Rewrites: []config.RewriteRule{{Result: template.MustParse("$rest")}},
	}

	analysis := optimizer.AnalyzeRoute(route)

	virtualPrefixes := []string{
		"",
		"data/",
		"data/archive/",
		"data/archive/2024/",
		"data/archive/2024/01/",
	}

	backendPrefix := "backend-prefix/"

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, vp := range virtualPrefixes {
			result := optimizer.GetOptimizedBackendPrefix(vp, backendPrefix, analysis)
			_ = result
		}
	}
}

// BenchmarkTransformVirtualPrefix benchmarks transforming virtual prefixes
func BenchmarkTransformVirtualPrefix(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	routes := []config.RouteConfig{
		{
			Path:     regexp.MustCompile("^users/(?P<id>[^/]+)/.*"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("$id")}},
		},
		{
			Path:     regexp.MustCompile("^assets/(.*)"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("static/$1")}},
		},
	}

	analyses := make([]*PrefixAnalysis, len(routes))
	for i, route := range routes {
		analyses[i] = optimizer.AnalyzeRoute(route)
	}

	virtualPrefixes := []string{
		"users/123/",
		"assets/images/",
		"",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, vp := range virtualPrefixes {
			for j, route := range routes {
				result := optimizer.TransformVirtualPrefix(vp, route, analyses[j])
				_ = result
			}
		}
	}
}

// BenchmarkPatternStructureAnalysis benchmarks full pattern structure analysis
func BenchmarkPatternStructureAnalysis(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	patterns := []string{
		"^simple/.*",
		"^users/(?P<userid>[^/]+)/files/(?P<filename>[^/]+)$",
		"^a/b/c/d/e/f/g/h/i/j/.*",
		"^(?P<category>[^/]+)/(?P<subcategory>[^/]+)/(?P<item>[^/]+)/.*",
		"^data/(?P<year>\\d{4})/(?P<month>\\d{2})/(?P<day>\\d{2})/.*",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, pattern := range patterns {
			structure := optimizer.analyzePatternStructure(pattern)
			_ = structure
		}
	}
}

// BenchmarkGetStaticPrefixFromStructure benchmarks extracting static prefixes from structures
func BenchmarkGetStaticPrefixFromStructure(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	patterns := []string{
		"^simple/path/to/data/.*",
		"^users/(?P<userid>[^/]+)/.*",
		"^a/b/c/d/e/f/g/h/i/j/(?P<rest>.*)",
		"^test.*",
		"^(?P<category>[^/]+)/.*",
	}

	structures := make([]*PatternStructure, len(patterns))
	for i, pattern := range patterns {
		structures[i] = optimizer.analyzePatternStructure(pattern)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, structure := range structures {
			prefix := optimizer.GetStaticPrefixFromStructure(structure)
			_ = prefix
		}
	}
}

// BenchmarkFullRouteAnalysisPipeline benchmarks the complete route analysis pipeline
func BenchmarkFullRouteAnalysisPipeline(b *testing.B) {
	optimizer := NewPrefixOptimizer()

	routes := []config.RouteConfig{
		{
			Path:     regexp.MustCompile("^users/(?P<userid>[^/]+)/(?P<rest>.*)"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("user-data/$userid/$rest")}},
		},
		{
			Path:     regexp.MustCompile("^assets/(?P<type>[^/]+)/(.*)"),
			Rewrites: []config.RewriteRule{{Result: template.MustParse("static/$type/$1")}},
		},
		{
			Path:     regexp.MustCompile(`^logs/(?P<date>\d{4}-\d{2}-\d{2})/.*\.log$`),
			Rewrites: nil,
		},
	}

	virtualPrefixes := []string{
		"users/abc123/",
		"assets/images/",
		"logs/2024-01-15/",
		"other/path/",
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		for _, route := range routes {
			analysis := optimizer.AnalyzeRoute(route)

			for _, vp := range virtualPrefixes {
				// Check if route can be skipped
				skip := optimizer.CanSkipRoute(vp, route, analysis)
				if !skip {
					// Compute physical prefix
					physicalPrefix, _ := optimizer.ComputePhysicalPrefix(vp, analysis)
					_ = physicalPrefix
				}
			}
		}
	}
}
