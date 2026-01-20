package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func TestBuildAuthConfigFromIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		src            *ir.AuthConfig
		expectErr      bool
		expectedRegion string
		expectedLeeway time.Duration
	}{
		{
			name:           "default config",
			src:            &ir.AuthConfig{},
			expectErr:      false,
			expectedRegion: "",
			expectedLeeway: 600 * time.Second,
		},
		{
			name: "with default region",
			src: &ir.AuthConfig{
				DefaultRegion: "us-east-1",
			},
			expectErr:      false,
			expectedRegion: "us-east-1",
			expectedLeeway: 600 * time.Second,
		},
		{
			name: "with clock skew leeway",
			src: &ir.AuthConfig{
				DefaultRegion:   "eu-west-1",
				ClockSkewLeeway: "300s",
			},
			expectErr:      false,
			expectedRegion: "eu-west-1",
			expectedLeeway: 300 * time.Second,
		},
		{
			name: "with invalid duration",
			src: &ir.AuthConfig{
				ClockSkewLeeway: "invalid",
			},
			expectErr: true,
		},
		{
			name: "with numeric duration",
			src: &ir.AuthConfig{
				ClockSkewLeeway: "900",
			},
			expectErr:      false,
			expectedLeeway: 900 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			cfg := buildAuthConfigFromIR(ctx, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedRegion, cfg.DefaultRegion)
				assert.Equal(t, tt.expectedLeeway, cfg.ClockSkewLeeway)
			}
		})
	}
}
