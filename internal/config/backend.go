package config

import (
	"time"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// BackendConfig holds the configuration for an S3 backend.
type BackendConfig struct {
	ID                string
	Endpoint          string
	Region            string
	Bucket            string
	Prefix            string
	Timeout           time.Duration
	Retries           int
	UseFIPS           bool
	UseGlobalEndpoint bool
	UseDualStack      bool
	Accelerate        bool
	Credentials       *CredentialsConfig
}

// populateBackendConfigFromIR populates a BackendConfig from its intermediate representation.
func populateBackendConfigFromIR(ctx *Context, dst *BackendConfig, id string, src *ir.BackendConfig) {
	if src.Bucket == "" {
		ctx.Enter("Bucket").Append("bucket is required")
	}

	var err error

	dst.ID = id
	dst.Endpoint = src.Endpoint
	dst.Region = src.Region
	dst.Bucket = src.Bucket
	dst.Prefix = src.Prefix
	timeout := time.Second * 60
	if src.Timeout != "" {
		timeout, err = parseDuration(src.Timeout)
		if err != nil {
			ctx.Enter("Timeout").Append("failed to parse duration: %w", err)
		}
	}
	dst.Timeout = timeout
	dst.Retries = src.Retries
	dst.UseFIPS = src.UseFIPS
	dst.UseGlobalEndpoint = src.UseGlobalEndpoint
	dst.UseDualStack = src.UseDualStack
	dst.Accelerate = src.Accelerate

	// Validate credentials if present
	if src.Credentials != nil {
		creds := new(CredentialsConfig)
		populateCredentialsConfigFromIR(ctx.Enter("Credentials"), creds, src.Credentials)
		dst.Credentials = creds
	}
}
