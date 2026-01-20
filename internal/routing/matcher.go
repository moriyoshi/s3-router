package routing

import (
	"context"
	"fmt"
	"regexp"

	lru "github.com/hashicorp/golang-lru/v2"
	"go.opentelemetry.io/otel"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/template"
)

type Decision struct {
	Backend      *config.BackendConfig
	RewrittenKey string
	Metadata     map[string]any
}

type Matcher struct {
	buckets map[string]config.BucketConfig
	cache   *lru.Cache[string, *Decision]
}

func NewMatcher(cfg *config.Config, cacheSize int) (*Matcher, error) {
	var cache *lru.Cache[string, *Decision]
	if cacheSize > 0 {
		var err error
		cache, err = lru.New[string, *Decision](cacheSize)
		if err != nil {
			return nil, fmt.Errorf("failed to create LRU cache: %w", err)
		}
	}

	return &Matcher{
		buckets: cfg.Buckets,
		cache:   cache,
	}, nil
}

func (m *Matcher) Match(ctx context.Context, bucket, objectKey, method string, headers map[string][]string) (*Decision, error) {
	tracer := otel.Tracer("github.com/moriyoshi/s3-router/routing")
	_, span := tracer.Start(ctx, "Match")
	defer span.End()

	// Check if bucket exists
	br, exists := m.buckets[bucket]
	if !exists {
		return nil, fmt.Errorf("bucket %q not found", bucket)
	}

	// Check cache - include only referenced headers in cache key for header-based conditions
	cacheKey := fmt.Sprintf("%s:%s:%s", bucket, objectKey, method)
	if m.cache != nil {
		if decision, found := m.cache.Get(cacheKey); found {
			return decision, nil
		}
	}

	// Iterate routes in order
	for routeIdx, route := range br.Routes {
		// Match path
		matches := route.Path.FindStringSubmatchIndex(objectKey)
		if matches == nil {
			continue
		}

		// Rewrite object key
		rewrittenKey := m.rewrite(objectKey, route.Path, matches, route.Rewrites)

		decision := &Decision{
			Backend:      route.Backend,
			RewrittenKey: rewrittenKey,
			Metadata: map[string]any{
				"original_key": objectKey,
				"route_index":  routeIdx,
			},
		}

		// Cache result
		if m.cache != nil {
			m.cache.Add(cacheKey, decision)
		}

		return decision, nil
	}

	return nil, fmt.Errorf("no matching route for bucket %q key %q", bucket, objectKey)
}

func substituteCaptures(t *template.Template, captures map[string]string, matches []int, originalKey string) string {
	phs := new(template.Placeholders)
	phs.SetNamedMapBorrow(captures)
	// Replace numbered captures like $1, $2
	if len(matches) > 0 {
		indexed := make([]string, len(matches)/2+1)
		for i, j := 0, 0; i < len(matches); j++ {
			indexed[j] = originalKey[matches[i]:matches[i+1]]
			i += 2
		}
		phs.SetIndexedAllBorrow(indexed)
	}
	result, err := t.Execute(phs)
	if err != nil {
		panic(fmt.Sprintf("template execution failed: %v", err))
	}
	return result
}

func (m *Matcher) rewrite(objectKey string, pathRegex *regexp.Regexp, matches []int, rewrites []config.RewriteRule) string {
	key := objectKey

	// Extract named groups from initial match
	subexpNames := pathRegex.SubexpNames()
	captures := make(map[string]string)
	for i, name := range subexpNames {
		if i > 0 && i < len(matches)/2 && name != "" {
			startIdx := matches[2*i]
			endIdx := matches[2*i+1]
			if startIdx >= 0 && endIdx >= 0 {
				captures[name] = objectKey[startIdx:endIdx]
			}
		}
	}

	// Apply rewrites sequentially
	for _, rewrite := range rewrites {
		if rewrite.Pattern != nil {
			// Match against current key
			subMatches := rewrite.Pattern.FindStringSubmatchIndex(key)
			if subMatches == nil {
				continue
			}

			// Build capture map for this pattern
			subexpNames := rewrite.Pattern.SubexpNames()
			for i, name := range subexpNames {
				if i > 0 && i < len(subMatches)/2 && name != "" {
					startIdx := subMatches[2*i]
					endIdx := subMatches[2*i+1]
					if startIdx >= 0 && endIdx >= 0 {
						captures[name] = key[startIdx:endIdx]
					}
				}
			}

			// Apply template substitution with subMatches for numbered groups
			key = substituteCaptures(rewrite.Result, captures, subMatches, key)
		} else {
			// No pattern means direct substitution with original matches for numbered captures
			key = substituteCaptures(rewrite.Result, captures, matches, objectKey)
		}
	}

	return key
}

func (m *Matcher) InvalidateCache() {
	if m.cache != nil {
		m.cache.Purge()
	}
}
