package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"syn/internal/repository/postgres"
)

func TestNewPool_InvalidURL(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := postgres.NewPool(ctx, "invalid:////url")
	if err == nil {
		t.Fatal("expected error parsing invalid database URL, got nil")
	}
	if !strings.Contains(err.Error(), "parsing database config") {
		t.Fatalf("expected parsing error, got %v", err)
	}
}

func TestNewPool_ConnectionTimeout(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Connecting to an unreachable RFC 5737 test port should gracefully timeout
	_, err := postgres.NewPool(ctx, "postgres://user:pass@192.0.2.1:5432/testdb?connect_timeout=1")
	if err == nil {
		t.Fatal("expected timeout error when connecting to unreachable host, got nil")
	}
	if !strings.Contains(err.Error(), "pinging database timed out") {
		t.Fatalf("expected ping timeout error, got %v", err)
	}
}

