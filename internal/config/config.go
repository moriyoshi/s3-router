package config

import (
	"github.com/moriyoshi/s3-router/internal/auth"
	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// Config represents the complete S3 Router configuration containing backends, buckets, and routing rules.
type Config struct {
	Backends         map[string]*BackendConfig
	Buckets          map[string]BucketConfig
	Features         map[string]bool
	CredentialsStore auth.CredentialsStore
	Server           *ServerConfig
	Auth             *AuthConfig
	VirtualHosts     *VirtualHostConfig
}

func (cfg *Config) PopulateFromIR(src *ir.Config) error {
	ctx := NewContext()
	populateConfigFromIR(ctx, cfg, src)
	return ctx.Errors()
}

// buildBackendConfigsFromIR constructs backend configurations from intermediate representation.
func buildBackendConfigsFromIR(ctx *Context, src map[string]ir.BackendConfig) map[string]*BackendConfig {
	if len(src) == 0 {
		ctx.Append("no backends defined")
		return nil
	}
	backends := make(map[string]*BackendConfig)
	// Validate backends
	for backendID, irConfig := range src {
		backend := new(BackendConfig)
		populateBackendConfigFromIR(ctx.EnterIndex(backendID), backend, backendID, &irConfig)
		backends[backendID] = backend
	}
	return backends
}

// buildBucketConfigsFromIR constructs bucket configurations from intermediate representation.
func buildBucketConfigsFromIR(ctx *Context, backends map[string]*BackendConfig, src []ir.BucketConfig) map[string]BucketConfig {
	if len(src) == 0 {
		ctx.Append("no buckets defined")
		return nil
	}

	buckets := make(map[string]BucketConfig, len(src))
	for i, irConfig := range src {
		var bucket BucketConfig
		populateBucketConfigFromIR(ctx.EnterIndex(i), backends, &bucket, &irConfig)
		buckets[bucket.Name] = bucket
	}
	return buckets
}

// populateConfigFromIR populates a Config struct from its intermediate representation.
func populateConfigFromIR(ctx *Context, dst *Config, src *ir.Config) {
	var err error
	dst.Backends = buildBackendConfigsFromIR(ctx.Enter("Backends"), src.Backends)
	dst.Buckets = buildBucketConfigsFromIR(ctx.Enter("Buckets"), dst.Backends, src.Buckets)
	dst.Features = src.Features
	if src.CredentialsStore != "" {
		dst.CredentialsStore, err = auth.NewFileCredentialsStore(src.CredentialsStore)
		if err != nil {
			ctx.Enter("CredentialsStore").Append(err)
		}
	}

	// Parse and validate server configuration
	if src.Server != nil {
		dst.Server = buildServerConfigFromIR(ctx.Enter("Server"), src.Server)
	}

	// Parse and validate auth configuration
	if src.Auth != nil {
		dst.Auth = buildAuthConfigFromIR(ctx.Enter("Auth"), src.Auth)
	}

	// Validate virtual hosts configuration
	if src.VirtualHosts != nil {
		dst.VirtualHosts = buildVirtualHostConfigFromIR(ctx.Enter("VirtualHosts"), src.VirtualHosts)
	}
}
