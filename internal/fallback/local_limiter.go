package fallback

import (
	"distributed-rate-limiter/internal/algorithm"
)

// LocalLimiter provides an in-process, degraded token bucket when Redis fails.
type LocalLimiter struct {
	// TODO: Add mutex, capacity, tokens, refill rate, and last refill timestamp
}

func NewLocalLimiter(capacity float64, refillRatePerSec float64) *LocalLimiter {
	// TODO: Initialize in-memory token bucket
	panic("TODO: implement NewLocalLimiter")
}

func (l *LocalLimiter) Check(cost int64) algorithm.Decision {
	// TODO: Refill and evaluate local tokens
	panic("TODO: implement Check")
}
