package bucket

import (
	"strings"
	"sync"

	"github.com/moriyoshi/s3-router/internal/config"
)

// SegmentType classifies what a path segment matches
type SegmentType int

const (
	// SegmentLiteral matches an exact string
	SegmentLiteral SegmentType = iota
	// SegmentSingleLevel matches exactly one directory level (e.g., [^/]*, [^/]+)
	SegmentSingleLevel
	// SegmentMultiLevel matches zero or more directory levels (e.g., .*, .+)
	SegmentMultiLevel
	// SegmentComplex matches other regex patterns
	SegmentComplex
)

// PathSegment represents a single segment in a tokenized pattern
type PathSegment struct {
	Type      SegmentType
	Value     string // original regex segment
	Literal   string // for Literal type, the actual string
	Optional  bool   // for SingleLevel: true if * (zero or more), false if + (one or more)
	Optional2 bool   // for MultiLevel: true if * (zero or more), false if + (one or more)
}

// PatternStructure represents the tokenized structure of a route pattern
type PatternStructure struct {
	Segments         []PathSegment
	StartsWithAnchor bool
	EndsWithAnchor   bool
	HasStaticPrefix  bool
	StaticPrefixLen  int // number of leading literal segments
}

// PrefixOptimizer analyzes regex patterns to extract static prefixes for efficient S3 queries
type PrefixOptimizer struct {
	cache map[string]*PrefixAnalysis
	mutex sync.RWMutex
}

// PrefixAnalysis contains the results of analyzing a route pattern for prefix optimization
type PrefixAnalysis struct {
	// StaticPrefix is the literal prefix that can be used in S3 ListObjects calls
	StaticPrefix string

	// CanOptimize indicates if this pattern allows for prefix optimization
	CanOptimize bool

	// RequiresFullScan indicates if we need to scan all objects (no prefix optimization possible)
	RequiresFullScan bool

	// RequiresNonEmptyRemainder indicates the pattern uses + instead of * after the static prefix,
	// meaning keys must have at least one character after the static prefix to match.
	// For example, ^foo/(.+) requires "foo/x" but won't match "foo/"
	RequiresNonEmptyRemainder bool

	// HasTrivialRewrite indicates the rewrite is simple enough to compute physical prefix
	// from virtual prefix (e.g., result is just "$rest" or "$1")
	HasTrivialRewrite bool

	// RewriteResultPrefix is the static prefix in the rewrite result (e.g., "PREFIX" from "PREFIX/$1")
	RewriteResultPrefix string
}

// NewPrefixOptimizer creates a new prefix optimizer with caching
func NewPrefixOptimizer() *PrefixOptimizer {
	return &PrefixOptimizer{
		cache: make(map[string]*PrefixAnalysis),
	}
}

// buildCacheKey creates a cache key from pattern and rewrites
func buildCacheKey(pattern string, rewrites []config.RewriteRule) string {
	if len(rewrites) == 0 {
		return pattern
	}
	// Include rewrite info in the key
	var b strings.Builder
	b.WriteString(pattern)
	for _, r := range rewrites {
		b.WriteByte('\x00') // separator
		if r.Pattern != nil {
			b.WriteString(r.Pattern.String())
		}
		b.WriteByte('\x00')
		if r.Result != nil {
			b.WriteString(r.Result.String())
		}
	}
	return b.String()
}

// AnalyzeRoute analyzes a route pattern to determine optimization opportunities
func (p *PrefixOptimizer) AnalyzeRoute(route config.RouteConfig) *PrefixAnalysis {
	cacheKey := buildCacheKey(route.Path.String(), route.Rewrites)

	// Check cache first
	p.mutex.RLock()
	if analysis, exists := p.cache[cacheKey]; exists {
		p.mutex.RUnlock()
		return analysis
	}
	p.mutex.RUnlock()

	// Perform analysis
	analysis := p.analyzePattern(route.Path.String(), route.Rewrites)

	// Cache result
	p.mutex.Lock()
	p.cache[cacheKey] = analysis
	p.mutex.Unlock()

	return analysis
}

