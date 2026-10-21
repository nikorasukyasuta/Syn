package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"sync"
)

// RotatingFileWriter implements an io.WriteCloser that automatically rotates
// log files when they exceed a configurable size limit and cleans up aged or excess backups.
type RotatingFileWriter struct {
	filename   string
	maxBytes   int64
	maxBackups int
	maxAge     time.Duration

	mu   sync.Mutex
	file *os.File
	size int64
}

// RotatorConfig defines parameters for file-based log rotation.
type RotatorConfig struct {
	// Filename is the destination file path (e.g., "logs/syn.log").
	Filename string
	// MaxSizeMB is the maximum size in megabytes before rotation triggers.
	MaxSizeMB int
	// MaxBackups is the maximum number of old log files to retain.
	MaxBackups int
	// MaxAgeDays is the maximum retention time in days before pruning old backups.
	MaxAgeDays int
}

// NewRotatingFileWriter initializes the rotating file writer and creates parent directories if needed.
func NewRotatingFileWriter(cfg RotatorConfig) (*RotatingFileWriter, error) {
	if cfg.Filename == "" {
		cfg.Filename = "logs/syn.log"
	}
	if cfg.MaxSizeMB <= 0 {
		cfg.MaxSizeMB = 10
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 5
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 30
	}

	dir := filepath.Dir(cfg.Filename)
	if err := os.MkdirAll(dir, 0777); err != nil {
		return nil, fmt.Errorf("creating log directory %q: %w", dir, err)
	}

	w := &RotatingFileWriter{
		filename:   cfg.Filename,
		maxBytes:   int64(cfg.MaxSizeMB) * 1024 * 1024,
		maxBackups: cfg.MaxBackups,
		maxAge:     time.Duration(cfg.MaxAgeDays) * 24 * time.Hour,
	}

	if err := w.openExistingOrNew(); err != nil {
		return nil, err
	}

	return w, nil
}

// Write writes data to the active log file, triggering rotation if the write would exceed maxBytes.
func (w *RotatingFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	writeLen := int64(len(p))
	if writeLen > w.maxBytes {
		return 0, fmt.Errorf("write length %d exceeds max file size %d", writeLen, w.maxBytes)
	}

	if w.file == nil {
		if err := w.openExistingOrNew(); err != nil {
			return 0, err
		}
	}

	if w.size+writeLen > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, fmt.Errorf("rotating log file: %w", err)
		}
	}

	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

// Close cleanly flushes and closes the active log file.
func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// openExistingOrNew opens the primary log file in append mode and records its current size.
func (w *RotatingFileWriter) openExistingOrNew() error {
	info, err := os.Stat(w.filename)
	if err != nil {
		if os.IsNotExist(err) {
			f, err := os.OpenFile(w.filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
			if err != nil {
				return fmt.Errorf("creating log file: %w", err)
			}
			w.file = f
			w.size = 0
			return nil
		}
		return fmt.Errorf("stat log file: %w", err)
	}

	f, err := os.OpenFile(w.filename, os.O_APPEND|os.O_WRONLY, 0666)
	if err != nil {
		return fmt.Errorf("opening existing log file: %w", err)
	}

	w.file = f
	w.size = info.Size()
	return nil
}

// rotate closes the current file, renames it with an ISO timestamp, creates a new file,
// and prunes older backups according to maxBackups and maxAge.
func (w *RotatingFileWriter) rotate() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}

	timestamp := time.Now().UTC().Format("2006-01-02T15-04-05.000")
	backupName := fmt.Sprintf("%s.%s", w.filename, timestamp)

	if err := os.Rename(w.filename, backupName); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("renaming log file to %s: %w", backupName, err)
	}

	f, err := os.OpenFile(w.filename, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		return fmt.Errorf("opening new log file after rotation: %w", err)
	}

	w.file = f
	w.size = 0

	// Prune old backups asynchronously to avoid blocking log writes
	go w.pruneBackups()

	return nil
}

type backupFile struct {
	path    string
	modTime time.Time
}

// pruneBackups removes backups that exceed maxBackups or are older than maxAge.
func (w *RotatingFileWriter) pruneBackups() {
	dir := filepath.Dir(w.filename)
	base := filepath.Base(w.filename)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	var backups []backupFile
	prefix := base + "."

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.HasPrefix(entry.Name(), prefix) {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			backups = append(backups, backupFile{
				path:    filepath.Join(dir, entry.Name()),
				modTime: info.ModTime(),
			})
		}
	}

	// Sort newest first
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})

	now := time.Now()
	for i, b := range backups {
		// Prune if count exceeds limit or if age exceeds threshold
		if i >= w.maxBackups || (w.maxAge > 0 && now.Sub(b.modTime) > w.maxAge) {
			_ = os.Remove(b.path)
		}
	}
}

// Ensure RotatingFileWriter implements io.WriteCloser at compile time.
var _ io.WriteCloser = (*RotatingFileWriter)(nil)
