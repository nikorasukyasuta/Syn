package ratelimit

import (
	"sync"
	"time"
)

// userBucket tracks rate limit tokens for a single Discord user.
type userBucket struct {
	tokens     float64
	lastRefill time.Time
}

// UserRateLimiter implements a thread-safe token bucket rate limiter per user ID.
type UserRateLimiter struct {
	mu             sync.Mutex
	burst          float64
	refillRate     float64 // tokens added per second
	buckets        map[string]*userBucket
	stopCh         chan struct{}
	cleanupRunning bool
}

// NewUserRateLimiter initializes a rate limiter with a maximum burst capacity
// and a continuous refill rate. Inactive users are pruned at cleanupInterval.
// Example: NewUserRateLimiter(5, time.Second, 5*time.Minute) allows a burst of 5 commands,
// refilling at 1 command per second.
func NewUserRateLimiter(burst int, refillPerToken time.Duration, cleanupInterval time.Duration) *UserRateLimiter {
	refillRate := 1.0 / refillPerToken.Seconds()
	l := &UserRateLimiter{
		burst:      float64(burst),
		refillRate: refillRate,
		buckets:    make(map[string]*userBucket),
		stopCh:     make(chan struct{}),
	}

	if cleanupInterval > 0 {
		l.cleanupRunning = true
		go l.cleanupLoop(cleanupInterval)
	}

	return l
}

// Allow checks if the given user is allowed to perform an action.
// If allowed, decrements one token and returns true, 0.
// If not allowed, returns false along with the duration until the next token is available.
func (l *UserRateLimiter) Allow(userID string) (bool, time.Duration) {
	if userID == "" {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, exists := l.buckets[userID]
	if !exists {
		// First request from user: start with (burst - 1) tokens remaining
		l.buckets[userID] = &userBucket{
			tokens:     l.burst - 1.0,
			lastRefill: now,
		}
		return true, 0
	}

	// Refill tokens proportional to elapsed time
	elapsed := now.Sub(b.lastRefill).Seconds()
	b.tokens += elapsed * l.refillRate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.lastRefill = now

	if b.tokens >= 1.0 {
		b.tokens -= 1.0
		return true, 0
	}

	// Calculate wait time until at least 1 token is available
	missing := 1.0 - b.tokens
	waitSec := missing / l.refillRate
	retryAfter := time.Duration(waitSec * float64(time.Second))
	if retryAfter < 100*time.Millisecond {
		retryAfter = 100 * time.Millisecond
	}

	return false, retryAfter
}

// Reset clears the rate limit state for a user.
func (l *UserRateLimiter) Reset(userID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.buckets, userID)
}

// Stop cleanly terminates the background cleanup goroutine.
func (l *UserRateLimiter) Stop() {
	l.mu.Lock()
	if l.cleanupRunning {
		close(l.stopCh)
		l.cleanupRunning = false
	}
	l.mu.Unlock()
}

// cleanupLoop periodically evicts buckets that have completely refilled to burst capacity.
func (l *UserRateLimiter) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.mu.Lock()
			now := time.Now()
			for uid, b := range l.buckets {
				elapsed := now.Sub(b.lastRefill).Seconds()
				tokens := b.tokens + elapsed*l.refillRate
				// If fully refilled to burst, the user is inactive; remove from map
				if tokens >= l.burst {
					delete(l.buckets, uid)
				}
			}
			l.mu.Unlock()
		case <-l.stopCh:
			return
		}
	}
}

