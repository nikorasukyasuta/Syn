// Package rtmetrics provides lock-free, Stop-The-World-free Go runtime memory
// and garbage collection telemetry using the standard runtime/metrics API.
package rtmetrics

import (
	"math"
	"runtime"
	"runtime/metrics"
)

// Snapshot holds a point-in-time reading of Go runtime memory, GC, and scheduler metrics.
type Snapshot struct {
	HeapAllocMB  float64
	HeapInUseMB  float64
	SysMB        float64
	StackInUseMB float64
	NumGC        uint32
	Goroutines   int
	CPUCores     int
	GOMAXPROCS   int
}

// Read captures a Snapshot of the Go runtime without triggering a Stop-The-World pause.
func Read() Snapshot {
	samples := [5]metrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
		{Name: "/memory/classes/total:bytes"},
		{Name: "/memory/classes/heap/stacks:bytes"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
	metrics.Read(samples[:])

	var heapObj, heapUnused, totalSys, stacks, gcCycles uint64
	if samples[0].Value.Kind() == metrics.KindUint64 {
		heapObj = samples[0].Value.Uint64()
	}
	if samples[1].Value.Kind() == metrics.KindUint64 {
		heapUnused = samples[1].Value.Uint64()
	}
	if samples[2].Value.Kind() == metrics.KindUint64 {
		totalSys = samples[2].Value.Uint64()
	}
	if samples[3].Value.Kind() == metrics.KindUint64 {
		stacks = samples[3].Value.Uint64()
	}
	if samples[4].Value.Kind() == metrics.KindUint64 {
		gcCycles = samples[4].Value.Uint64()
	}

	return Snapshot{
		HeapAllocMB:  bytesToMB(heapObj),
		HeapInUseMB:  bytesToMB(heapObj + heapUnused),
		SysMB:        bytesToMB(totalSys),
		StackInUseMB: bytesToMB(stacks),
		NumGC:        uint32(gcCycles),
		Goroutines:   runtime.NumGoroutine(),
		CPUCores:     runtime.NumCPU(),
		GOMAXPROCS:   runtime.GOMAXPROCS(0),
	}
}

func bytesToMB(b uint64) float64 {
	return math.Round((float64(b)/(1024.0*1024.0))*100) / 100
}

