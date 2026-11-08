package middleware

import (
	"context"
	"net/http"

	"distributed-rate-limiter/internal/algorithm"
)

// KeyExtractor extracts a rate-limiting key from an incoming HTTP request.
type KeyExtractor func(r *http.Request) string

// LimiterClient is the interface the middleware uses to check rate limits.
type LimiterClient interface {
	Allow(ctx context.Context, key string, ruleID string, cost int64) (algorithm.Decision, error)
}

// HTTPMiddleware wraps an http.Handler with distributed rate limiting.
func HTTPMiddleware(client LimiterClient, ruleID string, extractor KeyExtractor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// TODO:
			// 1. Extract key using extractor
			// 2. Call client.Allow(...)
			// 3. Set standard response headers:
			//    - X-RateLimit-Limit
			//    - X-RateLimit-Remaining
			//    - X-RateLimit-Reset
			// 4. If denied: set Retry-After header and respond with 429 Too Many Requests
			// 5. If allowed: pass through to next.ServeHTTP(w, r)
			panic("TODO: implement HTTPMiddleware")
		})
	}
}
