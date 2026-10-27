// Package telemetry provides a low-overhead (<1% CPU hard-capped) in-memory system
// resource usage cache and periodic time-series metric flusher for production
// monitoring and graphing on AWS EC2.
package telemetry

import (
	"math"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxCPUPercentBudget is the strict upper bound (1.0%) on CPU utilization allowed
// for the telemetry monitoring subsystem.
const MaxCPUPercentBudget = 1.0

// DBPoolStats represents a snapshot of PostgreSQL connection pool utilization.
type DBPoolStats struct {
	TotalConns    int32
	AcquiredConns int32
	IdleConns     int32
	MaxConns      int32
}

// DBStatsProvider abstracts database connection pool telemetry so the telemetry
// package remains decoupled from concrete database drivers.
type DBStatsProvider interface {
	PoolStats() DBPoolStats
}

// QueueStatsSnapshot represents a snapshot of the asynchronous command worker queue.
type QueueStatsSnapshot struct {
	Depth     int
	Capacity  int
	Workers   int
	Processed uint64
	Dropped   uint64
}

// QueueStatsProvider abstracts command queue telemetry.
type QueueStatsProvider interface {
	QueueStats() QueueStatsSnapshot
}

// LockStatsProvider abstracts per-user mutex manager telemetry.
type LockStatsProvider interface {
	ActiveLocksCount() int
}

// ResourceSample represents a single point-in-time system and application resource
// measurement formatted for time-series graphing (JSON Lines / CSV / Prometheus / Grafana).
type ResourceSample struct {
	Timestamp        string  `json:"timestamp"`
	EpochMS          int64   `json:"epoch_ms"`
	HostCPUPercent   float64 `json:"host_cpu_pct"`
	MonitorCPUPct    float64 `json:"monitor_cpu_pct"`
	LoadAvg1m        float64 `json:"load_avg_1m"`
	LoadAvg5m        float64 `json:"load_avg_5m"`
	LoadAvg15m       float64 `json:"load_avg_15m"`
	HostMemUsedMB    float64 `json:"host_mem_used_mb"`
	HostMemTotalMB   float64 `json:"host_mem_total_mb"`
	HostMemPercent   float64 `json:"host_mem_pct"`
	GoHeapAllocMB    float64 `json:"go_heap_alloc_mb"`
	GoHeapInUseMB    float64 `json:"go_heap_inuse_mb"`
	GoSysMB          float64 `json:"go_sys_mb"`
	GoStackInUseMB   float64 `json:"go_stack_inuse_mb"`
	GoNumGC          uint32  `json:"go_num_gc"`
	GoGCPauseTotalMS float64 `json:"go_gc_pause_total_ms"`
	GoGoroutines     int     `json:"go_goroutines"`
	CPUCores         int     `json:"cpu_cores"`
	GOMAXPROCS       int     `json:"gomaxprocs"`
	DBTotalConns     int32   `json:"db_total_conns"`
	DBAcquiredConns  int32   `json:"db_acquired_conns"`
	DBIdleConns      int32   `json:"db_idle_conns"`
	DBMaxConns       int32   `json:"db_max_conns"`
	QueueDepth       int     `json:"queue_depth"`
	QueueCapacity    int     `json:"queue_capacity"`
	QueueWorkers     int     `json:"queue_workers"`
	QueueProcessed   uint64  `json:"queue_processed_total"`
	QueueDropped     uint64  `json:"queue_dropped_total"`
	ActiveUserLocks  int     `json:"active_user_locks"`
}

// cpuTracker tracks previous /proc/stat CPU jiffies to compute accurate delta CPU utilization percentages.
type cpuTracker struct {
	mu        sync.Mutex
	prevTotal uint64
	prevIdle  uint64
	hasPrev   bool
}

// sampleCPUPercent reads the first line of /proc/stat using a fixed stack buffer
// and calculates the percentage of active CPU time since the last sample.
func (c *cpuTracker) sampleCPUPercent(procStatPath string) float64 {
	total, idle, ok := readProcStatTicks(procStatPath)
	if !ok {
		return 0
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.hasPrev || total <= c.prevTotal {
		c.prevTotal = total
		c.prevIdle = idle
		c.hasPrev = true
		return 0
	}

	deltaTotal := float64(total - c.prevTotal)
	deltaIdle := float64(idle - c.prevIdle)
	c.prevTotal = total
	c.prevIdle = idle

	if deltaTotal <= 0 {
		return 0
	}
	busyPct := ((deltaTotal - deltaIdle) / deltaTotal) * 100.0
	if busyPct < 0 {
		busyPct = 0
	}
	if busyPct > 100 {
		busyPct = 100
	}
	return round2(busyPct)
}

// readFirstBytes reads at most len(buf) bytes from a virtual /proc file without
// allocating heap buffers or reading trailing kernel lines.
func readFirstBytes(path string, buf []byte) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	n, _ := f.Read(buf)
	_ = f.Close()
	return n
}

// readProcStatTicks parses the aggregate "cpu " first line from /proc/stat using a stack buffer.
func readProcStatTicks(path string) (total uint64, idle uint64, ok bool) {
	var buf [256]byte
	n := readFirstBytes(path, buf[:])
	if n < 6 {
		return 0, 0, false
	}

	data := buf[:n]
	// Isolate the first line ("cpu  ...")
	for i, b := range data {
		if b == '\n' {
			data = data[:i]
			break
		}
	}
	if len(data) < 5 || data[0] != 'c' || data[1] != 'p' || data[2] != 'u' || data[3] != ' ' {
		return 0, 0, false
	}

	fieldIdx := 0
	i := 4
	for i < len(data) {
		for i < len(data) && data[i] == ' ' {
			i++
		}
		if i >= len(data) {
			break
		}
		var val uint64
		hasDigit := false
		for i < len(data) && data[i] >= '0' && data[i] <= '9' {
			val = val*10 + uint64(data[i]-'0')
			hasDigit = true
			i++
		}
		if hasDigit {
			total += val
			// Field 3 is idle, field 4 is iowait
			if fieldIdx == 3 || fieldIdx == 4 {
				idle += val
			}
			fieldIdx++
		} else {
			// Skip non-digit characters if any
			for i < len(data) && data[i] != ' ' {
				i++
			}
		}
	}

	if fieldIdx < 4 {
		return 0, 0, false
	}
	return total, idle, true
}

// ReadLoadAverages parses 1m, 5m, and 15m load averages from /proc/loadavg using a stack buffer.
func ReadLoadAverages(path string) (load1, load5, load15 float64) {
	var buf [128]byte
	n := readFirstBytes(path, buf[:])
	if n == 0 {
		return 0, 0, 0
	}
	parts := strings.Fields(string(buf[:n]))
	if len(parts) >= 3 {
		load1, _ = strconv.ParseFloat(parts[0], 64)
		load5, _ = strconv.ParseFloat(parts[1], 64)
		load15, _ = strconv.ParseFloat(parts[2], 64)
	}
	return round2(load1), round2(load5), round2(load15)
}

// ReadHostMemoryStats parses MemTotal and MemAvailable from the first 256 bytes of /proc/meminfo.
func ReadHostMemoryStats(path string) (usedMB, totalMB, usagePct float64) {
	var buf [256]byte
	n := readFirstBytes(path, buf[:])
	if n == 0 {
		return 0, 0, 0
	}

	var totalKB, availKB float64
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		if strings.HasPrefix(line, "MemTotal:") {
			totalKB = parseProcKB(line)
		} else if strings.HasPrefix(line, "MemAvailable:") {
			availKB = parseProcKB(line)
		}
		if totalKB > 0 && availKB > 0 {
			break
		}
	}

	if totalKB <= 0 {
		return 0, 0, 0
	}
	totalMB = totalKB / 1024.0
	usedMB = (totalKB - availKB) / 1024.0
	if usedMB < 0 {
		usedMB = 0
	}
	usagePct = (usedMB / totalMB) * 100.0
	return round2(usedMB), round2(totalMB), round2(usagePct)
}

