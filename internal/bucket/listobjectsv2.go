package bucket

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/config"
)

// ListObjectsV2Handler handles ListObjectsV2 operations for virtual buckets
type ListObjectsV2Handler struct {
	backendMgr          *backend.Manager
	cfg                 *config.Config
	logger              *slog.Logger
	prefixOptimizer     *PrefixOptimizer
	concurrentProcessor *ConcurrentProcessor
}

// NewListObjectsV2Handler creates a new ListObjectsV2 handler with Phase 2 optimizations
func NewListObjectsV2Handler(backendMgr *backend.Manager, cfg *config.Config, logger *slog.Logger) *ListObjectsV2Handler {
	optimizer := NewPrefixOptimizer()

	handler := &ListObjectsV2Handler{
		backendMgr:      backendMgr,
		cfg:             cfg,
		logger:          logger,
		prefixOptimizer: optimizer,
	}

	// Create concurrent processor after handler is initialized
	handler.concurrentProcessor = NewConcurrentProcessor(handler, optimizer)

	return handler
}

// VirtualObject represents an object in the virtual namespace
type VirtualObject struct {
	VirtualKey   string
	PhysicalKey  string
	BackendID    string
	LastModified time.Time
	ETag         string
	Size         int64
	StorageClass string
}

// HandleListObjectsV2 processes a ListObjectsV2 request for a virtual bucket with Phase 2 optimizations
func (h *ListObjectsV2Handler) HandleListObjectsV2(ctx context.Context, bucketName string, params ListObjectsV2Params) (*S3ListObjectsV2Response, error) {
	// Get bucket routes
	bucketConfig, exists := h.cfg.Buckets[bucketName]
	if !exists {
		return nil, fmt.Errorf("virtual bucket %q not found", bucketName)
	}

	h.logger.Debug("starting ListObjectsV2 with Phase 2 optimizations",
		"bucket", bucketName,
		"prefix", params.Prefix,
		"route_count", len(bucketConfig.Routes),
	)

	// Phase 2: Use concurrent processing with prefix optimization
	allObjects, err := h.concurrentProcessor.ProcessRoutesParallel(ctx, bucketConfig.Routes, bucketName, params)
	if err != nil {
		return nil, fmt.Errorf("failed to process routes: %w", err)
	}

	h.logger.Debug("completed parallel route processing",
		"bucket", bucketName,
		"total_objects", len(allObjects),
	)

	// Sort objects by virtual key (still needed for final response)
	sort.Slice(allObjects, func(i, j int) bool {
		return allObjects[i].VirtualKey < allObjects[j].VirtualKey
	})

	// Apply virtual filtering and pagination
	return h.buildResponse(bucketName, params, allObjects)
}

