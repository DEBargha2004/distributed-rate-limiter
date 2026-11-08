package algorithm

import (
	"context"
	"time"
)

// Limiter defines the unified interface that all rate limiting algorithms must satisfy.
type Limiter interface {
	// Check assesses whether a request consuming `cost` is allowed for `key` at timestamp `now`.
	Check(ctx context.Context, key string, cost int64, now time.Time) (Decision, error)
}
