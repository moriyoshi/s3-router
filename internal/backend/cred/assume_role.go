package cred

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/moriyoshi/s3-router/internal/config"
)

// AssumeRoleProvider wraps a base credential provider and assumes an IAM role.
type AssumeRoleProvider struct {
	baseProvider Provider
	roleConfig   *config.CredentialsAssumeRole
	region       string
}

// NewAssumeRoleProvider creates a new assume role provider that wraps the base provider.
func NewAssumeRoleProvider(baseProvider Provider, roleConfig *config.CredentialsAssumeRole, region string) *AssumeRoleProvider {
	return &AssumeRoleProvider{
		baseProvider: baseProvider,
		roleConfig:   roleConfig,
		region:       region,
	}
}

// Get retrieves credentials by first getting base credentials and then assuming the configured role.
func (p *AssumeRoleProvider) Get(ctx context.Context) (*CredentialSet, error) {
	return AssumeRole(ctx, p.baseProvider, p.roleConfig, p.region)
}

// AssumeRole assumes an IAM role using the provided base credentials and returns temporary credentials.
func AssumeRole(ctx context.Context, baseCredsProvider Provider, roleConfig *config.CredentialsAssumeRole, region string) (*CredentialSet, error) {
	if roleConfig == nil || roleConfig.RoleARN == "" {
		return nil, fmt.Errorf("assume_role is nil or role_arn is empty")
	}

	// Create a timeout context if the provided context doesn't have a deadline
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	baseCreds, err := baseCredsProvider.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve base credentials for role assumption: %w", err)
	}

	// Create STS client with base credentials
	stsConfig := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(baseCreds.AccessKeyID, baseCreds.SecretAccessKey, baseCreds.SessionToken),
	}
	stsClient := sts.NewFromConfig(stsConfig)

	// Set defaults
	sessionName := roleConfig.SessionName
	if sessionName == "" {
		sessionName = "s3-router"
	}

	duration := int32(roleConfig.Duration)
	if duration == 0 {
		duration = 3600 // 1 hour default
	}

	// Assume role
	result, err := stsClient.AssumeRole(ctx, &sts.AssumeRoleInput{
		RoleArn:         aws.String(roleConfig.RoleARN),
		RoleSessionName: aws.String(sessionName),
		DurationSeconds: aws.Int32(duration),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to assume role %q: %w", roleConfig.RoleARN, err)
	}

	if result.Credentials == nil {
		return nil, fmt.Errorf("assumed role returned nil credentials")
	}

	creds := result.Credentials
	return &CredentialSet{
		AccessKeyID:     aws.ToString(creds.AccessKeyId),
		SecretAccessKey: aws.ToString(creds.SecretAccessKey),
		SessionToken:    aws.ToString(creds.SessionToken),
	}, nil
}
