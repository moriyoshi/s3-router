package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func Test_buildServerConfigFromIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		config    ir.ServerConfig
		expectErr bool
		validate  func(*testing.T, *ServerConfig)
	}{
		{
			name: "valid with unit formats",
			config: ir.ServerConfig{
				ReadTimeout:    "30s",
				WriteTimeout:   "30s",
				IdleTimeout:    "60s",
				MaxBodySize:    "4GB",
				RouteCacheSize: "1k",
			},
			expectErr: false,
			validate: func(t *testing.T, p *ServerConfig) {
				t.Helper()
				assert.Equal(t, 30*time.Second, p.ReadTimeout)
				assert.Equal(t, 30*time.Second, p.WriteTimeout)
				assert.Equal(t, 60*time.Second, p.IdleTimeout)
				assert.Equal(t, int64(4*1024*1024*1024), p.MaxBodySize)
				assert.Equal(t, 1000, p.RouteCacheSize)
			},
		},
		{
			name: "valid with numeric formats (backward compat)",
			config: ir.ServerConfig{
				ReadTimeout:    "15",
				WriteTimeout:   "15",
				IdleTimeout:    "60",
				MaxBodySize:    "4294967296",
				RouteCacheSize: "1000",
			},
			expectErr: false,
			validate: func(t *testing.T, p *ServerConfig) {
				t.Helper()
				assert.Equal(t, 15*time.Second, p.ReadTimeout)
				assert.Equal(t, 15*time.Second, p.WriteTimeout)
				assert.Equal(t, 60*time.Second, p.IdleTimeout)
				assert.Equal(t, int64(4294967296), p.MaxBodySize)
				assert.Equal(t, 1000, p.RouteCacheSize)
			},
		},
		{
			name: "invalid read timeout",
			config: ir.ServerConfig{
				ReadTimeout: "invalid",
			},
			expectErr: true,
		},
		{
			name: "invalid max body size",
			config: ir.ServerConfig{
				MaxBodySize: "10TB",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			result := buildServerConfigFromIR(ctx, &tt.config)
			if tt.expectErr {
				assert.Error(t, ctx.Errors())
			} else {
				assert.NoError(t, ctx.Errors())
				if tt.validate != nil {
					tt.validate(t, result)
				}
			}
		})
	}
}
