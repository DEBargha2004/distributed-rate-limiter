package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
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

// FormatKey generates a deterministic, collision-safe Redis Cluster-compatible key.
// The key pattern is: rl:{<escapedRuleID>:<escapedSubject>}[:<suffix>]
// The hash tag {ruleID:subject} ensures all keys touched for this specific rule
// and subject co-locate on the same Redis Cluster slot without centralizing
// all subjects of a rule onto one hot shard.
// Optional suffix is placed outside the hash tag to share the slot.
func FormatKey(ruleID string, subject string, suffix ...string) string {
	cleanRuleID := strings.TrimSpace(ruleID)
	cleanSubject := strings.TrimSpace(subject)

	// Escape delimiter characters (':', '{', '}', and '%')
	// in cleanRuleID and cleanSubject to prevent collisions and hash-tag corruption.
	replacer := strings.NewReplacer(
		"%", "%25",
		":", "%3A",
		"{", "%7B",
		"}", "%7D",
	)

	cleanRuleID = replacer.Replace(cleanRuleID)
	cleanSubject = replacer.Replace(cleanSubject)

	// If a non-empty suffix is provided, append it outside the hash tag to share the slot.
	if len(suffix) > 0 && strings.TrimSpace(suffix[0]) != "" {
		return fmt.Sprintf("rl:{%s:%s}:%s", cleanRuleID, cleanSubject, strings.TrimSpace(suffix[0]))
	}

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

// Request represents an incoming rate limit request with its extracted dimensions.
type Request struct {
	// RuleID optionally limits evaluation to a specific rule ID.
	// If empty, all rules matching the request's dimensions are evaluated.
	RuleID string

	// Dimensions maps attribute types (ip, user, api_key, route) to their extracted values.
	Dimensions map[config.Dimension]string

	// Cost represents the quota cost of this request (default is 1).
	Cost int64
}

// MatchedRule pairs an active configuration Rule with the specific subject
// extracted from the request's dimensions.
type MatchedRule struct {
	Rule    *config.Rule
	Subject string
}

// MatchRules evaluates the incoming request against active rules in the configuration snapshot.
// It returns all rules whose configured dimension is present and non-empty in req.Dimensions.
// If req.RuleID is provided:
//   - Only that specific rule is evaluated. If not found in config, returns ErrRuleNotFound.
//   - If found, it matches only if its required dimension is non-empty in req.Dimensions.
//
// The returned slice is sorted alphabetically by Rule.ID for deterministic evaluation order.
func (e *Engine) MatchRules(req Request) ([]MatchedRule, error) {
	Ruleset := e.watcher.Get()
	if Ruleset == nil || len(Ruleset.Rules) == 0 {
		return nil, fmt.Errorf("ruleset not found")
	}

	if req.RuleID != "" {
		var rule *config.Rule = nil
		for _, rl := range Ruleset.Rules {
			if rl.ID == req.RuleID {
				rule = rl
			}
		}

		if rule == nil {
			return nil, ErrRuleNotFound
		}

		value, exists := req.Dimensions[rule.Dimension]
		value = strings.TrimSpace(value)
		if exists && value != "" {
			return []MatchedRule{
				{
					Rule:    rule,
					Subject: value,
				},
			}, nil
		}

		return []MatchedRule{}, nil

	}

	matched := []MatchedRule{}

	for _, rl := range Ruleset.Rules {
		value, exists := req.Dimensions[rl.Dimension]
		trimmed := strings.TrimSpace(value)
		if exists && trimmed != "" {
			matched = append(matched, MatchedRule{
				Rule:    rl,
				Subject: trimmed,
			})
		}
	}

	slices.SortFunc(matched, func(a, b MatchedRule) int {
		return strings.Compare(a.Rule.ID, b.Rule.ID)
	})

	return matched, nil
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
