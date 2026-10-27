package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"syn/internal/telemetry"
	"syn/internal/version"
)

// Server provides the public HTTP server hosting Terms of Service,
// Privacy Policy, landing portal, HTTP health checks, and time-series metrics.
type Server struct {
	httpServer       *http.Server
	logger           *slog.Logger
	port             string
	telemetryMonitor *telemetry.Monitor
}

// NewServer initializes a new Web Server on the specified port.
func NewServer(port string, logger *slog.Logger) *Server {
	if port == "" {
		port = "8080"
	}
	if logger == nil {
		logger = slog.Default()
	}

	s := &Server{
		logger: logger,
		port:   port,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/terms", s.handleTerms)
	mux.HandleFunc("/terms-and-conditions", s.handleTerms)
	mux.HandleFunc("/privacy", s.handlePrivacy)
	mux.HandleFunc("/privacy-policy", s.handlePrivacy)
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)

	s.httpServer = &http.Server{
		Addr:         ":" + port,
		Handler:      s.securityHeadersMiddleware(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return s
}

// SetTelemetryMonitor wires the resource telemetry monitor so /metrics can expose cached time-series data.
func (s *Server) SetTelemetryMonitor(mon *telemetry.Monitor) {
	s.telemetryMonitor = mon
}

// Handler returns the underlying HTTP handler, useful for testing.
func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Start runs the HTTP server. It blocks until Shutdown is called or an error occurs.
func (s *Server) Start() error {
	s.logger.Info("starting public web portal", "port", s.port)
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("web server listen error: %w", err)
	}
	return nil
}

// Shutdown gracefully shuts down the server with context timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down public web portal")
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(HomeHTML))
}

func (s *Server) handleTerms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(TermsHTML))
}

func (s *Server) handlePrivacy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(PrivacyHTML))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"version": version.CurrentVersion,
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if s.telemetryMonitor == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "disabled",
			"samples": []any{},
		})
		return
	}

	pending, flushed, lastFlush, filePath := s.telemetryMonitor.CacheStats()
	samples := s.telemetryMonitor.History()

	var lastFlushStr string
	if !lastFlush.IsZero() {
		lastFlushStr = lastFlush.Format(time.RFC3339)
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":         "ok",
		"version":        version.CurrentVersion,
		"metrics_file":   filePath,
		"buffered_count": pending,
		"flushed_total":  flushed,
		"last_flush_at":  lastFlushStr,
		"samples":        samples,
	})
}


