package governor

import (
	"testing"
	"time"
)

func TestBudgetGovernor_EnforcesOnePercentCap(t *testing.T) {
	t.Parallel()

	gov := NewBudgetGovernor()
	now := time.Now().UTC()

	// Initial call is allowed
	if !gov.Allow(now) {
		t.Fatal("expected initial Allow() to return true")
	}

	// Record 1ms of work -> next allowed is now + 100ms
	pct := gov.RecordWork(now, time.Millisecond)
	if pct > MaxCPUPercentBudget {
		t.Fatalf("expected CPUPercent <= %.2f%%, got %.2f%%", MaxCPUPercentBudget, pct)
	}

	// Immediate follow-up call within 100x duty-cycle window must be throttled
	if gov.Allow(now.Add(5 * time.Millisecond)) {
		t.Fatal("expected Allow() within 100x cooldown window to be throttled")
	}
	if gov.ThrottledCount() != 1 {
		t.Fatalf("expected ThrottledCount=1, got %d", gov.ThrottledCount())
	}
}

