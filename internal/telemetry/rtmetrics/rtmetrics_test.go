package rtmetrics

import "testing"

func TestReadRuntimeMetrics(t *testing.T) {
	t.Parallel()

	snap := Read()
	if snap.SysMB <= 0 {
		t.Fatalf("expected positive SysMB, got %v", snap.SysMB)
	}
	if snap.Goroutines <= 0 {
		t.Fatalf("expected positive Goroutines, got %d", snap.Goroutines)
	}
	if snap.CPUCores <= 0 || snap.GOMAXPROCS <= 0 {
		t.Fatalf("expected positive CPUCores (%d) and GOMAXPROCS (%d)", snap.CPUCores, snap.GOMAXPROCS)
	}
}

