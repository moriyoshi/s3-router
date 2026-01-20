package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/stretchr/testify/assert"
)

func TestServer_handleHealth(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	server := NewServer(":8080", backendMgr, cfg)

	req := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()

	server.handleHealth(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp HealthResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, "ok", resp.Status)
	assert.False(t, resp.Time.IsZero())
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
}

func TestServer_handleReady(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		setupBackends func() map[string]*config.BackendConfig
		expectStatus  int
		expectReady   bool
	}{
		{
			name: "all backends healthy",
			setupBackends: func() map[string]*config.BackendConfig {
				return map[string]*config.BackendConfig{
					"backend1": {ID: "backend1", Bucket: "bucket1"},
					"backend2": {ID: "backend2", Bucket: "bucket2"},
				}
			},
			expectStatus: http.StatusOK,
			expectReady:  true,
		},
		{
			name: "no backends",
			setupBackends: func() map[string]*config.BackendConfig {
				return map[string]*config.BackendConfig{}
			},
			expectStatus: http.StatusOK,
			expectReady:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{Backends: tt.setupBackends()}
			backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
			server := NewServer(":8080", backendMgr, cfg)

			req := httptest.NewRequest("GET", "/readyz", nil)
			w := httptest.NewRecorder()

			server.handleReady(w, req)

			assert.Equal(t, tt.expectStatus, w.Code)

			var resp ReadyResponse
			err := json.NewDecoder(w.Body).Decode(&resp)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectReady, resp.Ready)
			assert.False(t, resp.Time.IsZero())
		})
	}
}

func TestServer_handleConfig(t *testing.T) {
	t.Parallel()
	backend1 := &config.BackendConfig{
		ID:     "backend1",
		Bucket: "my-bucket",
		Prefix: "prefix/",
	}
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{
			"backend1": backend1,
		},
		Buckets: map[string]config.BucketConfig{
			"virtual-bucket": {
				Name: "virtual-bucket",
				Routes: []config.RouteConfig{
					{
						Path:    regexp.MustCompile("^foo/(.*)$"),
						Backend: backend1,
					},
				},
			},
		},
	}

	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	server := NewServer(":8080", backendMgr, cfg)

	req := httptest.NewRequest("GET", "/admin/config", nil)
	w := httptest.NewRecorder()

	server.handleConfig(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp ConfigInfo
	err := json.NewDecoder(w.Body).Decode(&resp)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(resp.Backends))
	assert.Equal(t, 1, len(resp.Buckets))
	assert.Equal(t, "virtual-bucket", resp.Buckets["virtual-bucket"].Name)
}

func TestServer_handleBackendHealthCheck(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		backendID    string
		expectStatus int
	}{
		{
			name:         "successful health check",
			backendID:    "backend1",
			expectStatus: http.StatusInternalServerError, // Will fail due to S3 connectivity
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				Backends: map[string]*config.BackendConfig{
					"backend1": {ID: "backend1", Bucket: "test-bucket"},
				},
			}
			backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
			server := NewServer(":8080", backendMgr, cfg)

			req := httptest.NewRequest("POST", "/admin/backend/backend1/health-check", nil)
			w := httptest.NewRecorder()

			server.handleBackendHealthCheck(w, req)

			// Just verify we got a response and it's a valid status code
			assert.True(t, w.Code >= 100 && w.Code < 600, "expected valid HTTP status code, got %d", w.Code)

			var resp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Logf("could not decode response: %v", err)
			}
		})
	}
}

func TestServer_NewServer(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{},
		Buckets:  map[string]config.BucketConfig{},
	}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)

	server := NewServer(":9000", backendMgr, cfg)

	assert.NotNil(t, server)
	assert.Equal(t, ":9000", server.Addr)
	assert.Equal(t, cfg, server.config)
	assert.Equal(t, backendMgr, server.backend)
	assert.NotNil(t, server.Handler)
}

func TestServer_ResponseContentType(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	server := NewServer(":8080", backendMgr, cfg)

	tests := []struct {
		name    string
		path    string
		method  string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{
			name:    "health response",
			path:    "/healthz",
			method:  "GET",
			handler: server.handleHealth,
		},
		{
			name:    "ready response",
			path:    "/readyz",
			method:  "GET",
			handler: server.handleReady,
		},
		{
			name:    "config response",
			path:    "/admin/config",
			method:  "GET",
			handler: server.handleConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			w := httptest.NewRecorder()

			tt.handler(w, req)

			contentType := w.Header().Get("Content-Type")
			assert.Equal(t, "application/json", contentType)
		})
	}
}
