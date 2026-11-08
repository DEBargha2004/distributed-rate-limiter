package fallback

import (
	"distributed-rate-limiter/internal/algorithm"
	"distributed-rate-limiter/internal/config"
)

// HandleFailure determines the fallback response when Redis cannot be reached.
func HandleFailure(policy config.FailurePolicy, local *LocalLimiter, cost int64) algorithm.Decision {
	// TODO:
	// 1. If local limiter is configured, evaluate local limiter.
	// 2. If policy is fail-open, allow request.
	// 3. Otherwise fail-closed (deny request).
	panic("TODO: implement HandleFailure")
}
