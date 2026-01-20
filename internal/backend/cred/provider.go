package cred

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

// CredentialSet represents a set of AWS credentials.
type CredentialSet struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

// Provider defines the interface for credential sources.
type Provider interface {
	// Get retrieves credentials from the source.
	Get(context.Context) (*CredentialSet, error)
}

// DefaultProvider uses AWS SDK's default credential chain.
// This includes environment variables, shared credentials file, IMDSv2, etc.
type DefaultProvider struct {
	credentialsProvider aws.CredentialsProvider
}

// NewDefaultProvider creates a new default credential provider.
func NewDefaultProvider() *DefaultProvider {
	cfg, _ := config.LoadDefaultConfig(context.Background())
	return &DefaultProvider{
		credentialsProvider: cfg.Credentials,
	}
}

// Get retrieves credentials using AWS SDK's default chain.
func (p *DefaultProvider) Get(ctx context.Context) (*CredentialSet, error) {
	awsCreds, err := p.credentialsProvider.Retrieve(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve credentials from AWS SDK default chain: %w", err)
	}
	return &CredentialSet{
		AccessKeyID:     awsCreds.AccessKeyID,
		SecretAccessKey: awsCreds.SecretAccessKey,
		SessionToken:    awsCreds.SessionToken,
	}, nil
}

// InlineProvider wraps inline static credentials.
// Deprecated: Use credentials_source with type "default", "file", or "aws-secrets-manager" instead.
type InlineProvider struct {
	creds *CredentialSet
}

// NewInlineProvider creates a new inline credential provider from the credential set.
// Deprecated: Use external credential sources instead.
func NewInlineProvider(accessKeyID, secretAccessKey, sessionToken string) *InlineProvider {
	return &InlineProvider{
		creds: &CredentialSet{
			AccessKeyID:     accessKeyID,
			SecretAccessKey: secretAccessKey,
			SessionToken:    sessionToken,
		},
	}
}

// Get retrieves the static inline credentials.
func (p *InlineProvider) Get(context.Context) (*CredentialSet, error) {
	if p.creds.AccessKeyID == "" {
		return nil, fmt.Errorf("inline credentials: access_key_id is required")
	}
	if p.creds.SecretAccessKey == "" {
		return nil, fmt.Errorf("inline credentials: secret_access_key is required")
	}
	return p.creds, nil
}

type credsProviderWrapper struct {
	Provider
}

func (c credsProviderWrapper) Retrieve(ctx context.Context) (aws.Credentials, error) {
	cs, err := c.Get(ctx)
	if err != nil {
		return aws.Credentials{}, err
	}
	return aws.Credentials{
		AccessKeyID:     cs.AccessKeyID,
		SecretAccessKey: cs.SecretAccessKey,
		SessionToken:    cs.SessionToken,
	}, nil
}

// ToAWSCredentialsProvider converts a Provider to an AWS SDK credentials provider.
func ToAWSCredentialsProvider(p Provider) aws.CredentialsProvider {
	return credsProviderWrapper{p}
}
