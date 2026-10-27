package syncutil_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"syn/internal/discord/syncutil"
)

func TestUserLockManager_SerializesSameUser(t *testing.T) {
	t.Parallel()

	mgr := syncutil.NewUserLockManager()
	user := "user_target_1"

	var counter int64
	var wg sync.WaitGroup

	// 50 goroutines incrementing a shared counter for the same user
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := mgr.Lock(user)
			defer unlock()

			// Critical section
			current := atomic.LoadInt64(&counter)
			time.Sleep(1 * time.Millisecond)
			atomic.StoreInt64(&counter, current+1)
		}()
	}

	wg.Wait()

	if counter != 50 {
		t.Fatalf("expected counter to be 50, got %d", counter)
	}

	if mgr.ActiveLocksCount() != 0 {
		t.Fatalf("expected lock map to be empty after all unlocks, got %d entries", mgr.ActiveLocksCount())
	}
}

func TestUserLockManager_ParallelDifferentUsers(t *testing.T) {
	t.Parallel()

	mgr := syncutil.NewUserLockManager()

	var wg sync.WaitGroup
	start := time.Now()

	// 10 different users acquiring locks in parallel, sleeping 50ms each.
	// If serialized, it would take ~500ms. In parallel, it should finish in < 250ms.
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			user := string(rune('A' + id))
			unlock := mgr.Lock(user)
			defer unlock()

			time.Sleep(50 * time.Millisecond)
		}(i)
	}

	wg.Wait()
	elapsed := time.Since(start)

	if elapsed > 350*time.Millisecond {
		t.Fatalf("expected concurrent locks for different users to run in parallel, took %v", elapsed)
	}

	if mgr.ActiveLocksCount() != 0 {
		t.Fatalf("expected lock map to be empty, got %d entries", mgr.ActiveLocksCount())
	}
}

func TestUserLockManager_EmptyUser(t *testing.T) {
	t.Parallel()

	mgr := syncutil.NewUserLockManager()
	unlock := mgr.Lock("")
	unlock() // Must not panic
}