// analyzePattern performs the core pattern analysis logic
func (p *PrefixOptimizer) analyzePattern(pattern string, rewrites []config.RewriteRule) *PrefixAnalysis {
	analysis := &PrefixAnalysis{}

	// Analyze pattern structure using tokenization
	structure := p.analyzePatternStructure(pattern)

	// Check if pattern is properly anchored at the beginning
	if !strings.HasPrefix(pattern, "^") {
		// Pattern is not anchored - it can match anywhere in the string
		// We cannot safely do prefix optimization
		analysis.CanOptimize = false
		analysis.RequiresFullScan = true
		analysis.StaticPrefix = ""
		// Still analyze rewrites for trivial patterns
		p.analyzeRewrites(analysis, rewrites)
		return analysis
	}

	// Remove start anchor for analysis (we've verified it exists)
	cleanPattern := pattern[1:]

	// Check if pattern ends with $ (but don't remove it yet)
	endsWithAnchor := strings.HasSuffix(cleanPattern, "$")

	// Remove end anchor if present for prefix extraction
	if endsWithAnchor {
		cleanPattern = cleanPattern[:len(cleanPattern)-1]
	}

	// Use structure-based static prefix extraction (handles all cases through tokenization)
	staticPrefix := p.GetStaticPrefixFromStructure(structure)

	// Check if this pattern can be safely optimized with prefix queries using structure
	// This checks if segments after the static prefix are safe (only wildcards, no constraints)
	canSafelyOptimize, requiresNonEmpty := p.canSafelyOptimizeWithPrefix(structure, structure.StaticPrefixLen)

	// Determine if we can optimize
	if len(staticPrefix) > 0 && canSafelyOptimize {
		analysis.CanOptimize = true
		analysis.RequiresFullScan = false
		analysis.StaticPrefix = staticPrefix
		analysis.RequiresNonEmptyRemainder = requiresNonEmpty
	} else {
		// Check if pattern allows any optimization
		analysis.CanOptimize = false
		analysis.RequiresFullScan = p.requiresFullScan(cleanPattern)
	}

	// Analyze rewrites for trivial patterns that allow physical prefix computation
	p.analyzeRewrites(analysis, rewrites)

	return analysis
}

// requiresFullScan determines if a pattern requires scanning all objects
func (p *PrefixOptimizer) requiresFullScan(pattern string) bool {
	// Patterns that start with wildcards or complex regex require full scan
	if len(pattern) == 0 {
		return true
	}

	firstChar := pattern[0]
	return firstChar == '.' || firstChar == '*' || firstChar == '+' ||
		firstChar == '?' || firstChar == '[' || firstChar == '(' || firstChar == '|'
}

// canSafelyOptimizeWithPrefix checks if a pattern can be safely optimized with prefix queries
// using the tokenized pattern structure.
// Safe patterns have: static prefix followed by wildcards that consume to the end
// Returns:
//   - canOptimize: whether prefix optimization is possible
//   - requiresNonEmpty: whether the pattern requires at least one char after static prefix
//
// Examples:
//
//	^users/.*       ✅ canOptimize=true, requiresNonEmpty=false
//	^users/.+       ✅ canOptimize=true, requiresNonEmpty=true
//	^users/[^/]+$   ✅ canOptimize=true, requiresNonEmpty=true (end-anchored)
//	^logs/.*\.log$  ✅ canOptimize=true (end-anchored, has constraints but safe)
//	^users/.*foo    ❌ canOptimize=false (constraint after wildcard, not end-anchored)
func (p *PrefixOptimizer) canSafelyOptimizeWithPrefix(structure *PatternStructure, staticPrefixLen int) (canOptimize bool, requiresNonEmpty bool) {
	// Check for exact match patterns first (all literal segments, end-anchored)
	// e.g., ^logs$ or ^foo/bar$ - these shouldn't use prefix optimization
	// because prefix "logs" would also match "logs.txt", "logsfoo", etc.
	if len(structure.Segments) == staticPrefixLen && structure.EndsWithAnchor && staticPrefixLen > 0 {
		// All segments are literal and pattern is end-anchored = exact match
		return false, false
	}

	// Handle case where there's no leading literal segments but first segment has a literal prefix
	// E.g., ^test.* tokenizes as ["test.*"] with staticPrefixLen=0 but Literal="test"
	hasComplexWithPrefix := false
	if staticPrefixLen == 0 && len(structure.Segments) > 0 {
		firstSeg := structure.Segments[0]
		if firstSeg.Type == SegmentComplex && firstSeg.Literal != "" {
			hasComplexWithPrefix = true
			// For end-anchored patterns with complex segment, it's safe
			if structure.EndsWithAnchor {
				requiresNonEmpty = strings.Contains(firstSeg.Value, ".+")
				return true, requiresNonEmpty
			}
			// For non-anchored, check if remainder is a simple wildcard
			remainder := firstSeg.Value[len(firstSeg.Literal):]
			if p.isSimpleWildcard(remainder) {
				return true, strings.HasPrefix(remainder, ".+")
			}
		}
	}

	// End-anchored patterns with a static prefix are always safe for optimization
	// because the anchor constrains matching to exact patterns
	if structure.EndsWithAnchor && staticPrefixLen > 0 {
		// Check for + patterns that require at least one character
		requiresNonEmpty = p.structureRequiresNonEmpty(structure, staticPrefixLen)
		return true, requiresNonEmpty
	}

	// No static prefix and no complex segment with literal prefix
	if staticPrefixLen == 0 && !hasComplexWithPrefix {
		return false, false // No static prefix to optimize with
	}

	// This case is handled above now, but keep for non-anchored exact patterns
	if len(structure.Segments) <= staticPrefixLen {
		// Pattern ends exactly at static prefix but not end-anchored
		// This is a prefix-only pattern, can optimize
		return true, false
	}

	// Check segments after the static prefix
	remainingSegments := structure.Segments[staticPrefixLen:]

	// Check if all remaining segments form a safe wildcard pattern
	return p.areSegmentsSafeForOptimization(remainingSegments)
}

