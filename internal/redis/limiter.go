package redis

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"distributed-rate-limiter/internal/algorithm"
	"distributed-rate-limiter/internal/config"
)

var (
	ErrNilScriptManager = errors.New("redis: script manager cannot be nil")
	ErrEmptyKey         = errors.New("redis: key cannot be empty")
	ErrInvalidResponse  = errors.New("redis: invalid response structure from script")
)

// Limiter executes rate limit checks against Redis using Lua scripts managed by ScriptManager.
// It implements the algorithm.Limiter interface.
type Limiter struct {
	sm   *ScriptManager
	rule config.Rule
}

// NewLimiter creates a new Limiter bound to a specific rate limiting rule.
func NewLimiter(sm *ScriptManager, rule config.Rule) (*Limiter, error) {
	if sm == nil {
		return nil, ErrNilScriptManager
	}
	if err := rule.Validate(); err != nil {
		return nil, fmt.Errorf("invalid rule config: %w", err)
	}

	return &Limiter{
		sm:   sm,
		rule: rule,
	}, nil
}

// Rule returns the configured rule for this limiter.
func (l *Limiter) Rule() config.Rule {
	return l.rule
}

// Check evaluates whether a request with the given cost is allowed under the configured rule.
// Server-side timestamping is handled by Redis TIME inside the Lua script.
func (l *Limiter) Check(ctx context.Context, key string, cost int64) (algorithm.Decision, error) {
	if key == "" {
		return algorithm.Decision{}, ErrEmptyKey
	}

	// 1. Resolve effective request cost
	effectiveCost := cost
	if effectiveCost <= 0 {
		effectiveCost = l.rule.DefaultCost
		if effectiveCost <= 0 {
			effectiveCost = 1
		}
	}

	// =========================================================================
	// LEARNER IMPLEMENTATION AREA [Brick 3.3]:
	//
	// Step A: Determine ScriptName and ARGV slice based on l.rule.Algorithm:
	//   - AlgorithmFixedWindow:
	//       script: ScriptFixedWindow
	//       args:   l.rule.Limit, int64(l.rule.Window.Seconds()), effectiveCost
	//   - AlgorithmSlidingWindow:
	//       script: ScriptSlidingWindow
	//       args:   l.rule.Limit, int64(l.rule.Window.Seconds()), effectiveCost
	//   - AlgorithmTokenBucket:
	//       script:   ScriptTokenBucket
	//       idleTTL:  calculate reasonable idle expiry (e.g. max(60, int64(l.rule.Window.Seconds())))
	//       refillStr: Format float as string (e.g. fmt.Sprintf("%f", l.rule.RefillRate)) to prevent float truncation
	//       args:     l.rule.Capacity, refillStr, effectiveCost, idleTTL
	//   - Default: return ErrUnknownScript
	//
	// Step B: Execute script via l.sm.Run(ctx, scriptName, []string{key}, args...)
	//
	// Step C: Parse the returned Lua array:
	//   Expected tuple: [allowed (1 or 0), remaining, retry_after_sec, reset_at_timestamp]
	//   Convert to algorithm.Decision:
	//     Allowed:    allowed == 1
	//     Limit:      configured limit (l.rule.Limit, or l.rule.Capacity for token bucket)
	//     Remaining:  remaining
	//     RetryAfter: time.Duration(retry_after_sec) * time.Second
	//     ResetAt:    time.Unix(reset_at_timestamp, 0)
	//     RuleID:     l.rule.ID
	// =========================================================================

	// TODO [Learner Brick 3.3]: implement Step A, B, and C

	var script ScriptName
	var args []any

	switch l.rule.Algorithm {
	case config.AlgorithmFixedWindow:
		script = ScriptFixedWindow
		args = append(args, l.rule.Limit, int64(l.rule.Window.Seconds()), effectiveCost)

	case config.AlgorithmSlidingWindow:
		script = ScriptSlidingWindow
		args = append(args, l.rule.Limit, int64(l.rule.Window.Seconds()), effectiveCost)

	case config.AlgorithmTokenBucket:
		idleTtl := int64(math.Max(60, l.rule.Window.Seconds()))
		refillStr := fmt.Sprintf("%f", l.rule.RefillRate)

		script = ScriptTokenBucket
		args = append(args, l.rule.Capacity, refillStr, effectiveCost, idleTtl)

	default:
		return algorithm.Decision{}, ErrUnknownScript
	}

	res, err := l.sm.Run(ctx, script, []string{key}, args...)
	if err != nil {
		return algorithm.Decision{}, err
	}

	arr, ok := res.([]any)

	if !ok || len(arr) < 4 {
		return algorithm.Decision{}, ErrInvalidResponse
	}

	allowed := arr[0].(int64) == 1
	remaining := arr[1].(int64)
	retry_after_sec := arr[2].(int64)
	reset_at_timestamp := arr[3].(int64)

	limit := l.rule.Limit
	if l.rule.Algorithm == config.AlgorithmTokenBucket {
		limit = l.rule.Capacity
	}
	retry_after := time.Duration(retry_after_sec) * time.Second
	reset_at := time.Unix(reset_at_timestamp, 0)
	ruleId := l.rule.ID

	return algorithm.Decision{
		Allowed:    allowed,
		Remaining:  remaining,
		Limit:      limit,
		RetryAfter: retry_after,
		ResetAt:    reset_at,
		RuleID:     ruleId,
	}, nil
}
