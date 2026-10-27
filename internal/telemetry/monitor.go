package telemetry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"syn/internal/logging"
	"syn/internal/telemetry/governor"
	"syn/internal/telemetry/procfs"
)

const (
	// DefaultMetricsLogPath is the default file path where cached resource telemetry is flushed.
	DefaultMetricsLogPath = "logs/metrics.jsonl"

	// DefaultSampleInterval is the default frequency for sampling runtime and host resource metrics into cache.
	DefaultSampleInterval = 15 * time.Second

	// DefaultFlushInterval is the default frequency for flushing the in-memory telemetry cache to disk.
	DefaultFlushInterval = 60 * time.Second

	// DefaultMaxHistorySamples is the number of recent samples retained in the in-memory ring cache
	// for real-time querying by the /health command and /metrics web endpoint.
	DefaultMaxHistorySamples = 240
)

// Config defines configuration parameters for the system resource telemetry monitor and cache.
type Config struct {
	FilePath          string
	SampleInterval    time.Duration
	FlushInterval     time.Duration
	MaxHistorySamples int
	MaxSizeMB         int
	MaxBackups        int
	MaxAgeDays        int
}

// DefaultConfig returns production defaults for the telemetry monitor.
func DefaultConfig() Config {
	return Config{
		FilePath:          DefaultMetricsLogPath,
		SampleInterval:    DefaultSampleInterval,
		FlushInterval:     DefaultFlushInterval,
		MaxHistorySamples: DefaultMaxHistorySamples,
		MaxSizeMB:         20,
		MaxBackups:        5,
		MaxAgeDays:        30,
	}
}

// Monitor samples system, Go runtime, database pool, queue, and mutex usage into a thread-safe
// in-memory cache and periodically flushes buffered samples to a dedicated rotating JSON Lines
// file (metrics.jsonl) for time-series graphing and monitoring.
// It enforces a strict <= 1.0% CPU utilization budget via governor.BudgetGovernor.
type Monitor struct {
	cfg           Config
	writer        io.WriteCloser
	logger        *slog.Logger
	cpu           *procfs.CPUTracker
	gov           *governor.BudgetGovernor
	dbProvider    DBStatsProvider
	queueProvider QueueStatsProvider
	lockProvider  LockStatsProvider

	mu            sync.RWMutex
	pendingBuffer []ResourceSample
	history       []ResourceSample
	totalFlushed  uint64
	lastFlushTime time.Time

	stopChan chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewMonitor initializes a new telemetry Monitor with a rotating metrics file writer.
func NewMonitor(
	cfg Config,
	dbProvider DBStatsProvider,
	queueProvider QueueStatsProvider,
	lockProvider LockStatsProvider,
	logger *slog.Logger,
) (*Monitor, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.FilePath == "" {
		cfg.FilePath = DefaultMetricsLogPath
	}
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = DefaultSampleInterval
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.MaxHistorySamples <= 0 {
		cfg.MaxHistorySamples = DefaultMaxHistorySamples
	}
	if cfg.MaxSizeMB <= 0 {
		cfg.MaxSizeMB = 20
	}
	if cfg.MaxBackups <= 0 {
		cfg.MaxBackups = 5
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 30
	}

	rotator, err := logging.NewRotatingFileWriter(logging.RotatorConfig{
		Filename:   cfg.FilePath,
		MaxSizeMB:  cfg.MaxSizeMB,
		MaxBackups: cfg.MaxBackups,
		MaxAgeDays: cfg.MaxAgeDays,
	})
	if err != nil {
		return nil, fmt.Errorf("initializing telemetry metrics rotating file (%s): %w", cfg.FilePath, err)
	}

	return NewMonitorWithWriter(cfg, rotator, dbProvider, queueProvider, lockProvider, logger), nil
}

// NewMonitorWithWriter constructs a Monitor using a custom io.WriteCloser (useful for testing).
func NewMonitorWithWriter(
	cfg Config,
	writer io.WriteCloser,
	dbProvider DBStatsProvider,
	queueProvider QueueStatsProvider,
	lockProvider LockStatsProvider,
	logger *slog.Logger,
) *Monitor {
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = DefaultSampleInterval
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.MaxHistorySamples <= 0 {
		cfg.MaxHistorySamples = DefaultMaxHistorySamples
	}

	cpu := procfs.NewCPUTracker()
	_ = cpu.SampleCPUPercent("/proc/stat")

	return &Monitor{
		cfg:           cfg,
		writer:        writer,
		logger:        logger,
		cpu:           cpu,
		gov:           governor.NewBudgetGovernor(),
		dbProvider:    dbProvider,
		queueProvider: queueProvider,
		lockProvider:  lockProvider,
		pendingBuffer: make([]ResourceSample, 0, 16),
		history:       make([]ResourceSample, 0, cfg.MaxHistorySamples),
		stopChan:      make(chan struct{}),
	}
}

// Start launches the background sampling and periodic disk-flush goroutine.
// It immediately records an initial baseline sample into the in-memory cache.
func (m *Monitor) Start() {
	m.SampleNow()

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()

		sampleTicker := time.NewTicker(m.cfg.SampleInterval)
		flushTicker := time.NewTicker(m.cfg.FlushInterval)
		defer sampleTicker.Stop()
		defer flushTicker.Stop()

		m.logger.Info("system resource telemetry monitor started",
			"metrics_file", m.cfg.FilePath,
			"sample_interval", m.cfg.SampleInterval.String(),
			"flush_interval", m.cfg.FlushInterval.String(),
			"max_cpu_budget_pct", MaxCPUPercentBudget,
		)

		for {
			select {
			case <-sampleTicker.C:
				m.SampleBounded()
			case <-flushTicker.C:
				if _, err := m.Flush(); err != nil {
					m.logger.Warn("failed to flush resource telemetry cache to metrics file",
						"error", err,
						"metrics_file", m.cfg.FilePath,
					)
				}
			case <-m.stopChan:
				return
			}
		}
	}()
}