// structureRequiresNonEmpty checks if any segment after static prefix requires at least one char
func (p *PrefixOptimizer) structureRequiresNonEmpty(structure *PatternStructure, staticPrefixLen int) bool {
	if staticPrefixLen >= len(structure.Segments) {
		return false
	}

	for _, seg := range structure.Segments[staticPrefixLen:] {
		switch seg.Type {
		case SegmentMultiLevel:
			if !seg.Optional2 { // .+ requires at least one
				return true
			}
		case SegmentSingleLevel:
			if !seg.Optional { // [^/]+ requires at least one
				return true
			}
		case SegmentComplex:
			// Check if it contains + patterns
			if strings.Contains(seg.Value, ".+") || strings.Contains(seg.Value, "[^/]+") {
				return true
			}
		}
	}
	return false
}

// isSimpleWildcard checks if a pattern is a simple wildcard that consumes everything
func (p *PrefixOptimizer) isSimpleWildcard(s string) bool {
	simplePatterns := []string{".*", ".+", "(.*)", "(.+)"}
	for _, pat := range simplePatterns {
		if s == pat || strings.HasSuffix(s, pat) {
			return true
		}
	}
	return false
}

// areSegmentsSafeForOptimization checks if segments form a safe pattern for prefix optimization
func (p *PrefixOptimizer) areSegmentsSafeForOptimization(segments []PathSegment) (canOptimize bool, requiresNonEmpty bool) {
	if len(segments) == 0 {
		return true, false
	}

	// For non-anchored patterns, we need segments that consume to end
	// Safe patterns: MultiLevel, SingleLevel, or Complex ending with wildcard

	lastSeg := segments[len(segments)-1]

	switch lastSeg.Type {
	case SegmentMultiLevel:
		return true, !lastSeg.Optional2
	case SegmentSingleLevel:
		return true, !lastSeg.Optional
	case SegmentComplex:
		// Check if it ends with a wildcard pattern
		if p.isSimpleWildcard(lastSeg.Value) || strings.HasSuffix(lastSeg.Value, ".*") || strings.HasSuffix(lastSeg.Value, ".+") {
			return true, strings.Contains(lastSeg.Value, ".+")
		}
	}

	return false, false
}