// listObjectsFromRoute lists objects from a specific backend route (used by both Phase 1 and Phase 2)
func (h *ListObjectsV2Handler) listObjectsFromRoute(ctx context.Context, route config.RouteConfig, params ListObjectsV2Params) ([]VirtualObject, error) {
	// Get backend client
	backendClient, err := h.backendMgr.GetClient(route.Backend.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get backend client %q: %w", route.Backend.ID, err)
	}

	input := &s3.ListObjectsV2Input{
		Bucket:  aws.String(backendClient.Bucket),
		MaxKeys: aws.Int32(int32(params.MaxKeys)),
	}

	// Apply backend prefix if configured, combined with any virtual prefix from params
	backendPrefix := backendClient.Prefix
	if params.Prefix != "" {
		if backendPrefix != "" {
			input.Prefix = aws.String(backendPrefix + params.Prefix)
		} else {
			input.Prefix = aws.String(params.Prefix)
		}
	} else if backendPrefix != "" {
		input.Prefix = aws.String(backendPrefix)
	}

	// Note: Delimiter is NOT passed to the backend - we handle it locally
	// to properly aggregate objects across multiple backends
	// Note: StartAfter and ContinuationToken are NOT passed to individual backends
	// Pagination is handled after collecting and sorting all objects from all backends

	h.logger.Debug("listing objects from backend",
		"backend", route.Backend.ID,
		"bucket", backendClient.Bucket,
		"prefix", aws.ToString(input.Prefix),
		"max_keys", params.MaxKeys,
	)

	// Call S3 ListObjectsV2 (via S3Operations to enable circuit breaker)
	result, err := backendClient.S3Operations.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("S3 ListObjectsV2 failed for backend %q: %w", route.Backend.ID, err)
	}

	virtualObjects := make([]VirtualObject, 0, len(result.Contents))

	for _, obj := range result.Contents {
		physicalKey := aws.ToString(obj.Key)

		// Remove backend prefix
		if backendPrefix != "" {
			if !strings.HasPrefix(physicalKey, backendPrefix) {
				continue // Skip objects not under our backend prefix
			}
			physicalKey = strings.TrimPrefix(physicalKey, backendPrefix)
		}

		// Apply reverse rewrite to get virtual key
		virtualKey := h.reverseRewrite(physicalKey, route)

		// Check if the virtual key matches the route pattern
		// This ensures we only return objects that belong to this route
		if !route.Path.MatchString(virtualKey) {
			continue
		}

		virtualObjects = append(virtualObjects, VirtualObject{
			VirtualKey:   virtualKey,
			PhysicalKey:  aws.ToString(obj.Key),
			BackendID:    route.Backend.ID,
			LastModified: *obj.LastModified,
			ETag:         aws.ToString(obj.ETag),
			Size:         aws.ToInt64(obj.Size),
			StorageClass: string(obj.StorageClass),
		})
	}

	h.logger.Debug("route processing completed",
		"backend", route.Backend.ID,
		"raw_objects", len(result.Contents),
		"filtered_objects", len(virtualObjects),
	)

	return virtualObjects, nil
}

