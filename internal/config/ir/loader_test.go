package ir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPopulateFromFileYAML(t *testing.T) {
	t.Parallel()
	content := `
backends:
  backend1:
    bucket: my-bucket
    region: us-west-2
buckets:
  - name: bucket1
    routes:
      - path: /api
        backend: backend1
features:
  feature1: true
credentials_store: vault
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	assert.Equal(t, "vault", cfg.CredentialsStore)
	assert.Len(t, cfg.Backends, 1)
	assert.Len(t, cfg.Buckets, 1)
	assert.True(t, cfg.Features["feature1"])
}

func TestPopulateFromFileJSON(t *testing.T) {
	t.Parallel()
	content := `{
  "backends": {
    "backend1": {
      "bucket": "my-bucket",
      "region": "us-east-1"
    }
  },
  "buckets": [
    {
      "name": "bucket1",
      "routes": [
        {
          "path": "/data",
          "backend": "backend1"
        }
      ]
    }
  ],
  "credentials_store": "aws_secrets"
}`
	tmpFile := filepath.Join(t.TempDir(), "config.json")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	assert.Equal(t, "aws_secrets", cfg.CredentialsStore)
	assert.Len(t, cfg.Backends, 1)
	assert.Len(t, cfg.Buckets, 1)
}

func TestPopulateFromFileWithServerConfig(t *testing.T) {
	t.Parallel()
	content := `
backends:
  backend1:
    bucket: my-bucket
buckets:
  - name: bucket1
    routes:
      - path: /
        backend: backend1
server:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 60s
  max_body_size: "4GB"
  route_cache_size: 1000
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	assert.NotNil(t, cfg.Server)
	assert.Equal(t, "30s", cfg.Server.ReadTimeout)
	assert.Equal(t, "30s", cfg.Server.WriteTimeout)
	assert.Equal(t, "60s", cfg.Server.IdleTimeout)
	assert.Equal(t, "4GB", cfg.Server.MaxBodySize)
	assert.Equal(t, 1000, cfg.Server.RouteCacheSize)
}

func TestPopulateFromFileWithAuthConfig(t *testing.T) {
	t.Parallel()
	content := `
backends:
  backend1:
    bucket: my-bucket
buckets:
  - name: bucket1
    routes:
      - path: /
        backend: backend1
auth:
  default_region: eu-west-1
  clock_skew_leeway: "30m"
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	assert.NotNil(t, cfg.Auth)
	assert.Equal(t, "eu-west-1", cfg.Auth.DefaultRegion)
	assert.Equal(t, "30m", cfg.Auth.ClockSkewLeeway)
}

func TestPopulateFromFileWithVirtualHostConfig(t *testing.T) {
	t.Parallel()
	content := `
backends:
  backend1:
    bucket: my-bucket
buckets:
  - name: bucket1
    routes:
      - path: /
        backend: backend1
virtual_hosts:
  hosts:
    - host1.example.com
    - host2.example.com
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	assert.NotNil(t, cfg.VirtualHosts)
	assert.Len(t, cfg.VirtualHosts.Hosts, 2)
}

func TestPopulateFromFileWithCredentials(t *testing.T) {
	t.Parallel()
	content := `
backends:
  backend1:
    bucket: my-bucket
    credentials:
      type: inline
      access_key_id: "AKIAIOSFODNN7EXAMPLE"
      secret_access_key: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
buckets:
  - name: bucket1
    routes:
      - path: /
        backend: backend1
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	assert.NotNil(t, cfg.Backends["backend1"].Credentials)
	assert.Equal(t, "inline", cfg.Backends["backend1"].Credentials.Type)
	assert.Equal(t, "AKIAIOSFODNN7EXAMPLE", cfg.Backends["backend1"].Credentials.AccessKeyID)
}

func TestPopulateFromFileWithAssumeRole(t *testing.T) {
	t.Parallel()
	content := `
backends:
  backend1:
    bucket: my-bucket
    credentials:
      type: assume_role
      assume_role:
        role_arn: "arn:aws:iam::123456789012:role/MyRole"
        session_name: "session123"
        duration: "1h"
buckets:
  - name: bucket1
    routes:
      - path: /
        backend: backend1
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	creds := cfg.Backends["backend1"].Credentials
	assert.NotNil(t, creds.AssumeRole)
	assert.Equal(t, "arn:aws:iam::123456789012:role/MyRole", creds.AssumeRole.RoleARN)
	assert.Equal(t, "session123", creds.AssumeRole.SessionName)
	assert.Equal(t, "1h", creds.AssumeRole.Duration)
}

