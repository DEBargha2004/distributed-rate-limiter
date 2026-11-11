package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"distributed-rate-limiter/internal/algorithm"
	"distributed-rate-limiter/internal/config"
	"distributed-rate-limiter/internal/redis"
)

var (
	ErrNilWatcher       = errors.New("engine: watcher cannot be nil")
	ErrNilScriptManager = errors.New("engine: script manager cannot be nil")
	ErrRuleNotFound     = errors.New("engine: rule not found")
	ErrEmptyRuleID      = errors.New("engine: rule id cannot be empty")
	ErrEmptySubject     = errors.New("engine: subject cannot be empty")
)

// FormatKey generates a deterministic Redis Cluster-compatible key.
// The hash tag {ruleID:subject} ensures all keys touched for this specific rule
// and subject co-locate on the same Redis Cluster slot without centralizing
// all subjects of a rule onto one hot shard.
func FormatKey(ruleID string, subject string) string {
	// Clean/trim delimiters to prevent key injection or collision
	cleanRuleID := strings.TrimSpace(ruleID)
	cleanSubject := strings.TrimSpace(subject)
	return fmt.Sprintf("rl:{%s:%s}", cleanRuleID, cleanSubject)
}

// Engine matches incoming requests against active rules and delegates rate limit execution.
// In this Phase 3 stub, it provides single-rule lookup and execution.
// Multi-rule matching and combining logic is implemented in Phase 4.
type Engine struct {
	watcher *config.Watcher
	sm      *redis.ScriptManager
}

// NewEngine creates a new Engine with access to configuration snapshots and the Redis script manager.
func NewEngine(watcher *config.Watcher, sm *redis.ScriptManager) (*Engine, error) {
	if watcher == nil {
		return nil, ErrNilWatcher
	}
	if sm == nil {
		return nil, ErrNilScriptManager
	}

	return &Engine{
		watcher: watcher,
		sm:      sm,
	}, nil
}

// GetRule finds a rule by its unique ID from the active configuration snapshot.
func (e *Engine) GetRule(ruleID string) (*config.Rule, error) {
	if ruleID == "" {
		return nil, ErrEmptyRuleID
	}

	ruleSet := e.watcher.Get()
	if ruleSet == nil || ruleSet.Rules == nil {
		return nil, ErrRuleNotFound
	}

	rule, ok := ruleSet.Rules[ruleID]
	if !ok || rule == nil {
		return nil, fmt.Errorf("%w: %s", ErrRuleNotFound, ruleID)
	}

	return rule, nil
}

// Allow evaluates a rate-limit check for a specific rule and subject.
// This is the primary execution path for the thin slice in Phase 3.
func (e *Engine) Allow(ctx context.Context, ruleID string, subject string, cost int64) (algorithm.Decision, error) {
	if subject == "" {
		return algorithm.Decision{}, ErrEmptySubject
	}

	rule, err := e.GetRule(ruleID)
	if err != nil {
		return algorithm.Decision{}, err
	}

	// 1. Format deterministic hash-tagged key
	key := FormatKey(rule.ID, subject)

	// 2. Instantiate limiter bound to this rule and script manager
	limiter, err := redis.NewLimiter(e.sm, *rule)
	if err != nil {
		return algorithm.Decision{}, fmt.Errorf("engine: failed to create limiter for rule %s: %w", rule.ID, err)
	}

	// 3. Delegate execution to Redis Lua
	return limiter.Check(ctx, key, cost)
}
