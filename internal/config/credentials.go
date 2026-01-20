package config

import (
	"time"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// CredentialsAssumeRole holds configuration for assuming an IAM role.
type CredentialsAssumeRole struct {
	RoleARN     string
	SessionName string
	Duration    time.Duration
}

// buildCredentialsAssumeRoleFromIR constructs a CredentialsAssumeRole from its intermediate representation.
func buildCredentialsAssumeRoleFromIR(ctx *Context, src *ir.CredentialsAssumeRole) *CredentialsAssumeRole {
	cfg := new(CredentialsAssumeRole)
	if src.RoleARN == "" {
		ctx.Enter("RoleARN").Append("credentials.assume_role.role_arn is required when assume_role is specified")
	}
	cfg.RoleARN = src.RoleARN
	cfg.SessionName = src.SessionName
	d := time.Second * 3600
	if src.Duration != "" {
		var err error
		d, err = parseDuration(src.Duration)
		if err != nil {
			ctx.Enter("Duration").Append("invalid value: %w", err)
		}
	}
	cfg.Duration = d
	return cfg
}

// SourceType represents the type of credential source.
type SourceType string

const (
	// SourceTypeDefault uses AWS SDK default credential chain.
	SourceTypeDefault SourceType = "default"
	// SourceTypeFile loads credentials from a file.
	SourceTypeFile SourceType = "file"
	// SourceTypeAWSSecretsManager loads credentials from AWS Secrets Manager.
	SourceTypeAWSSecretsManager SourceType = "aws-secrets-manager"
	// SourceTypeInline uses inline static credentials (deprecated).
	// Deprecated: Use SourceTypeFile, SourceTypeAWSSecretsManager, or SourceTypeDefault instead.
	SourceTypeInline SourceType = "inline"
)

// CredentialsConfig holds credential configuration with support for multiple credential sources.
type CredentialsConfig struct {
	Type       SourceType             `mapstructure:"type"`
	Path       string                 `mapstructure:"path,omitempty"`
	SecretName string                 `mapstructure:"secret_name,omitempty"`
	Region     string                 `mapstructure:"region,omitempty"`
	AssumeRole *CredentialsAssumeRole `mapstructure:"assume_role,omitempty"`
	// Inline credentials (only used when type is "inline")
	AccessKeyID     string `mapstructure:"access_key_id,omitempty"`
	SecretAccessKey string `mapstructure:"secret_access_key,omitempty"`
	SessionToken    string `mapstructure:"session_token,omitempty"`
}

// populateCredentialsConfigFromIR populates a CredentialsConfig from its intermediate representation.
func populateCredentialsConfigFromIR(ctx *Context, dst *CredentialsConfig, src *ir.CredentialsConfig) {
	// Set the type
	dst.Type = SourceType(src.Type)

	// Type-specific validation
	switch src.Type {
	case "file":
		if src.Path == "" {
			ctx.Enter("Path").Append("credentials type 'file' requires 'path' field")
		}
		dst.Path = src.Path
	case "aws-secrets-manager":
		if src.SecretName == "" {
			ctx.Enter("SecretName").Append("credentials type 'aws-secrets-manager' requires 'secret_name' field")
		}
		dst.SecretName = src.SecretName
	case "inline":
		if src.AccessKeyID == "" {
			ctx.Enter("AccessKeyID").Append("credentials type 'inline' requires 'access_key_id' field")
		}
		if src.SecretAccessKey == "" {
			ctx.Enter("SecretAccessKey").Append("credentials type 'inline' requires 'secret_access_key' field")
		}
		dst.AccessKeyID = src.AccessKeyID
		dst.SecretAccessKey = src.SecretAccessKey
		dst.SessionToken = src.SessionToken
	case "":
		ctx.Append("Type").Append("required")
	default:
		ctx.Append("Type").Append("unknown credentials type %s", src.Type)
	}

	// Validate assume_role if present
	if src.AssumeRole != nil {
		dst.AssumeRole = buildCredentialsAssumeRoleFromIR(ctx.Enter("AssumeRole"), src.AssumeRole)
	}
}