func TestPopulateFromFileWithRewrites(t *testing.T) {
	t.Parallel()
	content := `
backends:
  backend1:
    bucket: my-bucket
buckets:
  - name: bucket1
    routes:
      - path: /api
        backend: backend1
        rewrite:
          - pattern: "^/api/v1/(.*)$"
            result: "/v1/$1"
          - pattern: "^/api/v2/(.*)$"
            result: "/v2/$1"
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	routes := cfg.Buckets[0].Routes
	assert.Len(t, routes[0].Rewrites, 2)
	assert.Equal(t, "^/api/v1/(.*)$", routes[0].Rewrites[0].Pattern)
	assert.Equal(t, "/v1/$1", routes[0].Rewrites[0].Result)
}

func TestPopulateFromFileNotFound(t *testing.T) {
	t.Parallel()
	cfg := Config{}
	err := cfg.PopulateFromFile("/nonexistent/path/config.yaml")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read config file")
}

func TestPopulateFromFileInvalidYAML(t *testing.T) {
	t.Parallel()
	content := `
this is: [invalid yaml}
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to")
}

func TestPopulateFromFileComplexConfig(t *testing.T) {
	t.Parallel()
	content := `
backends:
  aws-us-east:
    endpoint: "https://s3.amazonaws.com"
    region: us-east-1
    bucket: prod-bucket
    prefix: /data
    timeout: "30s"
    retries: 3
    use_fips: true
    use_dual_stack: true
  aws-eu-west:
    region: eu-west-1
    bucket: eu-bucket
    credentials:
      type: file
      path: /etc/aws/creds
buckets:
  - name: primary
    routes:
      - path: /api
        backend: aws-us-east
        rewrite:
          - pattern: "^/api/(.*)$"
            result: "/$1"
  - name: secondary
    routes:
      - path: /data
        backend: aws-eu-west
server:
  read_timeout: "30s"
  write_timeout: "30s"
  idle_timeout: "60s"
  max_body_size: "4GB"
  route_cache_size: 2000
auth:
  default_region: us-east-1
  clock_skew_leeway: "15m"
features:
  enable_logging: true
  enable_metrics: false
`
	tmpFile := filepath.Join(t.TempDir(), "config.yaml")
	err := os.WriteFile(tmpFile, []byte(content), 0644)
	require.NoError(t, err)

	cfg := Config{}
	err = cfg.PopulateFromFile(tmpFile)
	require.NoError(t, err)

	// Verify backends
	assert.Len(t, cfg.Backends, 2)
	assert.Equal(t, "prod-bucket", cfg.Backends["aws-us-east"].Bucket)
	assert.Equal(t, "eu-bucket", cfg.Backends["aws-eu-west"].Bucket)

	// Verify buckets
	assert.Len(t, cfg.Buckets, 2)
	assert.Equal(t, "primary", cfg.Buckets[0].Name)
	assert.Equal(t, "secondary", cfg.Buckets[1].Name)

	// Verify server config
	assert.NotNil(t, cfg.Server)
	assert.Equal(t, 2000, cfg.Server.RouteCacheSize)

	// Verify auth config
	assert.NotNil(t, cfg.Auth)
	assert.Equal(t, "us-east-1", cfg.Auth.DefaultRegion)

	// Verify features
	assert.True(t, cfg.Features["enable_logging"])
	assert.False(t, cfg.Features["enable_metrics"])
}
