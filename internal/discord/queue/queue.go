package queue

import (
	"context"
	"log/slog"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

// Task represents a discrete command or interaction job to be processed by the worker pool.
type Task struct {
	ID          string
	CommandName string
	UserID      string
	Execute     func()
	EnqueuedAt  time.Time
}

// QueueStats provides real-time telemetry on command queue operations.
type QueueStats struct {
	QueueDepth     int    `json:"queue_depth"`
	QueueCapacity  int    `json:"queue_capacity"`
	WorkersCount   int    `json:"workers_count"`
	TotalEnqueued  uint64 `json:"total_enqueued"`
	TotalProcessed uint64 `json:"total_processed"`
	TotalDropped   uint64 `json:"total_dropped"`
}

// CommandQueue manages an asynchronous worker pool with a buffered channel queue,
// preventing dropped commands during traffic spikes and load surges.
type CommandQueue struct {
	tasks          chan *Task
	workersCount   int
	wg             sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc
	logger         *slog.Logger
	totalEnqueued  uint64
	totalProcessed uint64
	totalDropped   uint64
	stopped        atomic.Bool
}

// NewCommandQueue initializes a worker pool sized to match host CPU capabilities.
// If workers is <= 0, it automatically defaults to max(2, runtime.NumCPU() * 2).
func NewCommandQueue(capacity int, workers int, logger *slog.Logger) *CommandQueue {
	if capacity <= 0 {
		capacity = 1024
	}
	if workers <= 0 {
		workers = runtime.NumCPU() * 2
		if workers < 2 {
			workers = 2
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	q := &CommandQueue{
		tasks:        make(chan *Task, capacity),
		workersCount: workers,
		ctx:          ctx,
		cancel:       cancel,
		logger:       logger,
	}

	q.startWorkers()
	return q
}

// startWorkers launches the worker goroutines across available CPU cores.
func (q *CommandQueue) startWorkers() {
	for i := 0; i < q.workersCount; i++ {
		q.wg.Add(1)
		go q.worker(i)
	}
}

func (q *CommandQueue) worker(workerID int) {
	defer q.wg.Done()

	for {
		select {
		case task, ok := <-q.tasks:
			if !ok {
				return
			}
			q.runTask(workerID, task)
		case <-q.ctx.Done():
			// Drain remaining tasks before exiting
			for {
				select {
				case task, ok := <-q.tasks:
					if !ok {
						return
					}
					q.runTask(workerID, task)
				default:
					return
				}
			}
		}
	}
}

func (q *CommandQueue) runTask(workerID int, task *Task) {
	if task == nil || task.Execute == nil {
		return
	}

	waitDuration := time.Since(task.EnqueuedAt)
	if waitDuration > 500*time.Millisecond {
		q.logger.Warn("queued command waited before execution",
			"worker_id", workerID,
			"command", task.CommandName,
			"user_id", task.UserID,
			"wait_ms", waitDuration.Milliseconds(),
		)
	}

	defer func() {
		if rec := recover(); rec != nil {
			q.logger.Error("recovered panic inside queued command worker",
				"worker_id", workerID,
				"command", task.CommandName,
				"panic", rec,
			)
		}
		atomic.AddUint64(&q.totalProcessed, 1)
	}()

	task.Execute()
}

// Enqueue submits a task into the queue.
// Returns true if successfully queued, or false if the queue was stopped.
func (q *CommandQueue) Enqueue(task *Task) bool {
	if q.stopped.Load() {
		return false
	}

	if task.EnqueuedAt.IsZero() {
		task.EnqueuedAt = time.Now()
	}

	atomic.AddUint64(&q.totalEnqueued, 1)

	select {
	case q.tasks <- task:
		return true
	default:
		// Queue channel full: wait up to 1 second to avoid dropping
		timer := time.NewTimer(1 * time.Second)
		defer timer.Stop()

		select {
		case q.tasks <- task:
			return true
		case <-timer.C:
			atomic.AddUint64(&q.totalDropped, 1)
			q.logger.Error("command queue buffer full, command dropped",
				"command", task.CommandName,
				"user_id", task.UserID,
				"queue_depth", len(q.tasks),
			)
			return false
		}
	}
}

// Stats returns a snapshot of current queue throughput and backlog metrics.
func (q *CommandQueue) Stats() QueueStats {
	return QueueStats{
		QueueDepth:     len(q.tasks),
		QueueCapacity:  cap(q.tasks),
		WorkersCount:   q.workersCount,
		TotalEnqueued:  atomic.LoadUint64(&q.totalEnqueued),
		TotalProcessed: atomic.LoadUint64(&q.totalProcessed),
		TotalDropped:   atomic.LoadUint64(&q.totalDropped),
	}
}

// Stop initiates a graceful shutdown, draining pending tasks within the given timeout.
func (q *CommandQueue) Stop(timeout time.Duration) {
	if q.stopped.Swap(true) {
		return
	}

	q.cancel()
	close(q.tasks)

	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		q.logger.Info("command queue drained and stopped cleanly")
	case <-time.After(timeout):
		q.logger.Warn("command queue shutdown timed out; some tasks may be incomplete")
	}
}

