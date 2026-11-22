package fallback_test

import (
	"sync"
	"testing"
	"time"

	"distributed-rate-limiter/internal/fallback"
)

func TestLocalLimiter_BurstCapacityAndDenial(t *testing.T) {
	clk := newMockClock(time.Now())
	// Capacity: 5 tokens, Refill: 1 token/sec
	limiter := fallback.NewLocalLimiter(5, 1, fallback.WithLocalLimiterClock(clk))

	// Consume 5 tokens with cost 1 each
	for i := 0; i < 5; i++ {
		d := limiter.Check(1)
		if !d.Allowed {
			t.Fatalf("request %d should be allowed, got denied", i+1)
		}
		if expectedRemaining := int64(4 - i); d.Remaining != expectedRemaining {
			t.Fatalf("request %d: expected remaining %d, got %d", i+1, expectedRemaining, d.Remaining)
		}
	}

	// 6th request: bucket empty, must deny
	d := limiter.Check(1)
	if d.Allowed {
		t.Fatalf("expected request 6 to be denied when bucket is empty")
	}
	if d.Remaining != 0 {
		t.Fatalf("expected remaining 0, got %d", d.Remaining)
	}
	if d.RetryAfter < time.Second {
		t.Fatalf("expected retry-after >= 1s for missing 1 token at 1 token/sec, got %v", d.RetryAfter)
	}
}

func TestLocalLimiter_LazyRefill(t *testing.T) {
	clk := newMockClock(time.Now())
	// Capacity: 10, Refill: 2 tokens/sec
	limiter := fallback.NewLocalLimiter(10, 2, fallback.WithLocalLimiterClock(clk))

	// Empty the bucket with cost 10
	d := limiter.Check(10)
	if !d.Allowed || d.Remaining != 0 {
		t.Fatalf("expected full burst of 10 to succeed with 0 remaining, got allowed=%v, remaining=%d", d.Allowed, d.Remaining)
	}

	// Advance 2.5 seconds -> refills 5 tokens (tokens = 5)
	clk.Advance(2500 * time.Millisecond)

	// Consuming 4 tokens should succeed
	d = limiter.Check(4)
	if !d.Allowed {
		t.Fatalf("expected request with cost 4 to be allowed after refill, got denied")
	}
	if d.Remaining != 1 {
		t.Fatalf("expected remaining 1 token, got %d", d.Remaining)
	}

	// Advance 10 seconds -> should cap at capacity 10, not overflow
	clk.Advance(10 * time.Second)
	d = limiter.Check(10)
	if !d.Allowed || d.Remaining != 0 {
		t.Fatalf("expected bucket to cap at capacity 10 after long idle period, got allowed=%v, remaining=%d", d.Allowed, d.Remaining)
	}
}

func TestLocalLimiter_OversizedRequest(t *testing.T) {
	clk := newMockClock(time.Now())
	limiter := fallback.NewLocalLimiter(5, 1, fallback.WithLocalLimiterClock(clk))

	// Cost 10 > Capacity 5
	d := limiter.Check(10)
	if d.Allowed {
		t.Fatalf("expected cost 10 > capacity 5 to be denied immediately")
	}
	// Verify bucket was not mutated
	d2 := limiter.Check(5)
	if !d2.Allowed {
		t.Fatalf("expected original tokens (5) to remain unmutated after oversized rejection")
	}
}

func TestLocalLimiter_ClockSkewSafety(t *testing.T) {
	clk := newMockClock(time.Now())
	limiter := fallback.NewLocalLimiter(5, 1, fallback.WithLocalLimiterClock(clk))

	// Consume 2 tokens -> remaining 3
	_ = limiter.Check(2)

	// Simulate clock jumping backward (e.g. NTP adjustment)
	clk.Advance(-5 * time.Second)

	d := limiter.Check(1)
	if !d.Allowed {
		t.Fatalf("backward clock adjustment must not penalize or subtract tokens")
	}
}

func TestLocalLimiter_ConcurrentAccess(t *testing.T) {
	clk := newMockClock(time.Now())
	// Capacity 1000, 100 refill/sec
	limiter := fallback.NewLocalLimiter(1000, 100, fallback.WithLocalLimiterClock(clk))

	var wg sync.WaitGroup
	workers := 20
	iterations := 50

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				_ = limiter.Check(1)
			}
		}()
	}

	wg.Wait()
}

