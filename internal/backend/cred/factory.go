package cred

import (
	"fmt"

	"github.com/moriyoshi/s3-router/internal/config"
)

// NewProvider creates a credential provider based on the source configuration.
func NewProvider(cfg *config.CredentialsConfig) (Provider, error) {
	if cfg == nil {
		return NewDefaultProvider(), nil
	}

	var baseProvider Provider
	var err error

	switch cfg.Type {
	case config.SourceTypeDefault, "":
		baseProvider = NewDefaultProvider()

	case config.SourceTypeFile:
		if cfg.Path == "" {
			return nil, fmt.Errorf("file credential source requires 'path' field")
		}
		baseProvider, err = NewFileProvider(cfg.Path)
		if err != nil {
			return nil, err
		}

	case config.SourceTypeAWSSecretsManager:
		if cfg.SecretName == "" {
			return nil, fmt.Errorf("aws-secrets-manager credential source requires 'secret_name' field")
		}
		region := cfg.Region
		if region == "" {
			region = "us-east-1"
		}
		baseProvider, err = NewSecretsManagerProvider(cfg.SecretName, region)
		if err != nil {
			return nil, err
		}

	case config.SourceTypeInline:
		baseProvider = NewInlineProvider(cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken)

	default:
		return nil, fmt.Errorf("unknown credential source type: %q", cfg.Type)
	}

	// Wrap with AssumeRoleProvider if assume_role is configured
	if cfg.AssumeRole != nil && cfg.AssumeRole.RoleARN != "" {
		region := cfg.Region
		if region == "" {
			region = "us-east-1"
		}
		return NewAssumeRoleProvider(baseProvider, cfg.AssumeRole, region), nil
	}

	return baseProvider, nil
}