// analyzeRewrites analyzes rewrite rules to determine if they allow trivial prefix transformation
// Trivial rewrites are simple patterns like:
//   - result: "$rest" or "$1" - the captured group becomes the physical key directly
//   - result: "PREFIX/$rest" - a static prefix is prepended to the captured group
func (p *PrefixOptimizer) analyzeRewrites(analysis *PrefixAnalysis, rewrites []config.RewriteRule) {
	if len(rewrites) == 0 {
		// No rewrites means virtual key = physical key, which is trivially transformable
		analysis.HasTrivialRewrite = true
		analysis.RewriteResultPrefix = ""
		return
	}

	// For now, only handle single rewrite rule (most common case)
	if len(rewrites) != 1 {
		analysis.HasTrivialRewrite = false
		return
	}

	rewrite := rewrites[0]

	// Skip rewrites with their own pattern regex (chained rewrites are complex)
	if rewrite.Pattern != nil {
		analysis.HasTrivialRewrite = false
		return
	}

	result := rewrite.Result
	if result == nil {
		analysis.HasTrivialRewrite = false
		return
	}

	// Use Analysis() to inspect template structure
	tplAnalysis := result.Analysis()

	// Skip if result contains ${name:-default} style placeholders (optimization not feasible)
	if tplAnalysis.ContainsConditionals {
		analysis.HasTrivialRewrite = false
		return
	}

	// Case 1: Result is just a capture variable (e.g., "$rest", "$1")
	// This means: virtual "foo/bar" with pattern ^foo/(?P<rest>.*) → physical "bar"
	// A single capture variable has: no prefix, exactly one placeholder, no tail
	if tplAnalysis.Prefix == "" && len(tplAnalysis.Rest) == 1 && tplAnalysis.Rest[0].Tail == "" {
		analysis.HasTrivialRewrite = true
		analysis.RewriteResultPrefix = ""
		return
	}

	// Case 2: Result is "PREFIX/$var" - static prefix before capture
	// This means: virtual "foo/bar" → physical "PREFIX/bar"
	if tplAnalysis.Prefix != "" {
		// Check if the rest after prefix is just capture variable(s)
		// For now, assume all trivial patterns with prefix are valid
		analysis.HasTrivialRewrite = true
		analysis.RewriteResultPrefix = tplAnalysis.Prefix
		return
	}

	// Complex rewrite pattern
	analysis.HasTrivialRewrite = false
}

// ComputePhysicalPrefix computes the physical prefix from a virtual prefix for backend queries.
// Returns the physical prefix and whether the computation was successful.
// If the route has trivial rewrites, we can compute an exact physical prefix.
// Otherwise, we return empty string (query with just backend prefix).
func (p *PrefixOptimizer) ComputePhysicalPrefix(virtualPrefix string, analysis *PrefixAnalysis) (physicalPrefix string, ok bool) {
	if virtualPrefix == "" {
		// No virtual prefix
		// If we have a trivial rewrite with a result prefix, use that as the physical prefix
		if analysis.HasTrivialRewrite && analysis.RewriteResultPrefix != "" {
			return analysis.RewriteResultPrefix, true
		}
		// Otherwise, use empty physical prefix (backend prefix will be added)
		return "", true
	}

	// Check if virtual prefix matches the route's static prefix
	if analysis.StaticPrefix != "" {
		if !strings.HasPrefix(virtualPrefix, analysis.StaticPrefix) &&
			!strings.HasPrefix(analysis.StaticPrefix, virtualPrefix) {
			// Virtual prefix doesn't match this route at all
			return "", false
		}
	}

	// If we have trivial rewrite, we can compute the physical prefix
	if analysis.HasTrivialRewrite {
		if analysis.StaticPrefix == "" {
			// No static prefix means passthrough pattern (e.g., "(.*)" with no rewrite)
			// Virtual prefix equals physical prefix
			return analysis.RewriteResultPrefix + virtualPrefix, true
		}
		if strings.HasPrefix(virtualPrefix, analysis.StaticPrefix) {
			// Strip the virtual static prefix and add the rewrite result prefix
			remainder := strings.TrimPrefix(virtualPrefix, analysis.StaticPrefix)
			return analysis.RewriteResultPrefix + remainder, true
		}
		// Virtual prefix is a prefix of the static prefix (e.g., "fo" for route "^foo/...")
		// We can't optimize in this case
		return "", true
	}

	// Non-trivial rewrite - can't compute physical prefix
	return "", true
}

