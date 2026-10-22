package queue_test

import (
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"syn/internal/discord/queue"
)

func TestCommandQueue_EnqueueAndProcess(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	q := queue.NewCommandQueue(100, 4, silentLogger)
	defer q.Stop(2 * time.Second)

	var count int64
	var wg sync.WaitGroup

	for i := 0; i < 50; i++ {
		wg.Add(1)
		ok := q.Enqueue(&queue.Task{
			ID:          "task_1",
			CommandName: "test_cmd",
			UserID:      "user_1",
			Execute: func() {
				atomic.AddInt64(&count, 1)
				wg.Done()
			},
		})
		if !ok {
			t.Fatalf("expected task %d to be enqueued successfully", i)
		}
	}

	wg.Wait()

	if atomic.LoadInt64(&count) != 50 {
		t.Fatalf("expected 50 processed tasks, got %d", count)
	}

	stats := q.Stats()
	if stats.TotalEnqueued != 50 {
		t.Errorf("expected 50 enqueued, got %d", stats.TotalEnqueued)
	}
	if stats.TotalProcessed != 50 {
		t.Errorf("expected 50 processed, got %d", stats.TotalProcessed)
	}
	if stats.TotalDropped != 0 {
		t.Errorf("expected 0 dropped, got %d", stats.TotalDropped)
	}
}

func TestCommandQueue_WorkerPanicRecovery(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	q := queue.NewCommandQueue(10, 2, silentLogger)
	defer q.Stop(1 * time.Second)

	var wg sync.WaitGroup
	wg.Add(2)

	// 1st task panics
	q.Enqueue(&queue.Task{
		ID:          "panic_task",
		CommandName: "panic_cmd",
		Execute: func() {
			defer wg.Done()
			panic("intentional panic inside worker")
		},
	})

	// 2nd task should succeed normally despite previous panic
	var normalExecuted bool
	q.Enqueue(&queue.Task{
		ID:          "normal_task",
		CommandName: "normal_cmd",
		Execute: func() {
			normalExecuted = true
			wg.Done()
		},
	})

	wg.Wait()

	if !normalExecuted {
		t.Fatal("expected normal task to execute after previous task panic was recovered")
	}
}

func TestCommandQueue_GracefulStopDrainsPending(t *testing.T) {
	t.Parallel()

	silentLogger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	q := queue.NewCommandQueue(100, 2, silentLogger)

	var processed int64
	for i := 0; i < 20; i++ {
		q.Enqueue(&queue.Task{
			Execute: func() {
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt64(&processed, 1)
			},
		})
	}

	q.Stop(3 * time.Second)

	if atomic.LoadInt64(&processed) != 20 {
		t.Fatalf("expected all 20 tasks to be drained on stop, got %d", processed)
	}
}

