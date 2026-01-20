package bucket

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/moriyoshi/s3-router/internal/config"
)

// ConcurrentProcessor handles parallel backend queries for ListObjectsV2 operations
type ConcurrentProcessor struct {
	handler   *ListObjectsV2Handler
	optimizer *PrefixOptimizer
}

// NewConcurrentProcessor creates a new concurrent processor
func NewConcurrentProcessor(handler *ListObjectsV2Handler, optimizer *PrefixOptimizer) *ConcurrentProcessor {
	return &ConcurrentProcessor{
		handler:   handler,
		optimizer: optimizer,
	}
}

// RouteResult contains the result of processing a single route
type RouteResult struct {
	RouteIndex int
	Objects    []VirtualObject
	Error      error
	BackendID  string
	Duration   time.Duration
}

// ProcessRoutesParallel processes multiple routes concurrently and returns aggregated results
func (cp *ConcurrentProcessor) ProcessRoutesParallel(ctx context.Context, routes []config.RouteConfig, bucketName string, params ListObjectsV2Params) ([]VirtualObject, error) {
	if len(routes) == 0 {
		return []VirtualObject{}, nil
	}

	// Create channels for coordination
	resultsChan := make(chan RouteResult, len(routes))

	// Use a wait group to track completion
	var wg sync.WaitGroup

	// Create a context with timeout for safety
	ctx, cancel := context.WithTimeout(ctx, time.Minute*2)
	defer cancel()

	cp.handler.logger.Debug("starting parallel route processing",
		"route_count", len(routes),
		"bucket", bucketName,
		"prefix", params.Prefix,
	)

	// Launch goroutines for each route
	for i, route := range routes {
		wg.Add(1)
		go cp.processRouteWorker(ctx, i, route, params, resultsChan, &wg)
	}

	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	var allObjects []VirtualObject
	var errors []error
	routeCount := 0
	totalDuration := time.Duration(0)

	// Process results as they come in
outer:
	for {
		select {
		case result, ok := <-resultsChan:
			if !ok {
				break outer
			}
			routeCount++
			totalDuration += result.Duration

			if result.Error != nil {
				errors = append(errors, fmt.Errorf("route %d (backend %s): %w",
					result.RouteIndex, result.BackendID, result.Error))
			} else {
				allObjects = append(allObjects, result.Objects...)
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	cp.handler.logger.Debug("parallel processing completed",
		"processed_routes", routeCount,
		"total_objects", len(allObjects),
		"errors", len(errors),
		"average_duration", totalDuration/time.Duration(routeCount),
	)

	// If all routes failed, return an error
	if len(errors) > 0 && len(allObjects) == 0 {
		return nil, fmt.Errorf("all %d routes failed: %v", len(errors), errors)
	}

	// Log errors but continue if we got some results
	if len(errors) > 0 {
		cp.handler.logger.Warn("some routes failed but continuing",
			"failed_routes", len(errors),
			"successful_objects", len(allObjects),
		)
	}

	return allObjects, nil
}

// processRouteWithOptimization processes a single route with prefix optimization
func (cp *ConcurrentProcessor) processRouteWithOptimization(ctx context.Context, route config.RouteConfig, params ListObjectsV2Params, analysis *PrefixAnalysis) ([]VirtualObject, error) {
	// Get backend client
	backendClient, err := cp.handler.backendMgr.GetClient(route.Backend.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get backend client %q: %w", route.Backend.ID, err)
	}

	// Create optimized parameters for backend query
	optimizedParams := cp.createOptimizedParams(params, backendClient.Prefix, analysis)

	cp.handler.logger.Debug("backend query optimization",
		"backend", route.Backend.ID,
		"original_prefix", params.Prefix,
		"optimized_prefix", optimizedParams.Prefix,
		"can_optimize", analysis.CanOptimize,
		"static_prefix", analysis.StaticPrefix,
	)

	// Use the existing listObjectsFromRoute method but with optimized parameters
	return cp.handler.listObjectsFromRoute(ctx, route, optimizedParams)
}

// createOptimizedParams creates optimized S3 query parameters based on prefix analysis.
// For trivial rewrites, it computes the physical prefix from the virtual prefix.
// For complex rewrites, it returns empty prefix (backend prefix will be added by listObjectsFromRoute).
func (cp *ConcurrentProcessor) createOptimizedParams(params ListObjectsV2Params, backendPrefix string, analysis *PrefixAnalysis) ListObjectsV2Params {
	optimizedParams := params // Copy

	// Try to compute physical prefix from virtual prefix
	// For trivial rewrites like "$rest", we can transform the virtual prefix to physical prefix
	// Example: route ^foo/(?P<rest>.*) with rewrite $rest
	//   - Virtual prefix "foo/bar/" → Physical prefix "bar/"
	//   - Virtual prefix "" → Physical prefix ""
	if physicalPrefix, ok := cp.optimizer.ComputePhysicalPrefix(params.Prefix, analysis); ok && analysis.HasTrivialRewrite {
		optimizedParams.Prefix = physicalPrefix
		cp.handler.logger.Debug("computed physical prefix from virtual prefix",
			"virtual_prefix", params.Prefix,
			"physical_prefix", physicalPrefix,
			"static_prefix", analysis.StaticPrefix,
			"rewrite_result_prefix", analysis.RewriteResultPrefix,
		)
	} else {
		// Can't compute physical prefix - use empty (backend prefix will be added by listObjectsFromRoute)
		optimizedParams.Prefix = ""
	}

	// For pagination, we need to fetch more objects than requested to handle cross-backend merging
	// The final pagination will be applied in buildResponse after all objects are collected and sorted
	if analysis.RequiresFullScan || !analysis.CanOptimize {
		// Less efficient queries may need more objects to filter
		optimizedParams.MaxKeys = params.MaxKeys * 5 // Higher multiplier for inefficient queries
	} else {
		// Efficient queries can use smaller multiplier
		optimizedParams.MaxKeys = params.MaxKeys * 3 // Still need extra for cross-backend sorting
	}

	// Cap at S3 API limit (1000) to avoid backend rejections
	if optimizedParams.MaxKeys > 1000 {
		optimizedParams.MaxKeys = 1000
	}

	// Important: Clear pagination parameters for individual backend calls
	// We'll handle pagination after collecting and sorting all objects from all backends
	optimizedParams.StartAfter = ""
	optimizedParams.ContinuationToken = ""

	cp.handler.logger.Debug("created optimized params for backend query",
		"original_max_keys", params.MaxKeys,
		"optimized_max_keys", optimizedParams.MaxKeys,
		"original_prefix", params.Prefix,
		"optimized_prefix", optimizedParams.Prefix,
		"has_trivial_rewrite", analysis.HasTrivialRewrite,
		"pagination_cleared", "start_after and continuation_token cleared for backend queries",
	)

	return optimizedParams
}

// processRouteWorker processes a single route in a goroutine
func (cp *ConcurrentProcessor) processRouteWorker(ctx context.Context, routeIndex int, route config.RouteConfig, params ListObjectsV2Params, resultsChan chan<- RouteResult, wg *sync.WaitGroup) {
	defer wg.Done()

	start := time.Now()

	// Analyze route for prefix optimization
	analysis := cp.optimizer.AnalyzeRoute(route)

	// Check if we can skip this route entirely
	if cp.optimizer.CanSkipRoute(params.Prefix, route, analysis) {
		cp.handler.logger.Debug("skipping route due to prefix optimization",
			"route_index", routeIndex,
			"backend", route.Backend.ID,
			"static_prefix", analysis.StaticPrefix,
			"virtual_prefix", params.Prefix,
		)

		resultsChan <- RouteResult{
			RouteIndex: routeIndex,
			Objects:    []VirtualObject{},
			Error:      nil,
			BackendID:  route.Backend.ID,
			Duration:   time.Since(start),
		}
		return
	}

	cp.handler.logger.Debug("processing route",
		"route_index", routeIndex,
		"backend", route.Backend.ID,
		"can_optimize", analysis.CanOptimize,
		"static_prefix", analysis.StaticPrefix,
	)

	// Process route with optimization
	objects, err := cp.processRouteWithOptimization(ctx, route, params, analysis)

	duration := time.Since(start)

	if err != nil {
		cp.handler.logger.Warn("route processing failed",
			"route_index", routeIndex,
			"backend", route.Backend.ID,
			"error", err,
			"duration", duration,
		)

		// Send error but don't fail the entire operation
		resultsChan <- RouteResult{
			RouteIndex: routeIndex,
			Objects:    nil,
			Error:      err,
			BackendID:  route.Backend.ID,
			Duration:   duration,
		}
	} else {
		cp.handler.logger.Debug("route processing completed",
			"route_index", routeIndex,
			"backend", route.Backend.ID,
			"object_count", len(objects),
			"duration", duration,
		)

		resultsChan <- RouteResult{
			RouteIndex: routeIndex,
			Objects:    objects,
			Error:      nil,
			BackendID:  route.Backend.ID,
			Duration:   duration,
		}
	}
}
