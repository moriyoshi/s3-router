package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func TestBuildCredentialsAssumeRoleFromIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		src       *ir.CredentialsAssumeRole
		expectErr bool
	}{
		{
			name: "valid assume role with arn",
			src: &ir.CredentialsAssumeRole{
				RoleARN:     "arn:aws:iam::123456789012:role/MyRole",
				SessionName: "session1",
				Duration:    "1800s",
			},
			expectErr: false,
		},
		{
			name: "assume role without arn",
			src: &ir.CredentialsAssumeRole{
				RoleARN:     "",
				SessionName: "session1",
			},
			expectErr: true,
		},
		{
			name: "default duration when empty",
			src: &ir.CredentialsAssumeRole{
				RoleARN: "arn:aws:iam::123456789012:role/MyRole",
			},
			expectErr: false,
		},
		{
			name: "invalid duration",
			src: &ir.CredentialsAssumeRole{
				RoleARN:  "arn:aws:iam::123456789012:role/MyRole",
				Duration: "invalid",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			cfg := buildCredentialsAssumeRoleFromIR(ctx, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.src.RoleARN, cfg.RoleARN)
				assert.NotZero(t, cfg.Duration)
				if tt.src.Duration != "" {
					assert.Equal(t, 1800*time.Second, cfg.Duration)
				} else {
					assert.Equal(t, 3600*time.Second, cfg.Duration)
				}
			}
		})
	}
}

func TestPopulateCredentialsConfigFromIR(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		src       *ir.CredentialsConfig
		expectErr bool
	}{
		{
			name: "file credentials",
			src: &ir.CredentialsConfig{
				Type: "file",
				Path: "/path/to/credentials.json",
			},
			expectErr: false,
		},
		{
			name: "file without path",
			src: &ir.CredentialsConfig{
				Type: "file",
				Path: "",
			},
			expectErr: true,
		},
		{
			name: "aws-secrets-manager credentials",
			src: &ir.CredentialsConfig{
				Type:       "aws-secrets-manager",
				SecretName: "my-secret",
				Region:     "us-east-1",
			},
			expectErr: false,
		},
		{
			name: "aws-secrets-manager without secret name",
			src: &ir.CredentialsConfig{
				Type:       "aws-secrets-manager",
				SecretName: "",
			},
			expectErr: true,
		},
		{
			name: "inline credentials",
			src: &ir.CredentialsConfig{
				Type:            "inline",
				AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
				SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
			},
			expectErr: false,
		},
		{
			name: "inline without access key",
			src: &ir.CredentialsConfig{
				Type:            "inline",
				AccessKeyID:     "",
				SecretAccessKey: "secret",
			},
			expectErr: true,
		},
		{
			name: "inline without secret key",
			src: &ir.CredentialsConfig{
				Type:            "inline",
				AccessKeyID:     "key",
				SecretAccessKey: "",
			},
			expectErr: true,
		},
		{
			name: "empty type",
			src: &ir.CredentialsConfig{
				Type: "",
			},
			expectErr: true,
		},
		{
			name: "unknown type",
			src: &ir.CredentialsConfig{
				Type: "unknown",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &CredentialsConfig{}
			populateCredentialsConfigFromIR(ctx, dst, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCredentialsWithAssumeRole(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name             string
		src              *ir.CredentialsConfig
		expectErr        bool
		expectAssumeRole bool
	}{
		{
			name: "file credentials without assume role",
			src: &ir.CredentialsConfig{
				Type: "file",
				Path: "/path/to/creds.json",
			},
			expectErr:        false,
			expectAssumeRole: false,
		},
		{
			name: "file credentials with assume role",
			src: &ir.CredentialsConfig{
				Type: "file",
				Path: "/path/to/creds.json",
				AssumeRole: &ir.CredentialsAssumeRole{
					RoleARN: "arn:aws:iam::123456789012:role/MyRole",
				},
			},
			expectErr:        false,
			expectAssumeRole: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := NewContext()
			dst := &CredentialsConfig{}
			populateCredentialsConfigFromIR(ctx, dst, tt.src)
			err := ctx.Errors()

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.expectAssumeRole {
					assert.NotNil(t, dst.AssumeRole)
				} else {
					assert.Nil(t, dst.AssumeRole)
				}
			}
		})
	}
}

func TestCredentialSourceTypeConstants(t *testing.T) {
	t.Parallel()
	assert.Equal(t, SourceType("default"), SourceTypeDefault)
	assert.Equal(t, SourceType("file"), SourceTypeFile)
	assert.Equal(t, SourceType("aws-secrets-manager"), SourceTypeAWSSecretsManager)
	assert.Equal(t, SourceType("inline"), SourceTypeInline)
}