// buildResponse constructs the final S3ListObjectsV2Response with proper pagination support
//
//nolint:gocyclo
func (h *ListObjectsV2Handler) buildResponse(bucketName string, params ListObjectsV2Params, allObjects []VirtualObject) (*S3ListObjectsV2Response, error) {
	response := &S3ListObjectsV2Response{
		Name:           bucketName,
		Prefix:         params.Prefix,
		Delimiter:      params.Delimiter,
		MaxKeys:        params.MaxKeys,
		IsTruncated:    false,
		KeyCount:       0,
		Contents:       make([]S3Object, 0),
		CommonPrefixes: make([]S3CommonPrefix, 0),
	}

	// Set pagination parameters in response
	if params.StartAfter != "" {
		response.StartAfter = params.StartAfter
	}
	if params.ContinuationToken != "" {
		response.ContinuationToken = params.ContinuationToken
	}

	// Filter objects by virtual prefix first
	// This is necessary because backend queries may return more objects than match the virtual prefix
	prefixFiltered := h.filterByVirtualPrefix(allObjects, params.Prefix)

	// Extract common prefixes if delimiter is specified
	// This must happen BEFORE pagination to ensure proper grouping
	var commonPrefixes map[string]bool
	var objectsWithoutPrefixes []VirtualObject

	if params.Delimiter != "" {
		commonPrefixes = h.extractCommonPrefixes(prefixFiltered, params.Delimiter, params.Prefix)

		// Filter out objects that have common prefixes (they're represented by the prefix instead)
		for _, obj := range prefixFiltered {
			hasCommonPrefix := false
			for prefix := range commonPrefixes {
				if strings.HasPrefix(obj.VirtualKey, prefix) {
					hasCommonPrefix = true
					break
				}
			}
			if !hasCommonPrefix {
				objectsWithoutPrefixes = append(objectsWithoutPrefixes, obj)
			}
		}
	} else {
		objectsWithoutPrefixes = prefixFiltered
	}

	// Create a unified response list combining Contents and CommonPrefixes
	// This ensures MaxKeys applies to the total count
	type responseItem struct {
		isPrefix bool           // true if this is a common prefix
		object   S3Object       // valid if isPrefix=false
		prefix   S3CommonPrefix // valid if isPrefix=true
		sortKey  string         // for ordering and token generation
	}

	var unifiedList []responseItem

	// Add objects to unified list
	for _, obj := range objectsWithoutPrefixes {
		unifiedList = append(unifiedList, responseItem{
			isPrefix: false,
			object: S3Object{
				Key:          obj.VirtualKey,
				LastModified: obj.LastModified.Format(time.RFC3339),
				ETag:         obj.ETag,
				Size:         obj.Size,
				StorageClass: obj.StorageClass,
			},
			sortKey: obj.VirtualKey,
		})
	}

	// Add common prefixes to unified list (sorted and deduplicated)
	if len(commonPrefixes) > 0 {
		prefixList := make([]string, 0, len(commonPrefixes))
		for prefix := range commonPrefixes {
			prefixList = append(prefixList, prefix)
		}
		sort.Strings(prefixList) // Sort alphabetically

		for _, prefix := range prefixList {
			unifiedList = append(unifiedList, responseItem{
				isPrefix: true,
				prefix: S3CommonPrefix{
					Prefix: prefix,
				},
				sortKey: prefix,
			})
		}
	}

	// Sort unified list by sortKey
	sort.Slice(unifiedList, func(i, j int) bool {
		return unifiedList[i].sortKey < unifiedList[j].sortKey
	})

	// Apply pagination filtering AFTER delimiter grouping
	// This ensures continuation tokens point to items that actually exist in the response
	var paginatedItems []responseItem
	if len(unifiedList) > 0 {
		// Determine the effective start point for pagination
		// ContinuationToken takes precedence over StartAfter per AWS S3 API
		var startAfterKey string
		if params.ContinuationToken != "" {
			startAfterKey = params.ContinuationToken
		} else if params.StartAfter != "" {
			startAfterKey = params.StartAfter
		}

		// Find the starting index for pagination
		startIndex := 0
		if startAfterKey != "" {
			for i, item := range unifiedList {
				if item.sortKey > startAfterKey {
					startIndex = i
					break
				}
			}
			// If no item found after startAfterKey, start at end (no results)
			if startIndex == 0 && unifiedList[0].sortKey <= startAfterKey {
				startIndex = len(unifiedList)
			}
		}

		// Apply MaxKeys limit to the paginated list
		if startIndex < len(unifiedList) {
			endIndex := startIndex + params.MaxKeys
			if endIndex >= len(unifiedList) {
				paginatedItems = unifiedList[startIndex:]
			} else {
				response.IsTruncated = true
				paginatedItems = unifiedList[startIndex:endIndex]
				// NextContinuationToken should be the last item's key
				if len(paginatedItems) > 0 {
					response.NextContinuationToken = paginatedItems[len(paginatedItems)-1].sortKey
				}
			}
		}
	}

	// Populate response Contents and CommonPrefixes from paginatedItems
	for _, item := range paginatedItems {
		if item.isPrefix {
			response.CommonPrefixes = append(response.CommonPrefixes, item.prefix)
		} else {
			response.Contents = append(response.Contents, item.object)
		}
	}

	// KeyCount should be total items returned (Contents + CommonPrefixes)
	response.KeyCount = len(response.Contents) + len(response.CommonPrefixes)

	h.logger.Debug("built ListObjectsV2 response with pagination",
		"bucket", bucketName,
		"key_count", response.KeyCount,
		"object_count", len(response.Contents),
		"common_prefix_count", len(response.CommonPrefixes),
		"is_truncated", response.IsTruncated,
		"next_continuation_token", response.NextContinuationToken,
		"has_continuation_token", params.ContinuationToken != "",
		"has_start_after", params.StartAfter != "",
	)

	return response, nil
}

// extractCommonPrefixes extracts common prefixes from objects when a delimiter is specified
func (h *ListObjectsV2Handler) extractCommonPrefixes(objects []VirtualObject, delimiter, prefix string) map[string]bool {
	prefixes := make(map[string]bool)

	for _, obj := range objects {
		// Get the key relative to the prefix
		key := obj.VirtualKey
		if prefix != "" && strings.HasPrefix(key, prefix) {
			key = key[len(prefix):]
		}

		// Find the first occurrence of the delimiter
		delimiterIdx := strings.Index(key, delimiter)
		if delimiterIdx >= 0 {
			// Extract the common prefix (including the delimiter)
			commonPrefix := prefix + key[:delimiterIdx+len(delimiter)]
			prefixes[commonPrefix] = true
		}
	}

	return prefixes
}

