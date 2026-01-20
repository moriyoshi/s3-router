package config

import (
	"regexp"

	"github.com/moriyoshi/s3-router/internal/config/ir"
	"github.com/moriyoshi/s3-router/internal/template"
)

// RewriteRule represents a pattern matching and rewriting rule for paths.
type RewriteRule struct {
	Pattern *regexp.Regexp
	Result  *template.Template
}

// populateRewriteRuleFromIR populates a RewriteRule from its intermediate representation.
func populateRewriteRuleFromIR(ctx *Context, dst *RewriteRule, src *ir.RewriteRule) {
	if src.Pattern != "" {
		regex, err := regexp.Compile(src.Pattern)
		if err != nil {
			ctx.Enter("Pattern").Append("invalid pattern regex: %w", err)
		}
		dst.Pattern = regex
	}
	tmpl, err := template.Parse(src.Result)
	if err != nil {
		ctx.Enter("Result").Append("invalid template: %w", err)
	}
	dst.Result = tmpl
}

// RouteConfig represents a routing rule that matches a path pattern and routes to a backend.
type RouteConfig struct {
	Path     *regexp.Regexp
	Backend  *BackendConfig
	Rewrites []RewriteRule
}

// populateRouteConfigFromIR populates a RouteConfig from its intermediate representation.
func populateRouteConfigFromIR(ctx *Context, backends map[string]*BackendConfig, dst *RouteConfig, src *ir.RouteConfig) {
	if src.Path == "" {
		ctx.Enter("Path").Append("path is required")
	} else {
		path, err := regexp.Compile(src.Path)
		if err != nil {
			ctx.Enter("Path").Append(err)
		}
		dst.Path = path
	}

	if src.Backend == "" {
		ctx.Append("backend is required")
	}
	// Check backend exists
	backend, exists := backends[src.Backend]
	if !exists {
		ctx.Append("backend %s not found", src.Backend)
	}
	dst.Backend = backend

	// Compile path regex
	pathRegex, err := regexp.Compile(src.Path)
	if err != nil {
		ctx.Append("invalid path regex %s: %w", src.Path, err)
	}
	dst.Path = pathRegex

	// Compile rewrite patterns
	rewrites := make([]RewriteRule, len(src.Rewrites))
	for j, irConfig := range src.Rewrites {
		var rewriteRule RewriteRule
		populateRewriteRuleFromIR(ctx.EnterIndex(j), &rewriteRule, &irConfig)
		rewrites[j] = rewriteRule
	}
	dst.Rewrites = rewrites
}

// buildRouteConfigsFromIR constructs route configurations from intermediate representation.
func buildRouteConfigsFromIR(ctx *Context, backends map[string]*BackendConfig, src []ir.RouteConfig) []RouteConfig {
	if len(src) == 0 {
		ctx.Append("no routes defined")
	}
	routes := make([]RouteConfig, len(src))
	for i, irConfig := range src {
		populateRouteConfigFromIR(ctx.EnterIndex(i), backends, &routes[i], &irConfig)
	}
	return routes
}

// BucketConfig represents the configuration for an S3 bucket including its routing rules.
type BucketConfig struct {
	Name   string
	Routes []RouteConfig
}

// populateBucketConfigFromIR populates a BucketConfig from its intermediate representation.
func populateBucketConfigFromIR(ctx *Context, backends map[string]*BackendConfig, dst *BucketConfig, src *ir.BucketConfig) {
	if src.Name == "" {
		ctx.Enter("Name").Append("bucket name is required")
	}
	dst.Name = src.Name
	dst.Routes = buildRouteConfigsFromIR(ctx.Enter("Routes"), backends, src.Routes)
}
