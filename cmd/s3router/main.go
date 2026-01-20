package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moriyoshi/s3-router/internal/admin"
	"github.com/moriyoshi/s3-router/internal/auth"
	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/backend/proxy"
	"github.com/moriyoshi/s3-router/internal/bucket"
	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/moriyoshi/s3-router/internal/observability"
	"github.com/moriyoshi/s3-router/internal/routing"
	"github.com/moriyoshi/s3-router/internal/server"
)

// parseS3URL parses an S3 URL (s3://bucket/key) into bucket and key components
func parseS3URL(s3URL string) (bucket, key string, err error) {
	u, err := url.Parse(s3URL)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "s3" {
		return "", "", fmt.Errorf("expected s3:// scheme, got %s://", u.Scheme)
	}
	bucket = u.Host
	key = strings.TrimPrefix(u.Path, "/")
	return bucket, key, nil
}

func main() { //nolint:gocyclo
	var (
		configPath      = flag.String("config", "config.yaml", "Path to configuration file")
		listenAddr      = flag.String("listen", ":8080", "Address to listen on")
		adminAddr       = flag.String("admin", ":9090", "Admin server address")
		logLevel        = flag.String("log-level", "info", "Log level: debug, info, warn, error")
		logFormat       = flag.String("log-format", "auto", "Log format: auto, text, json")
		requestTimeout  = flag.Duration("timeout", 30*time.Second, "Request timeout")
		credentialsPath = flag.String("credentials", "", "Path to credentials file (optional)")
		checkRoute      = flag.String("check-route", "", "Check routing for S3 URL (e.g., s3://bucket/key) and exit")
	)
	flag.Parse()

	ctx := context.Background()

	// Initialize OpenTelemetry
	otelProvider, err := observability.InitOpenTelemetry(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to initialize OpenTelemetry: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := otelProvider.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintf(os.Stderr, "failed to shutdown OpenTelemetry: %v\n", err)
		}
	}()

	// Setup logger
	logger := observability.NewLoggerWithFormat(*logLevel, *logFormat)
	logger.Info("s3-router starting", "version", "0.1.0")

	// Load configuration
	cfg, err := config.LoadFromFile(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	logger.Info("configuration loaded", "backends", len(cfg.Backends), "buckets", len(cfg.Buckets))

	// Load credentials store
	var credStore auth.CredentialsStore

	// Use credentials store from config if available, or from CLI flag
	if cfg.CredentialsStore != nil {
		credStore = cfg.CredentialsStore
		logger.Info("using credentials store from config")
	} else if *credentialsPath != "" {
		credStore, err = auth.NewFileCredentialsStore(*credentialsPath)
		if err != nil {
			logger.Error("failed to load credentials", "error", err)
			os.Exit(1)
		}
		logger.Info("credentials store loaded", "path", *credentialsPath)
	} else {
		logger.Error("fatal error: no credentials store configured - provide via -credentials flag or credentials_store in config")
		os.Exit(1)
	}

	// Create backend manager
	backendMgr, err := backend.NewManager(cfg, *requestTimeout)
	if err != nil {
		logger.Error("failed to create backend manager", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := backendMgr.Close(); err != nil {
			logger.Error("failed to close backend manager", "error", err)
		}
	}()

	// Get parsed server configuration with defaults (set during validation)
	var readTimeout, writeTimeout, idleTimeout time.Duration
	var maxBodySize int64
	var routeCacheSize int

	if cfg.Server != nil {
		readTimeout = cfg.Server.ReadTimeout
		writeTimeout = cfg.Server.WriteTimeout
		idleTimeout = cfg.Server.IdleTimeout
		maxBodySize = cfg.Server.MaxBodySize
		routeCacheSize = cfg.Server.RouteCacheSize
	} else {
		// Fallback defaults (should not happen if config validation succeeds)
		readTimeout = 15 * time.Second
		writeTimeout = 15 * time.Second
		idleTimeout = 60 * time.Second
		maxBodySize = 4 * 1024 * 1024 * 1024 // 4GB default
		routeCacheSize = 1000
	}

	// Create routing matcher
	matcher, err := routing.NewMatcher(cfg, routeCacheSize)
	if err != nil {
		logger.Error("failed to create routing matcher", "error", err)
		os.Exit(1)
	}

	// Handle -check-route mode
	if *checkRoute != "" {
		bucketName, objectKey, err := parseS3URL(*checkRoute)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		decision, err := matcher.Match(ctx, bucketName, objectKey, "GET", nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "No route matched: %v\n", err)
			os.Exit(1)
		}

		// Get backend config to show the full physical path
		backendCfg, exists := cfg.Backends[decision.Backend.ID]
		if !exists {
			fmt.Fprintf(os.Stderr, "Error: backend %q not found in config\n", decision.Backend.ID)
			os.Exit(1)
		}

		physicalKey := backendCfg.Prefix + decision.RewrittenKey

		fmt.Printf("Input:\n")
		fmt.Printf("  Bucket: %s\n", bucketName)
		fmt.Printf("  Key:    %s\n", objectKey)
		fmt.Printf("\nRouting Decision:\n")
		fmt.Printf("  Backend:      %s\n", decision.Backend.ID)
		fmt.Printf("  Rewritten Key: %s\n", decision.RewrittenKey)
		fmt.Printf("\nPhysical Location:\n")
		fmt.Printf("  Bucket: %s\n", backendCfg.Bucket)
		fmt.Printf("  Key:    %s\n", physicalKey)
		fmt.Printf("  S3 URL: s3://%s/%s\n", backendCfg.Bucket, physicalKey)
		os.Exit(0)
	}

	// Create auth verifier
	verifier := server.NewVerifier(cfg, credStore)

	// Create bucket operation handler for bucket-level control-plane operations
	bucketOpsHandler := bucket.NewBucketOperationHandler(cfg)

	// Create ListObjectsV2 handler for virtual bucket object listing
	listObjectsV2Handler := bucket.NewListObjectsV2Handler(backendMgr, cfg, logger)

	// Create proxy executor
	executor := proxy.NewExecutor(backendMgr)
	executor.SetMatcher(matcher)

	// Create metrics
	metrics := observability.NewMetrics()

	s := &server.Server{
		Logger:               logger,
		Matcher:              matcher,
		Verifier:             verifier,
		BucketOpsHandler:     bucketOpsHandler,
		ListObjectsV2Handler: listObjectsV2Handler,
		Executor:             executor,
		Metrics:              metrics,
		MaxBodySize:          maxBodySize,
		VirtualHostChecker:   cfg.VirtualHosts,
	}

	// S3 API handler with middleware chain
	// Note: We don't use ServeMux here because it redirects paths containing "//" to "/"
	// which breaks S3 object keys that contain consecutive slashes.
	var mainHandler http.Handler = http.HandlerFunc(s.HandleS3Request)
	mainHandler = observability.RequestLogger(logger)(mainHandler)
	mainHandler = observability.TraceIDMiddleware(mainHandler)
	mainHandler = observability.OTelMiddleware(otelProvider.Tracer)(mainHandler)

	var serversMu sync.Mutex
	servers := make(map[interface {
		Shutdown(context.Context) error
		Close() error
	}]struct{})

	// Setup admin server
	adminServer := admin.NewServer(*adminAddr, backendMgr, cfg)
	servers[adminServer] = struct{}{}

	// Channel to signal startup errors from goroutines
	startupErrChan := make(chan error, 2)

	// Start admin server in goroutine
	go func() {
		logger.Info("admin server starting", "addr", *adminAddr)
		if err := adminServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serversMu.Lock()
			delete(servers, adminServer)
			serversMu.Unlock()
			logger.Error("admin server error", "error", err)
			startupErrChan <- fmt.Errorf("admin server failed to start: %w", err)
		}
	}()

	server := &http.Server{
		Addr:         *listenAddr,
		Handler:      mainHandler,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
	serversMu.Lock()
	servers[server] = struct{}{}
	serversMu.Unlock()

	// Start main server in goroutine
	go func() {
		logger.Info("main server starting", "addr", *listenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			serversMu.Lock()
			delete(servers, server)
			serversMu.Unlock()
			logger.Error("server error", "error", err)
			startupErrChan <- fmt.Errorf("main server failed to start: %w", err)
		}
	}()

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for either a signal or a startup error
	select {
	case <-sigChan:
		logger.Info("shutdown signal received")
	case err := <-startupErrChan:
		logger.Error("fatal startup error", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serversMu.Lock()
	defer serversMu.Unlock()
	for server := range servers {
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("server shutdown error", "error", err)
			err = server.Close()
			if err != nil {
				logger.Error("server close error", "error", err)
			}
		}
	}

	logger.Info("s3-router stopped")
}