// filterByVirtualPrefix filters objects to only include those matching the virtual prefix
func (h *ListObjectsV2Handler) filterByVirtualPrefix(objects []VirtualObject, prefix string) []VirtualObject {
	if prefix == "" {
		return objects
	}

	filtered := make([]VirtualObject, 0, len(objects))
	for _, obj := range objects {
		if strings.HasPrefix(obj.VirtualKey, prefix) {
			filtered = append(filtered, obj)
		}
	}

	h.logger.Debug("filtered objects by virtual prefix",
		"prefix", prefix,
		"input_count", len(objects),
		"output_count", len(filtered),
	)

	return filtered
}

// findStartIndex finds the index of the first object that comes after the given key
// Uses binary search for efficiency when dealing with large object lists
func (h *ListObjectsV2Handler) findStartIndex(objects []VirtualObject, startAfterKey string) int {
	// Binary search for the first object greater than startAfterKey
	left, right := 0, len(objects)

	for left < right {
		mid := left + (right-left)/2
		if objects[mid].VirtualKey <= startAfterKey {
			left = mid + 1
		} else {
			right = mid
		}
	}

	return left
}

// reverseRewrite applies reverse rewrite rules to convert physical key back to virtual key
func (h *ListObjectsV2Handler) reverseRewrite(physicalKey string, route config.RouteConfig) string {
	// Phase 2: Implement proper reverse transformation based on rewrite rules
	if len(route.Rewrites) > 0 {
		return h.applyReverseRewrites(physicalKey, route)
	}

	// For routes without rewrites, virtual key equals physical key
	return physicalKey
}

// applyReverseRewrites applies reverse transformation using route rewrite rules
// to convert a physical key back to its virtual key representation.
//
// The reverse rewrite handles common patterns:
// 1. Simple capture: path=^foo/(?P<rest>.*) result=$rest → prepend "foo/"
// 2. Prefix replacement: path=^bar/(.*) result=PREFIX/$1 → strip "PREFIX/", prepend "bar/"
// 3. Chained rewrites: apply in reverse order
func (h *ListObjectsV2Handler) applyReverseRewrites(physicalKey string, route config.RouteConfig) string {
	if len(route.Rewrites) == 0 {
		return physicalKey
	}

	// Extract static prefix from the path regex (the literal part before any metacharacters)
	staticPrefix := extractStaticPrefixFromRegex(route.Path.String())

	// Process rewrites in reverse order to undo the transformations
	currentKey := physicalKey
	for i := len(route.Rewrites) - 1; i >= 0; i-- {
		rewrite := route.Rewrites[i]
		currentKey = h.reverseRewriteRule(currentKey, rewrite, route, staticPrefix)
	}

	return currentKey
}

