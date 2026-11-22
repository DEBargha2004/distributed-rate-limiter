package fallback

import (
	"math"
	"sync"
	"time"

	"distributed-rate-limiter/internal/algorithm"
)

// LocalLimiter provides an in-process, degraded token bucket when Redis fails.
// It is thread-safe and supports an injectable Clock for deterministic testing.
type LocalLimiter struct {
	mu               sync.Mutex
	capacity         float64
	tokens           float64
	refillRatePerSec float64
	lastRefill       time.Time
	clock            Clock
}

// LocalLimiterOption configures optional parameters for LocalLimiter.
type LocalLimiterOption func(*LocalLimiter)

// WithLocalLimiterClock injects a custom clock into LocalLimiter.
func WithLocalLimiterClock(c Clock) LocalLimiterOption {
	return func(l *LocalLimiter) {
		l.clock = c
	}
}

// NewLocalLimiter initializes a thread-safe in-memory token bucket.
func NewLocalLimiter(capacity float64, refillRatePerSec float64, opts ...LocalLimiterOption) *LocalLimiter {
	l := &LocalLimiter{
		capacity:         capacity,
		refillRatePerSec: refillRatePerSec,
		tokens:           capacity,
	}

	for _, opt := range opts {
		opt(l)
	}

	if l.clock == nil {
		l.clock = realClock{}
	}

	l.lastRefill = l.clock.Now()
	return l
}

// Check evaluates whether a request with the given cost is allowed under local tokens.
// It lazily refills tokens based on elapsed time since lastRefill, updates remaining tokens,
// and computes RetryAfter and ResetAt if denied.
func (l *LocalLimiter) Check(cost int64) algorithm.Decision {
	l.mu.Lock()
	defer l.mu.Unlock()

	Now := l.clock.Now()
	ElapsedTime := Now.Sub(l.lastRefill)
	if ElapsedTime > 0 {
		l.tokens = min(l.capacity, l.tokens+float64(ElapsedTime.Seconds())*l.refillRatePerSec)
		l.lastRefill = l.clock.Now()
	}

	if cost > int64(l.tokens) {
		Remaining := int64(l.tokens)
		Missing := cost - int64(l.tokens)
		RetryAfter := time.Duration(math.Ceil(float64(Missing)/l.refillRatePerSec)) * time.Second
		return algorithm.Decision{
			Allowed:    false,
			Limit:      int64(l.capacity),
			Remaining:  int64(Remaining),
			RetryAfter: RetryAfter,
			ResetAt:    Now.Add(RetryAfter),
		}
	} else {
		RetryAfter := 0
		Remaining := int64(l.tokens) - cost
		Missing := l.capacity - l.tokens

		l.tokens = float64(Remaining)
		return algorithm.Decision{
			Allowed:    true,
			Limit:      int64(l.capacity),
			Remaining:  Remaining,
			RetryAfter: time.Duration(RetryAfter) * time.Second,
			ResetAt:    Now.Add(time.Duration(math.Ceil(Missing/l.refillRatePerSec)) * time.Second),
		}
	}
}
