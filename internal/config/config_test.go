package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func TestConfig_PopulateFromIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		config    ir.Config
		expectErr bool
		errMsg    string
	}{
		{
			name: "valid config",
			config: ir.Config{
				Backends: map[string]ir.BackendConfig{
					"backend1": {
						Bucket: "my-bucket",
					},
				},
				Buckets: []ir.BucketConfig{
					{
						Name: "my-virtual-bucket",
						Routes: []ir.RouteConfig{
							{
								Path:    "^test/(.*)$",
								Backend: "backend1",
							},
						},
					},
				},
			},
			expectErr: false,
		},
		{
			name: "no backends",
			config: ir.Config{
				Backends: map[string]ir.BackendConfig{},
				Buckets:  []ir.BucketConfig{},
			},
			expectErr: true,
		},
		{
			name: "backend without bucket",
			config: ir.Config{
				Backends: map[string]ir.BackendConfig{
					"backend1": {
						Bucket: "",
					},
				},
				Buckets: []ir.BucketConfig{},
			},
			expectErr: true,
		},
		{
			name: "route references nonexistent backend",
			config: ir.Config{
				Backends: map[string]ir.BackendConfig{
					"backend1": {
						Bucket: "my-bucket",
					},
				},
				Buckets: []ir.BucketConfig{
					{
						Name: "my-virtual-bucket",
						Routes: []ir.RouteConfig{
							{
								Path:    "^test/",
								Backend: "nonexistent",
							},
						},
					},
				},
			},
			expectErr: true,
		},
		{
			name: "invalid regex path",
			config: ir.Config{
				Backends: map[string]ir.BackendConfig{
					"backend1": {
						Bucket: "my-bucket",
					},
				},
				Buckets: []ir.BucketConfig{
					{
						Name: "my-virtual-bucket",
						Routes: []ir.RouteConfig{
							{
								Path:    "[invalid(regex",
								Backend: "backend1",
							},
						},
					},
				},
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			err := cfg.PopulateFromIR(&tt.config)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