// reverseRewriteRule attempts to reverse a single rewrite rule
func (h *ListObjectsV2Handler) reverseRewriteRule(key string, rewrite config.RewriteRule, route config.RouteConfig, staticPrefix string) string {
	result := rewrite.Result

	// Use Analysis() to inspect template structure
	analysis := result.Analysis()

	// If result contains ${name:-default} style placeholders, optimization is not feasible
	// because we can't determine which captures were used
	if analysis.ContainsConditionals {
		// Fallback: prepend static prefix
		return staticPrefix + key
	}

	// Analyze the result template to understand the transformation
	// Common patterns:
	// 1. "$rest" or "$1" - captured group becomes the whole key
	// 2. "PREFIX/$1" or "PREFIX/$rest" - prefix added before capture
	// 3. "$1/SUFFIX" - suffix added after capture
	// 4. "$1/$2" or "$category/$file" - multiple captures (passthrough)
	// 5. "PREFIX/$1/$2" - prefix with multiple captures

	// Case 1: Result is just a capture variable (e.g., "$rest", "$1")
	// A single capture variable has: no prefix, exactly one placeholder, no tail
	if analysis.Prefix == "" && len(analysis.Rest) == 1 && analysis.Rest[0].Tail == "" {
		// The physical key IS the captured content
		// Reconstruct by prepending the static prefix from the path regex
		return staticPrefix + key
	}

	// Case 2: Result is only capture variables (e.g., "$1/$2", "$category/$file")
	// These are essentially passthrough - just prepend static prefix
	// Only capture variables: no prefix and no tail after last placeholder
	if len(analysis.Rest) > 0 && analysis.Prefix == "" && analysis.Rest[len(analysis.Rest)-1].Tail == "" {
		return staticPrefix + key
	}

	// Case 3: Result has a static prefix before captures (e.g., "PREFIX/$1", "PREFIX/$1/$2")
	if analysis.Prefix != "" {
		// Strip the prefix that was added during forward rewrite
		if strings.HasPrefix(key, analysis.Prefix) {
			strippedKey := strings.TrimPrefix(key, analysis.Prefix)
			return staticPrefix + strippedKey
		}
	}

	// Case 4: Result has a static suffix after the last capture (e.g., "$1/SUFFIX")
	if len(analysis.Rest) > 0 {
		suffixAfterCaptures := analysis.Rest[len(analysis.Rest)-1].Tail
		if suffixAfterCaptures != "" {
			// Strip the suffix that was added during forward rewrite
			if strings.HasSuffix(key, suffixAfterCaptures) {
				strippedKey := strings.TrimSuffix(key, suffixAfterCaptures)
				return staticPrefix + strippedKey
			}
		}
	}

	// Case 5: Complex pattern - try to use the rewrite pattern regex if available
	if rewrite.Pattern != nil {
		// The pattern was applied to intermediate key; this is harder to reverse
		// For now, prepend static prefix as best effort
		return staticPrefix + key
	}

	// Fallback: prepend static prefix
	return staticPrefix + key
}

// extractPrefixBeforeCapture extracts static prefix before a capture variable
// e.g., "SPECIAL/$1" returns "SPECIAL/"
func extractPrefixBeforeCapture(result string) string {
	// Find the first $ that starts a capture
	dollarIdx := strings.Index(result, "$")
	if dollarIdx <= 0 {
		return ""
	}

	prefix := result[:dollarIdx]
	remainder := result[dollarIdx:]

	// Check that what follows $ is a valid capture variable and nothing else
	if len(remainder) > 1 {
		varPart := remainder[1:]
		// Find end of variable name
		endIdx := 0
		for endIdx < len(varPart) && isAlphanumericChar(varPart[endIdx]) {
			endIdx++
		}
		// If there's nothing after the variable, this is prefix + capture
		if endIdx == len(varPart) {
			return prefix
		}
	}

	return ""
}

// extractSuffixAfterCapture extracts static suffix after a capture variable
// e.g., "$1/data" returns "/data"
func extractSuffixAfterCapture(result string) string {
	// Find the last $ that starts a capture
	dollarIdx := strings.LastIndex(result, "$")
	if dollarIdx < 0 || dollarIdx >= len(result)-1 {
		return ""
	}

	// Find end of variable name after $
	varStart := dollarIdx + 1
	endIdx := varStart
	for endIdx < len(result) && isAlphanumericChar(result[endIdx]) {
		endIdx++
	}

	// If there's content after the variable, that's the suffix
	if endIdx < len(result) {
		return result[endIdx:]
	}

	return ""
}

// extractStaticPrefixFromRegex extracts the literal prefix from a regex pattern
func extractStaticPrefixFromRegex(pattern string) string {
	// Remove anchors
	pattern = strings.TrimPrefix(pattern, "^")
	pattern = strings.TrimSuffix(pattern, "$")

	var prefix strings.Builder
	for _, r := range pattern {
		switch r {
		case '.', '*', '+', '?', '[', ']', '(', ')', '|', '^', '$', '{', '}', '\\':
			// Hit a metacharacter, return what we have
			return prefix.String()
		default:
			prefix.WriteRune(r)
		}
	}

	return prefix.String()
}

// isAlphanumericChar checks if a byte is alphanumeric or underscore
func isAlphanumericChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
