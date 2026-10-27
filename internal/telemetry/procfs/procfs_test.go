package procfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProcfsParsers(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	// 1. Test /proc/loadavg
	loadFile := filepath.Join(tmpDir, "loadavg")
	if err := os.WriteFile(loadFile, []byte("0.42 0.31 0.19 2/215 12345\n"), 0600); err != nil {
		t.Fatalf("failed writing mock loadavg: %v", err)
	}
	l1, l5, l15 := ReadLoadAverages(loadFile)
	if l1 != 0.42 || l5 != 0.31 || l15 != 0.19 {
		t.Fatalf("unexpected load averages: got (%v, %v, %v)", l1, l5, l15)
	}

	// 2. Test /proc/meminfo
	memFile := filepath.Join(tmpDir, "meminfo")
	memContent := "MemTotal:        4096000 kB\nMemFree:         1024000 kB\nMemAvailable:    2048000 kB\n"
	if err := os.WriteFile(memFile, []byte(memContent), 0600); err != nil {
		t.Fatalf("failed writing mock meminfo: %v", err)
	}
	usedMB, totalMB, pct := ReadHostMemoryStats(memFile)
	if totalMB != 4000 || usedMB != 2000 || pct != 50 {
		t.Fatalf("unexpected memory stats: used=%v total=%v pct=%v", usedMB, totalMB, pct)
	}

	// 3. Test /proc/stat CPU delta
	statFile := filepath.Join(tmpDir, "stat")
	if err := os.WriteFile(statFile, []byte("cpu  100 0 100 800 0 0 0 0 0 0\n"), 0600); err != nil {
		t.Fatalf("failed writing mock stat: %v", err)
	}
	tracker := NewCPUTracker()
	if firstPct := tracker.SampleCPUPercent(statFile); firstPct != 0 {
		t.Fatalf("expected 0 on initial baseline sample, got %v", firstPct)
	}
	if err := os.WriteFile(statFile, []byte("cpu  150 0 150 900 0 0 0 0 0 0\n"), 0600); err != nil {
		t.Fatalf("failed updating mock stat: %v", err)
	}
	if secondPct := tracker.SampleCPUPercent(statFile); secondPct != 50 {
		t.Fatalf("expected 50%% CPU utilization, got %v", secondPct)
	}
}

