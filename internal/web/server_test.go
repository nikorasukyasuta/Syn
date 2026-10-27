package web_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"syn/internal/telemetry"
	"syn/internal/version"
	"syn/internal/web"
)

func TestWebServer_Endpoints(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := web.NewServer("0", logger)
	handler := srv.Handler()

	tests := []struct {
		name           string
		path           string
		expectedStatus int
		expectedBody   string
		expectedHeader string
	}{
		{
			name:           "Root path serves Home HTML",
			path:           "/",
			expectedStatus: http.StatusOK,
			expectedBody:   "Syn Discord Security & Community Suite",
			expectedHeader: "text/html; charset=utf-8",
		},
		{
			name:           "Terms endpoint serves Terms HTML",
			path:           "/terms",
			expectedStatus: http.StatusOK,
			expectedBody:   "Terms & Conditions",
			expectedHeader: "text/html; charset=utf-8",
		},
		{
			name:           "Terms and Conditions alias serves Terms HTML",
			path:           "/terms-and-conditions",
			expectedStatus: http.StatusOK,
			expectedBody:   "Terms & Conditions",
			expectedHeader: "text/html; charset=utf-8",
		},
		{
			name:           "Privacy endpoint serves Privacy Policy HTML",
			path:           "/privacy",
			expectedStatus: http.StatusOK,
			expectedBody:   "Privacy Policy",
			expectedHeader: "text/html; charset=utf-8",
		},
		{
			name:           "Privacy Policy alias serves Privacy Policy HTML",
			path:           "/privacy-policy",
			expectedStatus: http.StatusOK,
			expectedBody:   "Privacy Policy",
			expectedHeader: "text/html; charset=utf-8",
		},
		{
			name:           "Health check serves JSON",
			path:           "/health",
			expectedStatus: http.StatusOK,
			expectedBody:   version.CurrentVersion,
			expectedHeader: "application/json",
		},
		{
			name:           "Unknown path returns 404",
			path:           "/nonexistent",
			expectedStatus: http.StatusNotFound,
			expectedBody:   "404 page not found",
			expectedHeader: "text/plain; charset=utf-8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.expectedStatus {
				t.Errorf("expected status %d, got %d", tc.expectedStatus, rec.Code)
			}

			body := rec.Body.String()
			if !strings.Contains(body, tc.expectedBody) {
				t.Errorf("expected body to contain %q, got: %s", tc.expectedBody, body)
			}

			contentType := rec.Header().Get("Content-Type")
			if !strings.Contains(contentType, tc.expectedHeader) {
				t.Errorf("expected content-type %q, got %q", tc.expectedHeader, contentType)
			}

			// Verify security headers
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Errorf("missing or invalid X-Content-Type-Options header")
			}
			if rec.Header().Get("X-Frame-Options") != "DENY" {
				t.Errorf("missing or invalid X-Frame-Options header")
			}
		})
	}
}

func TestWebServer_HealthJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := web.NewServer("0", logger)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	srv.Handler().ServeHTTP(rec, req)

	var resp map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse health response as json: %v", err)
	}

	if resp["status"] != "ok" {
		t.Errorf("expected status 'ok', got %q", resp["status"])
	}
	if resp["version"] != version.CurrentVersion {
		t.Errorf("expected version %q, got %q", version.CurrentVersion, resp["version"])
	}
}

func TestWebServer_StartAndShutdown(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Use port 0 to bind to an ephemeral available port
	srv := web.NewServer("28919", logger)

	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Start()
	}()

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("failed to gracefully shutdown web server: %v", err)
	}

	err := <-errChan
	if err != nil {
		t.Errorf("expected clean shutdown, got error: %v", err)
	}
}

func TestWebServer_MetricsEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := web.NewServer("0", logger)

	// 1. Without telemetry monitor configured
	req1 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec1 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /metrics without monitor, got %d", rec1.Code)
	}
	if !strings.Contains(rec1.Body.String(), `"status":"disabled"`) {
		t.Fatalf("expected disabled status when monitor is nil, got %s", rec1.Body.String())
	}

	// 2. With active telemetry monitor and cached sample
	tmpFile := filepath.Join(t.TempDir(), "metrics.jsonl")
	mon, err := telemetry.NewMonitor(telemetry.Config{
		FilePath:          tmpFile,
		SampleInterval:    time.Minute,
		FlushInterval:     time.Minute,
		MaxHistorySamples: 10,
	}, nil, nil, nil, logger)
	if err != nil {
		t.Fatalf("failed to create telemetry monitor: %v", err)
	}
	defer mon.Stop()

	mon.SampleNow()
	srv.SetTelemetryMonitor(mon)

	req2 := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on /metrics with monitor, got %d", rec2.Code)
	}

	var payload map[string]any
	if err := json.Unmarshal(rec2.Body.Bytes(), &payload); err != nil {
		t.Fatalf("failed to unmarshal /metrics JSON: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected status 'ok', got %v", payload["status"])
	}
	samples, ok := payload["samples"].([]any)
	if !ok || len(samples) != 1 {
		t.Fatalf("expected 1 cached sample in /metrics response, got %v", payload["samples"])
	}
}
