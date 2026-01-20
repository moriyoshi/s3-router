package cred

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// SecretsManagerProvider loads credentials from AWS Secrets Manager.
type SecretsManagerProvider struct {
	secretName string
	region     string
	client     *secretsmanager.Client
}

// NewSecretsManagerProvider creates a new AWS Secrets Manager credential provider.
func NewSecretsManagerProvider(secretName, region string) (*SecretsManagerProvider, error) {
	if secretName == "" {
		return nil, fmt.Errorf("secret_name is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config for secrets manager: %w", err)
	}

	return &SecretsManagerProvider{
		secretName: secretName,
		region:     region,
		client:     secretsmanager.NewFromConfig(cfg),
	}, nil
}

// Get retrieves credentials from AWS Secrets Manager.
func (p *SecretsManagerProvider) Get(ctx context.Context) (*CredentialSet, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := p.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &p.secretName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve secret from AWS Secrets Manager: %w", err)
	}

	var secretData struct {
		AccessKeyID     string `json:"access_key_id"`
		SecretAccessKey string `json:"secret_access_key"`
		SessionToken    string `json:"session_token,omitempty"`
	}

	var secretString string
	if result.SecretString != nil {
		secretString = *result.SecretString
	} else if result.SecretBinary != nil {
		secretString = string(result.SecretBinary)
	} else {
		return nil, fmt.Errorf("secret has no string or binary value")
	}

	if err := json.Unmarshal([]byte(secretString), &secretData); err != nil {
		return nil, fmt.Errorf("failed to parse secret as JSON: %w", err)
	}

	if secretData.AccessKeyID == "" {
		return nil, fmt.Errorf("secret missing 'access_key_id' field")
	}
	if secretData.SecretAccessKey == "" {
		return nil, fmt.Errorf("secret missing 'secret_access_key' field")
	}

	return &CredentialSet{
		AccessKeyID:     secretData.AccessKeyID,
		SecretAccessKey: secretData.SecretAccessKey,
		SessionToken:    secretData.SessionToken,
	}, nil
}