// TransformVirtualPrefix converts a virtual prefix to a physical prefix for a specific route
func (p *PrefixOptimizer) TransformVirtualPrefix(virtualPrefix string, route config.RouteConfig, analysis *PrefixAnalysis) string {
	if virtualPrefix == "" {
		return analysis.StaticPrefix
	}

	// If we have a static prefix and the virtual prefix starts with it, we can optimize
	if analysis.StaticPrefix != "" && strings.HasPrefix(virtualPrefix, analysis.StaticPrefix) {
		// Virtual prefix aligns with static prefix, use as-is
		return virtualPrefix
	}

	// For complex patterns, try to apply reverse logic
	// This is a simplified version - in a full implementation, we'd need to
	// analyze the rewrite rules to properly transform virtual to physical prefixes
	if analysis.CanOptimize {
		return analysis.StaticPrefix + virtualPrefix
	}

	// Fallback: use static prefix only
	return analysis.StaticPrefix
}

// CanSkipRoute determines if a route can be completely skipped for a given virtual prefix
func (p *PrefixOptimizer) CanSkipRoute(virtualPrefix string, route config.RouteConfig, analysis *PrefixAnalysis) bool {
	if analysis.RequiresFullScan {
		return false // Never skip if we need full scan
	}

	if !analysis.CanOptimize {
		return false // Can't optimize, so can't skip
	}

	if analysis.StaticPrefix == "" {
		return false // No static prefix to compare against
	}

	// If virtual prefix doesn't start with the route's static prefix,
	// this route definitely won't match
	return virtualPrefix != "" && !strings.HasPrefix(virtualPrefix, analysis.StaticPrefix) &&
		!strings.HasPrefix(analysis.StaticPrefix, virtualPrefix)
}

// GetOptimizedBackendPrefix returns the optimal prefix to use for backend S3 queries
func (p *PrefixOptimizer) GetOptimizedBackendPrefix(virtualPrefix, backendPrefix string, analysis *PrefixAnalysis) string {
	physicalPrefix := analysis.StaticPrefix

	if virtualPrefix != "" {
		if analysis.CanOptimize && strings.HasPrefix(virtualPrefix, analysis.StaticPrefix) {
			// Use the virtual prefix directly if it aligns
			physicalPrefix = virtualPrefix
		} else if analysis.StaticPrefix != "" {
			// Use static prefix only
			physicalPrefix = analysis.StaticPrefix
		} else {
			// No optimization possible, use virtual prefix as fallback
			physicalPrefix = virtualPrefix
		}
	}

	// Combine with backend prefix
	if backendPrefix != "" {
		return backendPrefix + physicalPrefix
	}

	return physicalPrefix
}

// tokenizePattern splits a regex pattern into path segments
// Examples:
//
//	^foo/bar/baz → ["foo", "bar", "baz"]
//	^foo/([^/]+)/bar → ["foo", "([^/]+)", "bar"]
//	^foo/bar/([^/]+)/baz/.* → ["foo", "bar", "([^/]+)", "baz", ".*"]
func tokenizePattern(pattern string) []string {
	// Remove leading ^ and trailing $
	p := pattern
	p = strings.TrimPrefix(p, "^")
	p = strings.TrimSuffix(p, "$")

	// Split by /, but keep regex groups intact
	var segments []string
	var current strings.Builder
	inGroup := 0

	for i := 0; i < len(p); i++ {
		ch := p[i]
		if ch == '(' {
			inGroup++
			current.WriteByte(ch)
		} else if ch == ')' {
			inGroup--
			current.WriteByte(ch)
		} else if ch == '/' && inGroup == 0 {
			// Found a segment separator
			seg := current.String()
			if seg != "" {
				segments = append(segments, seg)
			}
			current.Reset()
		} else {
			current.WriteByte(ch)
		}
	}

	// Don't forget the last segment
	if current.Len() > 0 {
		segments = append(segments, current.String())
	}

	return segments
}

