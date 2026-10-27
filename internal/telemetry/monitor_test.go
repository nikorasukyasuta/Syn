package telemetry

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"syn/internal/telemetry/procfs"
)

type mockWriteCloser struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	failErr error
	closed  bool
}

func (m *mockWriteCloser) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failErr != nil {
		return 0, m.failErr
	}
	return m.buf.Write(p)
}

func (m *mockWriteCloser) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *mockWriteCloser) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

type stubDBStats struct{}

func (s stubDBStats) PoolStats() DBPoolStats {
	return DBPoolStats{
		TotalConns:    8,
		AcquiredConns: 3,
		IdleConns:     5,
		MaxConns:      20,
	}
}

type stubQueueStats struct{}

func (s stubQueueStats) QueueStats() QueueStatsSnapshot {
	return QueueStatsSnapshot{
		Depth:     2,
		Capacity:  1024,
		Workers:   4,
		Processed: 150,
		Dropped:   0,
	}
}

type stubLockStats struct{}

func (s stubLockStats) ActiveLocksCount() int {
	return 5
}

func TestProcParsers(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// 1. Test /proc/loadavg parsing
	loadFile := filepath.Join(tmpDir, "loadavg")
	if err := os.WriteFile(loadFile, []byte("0.42 0.31 0.19 2/215 12345\n"), 0600); err != nil {
		t.Fatalf("failed writing mock loadavg: %v", err)
	}
	l1, l5, l15 := ReadLoadAverages(loadFile)
	if l1 != 0.42 || l5 != 0.31 || l15 != 0.19 {
		t.Fatalf("unexpected load averages: got (%v, %v, %v)", l1, l5, l15)
	}

	// 2. Test /proc/meminfo parsing
	memFile := filepath.Join(tmpDir, "meminfo")
	memContent := "MemTotal:        4096000 kB\nMemFree:         1024000 kB\nMemAvailable:    2048000 kB\n"
	if err := os.WriteFile(memFile, []byte(memContent), 0600); err != nil {
		t.Fatalf("failed writing mock meminfo: %v", err)
	}
	usedMB, totalMB, pct := ReadHostMemoryStats(memFile)
	if totalMB != 4000 || usedMB != 2000 || pct != 50 {
		t.Fatalf("unexpected memory stats: used=%v total=%v pct=%v", usedMB, totalMB, pct)
	}

	// 3. Test /proc/stat CPU delta calculation
	statFile := filepath.Join(tmpDir, "stat")
	// user=100, nice=0, system=100, idle=800, iowait=0 -> total=1000, idle=800
	if err := os.WriteFile(statFile, []byte("cpu  100 0 100 800 0 0 0 0 0 0\n"), 0600); err != nil {
		t.Fatalf("failed writing mock stat: %v", err)
	}
	tracker := procfs.NewCPUTracker()
	firstPct := tracker.SampleCPUPercent(statFile)
	if firstPct != 0 {
		t.Fatalf("expected 0 on initial baseline sample, got %v", firstPct)
	}

	// Advance by +100 busy ticks and +100 idle ticks -> 50% CPU usage
	if err := os.WriteFile(statFile, []byte("cpu  150 0 150 900 0 0 0 0 0 0\n"), 0600); err != nil {
		t.Fatalf("failed updating mock stat: %v", err)
	}
	secondPct := tracker.SampleCPUPercent(statFile)
	if secondPct != 50 {
		t.Fatalf("expected 50%% CPU utilization, got %v", secondPct)
	}
}

