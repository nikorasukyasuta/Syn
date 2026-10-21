package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRotatingFileWriter_BasicWriteAndAppend(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "test.log")

	w, err := NewRotatingFileWriter(RotatorConfig{
		Filename:   logPath,
		MaxSizeMB:  1,
		MaxBackups: 3,
		MaxAgeDays: 1,
	})
	if err != nil {
		t.Fatalf("failed to create writer: %v", err)
	}
	defer w.Close()

	msg1 := []byte("Hello log 1\n")
	n, err := w.Write(msg1)
	if err != nil || n != len(msg1) {
		t.Fatalf("unexpected write error: %v", err)
	}

	content, err := os.ReadFile(logPath)
	if err != nil || string(content) != string(msg1) {
		t.Fatalf("expected content %q, got %q", string(msg1), string(content))
	}

	// Close and reopen to verify append
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	w2, err := NewRotatingFileWriter(RotatorConfig{
		Filename:   logPath,
		MaxSizeMB:  1,
		MaxBackups: 3,
		MaxAgeDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w2.Close()

	msg2 := []byte("Hello log 2\n")
	_, err = w2.Write(msg2)
	if err != nil {
		t.Fatal(err)
	}

	content2, err := os.ReadFile(logPath)
	if err != nil || string(content2) != "Hello log 1\nHello log 2\n" {
		t.Fatalf("expected appended content, got %q", string(content2))
	}
}

func TestRotatingFileWriter_Rotation(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "rotation.log")

	w, err := NewRotatingFileWriter(RotatorConfig{
		Filename:   logPath,
		MaxSizeMB:  1, // We will manually set maxBytes for test
		MaxBackups: 2,
		MaxAgeDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// Override maxBytes to 50 bytes for immediate rotation testing
	w.maxBytes = 50

	data := []byte("12345678901234567890\n") // 21 bytes
	_, err = w.Write(data)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.Write(data) // 42 bytes
	if err != nil {
		t.Fatal(err)
	}

	// Third write exceeds 50 bytes -> triggers rotation!
	_, err = w.Write(data) // rotation triggered, write lands in new file
	if err != nil {
		t.Fatal(err)
	}

	// Let pruning complete
	time.Sleep(50 * time.Millisecond)

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	// Expect primary file + 1 rotated backup
	var rotatedBackups int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "rotation.log.") {
			rotatedBackups++
		}
	}

	if rotatedBackups != 1 {
		t.Fatalf("expected 1 rotated backup file, found %d", rotatedBackups)
	}
}

func TestRotatingFileWriter_PruneBackups(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "prune.log")

	w := &RotatingFileWriter{
		filename:   logPath,
		maxBytes:   1000,
		maxBackups: 2,
		maxAge:     24 * time.Hour,
	}

	// Manually create 4 simulated backup files with timestamps
	now := time.Now()
	for i := 1; i <= 4; i++ {
		name := filepath.Join(tempDir, fmt.Sprintf("prune.log.2026-01-0%d", i))
		_ = os.WriteFile(name, []byte("backup"), 0600)
		modTime := now.Add(time.Duration(i) * time.Hour)
		_ = os.Chtimes(name, modTime, modTime)
	}

	w.pruneBackups()

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}

	var remaining int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "prune.log.") {
			remaining++
		}
	}

	if remaining != 2 {
		t.Fatalf("expected exactly 2 backups after pruning, found %d", remaining)
	}
}

func TestRotatingFileWriter_WriteExceedsMaxSize(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "limit.log")

	w, err := NewRotatingFileWriter(RotatorConfig{
		Filename:  logPath,
		MaxSizeMB: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.maxBytes = 10
	_, err = w.Write([]byte("this string is much longer than 10 bytes"))
	if err == nil {
		t.Fatal("expected error when single write exceeds maxBytes, got nil")
	}
}

func TestRotatingFileWriter_ConcurrentWrites(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	logPath := filepath.Join(tempDir, "concurrent.log")

	w, err := NewRotatingFileWriter(RotatorConfig{
		Filename:   logPath,
		MaxSizeMB:  1,
		MaxBackups: 3,
		MaxAgeDays: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	w.maxBytes = 200 // cause frequent rotations

	var wg sync.WaitGroup
	workers := 10
	iterations := 25

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_, _ = w.Write([]byte(fmt.Sprintf("worker %d line %d\n", id, j)))
			}
		}(i)
	}

	wg.Wait()
}

