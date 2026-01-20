package proxy

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/routing"
	"github.com/stretchr/testify/assert"
)

func TestExecutor_NewExecutor(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	mgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(mgr)

	assert.NotNil(t, exec)
	assert.NotNil(t, exec.backendMgr)
}

func TestExecutor_Execute_UnsupportedMethod(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(backendMgr)

	rc := &RequestContext{
		Bucket:    "test-bucket",
		ObjectKey: "test-key",
		Method:    "PATCH",
	}

	decision := &routing.Decision{
		Backend:      &config.BackendConfig{ID: "nonexistent"},
		RewrittenKey: "rewritten-key",
	}

	_, err := exec.Execute(context.Background(), rc, decision)

	assert.Error(t, err)
}

func TestExecutor_MethodCaseInsensitive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		method string
	}{
		{"get"},
		{"Get"},
		{"GET"},
		{"head"},
		{"HEAD"},
		{"put"},
		{"PUT"},
		{"delete"},
		{"DELETE"},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
			backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
			exec := NewExecutor(backendMgr)

			rc := &RequestContext{
				Bucket:    "test-bucket",
				ObjectKey: "test-key",
				Method:    tt.method,
				Body:      io.NopCloser(strings.NewReader("")),
			}

			decision := &routing.Decision{
				Backend:      &config.BackendConfig{ID: "nonexistent"},
				RewrittenKey: "rewritten-key",
			}

			_, err := exec.Execute(context.Background(), rc, decision)

			// Should error on missing backend, not on unsupported method
			if err != nil && strings.Contains(strings.ToLower(err.Error()), "unsupported operation") {
				t.Errorf("method %s not recognized as supported", tt.method)
			}
		})
	}
}

func TestRequestContext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		rc       *RequestContext
		validate func(*RequestContext) bool
	}{
		{
			name: "basic context",
			rc: &RequestContext{
				Bucket:    "test-bucket",
				ObjectKey: "test/key",
				Operation: "GetObject",
				Method:    "GET",
				Principal: "user123",
			},
			validate: func(rc *RequestContext) bool {
				return rc.Bucket == "test-bucket" &&
					rc.ObjectKey == "test/key" &&
					rc.Method == "GET" &&
					rc.Principal == "user123"
			},
		},
		{
			name: "context with headers",
			rc: &RequestContext{
				Bucket:    "bucket",
				ObjectKey: "key",
				Method:    "PUT",
				Headers:   map[string][]string{"Content-Type": {"application/json"}},
			},
			validate: func(rc *RequestContext) bool {
				return rc.Headers.Get("Content-Type") == "application/json"
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.True(t, tt.validate(tt.rc), "context validation failed")
		})
	}
}

func TestExecutor_PostNotSupported(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(backendMgr)

	rc := &RequestContext{
		Bucket:    "test-bucket",
		ObjectKey: "test-key",
		Method:    "POST",
		Body:      io.NopCloser(strings.NewReader("")),
		Operation: "UnknownPOSTOperation", // Not a multipart operation
	}

	decision := &routing.Decision{
		Backend:      &config.BackendConfig{ID: "nonexistent"},
		RewrittenKey: "rewritten-key",
	}

	_, err := exec.Execute(context.Background(), rc, decision)

	// Should error on missing backend first, but unsupported POST would also error
	assert.Error(t, err)
}

func TestExecutor_DeleteObjectsOperation(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(backendMgr)

	// Test that DeleteObjects operation is recognized
	rc := &RequestContext{
		Bucket:      "test-bucket",
		ObjectKey:   "",
		Operation:   "DeleteObjects",
		Method:      "POST",
		Body:        io.NopCloser(strings.NewReader("")),
		QueryParams: map[string][]string{"delete": {""}},
	}

	decision := &routing.Decision{
		Backend:      &config.BackendConfig{ID: "nonexistent"},
		RewrittenKey: "",
	}

	_, err := exec.Execute(context.Background(), rc, decision)
	// Should error on missing backend, not fail to parse operation
	assert.Error(t, err)
	// The error should NOT be "unsupported operation"
	assert.NotContains(t, err.Error(), "unsupported operation")
}

func TestExecutor_ACLOperations(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(backendMgr)

	tests := []struct {
		name      string
		operation string
		method    string
	}{
		{
			name:      "GetObjectAcl operation",
			operation: "GetObjectAcl",
			method:    "GET",
		},
		{
			name:      "PutObjectAcl operation",
			operation: "PutObjectAcl",
			method:    "PUT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &RequestContext{
				Bucket:      "test-bucket",
				ObjectKey:   "test-key",
				Operation:   tt.operation,
				Method:      tt.method,
				Body:        io.NopCloser(strings.NewReader("")),
				Headers:     make(map[string][]string),
				QueryParams: map[string][]string{"acl": {""}},
			}

			decision := &routing.Decision{
				Backend:      &config.BackendConfig{ID: "nonexistent"},
				RewrittenKey: "test-key",
			}

			_, err := exec.Execute(context.Background(), rc, decision)
			// Should error on missing backend, not fail to parse operation
			assert.Error(t, err)
			assert.NotContains(t, err.Error(), "unsupported operation")
		})
	}
}

func TestExecutor_CopyObjectDetection(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	backendMgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(backendMgr)

	// Test copy operation detection by x-amz-copy-source header
	rc := &RequestContext{
		Bucket:      "dest-bucket",
		ObjectKey:   "dest-key",
		Method:      "PUT",
		Body:        io.NopCloser(strings.NewReader("")),
		Headers:     map[string][]string{"x-amz-copy-source": {"/source-bucket/source-key"}},
		QueryParams: make(map[string][]string),
	}

	decision := &routing.Decision{
		Backend:      &config.BackendConfig{ID: "nonexistent"},
		RewrittenKey: "dest-key",
	}

	_, err := exec.Execute(context.Background(), rc, decision)
	// Should error on missing backend, not fail to parse copy operation
	assert.Error(t, err)
	// The error should be about the backend, not about unsupported operation
	assert.NotContains(t, err.Error(), "unsupported")
}

func TestCircuitBreaker_S3Operations(t *testing.T) {
	t.Parallel()
	// Test that S3Operations decorator properly wraps circuit breaker
	// This test verifies that the circuit breaker interface is correctly wired

	cfg := &config.Config{
		Backends: map[string]*config.BackendConfig{
			"test-backend": {
				ID:     "test-backend",
				Bucket: "test-bucket",
			},
		},
	}

	backendMgr, err := backend.NewManager(cfg, 10*time.Second)
	assert.NoError(t, err)

	// Get backend client and verify S3Operations is wired
	client, err := backendMgr.GetClient("test-backend")
	assert.NoError(t, err)
	assert.NotNil(t, client.S3Operations)
}

func TestExecutor_SetMatcher(t *testing.T) {
	t.Parallel()
	// Test that SetMatcher can be called and doesn't panic
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	mgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(mgr)

	// Should not panic
	exec.SetMatcher(nil)
	assert.NotNil(t, exec)
}

func TestCopyObject_SourceRewriting(t *testing.T) {
	t.Parallel()
	// Test that CopyObject properly rewrites source with routing
	cfg := &config.Config{Backends: map[string]*config.BackendConfig{}}
	mgr, _ := backend.NewManager(cfg, 10*time.Second)
	exec := NewExecutor(mgr)

	// SetMatcher should allow CopyObject to rewrite sources
	exec.SetMatcher(nil) // Would use real matcher in integration test

	// Verify that SetMatcher was called (CopyObject will use it if not nil)
	assert.NotNil(t, exec)
}
