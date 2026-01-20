package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/moriyoshi/s3-router/internal/backend"
	"github.com/moriyoshi/s3-router/internal/config"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Server struct {
	http.Server
	backend *backend.Manager
	config  *config.Config
}

type HealthResponse struct {
	Status string    `json:"status"`
	Time   time.Time `json:"time"`
}

type ReadyResponse struct {
	Ready    bool                         `json:"ready"`
	Backends map[string]BackendHealthInfo `json:"backends"`
	Time     time.Time                    `json:"time"`
}

type BackendHealthInfo struct {
	Healthy             bool      `json:"healthy"`
	LastCheck           time.Time `json:"last_check"`
	LastError           string    `json:"last_error,omitempty"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
}

type ConfigInfo struct {
	Backends map[string]*config.BackendConfig `json:"backends"`
	Buckets  map[string]config.BucketConfig   `json:"buckets"`
}

func NewServer(addr string, backendMgr *backend.Manager, cfg *config.Config) *Server {
	mux := http.NewServeMux()

	s := &Server{
		backend: backendMgr,
		config:  cfg,
		Server: http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}

	// Setup routes
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /metrics", promhttp.Handler().ServeHTTP)
	mux.HandleFunc("GET /admin/config", s.handleConfig)
	mux.HandleFunc("POST /admin/backend/{id}/health-check", s.handleBackendHealthCheck)

	return s
}

func (s *Server) Start() error {
	return s.ListenAndServe()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := HealthResponse{
		Status: "ok",
		Time:   time.Now(),
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Check backend health
	backendInfos := make(map[string]BackendHealthInfo)
	allReady := true

	for id, client := range s.backend.GetClients() {
		healthy, lastCheck, lastError, consecutiveFailures := client.Health.GetHealthSnapshot()
		info := BackendHealthInfo{
			Healthy:             healthy,
			LastCheck:           lastCheck,
			LastError:           lastError,
			ConsecutiveFailures: consecutiveFailures,
		}
		backendInfos[id] = info

		if !healthy {
			allReady = false
		}
	}

	resp := ReadyResponse{
		Ready:    allReady,
		Backends: backendInfos,
		Time:     time.Now(),
	}

	if allReady {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	info := ConfigInfo{
		Backends: s.config.Backends,
		Buckets:  s.config.Buckets,
	}

	_ = json.NewEncoder(w).Encode(info)
}

func (s *Server) handleBackendHealthCheck(w http.ResponseWriter, r *http.Request) {
	// Extract backend ID from path: /admin/backend/{id}/health-check
	backendID := strings.TrimPrefix(r.URL.Path, "/admin/backend/")
	backendID = strings.TrimSuffix(backendID, "/health-check")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := s.backend.HealthCheck(ctx, backendID)

	w.Header().Set("Content-Type", "application/json")

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