// classifySegment determines what type of segment this is
func classifySegment(segment string) PathSegment {
	ps := PathSegment{Value: segment}

	// Check for multi-level patterns (match across /)
	if segment == ".*" {
		ps.Type = SegmentMultiLevel
		ps.Optional2 = true
		return ps
	}
	if segment == ".+" {
		ps.Type = SegmentMultiLevel
		ps.Optional2 = false
		return ps
	}
	if segment == "(.*)" || segment == "(?P<rest>.*)" {
		ps.Type = SegmentMultiLevel
		ps.Optional2 = true
		return ps
	}
	if strings.HasSuffix(segment, ".*)") {
		ps.Type = SegmentMultiLevel
		ps.Optional2 = true
		return ps
	}
	if segment == "(.+)" || strings.HasSuffix(segment, ".+)") {
		ps.Type = SegmentMultiLevel
		ps.Optional2 = false
		return ps
	}

	// Check for single-level patterns (match within one /)
	if segment == "[^/]*" {
		ps.Type = SegmentSingleLevel
		ps.Optional = true
		return ps
	}
	if segment == "[^/]+" {
		ps.Type = SegmentSingleLevel
		ps.Optional = false
		return ps
	}
	if segment == "([^/]*)" {
		ps.Type = SegmentSingleLevel
		ps.Optional = true
		return ps
	}
	if segment == "([^/]+)" {
		ps.Type = SegmentSingleLevel
		ps.Optional = false
		return ps
	}
	// Named captures like (?P<name>[^/]*) or (?P<name>[^/]+)
	if strings.HasPrefix(segment, "(?P<") && strings.HasSuffix(segment, "[^/]*)") {
		ps.Type = SegmentSingleLevel
		ps.Optional = true
		return ps
	}
	if strings.HasPrefix(segment, "(?P<") && strings.HasSuffix(segment, "[^/]+)") {
		ps.Type = SegmentSingleLevel
		ps.Optional = false
		return ps
	}

	// Check if it's a literal (no regex metacharacters)
	if !hasRegexMetachars(segment) {
		ps.Type = SegmentLiteral
		ps.Literal = segment
		return ps
	}

	// For complex patterns, try to extract a literal prefix
	// e.g., "test.*" → literal="test", type=Complex
	// This handles cases like ^test.* where there's a static prefix before the regex
	literalPrefix := extractLiteralPrefix(segment)
	ps.Type = SegmentComplex
	ps.Literal = literalPrefix
	return ps
}

// extractLiteralPrefix extracts the literal string before the first regex metacharacter
// e.g., "test.*" → "test", "foo[a-z]+" → "foo"
func extractLiteralPrefix(s string) string {
	metachars := ".*+?[](){}|^$\\"
	for i, ch := range s {
		if strings.ContainsRune(metachars, ch) {
			return s[:i]
		}
	}
	return s // no metacharacters found (shouldn't happen if called from classifySegment)
}

// hasRegexMetachars checks if a string has regex special characters
func hasRegexMetachars(s string) bool {
	metachars := ".*+?[](){}|^$\\"
	for _, ch := range s {
		if strings.ContainsRune(metachars, ch) {
			return true
		}
	}
	return false
}

// analyzePatternStructure tokenizes and classifies a pattern
func (p *PrefixOptimizer) analyzePatternStructure(pattern string) *PatternStructure {
	ps := &PatternStructure{
		StartsWithAnchor: strings.HasPrefix(pattern, "^"),
		EndsWithAnchor:   strings.HasSuffix(pattern, "$"),
	}

	segments := tokenizePattern(pattern)
	for _, seg := range segments {
		ps.Segments = append(ps.Segments, classifySegment(seg))
	}

	// Count leading literal segments
	for i, seg := range ps.Segments {
		if seg.Type == SegmentLiteral {
			ps.StaticPrefixLen = i + 1
		} else {
			break
		}
	}

	ps.HasStaticPrefix = ps.StaticPrefixLen > 0

	return ps
}

// GetStaticPrefixFromStructure extracts static prefix from pattern structure
// Handles both leading literal segments and literal prefixes of complex segments
func (p *PrefixOptimizer) GetStaticPrefixFromStructure(structure *PatternStructure) string {
	if len(structure.Segments) == 0 {
		return ""
	}

	// If we have leading literals, use that count
	if structure.HasStaticPrefix {
		var prefix strings.Builder
		for i := 0; i < structure.StaticPrefixLen; i++ {
			if i > 0 {
				prefix.WriteString("/")
			}
			prefix.WriteString(structure.Segments[i].Literal)
		}
		prefix.WriteString("/")
		return prefix.String()
	}

	// If no leading literals, check if first segment is complex with a literal prefix
	// This handles patterns like ^test.* which tokenize as single segment ["test.*"]
	if len(structure.Segments) > 0 {
		firstSeg := structure.Segments[0]
		if firstSeg.Type == SegmentComplex && firstSeg.Literal != "" {
			// Return the literal prefix as-is (no trailing /)
			// because this segment doesn't have a / separator
			return firstSeg.Literal
		}
	}

	return ""
}
