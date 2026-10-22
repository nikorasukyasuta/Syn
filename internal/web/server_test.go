package web_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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