// Stop halts background sampling, flushes all remaining cached samples to the metrics file,
// and closes the underlying file writer.
func (m *Monitor) Stop() error {
	var stopErr error
	m.stopOnce.Do(func() {
		close(m.stopChan)
		m.wg.Wait()

		flushed, err := m.Flush()
		if err != nil {
			m.logger.Warn("error flushing final telemetry samples on shutdown", "error", err)
			stopErr = err
		} else if flushed > 0 {
			m.logger.Info("flushed remaining cached telemetry samples on shutdown", "samples_flushed", flushed)
		}

		if m.writer != nil {
			if closeErr := m.writer.Close(); closeErr != nil && stopErr == nil {
				stopErr = closeErr
			}
		}
	})
	return stopErr
}

// SampleBounded enforces the <= 1.0% CPU budget governor before taking a sample.
// If the telemetry subsystem has used >= 1.0% of elapsed time or is within the 100x
// duty-cycle cooldown window of the previous operation, it returns the most recent cached
// sample in O(1) without performing any I/O or runtime reads.
func (m *Monitor) SampleBounded() (ResourceSample, bool) {
	now := time.Now().UTC()
	if cached, hasHistory := m.Latest(); hasHistory && !m.gov.Allow(now) {
		return cached, false
	}
	return m.SampleNow(), true
}

// SampleNow captures an immediate snapshot of host, runtime, database, and queue metrics,
// records the exact execution time against the 1% CPU budget governor, and appends the sample
// to both the pending flush buffer and the bounded in-memory history cache.
func (m *Monitor) SampleNow() ResourceSample {
	start := time.Now().UTC()
	currentMonCPU := m.gov.CPUPercent()

	sample := collectRuntimeSample(start, m.cpu, currentMonCPU, m.dbProvider, m.queueProvider, m.lockProvider)
	workDur := time.Since(start)
	sample.MonitorCPUPct = m.gov.RecordWork(start, workDur)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.pendingBuffer = append(m.pendingBuffer, sample)
	if len(m.history) >= m.cfg.MaxHistorySamples {
		copy(m.history, m.history[1:])
		m.history[len(m.history)-1] = sample
	} else {
		m.history = append(m.history, sample)
	}

	return sample
}

// Flush atomically drains the pending in-memory sample cache and writes all buffered
// samples as newline-delimited JSON (JSONL) records to the dedicated metrics log file.
func (m *Monitor) Flush() (int, error) {
	start := time.Now().UTC()

	m.mu.Lock()
	if len(m.pendingBuffer) == 0 {
		m.mu.Unlock()
		return 0, nil
	}
	batch := make([]ResourceSample, len(m.pendingBuffer))
	copy(batch, m.pendingBuffer)
	m.pendingBuffer = m.pendingBuffer[:0]
	m.mu.Unlock()

	var buf bytes.Buffer
	buf.Grow(len(batch) * 320)
	enc := json.NewEncoder(&buf)
	for _, sample := range batch {
		if err := enc.Encode(sample); err != nil {
			return 0, fmt.Errorf("encoding resource sample: %w", err)
		}
	}

	if m.writer != nil {
		if _, err := m.writer.Write(buf.Bytes()); err != nil {
			m.mu.Lock()
			m.pendingBuffer = append(batch, m.pendingBuffer...)
			m.mu.Unlock()
			return 0, fmt.Errorf("writing telemetry batch to disk: %w", err)
		}
	}

	workDur := time.Since(start)
	now := time.Now().UTC()
	_ = m.gov.RecordWork(now, workDur)

	m.mu.Lock()
	m.totalFlushed += uint64(len(batch))
	m.lastFlushTime = now
	m.mu.Unlock()

	m.logger.Debug("flushed system resource telemetry cache to disk",
		"samples_flushed", len(batch),
		"flush_duration_us", workDur.Microseconds(),
		"metrics_file", m.cfg.FilePath,
	)

	return len(batch), nil
}

// MonitorCPUPercent returns the percentage of wall-clock time spent executing telemetry
// collection and disk flushing since the monitor was initialized (strictly <= 1.0%).
func (m *Monitor) MonitorCPUPercent() float64 {
	return m.gov.CPUPercent()
}

// Latest returns the most recently collected ResourceSample and true, or an empty struct and false if none exist.
func (m *Monitor) Latest() (ResourceSample, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.history) == 0 {
		return ResourceSample{}, false
	}
	return m.history[len(m.history)-1], true
}

// History returns a chronological copy of the recent in-memory cached ResourceSamples.
func (m *Monitor) History() []ResourceSample {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ResourceSample, len(m.history))
	copy(out, m.history)
	return out
}

// CacheStats returns the current number of buffered (unflushed) samples, total flushed count,
// last flush timestamp, and the configured metrics file path.
func (m *Monitor) CacheStats() (pending int, totalFlushed uint64, lastFlush time.Time, filePath string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pendingBuffer), m.totalFlushed, m.lastFlushTime, m.cfg.FilePath
}
