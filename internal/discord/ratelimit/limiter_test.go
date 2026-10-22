package ratelimit

import (
	"sync"
	"testing"
	"time"
)

func TestUserRateLimiter_BurstAndRefill(t *testing.T) {
	t.Parallel()

	// 3 tokens burst, 1 token per 100ms
	limiter := NewUserRateLimiter(3, 100*time.Millisecond, 0)
	defer limiter.Stop()

	user := "user_123"

	// 1st request - allowed
	allowed, wait := limiter.Allow(user)
	if !allowed || wait != 0 {
		t.Fatalf("expected 1st request to be allowed, got allowed=%t, wait=%v", allowed, wait)
	}

	// 2nd request - allowed
	allowed, wait = limiter.Allow(user)
	if !allowed || wait != 0 {
		t.Fatalf("expected 2nd request to be allowed, got allowed=%t, wait=%v", allowed, wait)
	}

	// 3rd request - allowed
	allowed, wait = limiter.Allow(user)
	if !allowed || wait != 0 {
		t.Fatalf("expected 3rd request to be allowed, got allowed=%t, wait=%v", allowed, wait)
	}

	// 4th request - exceeded burst, should be blocked!
	allowed, wait = limiter.Allow(user)
	if allowed {
		t.Fatal("expected 4th request to be blocked by rate limit")
	}
	if wait <= 0 {
		t.Fatalf("expected positive retry-after duration, got %v", wait)
	}

	// Wait 150ms for at least 1 token to refill
	time.Sleep(150 * time.Millisecond)

	// 5th request - should now be allowed
	allowed, _ = limiter.Allow(user)
	if !allowed {
		t.Fatal("expected request to be allowed after token refill")
	}
}

func TestUserRateLimiter_ConcurrentUsers(t *testing.T) {
	t.Parallel()

	limiter := NewUserRateLimiter(5, time.Second, 0)
	defer limiter.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			user := string(rune('A' + id))
			for j := 0; j < 5; j++ {
				allowed, _ := limiter.Allow(user)
				if !allowed {
					t.Errorf("expected initial 5 requests to be allowed for user %s", user)
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestUserRateLimiter_Reset(t *testing.T) {
	t.Parallel()

	limiter := NewUserRateLimiter(1, time.Minute, 0)
	defer limiter.Stop()

	user := "user_reset"
	allowed, _ := limiter.Allow(user)
	if !allowed {
		t.Fatal("expected 1st request allowed")
	}

	allowed, _ = limiter.Allow(user)
	if allowed {
		t.Fatal("expected 2nd request blocked")
	}

	limiter.Reset(user)

	allowed, _ = limiter.Allow(user)
	if !allowed {
		t.Fatal("expected request to be allowed after reset")
	}
}

