package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func TestPopulateBackendConfigFromIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		id        string
		src       *ir.BackendConfig
		expectErr bool
	}{
		{
			name: "minimal valid backend",
			id:   "test-backend",
			src: &ir.BackendConfig{
				Bucket: "my-bucket",
			},
			expectErr: false,
		},
		{
			name: "missing bucket",
			id:   "test-backend",
			src: &ir.BackendConfig{
				Bucket: "",
			},
			expectErr: true,
		},
		{
			name: "with custom timeout",
			id:   "test-backend",
			src: &ir.BackendConfig{
				Bucket:  "my-bucket",
				Timeout: "30s",
			},
			expectErr: false,
		},
		{
			name: "with region and endpoint",
			id:   "s3-backend",
			src: &ir.BackendConfig{
				Bucket:   "my-bucket",
				Region:   "us-west-2",
				Endpoint: "https://s3.us-west-2.amazonaws.com",
				Prefix:   "data/",
			},
			expectErr: false,
		},
		{
			name: "with all flags",
			id:   "test-backend",
			src: &ir.BackendConfig{
				Bucket:            "my-bucket",
				UseFIPS:           true,
				UseGlobalEndpoint: true,
				UseDualStack:      true,
				Accelerate:        true,
				Retries:           5,
			},
			expectErr: false,
		},
		{
			name: "with use_path_style enabled",
			id:   "test-backend",
			src: &ir.BackendConfig{
				Bucket:       "my-bucket",
				UsePathStyle: true,
			},
			expectErr: false,
		},
		{
			name: "with use_path_style and other flags",
			id:   "test-backend",
			src: &ir.BackendConfig{
				Bucket:       "my-bucket",
				UsePathStyle: true,
				UseFIPS:      true,
				Retries:      3,
			},
			expectErr: false,
		},
		{
			name: "invalid timeout",
			id:   "test-backend",
			src: &ir.BackendConfig{
				Bucket:  "my-bucket",
				Timeout: "invalid",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &BackendConfig{}
			populateBackendConfigFromIR(ctx, dst, tt.id, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.id, dst.ID)
				assert.Equal(t, tt.src.Bucket, dst.Bucket)
				assert.Equal(t, tt.src.UsePathStyle, dst.UsePathStyle)
			}
		})
	}
}

func TestBackendConfigUsePathStyle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		src           *ir.BackendConfig
		expectEnabled bool
	}{
		{
			name: "use_path_style explicitly enabled",
			src: &ir.BackendConfig{
				Bucket:       "my-bucket",
				UsePathStyle: true,
			},
			expectEnabled: true,
		},
		{
			name: "use_path_style explicitly disabled",
			src: &ir.BackendConfig{
				Bucket:       "my-bucket",
				UsePathStyle: false,
			},
			expectEnabled: false,
		},
		{
			name: "use_path_style default (not set)",
			src: &ir.BackendConfig{
				Bucket: "my-bucket",
			},
			expectEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &BackendConfig{}
			populateBackendConfigFromIR(ctx, dst, "test", tt.src)
			err := ctx.Errors()

			assert.NoError(t, err)
			assert.Equal(t, tt.expectEnabled, dst.UsePathStyle)
		})
	}
}

func TestBackendConfigWithCredentials(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		src       *ir.BackendConfig
		expectErr bool
		hasCreds  bool
	}{
		{
			name: "backend without credentials",
			src: &ir.BackendConfig{
				Bucket: "my-bucket",
			},
			expectErr: false,
			hasCreds:  false,
		},
		{
			name: "backend with file credentials",
			src: &ir.BackendConfig{
				Bucket: "my-bucket",
				Credentials: &ir.CredentialsConfig{
					Type: "file",
					Path: "/path/to/creds.json",
				},
			},
			expectErr: false,
			hasCreds:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &BackendConfig{}
			populateBackendConfigFromIR(ctx, dst, "test", tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.hasCreds {
					assert.NotNil(t, dst.Credentials)
				} else {
					assert.Nil(t, dst.Credentials)
				}
			}
		})
	}
}
