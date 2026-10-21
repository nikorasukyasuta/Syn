package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestParseLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  slog.Level
	}{
		{"DEBUG", slog.LevelDebug},
		{"debug", slog.LevelDebug},
		{"WARN", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"ERROR", slog.LevelError},
		{"INFO", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
		{"", slog.LevelInfo},
	}

	for _, tc := range tests {
		got := ParseLevel(tc.input)
		if got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestSetup_StdoutAndFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "app.log")

	cfg := Config{
		Level:        "DEBUG",
		Format:       "json",
		FilePath:     logPath,
		MaxSizeMB:    1,
		MaxBackups:   2,
		MaxAgeDays:   7,
		EnableStdout: true,
		EnableFile:   true,
	}

	logger, closer, err := Setup(cfg)
	if err != nil {
		t.Fatalf("unexpected Setup error: %v", err)
	}
	if closer == nil {
		t.Fatal("expected non-nil closer")
	}
	defer closer.Close()

	logger.Info("test info message", "key", "val")

	_ = closer.Close()

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed reading log file: %v", err)
	}
	if len(content) == 0 {
		t.Fatal("expected log file to contain content")
	}
}

func TestSetup_TextFormat(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "text.log")

	cfg := Config{
		Level:        "INFO",
		Format:       "text",
		FilePath:     logPath,
		MaxSizeMB:    1,
		EnableStdout: false,
		EnableFile:   true,
	}

	logger, closer, err := Setup(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()

	logger.Info("hello world")
	_ = closer.Close()

	content, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 {
		t.Fatal("expected text log content")
	}
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg.Level != "INFO" || cfg.Format != "json" || cfg.FilePath == "" {
		t.Errorf("unexpected default config: %+v", cfg)
	}
}