func TestMonitor_SampleCacheAndFlush(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	writer := &mockWriteCloser{}

	cfg := Config{
		FilePath:          "logs/test_metrics.jsonl",
		SampleInterval:    50 * time.Millisecond,
		FlushInterval:     200 * time.Millisecond,
		MaxHistorySamples: 3,
	}

	mon := NewMonitorWithWriter(cfg, writer, stubDBStats{}, stubQueueStats{}, stubLockStats{}, silentLogger)

	// Record 4 samples; MaxHistorySamples is 3 so history should cap at 3 while pendingBuffer holds all 4
	for i := 0; i < 4; i++ {
		s := mon.SampleNow()
		if s.DBTotalConns != 8 || s.QueueProcessed != 150 || s.ActiveUserLocks != 5 {
			t.Fatalf("unexpected subsystem metrics in sample: %+v", s)
		}
	}

	hist := mon.History()
	if len(hist) != 3 {
		t.Fatalf("expected bounded history of 3 samples, got %d", len(hist))
	}

	pending, totalFlushed, _, _ := mon.CacheStats()
	if pending != 4 || totalFlushed != 0 {
		t.Fatalf("expected 4 pending and 0 flushed, got pending=%d flushed=%d", pending, totalFlushed)
	}

	// Simulate transient disk error during Flush: pending buffer must be preserved
	writer.failErr = errors.New("disk full")
	if _, err := mon.Flush(); err == nil {
		t.Fatal("expected error when writer fails")
	}
	pendingAfterErr, _, _, _ := mon.CacheStats()
	if pendingAfterErr != 4 {
		t.Fatalf("expected all 4 samples retained in cache after write failure, got %d", pendingAfterErr)
	}

	// Recover writer and flush to JSONL
	writer.failErr = nil
	flushed, err := mon.Flush()
	if err != nil {
		t.Fatalf("unexpected flush error: %v", err)
	}
	if flushed != 4 {
		t.Fatalf("expected 4 flushed samples, got %d", flushed)
	}

	pendingAfterFlush, totalFlushedAfter, lastFlush, _ := mon.CacheStats()
	if pendingAfterFlush != 0 || totalFlushedAfter != 4 || lastFlush.IsZero() {
		t.Fatalf("unexpected cache stats after flush: pending=%d total=%d lastFlush=%v",
			pendingAfterFlush, totalFlushedAfter, lastFlush)
	}

	// Verify each flushed line is valid JSON matching ResourceSample
	lines := strings.Split(strings.TrimSpace(writer.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 JSONL lines in output, got %d", len(lines))
	}
	for idx, line := range lines {
		var parsed ResourceSample
		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			t.Fatalf("line %d is not valid JSON: %v", idx, err)
		}
		if parsed.Timestamp == "" || parsed.EpochMS <= 0 {
			t.Fatalf("line %d missing timestamp/epoch_ms: %+v", idx, parsed)
		}
	}
}

func TestMonitor_StartAndStopFlushesRemaining(t *testing.T) {
	t.Parallel()

	tmpFile := filepath.Join(t.TempDir(), "metrics.jsonl")
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	mon, err := NewMonitor(Config{
		FilePath:          tmpFile,
		SampleInterval:    25 * time.Millisecond,
		FlushInterval:     10 * time.Second, // Long flush interval so Stop() must flush cached samples
		MaxHistorySamples: 50,
	}, stubDBStats{}, stubQueueStats{}, stubLockStats{}, silentLogger)
	if err != nil {
		t.Fatalf("failed to create monitor: %v", err)
	}

	mon.Start()
	time.Sleep(70 * time.Millisecond)

	if err := mon.Stop(); err != nil {
		t.Fatalf("unexpected error stopping monitor: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("expected metrics file to be written on Stop(): %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 samples flushed to %s, got %d", tmpFile, len(lines))
	}
}

func TestMonitor_CPUBudgetGovernorUnderOnePercent(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	writer := &mockWriteCloser{}

	mon := NewMonitorWithWriter(DefaultConfig(), writer, stubDBStats{}, stubQueueStats{}, stubLockStats{}, silentLogger)

	// Hammer SampleBounded() 500 times back-to-back in a tight loop.
	// The 1% CPU budget governor (100x duty-cycle cooldown) must throttle intermediate calls
	// and serve the cached sample in O(1), keeping MonitorCPUPercent() <= 1.0%.
	executedCount := 0
	throttledCount := 0
	for i := 0; i < 500; i++ {
		_, sampled := mon.SampleBounded()
		if sampled {
			executedCount++
		} else {
			throttledCount++
		}
	}

	if throttledCount == 0 {
		t.Fatal("expected tight-loop SampleBounded calls to be throttled by the 1% CPU budget governor")
	}

	cpuPct := mon.MonitorCPUPercent()
	if cpuPct > MaxCPUPercentBudget {
		t.Fatalf("expected MonitorCPUPercent() <= %.2f%%, got %.2f%%", MaxCPUPercentBudget, cpuPct)
	}
}

func BenchmarkSampleNow(b *testing.B) {
	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	writer := &mockWriteCloser{}
	mon := NewMonitorWithWriter(DefaultConfig(), writer, stubDBStats{}, stubQueueStats{}, stubLockStats{}, silentLogger)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mon.SampleNow()
	}
}


