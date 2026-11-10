package algorithm

import (
	"context"
)

// Limiter defines the unified interface that all rate limiting algorithms must satisfy.
type Limiter interface {
	// Check assesses whether a request consuming `cost` is allowed for `key`.
	// Server-side timestamp is derived by Redis TIME inside atomic scripts.
	Check(ctx context.Context, key string, cost int64) (Decision, error)
}
