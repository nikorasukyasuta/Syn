package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

// Config specifies enterprise logging configuration parameters.
type Config struct {
	Level        string
	Format       string // "json" or "text"
	FilePath     string // e.g. "logs/syn.log"
	MaxSizeMB    int
	MaxBackups   int
	MaxAgeDays   int
	EnableStdout bool
	EnableFile   bool
}

// DefaultConfig returns production defaults for the logging suite.
func DefaultConfig() Config {
	return Config{
		Level:        "INFO",
		Format:       "json",
		FilePath:     "logs/syn.log",
		MaxSizeMB:    10,
		MaxBackups:   5,
		MaxAgeDays:   30,
		EnableStdout: true,
		EnableFile:   true,
	}
}

// Setup initializes the rotating file log suite, configures structured slog formatting,
// and returns the constructed logger along with an io.Closer for graceful flush on shutdown.
func Setup(cfg Config) (*slog.Logger, io.Closer, error) {
	var writers []io.Writer
	var closer io.Closer

	if cfg.EnableStdout {
		writers = append(writers, os.Stdout)
	}

	if cfg.EnableFile && cfg.FilePath != "" {
		rotator, err := NewRotatingFileWriter(RotatorConfig{
			Filename:   cfg.FilePath,
			MaxSizeMB:  cfg.MaxSizeMB,
			MaxBackups: cfg.MaxBackups,
			MaxAgeDays: cfg.MaxAgeDays,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("initializing rotating file logger: %w", err)
		}
		writers = append(writers, rotator)
		closer = rotator
	}

	if len(writers) == 0 {
		writers = append(writers, os.Stdout)
	}

	combinedOutput := io.MultiWriter(writers...)
	logLevel := ParseLevel(cfg.Level)

	handlerOpts := &slog.HandlerOptions{
		Level: logLevel,
	}

	var handler slog.Handler
	if strings.ToLower(strings.TrimSpace(cfg.Format)) == "text" {
		handler = slog.NewTextHandler(combinedOutput, handlerOpts)
	} else {
		handler = slog.NewJSONHandler(combinedOutput, handlerOpts)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger, closer, nil
}

// ParseLevel converts a string into the corresponding slog.Level.
func ParseLevel(raw string) slog.Level {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

