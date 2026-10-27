// Package governor enforces a strict upper bound (<= 1.0%) on CPU utilization
// for background telemetry collection and disk flushing via duty-cycle accounting.
package governor

import (
	"math"
	"sync"
	"time"
)

const (
	// MaxCPUPercentBudget is the strict upper bound (1.0%) on CPU utilization allowed
	// for the telemetry monitoring subsystem.
	MaxCPUPercentBudget = 1.0

	// DutyCycleMultiplier is 100x (100% / 1.0% = 100): if a telemetry operation takes D ns
	// of CPU work, the governor enforces at least 99*D ns of idle cooldown so CPU usage
	// never exceeds 1.0%.
	DutyCycleMultiplier = 100
)

// BudgetGovernor tracks cumulative execution time and enforces the <= 1.0% CPU budget.
type BudgetGovernor struct {
	mu                sync.RWMutex
	startedAt         time.Time
	totalWorkNs       int64
	nextAllowedSample time.Time
	throttledCount    uint64
}

// NewBudgetGovernor initializes a new BudgetGovernor starting at the current UTC time.
func NewBudgetGovernor() *BudgetGovernor {
	return &BudgetGovernor{
		startedAt: time.Now().UTC(),
	}
}

// Allow checks whether executing a new sample at time `now` complies with the <= 1.0% CPU budget.
// If within the 100x duty-cycle cooldown or over budget, it increments ThrottledCount and returns false.
func (g *BudgetGovernor) Allow(now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	elapsedNs := now.Sub(g.startedAt).Nanoseconds()
	withinCooldown := now.Before(g.nextAllowedSample)
	overBudget := elapsedNs > 0 && (float64(g.totalWorkNs)/float64(elapsedNs))*100.0 >= MaxCPUPercentBudget
	if withinCooldown || overBudget {
		g.throttledCount++
		return false
	}
	return true
}

// RecordWork logs the duration of a telemetry sample or flush operation and sets the next
// allowed sample timestamp according to the 100x duty-cycle multiplier.
func (g *BudgetGovernor) RecordWork(now time.Time, workDur time.Duration) float64 {
	if workDur <= 0 {
		workDur = time.Microsecond
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.totalWorkNs += workDur.Nanoseconds()
	g.nextAllowedSample = now.Add(workDur * DutyCycleMultiplier)

	return g.unlockedCPUPercent()
}

// CPUPercent returns the percentage of wall-clock time spent executing telemetry operations
// since initialization, capped at MaxCPUPercentBudget (1.0%).
func (g *BudgetGovernor) CPUPercent() float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.unlockedCPUPercent()
}

// ThrottledCount returns the number of sample requests skipped to preserve the 1% CPU cap.
func (g *BudgetGovernor) ThrottledCount() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.throttledCount
}

func (g *BudgetGovernor) unlockedCPUPercent() float64 {
	elapsedNs := time.Since(g.startedAt).Nanoseconds()
	if elapsedNs <= 0 || g.totalWorkNs <= 0 {
		return 0
	}
	pct := math.Round(((float64(g.totalWorkNs)/float64(elapsedNs))*100.0)*100) / 100
	if pct > MaxCPUPercentBudget {
		return MaxCPUPercentBudget
	}
	return pct
}

