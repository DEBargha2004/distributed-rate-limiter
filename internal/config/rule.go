package config

import (
	"errors"
	"time"
)

// Common validation errors.
var (
	ErrEmptyRuleID              = errors.New("rule id cannot be empty")
	ErrInvalidDimension         = errors.New("invalid dimension: must be ip, user, api_key, or route")
	ErrInvalidAlgorithm         = errors.New("invalid algorithm: must be fixed_window, sliding_window, or token_bucket")
	ErrInvalidFailurePolicy     = errors.New("invalid failure policy: must be fail_open or fail_closed")
	ErrInvalidLimit             = errors.New("limit must be greater than zero")
	ErrInvalidWindow            = errors.New("window must be greater than zero")
	ErrInvalidTokenBucketConfig = errors.New("token bucket requires capacity > 0 and refill_rate > 0")
	ErrEmptyRuleSet             = errors.New("rules map cannot be empty")
	ErrNilRule                  = errors.New("rule in rules map cannot be nil")
)

// AlgorithmType identifies the rate limiting strategy.
type AlgorithmType string

const (
	AlgorithmFixedWindow   AlgorithmType = "fixed_window"
	AlgorithmSlidingWindow AlgorithmType = "sliding_window"
	AlgorithmTokenBucket   AlgorithmType = "token_bucket"
)

// FailurePolicy defines behavior when Redis is unavailable.
type FailurePolicy string

const (
	PolicyFailOpen   FailurePolicy = "fail_open"
	PolicyFailClosed FailurePolicy = "fail_closed"
)

// Dimension defines what attribute to rate limit on.
type Dimension string

const (
	DimensionIP     Dimension = "ip"
	DimensionUser   Dimension = "user"
	DimensionAPIKey Dimension = "api_key"
	DimensionRoute  Dimension = "route"
)

// Rule represents a single rate limit rule configuration.
type Rule struct {
	ID            string        `yaml:"id"`
	Dimension     Dimension     `yaml:"dimension"`
	Algorithm     AlgorithmType `yaml:"algorithm"`
	Limit         int64         `yaml:"limit"`
	Window        time.Duration `yaml:"window"`
	Capacity      int64         `yaml:"capacity,omitempty"`
	RefillRate    float64       `yaml:"refill_rate,omitempty"`
	DefaultCost   int64         `yaml:"default_cost"`
	FailurePolicy FailurePolicy `yaml:"failure_policy"`
}

// RuleSet holds the complete active collection of rules.
type RuleSet struct {
	Version string           `yaml:"version"`
	Rules   map[string]*Rule `yaml:"rules"`
}

// Validate validates that a single Rule contains all required and valid fields.
//
// TODO (Learner Task 1.1):
// Implement the validation logic:
// 1. Check ID is not empty (ErrEmptyRuleID).
// 2. Check Dimension is one of: DimensionIP, DimensionUser, DimensionAPIKey, DimensionRoute (ErrInvalidDimension).
// 3. Check FailurePolicy is PolicyFailOpen or PolicyFailClosed (ErrInvalidFailurePolicy).
// 4. Check Algorithm:
//   - If AlgorithmTokenBucket: Capacity > 0 and RefillRate > 0 (ErrInvalidTokenBucketConfig).
//   - If AlgorithmFixedWindow or AlgorithmSlidingWindow: Limit > 0 (ErrInvalidLimit) and Window > 0 (ErrInvalidWindow).
//   - If none of the above: return ErrInvalidAlgorithm.
//
// 5. If DefaultCost <= 0, you can set it to a sensible default (e.g. 1) or require DefaultCost > 0.
// 6. Return nil if everything is valid.
func (r *Rule) Validate() error {
	if r.ID == "" {
		return ErrEmptyRuleID
	}

	switch r.Dimension {
	case DimensionIP, DimensionUser, DimensionAPIKey, DimensionRoute:
		break
	default:
		return ErrInvalidDimension
	}

	switch r.FailurePolicy {
	case PolicyFailOpen, PolicyFailClosed:
		break
	default:
		return ErrInvalidFailurePolicy
	}

	switch r.Algorithm {
	case AlgorithmTokenBucket:
		if r.Capacity <= 0 || r.RefillRate <= 0 {
			return ErrInvalidTokenBucketConfig
		}
	case AlgorithmFixedWindow, AlgorithmSlidingWindow:
		if r.Limit <= 0 {
			return ErrInvalidLimit
		}

		if r.Window <= 0 {
			return ErrInvalidWindow
		}
	default:
		return ErrInvalidAlgorithm
	}

	if r.DefaultCost <= 0 {
		r.DefaultCost = 1
	}

	return nil
}

// Validate validates the entire RuleSet collection.
//
// TODO (Learner Task 1.1):
// Implement the validation logic:
// 1. Check that r.Rules is not nil and has at least 1 rule (ErrEmptyRuleSet).
// 2. Iterate through r.Rules:
//   - Ensure the rule pointer is not nil (ErrNilRule).
//   - Call rule.Validate(). If it returns an error, wrap and return it.
//
// 3. Return nil if the entire set is valid.
func (rs *RuleSet) Validate() error {
	if len(rs.Rules) == 0 {
		return ErrEmptyRuleSet
	}

	for _, rule := range rs.Rules {
		if rule == nil {
			return ErrNilRule
		}

		if err := rule.Validate(); err != nil {
			return err
		}
	}

	return nil

}
