package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

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

// Combine merges multiple rate-limiting decisions into an aggregate decision per Spec R13.
// Semantics:
//   - Empty input: If decisions is empty, return Allowed: true, Remaining: math.MaxInt64.
//   - Deny-if-any: The aggregate is Allowed only if ALL input decisions are Allowed.
//   - Most restrictive wait: If denied, RetryAfter is max(RetryAfter) across all denying decisions.
//   - ResetAt: If denied, ResetAt corresponds to the latest reset time among the denying decisions.
//     If all allowed, ResetAt is the latest reset time among all decisions.
//   - Bottleneck remaining: Remaining is min(Remaining) across all decisions.
//   - RuleID & Limit: Attributed to the decision dictating the outcome:
//     if denied, the rule with the max RetryAfter; if allowed, the rule with the min Remaining.
func Combine(decisions []algorithm.Decision) algorithm.Decision {
	// =========================================================================
	// LEARNER IMPLEMENTATION AREA [Brick 4.3]:
	// Implement multi-decision aggregation per Spec R13.
	// =========================================================================

	if len(decisions) == 0 {
		return algorithm.Decision{
			Allowed:   true,
			Remaining: math.MaxInt64,
		}
	}

	hasDenied := false
	var worstDenial algorithm.Decision
	bottleneck := decisions[0]
	var latestReset time.Time

	for _, d := range decisions {
		if d.ResetAt.After(latestReset) {
			latestReset = d.ResetAt
		}

		if d.Remaining < bottleneck.Remaining {
			bottleneck = d
		}

		if !d.Allowed {
			hasDenied = true
			if d.RetryAfter > worstDenial.RetryAfter {
				worstDenial = d
			}
		}

	}

	if hasDenied {
		return algorithm.Decision{
			Allowed:    false,
			RuleID:     worstDenial.RuleID,
			Limit:      worstDenial.Limit,
			Remaining:  bottleneck.Remaining,
			RetryAfter: worstDenial.RetryAfter,
			ResetAt:    latestReset,
		}
	}

	return algorithm.Decision{
		Allowed:    true,
		RetryAfter: 0,
		Remaining:  bottleneck.Remaining,
		Limit:      bottleneck.Limit,
		ResetAt:    latestReset,
		RuleID:     bottleneck.RuleID,
	}

}

// ResolveCost calculates the effective quota cost for a request and rule according to
// the resolution hierarchy:
//  1. reqCost if reqCost > 0
//  2. defaultCost if defaultCost > 0
//  3. 1 (standard single-unit fallback)
//
// Any non-positive cost (zero or negative) is sanitized to prevent callers
// from bypassing rate limits or gaining quota.
func ResolveCost(reqCost, defaultCost int64) int64 {
	// =========================================================================
	// LEARNER IMPLEMENTATION AREA [Brick 4.4]:
	// Implement cost resolution hierarchy:
	// - If reqCost > 0, return reqCost
	// - Else if defaultCost > 0, return defaultCost
	// - Else return 1
	// =========================================================================
	if reqCost > 0 {
		return reqCost
	} else if defaultCost > 0 {
		return defaultCost
	} else {
		return 1
	}
}

// AllowRequest evaluates an incoming Request against all matching rules in the active configuration.
// It executes each matching rule against Redis via limiter.Check, accepts partial consumption
// across Redis Cluster slots per ADR 003, and combines the results via Combine.
func (e *Engine) AllowRequest(ctx context.Context, req Request) (algorithm.Decision, error) {
	// =========================================================================
	// LEARNER IMPLEMENTATION AREA [Brick 4.3 & 4.4]:
	// Step 1: Match rules using e.MatchRules(req).
	// Step 2: If len(matches) == 0, return Combine(nil), nil.
	// Step 3: For each match, determine effective cost using ResolveCost(req.Cost, match.Rule.DefaultCost).
	//         Format key using FormatKey(rule.ID, match.Subject),
	//         instantiate or reuse limiter via redis.NewLimiter,
	//         and execute limiter.Check(ctx, key, cost).
	// Step 4: Collect all decisions and return Combine(decisions), nil.
	// =========================================================================

	matches, err := e.MatchRules(req)

	if err != nil {
		return algorithm.Decision{}, err
	}

	if len(matches) == 0 {
		return Combine(nil), nil
	}

	Decisions := []algorithm.Decision{}

	for _, match := range matches {
		cost := ResolveCost(req.Cost, match.Rule.DefaultCost)

		decision, err := e.Allow(ctx, match.Rule.ID, match.Subject, cost)
		if err != nil {
			return algorithm.Decision{}, err
		}
		Decisions = append(Decisions, decision)
	}

	return Combine(Decisions), nil
}