func parseProcKB(line string) float64 {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		val, _ := strconv.ParseFloat(fields[1], 64)
		return val
	}
	return 0
}

func bytesToMB(b uint64) float64 {
	return round2(float64(b) / (1024.0 * 1024.0))
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

// readGoRuntimeMetrics reads Go memory and GC counters via runtime/metrics without
// triggering a Stop-The-World (STW) pause (unlike runtime.ReadMemStats).
func readGoRuntimeMetrics() (heapAllocMB, heapInUseMB, sysMB, stackInUseMB float64, numGC uint32) {
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

	return bytesToMB(heapObj),
		bytesToMB(heapObj + heapUnused),
		bytesToMB(totalSys),
		bytesToMB(stacks),
		uint32(gcCycles)
}

// collectRuntimeSample gathers Go runtime, OS /proc, DB pool, queue, and mutex metrics
// using lock-free runtime/metrics and bounded stack-buffer /proc reads.
func collectRuntimeSample(
	now time.Time,
	cpu *cpuTracker,
	monitorCPUPct float64,
	dbProvider DBStatsProvider,
	queueProvider QueueStatsProvider,
	lockProvider LockStatsProvider,
) ResourceSample {
	heapAllocMB, heapInUseMB, sysMB, stackInUseMB, numGC := readGoRuntimeMetrics()
	l1, l5, l15 := ReadLoadAverages("/proc/loadavg")
	memUsed, memTotal, memPct := ReadHostMemoryStats("/proc/meminfo")
	cpuPct := cpu.sampleCPUPercent("/proc/stat")

	sample := ResourceSample{
		Timestamp:       now.UTC().Format(time.RFC3339),
		EpochMS:         now.UTC().UnixMilli(),
		HostCPUPercent:  cpuPct,
		MonitorCPUPct:   monitorCPUPct,
		LoadAvg1m:       l1,
		LoadAvg5m:       l5,
		LoadAvg15m:      l15,
		HostMemUsedMB:   memUsed,
		HostMemTotalMB:  memTotal,
		HostMemPercent:  memPct,
		GoHeapAllocMB:   heapAllocMB,
		GoHeapInUseMB:   heapInUseMB,
		GoSysMB:         sysMB,
		GoStackInUseMB:  stackInUseMB,
		GoNumGC:         numGC,
		GoGoroutines:    runtime.NumGoroutine(),
		CPUCores:        runtime.NumCPU(),
		GOMAXPROCS:      runtime.GOMAXPROCS(0),
	}

	if dbProvider != nil {
		dbStat := dbProvider.PoolStats()
		sample.DBTotalConns = dbStat.TotalConns
		sample.DBAcquiredConns = dbStat.AcquiredConns
		sample.DBIdleConns = dbStat.IdleConns
		sample.DBMaxConns = dbStat.MaxConns
	}

	if queueProvider != nil {
		qStat := queueProvider.QueueStats()
		sample.QueueDepth = qStat.Depth
		sample.QueueCapacity = qStat.Capacity
		sample.QueueWorkers = qStat.Workers
		sample.QueueProcessed = qStat.Processed
		sample.QueueDropped = qStat.Dropped
	}

	if lockProvider != nil {
		sample.ActiveUserLocks = lockProvider.ActiveLocksCount()
	}

	return sample
}
