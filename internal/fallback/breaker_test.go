package fallback_test

import (
	"sync"
	"testing"
	"time"

	"distributed-rate-limiter/internal/fallback"
)

type mockClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMockClock(initial time.Time) *mockClock {
	return &mockClock{now: initial}
}

func (m *mockClock) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

func (m *mockClock) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
}

func TestCircuitBreaker_InitialClosed(t *testing.T) {
	cb := fallback.NewCircuitBreaker(3, 100*time.Millisecond)
	if got := cb.State(); got != fallback.StateClosed {
		t.Fatalf("expected state %v, got %v", fallback.StateClosed, got)
	}
	if !cb.AllowExecution() {
		t.Fatalf("expected AllowExecution to be true in initial closed state")
	}
}

func TestCircuitBreaker_ClosedSuccessResetsFailures(t *testing.T) {
	cb := fallback.NewCircuitBreaker(3, 100*time.Millisecond)

	cb.RecordFailure()
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateClosed {
		t.Fatalf("expected state %v, got %v", fallback.StateClosed, got)
	}

	// Intermittent success resets failure counter
	cb.RecordSuccess()
	if got := cb.State(); got != fallback.StateClosed {
		t.Fatalf("expected state %v, got %v", fallback.StateClosed, got)
	}

	// Another failure should not trip because counter was reset
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateClosed {
		t.Fatalf("expected state %v, got %v", fallback.StateClosed, got)
	}
	if !cb.AllowExecution() {
		t.Fatalf("expected AllowExecution to be true")
	}
}

func TestCircuitBreaker_TripsToOpenAfterThreshold(t *testing.T) {
	clk := newMockClock(time.Now())
	cb := fallback.NewCircuitBreaker(3, 500*time.Millisecond, fallback.WithClock(clk))

	cb.RecordFailure()
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateClosed {
		t.Fatalf("expected state %v, got %v", fallback.StateClosed, got)
	}
	if !cb.AllowExecution() {
		t.Fatalf("expected AllowExecution to be true before threshold")
	}

	// 3rd failure reaches threshold
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateOpen, got)
	}
	if cb.AllowExecution() {
		t.Fatalf("expected Open breaker to reject execution")
	}
}

func TestCircuitBreaker_OpenUntilCooldownExpires(t *testing.T) {
	clk := newMockClock(time.Now())
	cooldown := 500 * time.Millisecond
	cb := fallback.NewCircuitBreaker(2, cooldown, fallback.WithClock(clk))

	cb.RecordFailure()
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateOpen, got)
	}

	// Advance time by half cooldown - still open
	clk.Advance(250 * time.Millisecond)
	if got := cb.State(); got != fallback.StateOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateOpen, got)
	}
	if cb.AllowExecution() {
		t.Fatalf("expected AllowExecution to be false during cooldown")
	}

	// Advance time past cooldown - becomes half-open
	clk.Advance(251 * time.Millisecond)
	if got := cb.State(); got != fallback.StateHalfOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateHalfOpen, got)
	}
}

func TestCircuitBreaker_HalfOpen_SingleProbeRecovery(t *testing.T) {
	clk := newMockClock(time.Now())
	cooldown := 500 * time.Millisecond
	cb := fallback.NewCircuitBreaker(2, cooldown, fallback.WithClock(clk))

	// Trip to open
	cb.RecordFailure()
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateOpen, got)
	}

	// Advance past cooldown
	clk.Advance(501 * time.Millisecond)

	// First execution is the canary probe -> Allowed
	if !cb.AllowExecution() {
		t.Fatalf("canary probe must be allowed")
	}

	// Concurrent / subsequent calls while probe is running -> Rejected
	if cb.AllowExecution() {
		t.Fatalf("only single probe allowed during HalfOpen")
	}

	// Canary probe succeeds -> Transitions back to Closed
	cb.RecordSuccess()
	if got := cb.State(); got != fallback.StateClosed {
		t.Fatalf("expected state %v after probe success, got %v", fallback.StateClosed, got)
	}
	if !cb.AllowExecution() {
		t.Fatalf("breaker is closed; requests must be allowed")
	}
}

func TestCircuitBreaker_HalfOpen_ProbeFailsTripsBackToOpen(t *testing.T) {
	clk := newMockClock(time.Now())
	cooldown := 500 * time.Millisecond
	cb := fallback.NewCircuitBreaker(2, cooldown, fallback.WithClock(clk))

	// Trip to open
	cb.RecordFailure()
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateOpen, got)
	}

	// Advance past cooldown into half-open
	clk.Advance(501 * time.Millisecond)
	if !cb.AllowExecution() {
		t.Fatalf("canary probe must be allowed")
	}

	// Canary probe fails -> Immediately trips back to Open
	cb.RecordFailure()
	if got := cb.State(); got != fallback.StateOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateOpen, got)
	}
	if cb.AllowExecution() {
		t.Fatalf("tripped back to Open; must reject execution")
	}

	// Another cooldown needed
	clk.Advance(250 * time.Millisecond)
	if cb.AllowExecution() {
		t.Fatalf("still within cooldown; must reject")
	}
	clk.Advance(251 * time.Millisecond)
	if got := cb.State(); got != fallback.StateHalfOpen {
		t.Fatalf("expected state %v, got %v", fallback.StateHalfOpen, got)
	}
	if !cb.AllowExecution() {
		t.Fatalf("new probe must be allowed")
	}
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := fallback.NewCircuitBreaker(5, 50*time.Millisecond)

	var wg sync.WaitGroup
	workers := 20
	iterations := 200

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				if cb.AllowExecution() {
					if (workerID+j)%7 == 0 {
						cb.RecordFailure()
					} else {
						cb.RecordSuccess()
					}
				} else {
					cb.RecordFailure()
				}
				_ = cb.State()
			}
		}(i)
	}

	wg.Wait()
}
