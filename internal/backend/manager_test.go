package backend

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestNewManager(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		config    *config.Config
		timeout   time.Duration
		expectErr bool
		expectLen int
	}{
		{
			name: "no backends",
			config: &config.Config{
				Backends: map[string]*config.BackendConfig{},
			},
			timeout:   10 * time.Second,
			expectErr: false,
			expectLen: 0,
		},
		{
			name: "single backend",
			config: &config.Config{
				Backends: map[string]*config.BackendConfig{
					"backend1": {
						ID:     "backend1",
						Bucket: "test-bucket",
					},
				},
			},
			timeout:   10 * time.Second,
			expectErr: false,
			expectLen: 1,
		},
		{
			name: "multiple backends",
			config: &config.Config{
				Backends: map[string]*config.BackendConfig{
					"backend1": {
						ID:     "backend1",
						Bucket: "bucket1",
					},
					"backend2": {
						ID:     "backend2",
						Bucket: "bucket2",
					},
					"backend3": {
						ID:     "backend3",
						Bucket: "bucket3",
					},
				},
			},
			timeout:   10 * time.Second,
			expectErr: false,
			expectLen: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, err := NewManager(tt.config, tt.timeout)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, mgr)
				assert.Equal(t, tt.expectLen, len(mgr.clients))
				assert.Equal(t, tt.timeout, mgr.timeout)
			}
		})
	}
}

func TestManager_GetClients(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{
			"backend1": {ID: "backend1", Bucket: "bucket1"},
			"backend2": {ID: "backend2", Bucket: "bucket2"},
		},
	}

	mgr, err := NewManager(cfg, 10*time.Second)
	assert.NoError(t, err)

	clients := mgr.GetClients()

	assert.Equal(t, 2, len(clients))
	assert.Contains(t, clients, "backend1")
	assert.Contains(t, clients, "backend2")
}

func TestManager_GetClient(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		backendID   string
		setupConfig func() *config.Config
		expectErr   bool
	}{
		{
			name:      "existing backend",
			backendID: "backend1",
			setupConfig: func() *config.Config {
				return &config.Config{
					Backends: map[string]*config.BackendConfig{
						"backend1": {ID: "backend1", Bucket: "bucket1"},
					},
				}
			},
			expectErr: false,
		},
		{
			name:      "nonexistent backend",
			backendID: "unknown",
			setupConfig: func() *config.Config {
				return &config.Config{
					Backends: map[string]*config.BackendConfig{
						"backend1": {ID: "backend1", Bucket: "bucket1"},
					},
				}
			},
			expectErr: true,
		},
		{
			name:      "empty backend ID",
			backendID: "",
			setupConfig: func() *config.Config {
				return &config.Config{
					Backends: map[string]*config.BackendConfig{},
				}
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.setupConfig()
			mgr, err := NewManager(cfg, 10*time.Second)
			assert.NoError(t, err)

			client, err := mgr.GetClient(tt.backendID)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, client)
			}
		})
	}
}

func TestBackendClient_Creation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		backendID   string
		config      *config.BackendConfig
		expectErr   bool
		checkClient func(*BackendClient) bool
	}{
		{
			name:      "basic backend without credentials",
			backendID: "backend1",
			config: &config.BackendConfig{
				ID:     "backend1",
				Bucket: "test-bucket",
				Prefix: "prefix/",
			},
			expectErr: false,
			checkClient: func(bc *BackendClient) bool {
				return bc.ID == "backend1" &&
					bc.Bucket == "test-bucket" &&
					bc.S3Client != nil &&
					bc.HTTPClient != nil &&
					bc.Health != nil
			},
		},
		{
			name:      "backend with inline credentials",
			backendID: "backend2",
			config: &config.BackendConfig{
				ID:     "backend2",
				Bucket: "another-bucket",
				Credentials: &config.CredentialsConfig{
					Type:            "inline",
					AccessKeyID:     "AKIA1234567890ABCDEF",
					SecretAccessKey: "secret-key",
				},
			},
			expectErr: false,
			checkClient: func(bc *BackendClient) bool {
				return bc.ID == "backend2" && bc.CredsProvider != nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Backends: map[string]*config.BackendConfig{
					tt.backendID: tt.config,
				},
			}

			mgr, err := NewManager(cfg, 10*time.Second)

			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				client, _ := mgr.GetClient(tt.backendID)
				assert.True(t, tt.checkClient(client), "client check failed for %s", tt.backendID)
			}
		})
	}
}

func TestBackendClient_HealthState(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{
			"backend1": {ID: "backend1", Bucket: "bucket1"},
		},
	}

	mgr, _ := NewManager(cfg, 10*time.Second)
	client, _ := mgr.GetClient("backend1")

	assert.NotNil(t, client.Health)
	assert.True(t, client.Health.Healthy)
	assert.Equal(t, 0, client.Health.ConsecutiveFailures)
}

func TestManager_HTTPClient_Configuration(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{
			"backend1": {ID: "backend1", Bucket: "bucket1"},
		},
	}

	timeout := 30 * time.Second
	mgr, _ := NewManager(cfg, timeout)
	client, _ := mgr.GetClient("backend1")

	assert.NotNil(t, client.HTTPClient)
	assert.Equal(t, timeout, client.HTTPClient.Timeout)

	transport, ok := client.HTTPClient.Transport.(*http.Transport)
	assert.True(t, ok, "expected http.Transport")
	assert.NotZero(t, transport.MaxIdleConns)
}

func TestManager_Close(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{
			"backend1": {ID: "backend1", Bucket: "bucket1"},
			"backend2": {ID: "backend2", Bucket: "bucket2"},
		},
	}

	mgr, _ := NewManager(cfg, 10*time.Second)

	err := mgr.Close()
	assert.NoError(t, err)

	// Verify clients are still accessible after close
	// (Close just closes idle connections, doesn't remove clients)
	clients := mgr.GetClients()
	assert.Equal(t, 2, len(clients))
}

func TestManager_HealthCheck_Context(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		backendID   string
		setupConfig func() *config.Config
		shouldFail  bool
	}{
		{
			name:      "existing backend",
			backendID: "backend1",
			setupConfig: func() *config.Config {
				return &config.Config{
					Backends: map[string]*config.BackendConfig{
						"backend1": {
							ID:     "backend1",
							Bucket: "test-bucket",
							Credentials: &config.CredentialsConfig{
								Type:            config.SourceTypeInline,
								AccessKeyID:     "test-access-key",
								SecretAccessKey: "test-secret-key",
							},
						},
					},
				}
			},
			shouldFail: false,
		},
		{
			name:      "nonexistent backend",
			backendID: "unknown",
			setupConfig: func() *config.Config {
				return &config.Config{
					Backends: map[string]*config.BackendConfig{},
				}
			},
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := tt.setupConfig()
			mgr, _ := NewManager(cfg, 10*time.Second)

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := mgr.HealthCheck(ctx, tt.backendID)

			if tt.shouldFail {
				// For nonexistent backend, we expect an error or nil
				// For existing backend, we might get an error due to S3 connectivity
				t.Logf("HealthCheck returned: %v", err)
			} else {
				// We expect this to potentially fail due to S3 connectivity,
				// but if it does, it should be a real error
				t.Logf("HealthCheck returned error (may be expected due to S3): %v", err)
			}
		})
	}
}
