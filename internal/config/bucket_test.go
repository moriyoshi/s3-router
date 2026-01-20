package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func TestPopulateBucketConfigFromIR(t *testing.T) {
	t.Parallel()
	backends := map[string]*BackendConfig{
		"backend1": {
			ID:     "backend1",
			Bucket: "real-bucket",
		},
	}

	tests := []struct {
		name      string
		src       *ir.BucketConfig
		expectErr bool
	}{
		{
			name: "empty bucket name",
			src: &ir.BucketConfig{
				Name: "",
			},
			expectErr: true,
		},
		{
			name: "bucket with single route",
			src: &ir.BucketConfig{
				Name: "my-bucket",
				Routes: []ir.RouteConfig{
					{
						Path:    "^test/(.*)$",
						Backend: "backend1",
					},
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &BucketConfig{}
			populateBucketConfigFromIR(ctx, backends, dst, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.src.Name, dst.Name)
			}
		})
	}
}

func TestPopulateRewriteRuleFromIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		src       *ir.RewriteRule
		expectErr bool
	}{
		{
			name: "valid pattern and result",
			src: &ir.RewriteRule{
				Pattern: "^(.*)$",
				Result:  "rewritten/$1",
			},
			expectErr: false,
		},
		{
			name: "empty pattern",
			src: &ir.RewriteRule{
				Pattern: "",
				Result:  "result",
			},
			expectErr: false,
		},
		{
			name: "invalid regex",
			src: &ir.RewriteRule{
				Pattern: "[invalid",
				Result:  "result",
			},
			expectErr: true,
		},
		{
			name: "complex regex pattern",
			src: &ir.RewriteRule{
				Pattern: "^uploads/([a-z0-9]+)/(.*)$",
				Result:  "data/$1/$2",
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &RewriteRule{}
			populateRewriteRuleFromIR(ctx, dst, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestPopulateRouteConfigFromIR(t *testing.T) {
	t.Parallel()
	backends := map[string]*BackendConfig{
		"backend1": {
			ID:     "backend1",
			Bucket: "real-bucket",
		},
	}

	tests := []struct {
		name      string
		src       *ir.RouteConfig
		expectErr bool
	}{
		{
			name: "valid route",
			src: &ir.RouteConfig{
				Path:    "^test/(.*)$",
				Backend: "backend1",
			},
			expectErr: false,
		},
		{
			name: "missing path",
			src: &ir.RouteConfig{
				Path:    "",
				Backend: "backend1",
			},
			expectErr: true,
		},
		{
			name: "missing backend",
			src: &ir.RouteConfig{
				Path:    "^test/(.*)$",
				Backend: "",
			},
			expectErr: true,
		},
		{
			name: "invalid backend",
			src: &ir.RouteConfig{
				Path:    "^test/(.*)$",
				Backend: "nonexistent",
			},
			expectErr: true,
		},
		{
			name: "invalid path regex",
			src: &ir.RouteConfig{
				Path:    "[invalid",
				Backend: "backend1",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &RouteConfig{}
			populateRouteConfigFromIR(ctx, backends, dst, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBuildRouteConfigsFromIR(t *testing.T) {
	t.Parallel()
	backends := map[string]*BackendConfig{
		"backend1": {
			ID:     "backend1",
			Bucket: "real-bucket",
		},
	}

	tests := []struct {
		name        string
		routes      []ir.RouteConfig
		expectErr   bool
		expectCount int
	}{
		{
			name:        "no routes",
			routes:      []ir.RouteConfig{},
			expectErr:   true,
			expectCount: 0,
		},
		{
			name: "single route",
			routes: []ir.RouteConfig{
				{
					Path:    "^test/(.*)$",
					Backend: "backend1",
				},
			},
			expectErr:   false,
			expectCount: 1,
		},
		{
			name: "multiple routes",
			routes: []ir.RouteConfig{
				{
					Path:    "^test/(.*)$",
					Backend: "backend1",
				},
				{
					Path:    "^data/(.*)$",
					Backend: "backend1",
				},
			},
			expectErr:   false,
			expectCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			routes := buildRouteConfigsFromIR(ctx, backends, tt.routes)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tt.expectCount, len(routes))
		})
	}
}
