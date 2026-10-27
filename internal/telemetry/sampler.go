// Package telemetry provides a low-overhead (<1% CPU hard-capped) in-memory system
// resource usage cache and periodic time-series metric flusher for production
// monitoring and graphing on AWS EC2.
package telemetry

import (
	"time"

	"syn/internal/telemetry/governor"
	"syn/internal/telemetry/procfs"
	"syn/internal/telemetry/rtmetrics"
)

// MaxCPUPercentBudget is the strict upper bound (1.0%) on CPU utilization allowed
// for the telemetry monitoring subsystem.
const MaxCPUPercentBudget = governor.MaxCPUPercentBudget

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

// ReadLoadAverages delegates to procfs.ReadLoadAverages for 1m, 5m, and 15m load averages.
func ReadLoadAverages(path string) (load1, load5, load15 float64) {
	return procfs.ReadLoadAverages(path)
}

// ReadHostMemoryStats delegates to procfs.ReadHostMemoryStats for /proc/meminfo parsing.
func ReadHostMemoryStats(path string) (usedMB, totalMB, usagePct float64) {
	return procfs.ReadHostMemoryStats(path)
}

// collectRuntimeSample gathers Go runtime, OS /proc, DB pool, queue, and mutex metrics
// using lock-free rtmetrics and stack-buffered procfs readers.
func collectRuntimeSample(
	now time.Time,
	cpu *procfs.CPUTracker,
	monitorCPUPct float64,
	dbProvider DBStatsProvider,
	queueProvider QueueStatsProvider,
	lockProvider LockStatsProvider,
) ResourceSample {
	rt := rtmetrics.Read()
	l1, l5, l15 := procfs.ReadLoadAverages("/proc/loadavg")
	memUsed, memTotal, memPct := procfs.ReadHostMemoryStats("/proc/meminfo")
	cpuPct := cpu.SampleCPUPercent("/proc/stat")

	sample := ResourceSample{
		Timestamp:      now.UTC().Format(time.RFC3339),
		EpochMS:        now.UTC().UnixMilli(),
		HostCPUPercent: cpuPct,
		MonitorCPUPct:  monitorCPUPct,
		LoadAvg1m:      l1,
		LoadAvg5m:      l5,
		LoadAvg15m:     l15,
		HostMemUsedMB:  memUsed,
		HostMemTotalMB: memTotal,
		HostMemPercent: memPct,
		GoHeapAllocMB:  rt.HeapAllocMB,
		GoHeapInUseMB:  rt.HeapInUseMB,
		GoSysMB:        rt.SysMB,
		GoStackInUseMB: rt.StackInUseMB,
		GoNumGC:        rt.NumGC,
		GoGoroutines:   rt.Goroutines,
		CPUCores:       rt.CPUCores,
		GOMAXPROCS:     rt.GOMAXPROCS,
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
