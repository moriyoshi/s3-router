package backend

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/sony/gobreaker"

	"github.com/moriyoshi/s3-router/internal/backend/cred"
	"github.com/moriyoshi/s3-router/internal/config"
)

type HealthState struct {
	mu                  sync.RWMutex
	Healthy             bool
	LastCheck           time.Time
	LastError           string
	ConsecutiveFailures int
}

// GetHealthSnapshot returns a thread-safe snapshot of the health state
func (h *HealthState) GetHealthSnapshot() (healthy bool, lastCheck time.Time, lastError string, consecutiveFailures int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.Healthy, h.LastCheck, h.LastError, h.ConsecutiveFailures
}

// SetHealthy sets the health state to healthy with current timestamp
func (h *HealthState) SetHealthy() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Healthy = true
	h.LastCheck = time.Now()
	h.ConsecutiveFailures = 0
	h.LastError = ""
}

// SetUnhealthy sets the health state to unhealthy with error message
func (h *HealthState) SetUnhealthy(errMsg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.Healthy = false
	h.LastCheck = time.Now()
	h.LastError = errMsg
	h.ConsecutiveFailures++
}

type BackendClient struct {
	*config.BackendConfig
	Region           string // Resolved region (backend.region > defaultRegion)
	EndpointResolver *EndpointResolver
	S3Client         *s3.Client
	S3Operations     S3Operations
	CredsProvider    cred.Provider
	HTTPClient       *http.Client
	Health           *HealthState
}

func (bc *BackendClient) DecorateLogger(l *slog.Logger) *slog.Logger {
	return l.With(
		slog.Group("backend",
			"id", bc.ID,
			"endpoint", bc.Endpoint,
			"region", bc.Region,
			"bucket", bc.Bucket,
			"prefix", bc.Prefix,
		),
	)
}

type Manager struct {
	clients       map[string]*BackendClient
	timeout       time.Duration
	defaultRegion string
}

func NewManager(cfg *config.Config, timeout time.Duration) (*Manager, error) {
	defaultRegion := "us-east-1"
	if cfg.Auth != nil && cfg.Auth.DefaultRegion != "" {
		defaultRegion = cfg.Auth.DefaultRegion
	}
	m := &Manager{
		clients:       make(map[string]*BackendClient),
		timeout:       timeout,
		defaultRegion: defaultRegion,
	}

	for _, bcfg := range cfg.Backends {
		client, err := m.createBackendClient(bcfg.ID, bcfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create backend client for %q: %w", bcfg.ID, err)
		}
		m.clients[bcfg.ID] = client
	}

	return m, nil
}

func (m *Manager) GetClients() map[string]*BackendClient {
	return m.clients
}

func (m *Manager) createBackendClient(id string, bcfg *config.BackendConfig) (*BackendClient, error) {
	// Determine effective region (backend.region > defaultRegion)
	region := m.defaultRegion
	if bcfg.Region != "" {
		region = bcfg.Region
	}

	// Create credentials provider
	credsProvider, err := cred.NewProvider(bcfg.Credentials)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize credentials provider: %w", err)
	}

	// Create HTTP transport
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		MaxConnsPerHost:     10,
		IdleConnTimeout:     90 * time.Second,
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   m.timeout,
	}

	// Create base config
	cfg := aws.Config{
		Region:      region,
		Credentials: cred.ToAWSCredentialsProvider(credsProvider),
		HTTPClient:  httpClient,
	}

	// Create endpoint resolver for this backend
	endpointResolver, err := newEndpointResolver(bcfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create EndpointResolver: %w", err)
	}

	// Parse and apply per-backend timeout if configured
	var clientOptions []func(*s3.Options)
	clientOptions = append(clientOptions, func(o *s3.Options) {
		o.HTTPClient = &http.Client{
			Transport: transport,
			Timeout:   bcfg.Timeout,
		}
	})

	// AWS SDK retries are enabled to handle transient failures and redirects (3xx).
	// The circuit breaker filters out non-fatal errors (4xx client errors like 404/403)
	// and only counts actual backend failures (5xx, connection errors, etc.) toward
	// circuit breaker state. This allows retries to handle transient issues while still
	// providing failure isolation via the circuit breaker.
	clientOptions = append(clientOptions, func(o *s3.Options) {
		o.Retryer = retry.NewStandard(func(opts *retry.StandardOptions) {
			opts.MaxAttempts = 3 // Allow retries for transient failures and redirects
		})
	})

	// Always apply checksum and endpoint resolver
	clientOptions = append(clientOptions, func(o *s3.Options) {
		o.RequestChecksumCalculation = aws.RequestChecksumCalculationWhenRequired
		o.EndpointResolverV2 = endpointResolver
	})

	// Create S3 client with options
	s3Client := s3.NewFromConfig(cfg, clientOptions...)

	// Create circuit breaker with IsSuccessful to filter non-fatal errors
	cb := gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        fmt.Sprintf("backend-%s", id),
		MaxRequests: 3,
		Interval:    time.Second,
		Timeout:     10 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
			return counts.Requests >= 3 && failureRatio >= 0.6
		},
		// IsSuccessful treats non-fatal S3 errors (404, 403, etc.) as successes
		// to prevent false positives from triggering circuit breaker isolation.
		// Only actual backend failures (5xx, network errors) count as failures.
		IsSuccessful: IsNonFatalS3Error,
	})

	return &BackendClient{
		BackendConfig:    bcfg,
		Region:           region,
		EndpointResolver: endpointResolver,
		S3Client:         s3Client,
		S3Operations:     NewCircuitBreakerS3Operations(s3Client, cb),
		CredsProvider:    credsProvider,
		HTTPClient:       httpClient,
		Health:           &HealthState{Healthy: true},
	}, nil
}

func (m *Manager) GetClient(backendID string) (*BackendClient, error) {
	client, exists := m.clients[backendID]
	if !exists {
		return nil, fmt.Errorf("backend %q not found", backendID)
	}
	return client, nil
}

func (m *Manager) HealthCheck(ctx context.Context, backendID string) error {
	client, err := m.GetClient(backendID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Try head bucket
	_, err = client.S3Client.HeadBucket(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(client.Bucket),
	})

	if err != nil {
		client.Health.SetUnhealthy(err.Error())
		return err
	}

	client.Health.SetHealthy()
	return nil
}

func (m *Manager) Close() error {
	// Close HTTP clients
	for _, client := range m.clients {
		client.HTTPClient.CloseIdleConnections()
	}
	return nil
}
