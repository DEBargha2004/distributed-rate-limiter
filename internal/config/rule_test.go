package config

import (
	"errors"
	"testing"
	"time"
)

func TestRule_Validate(t *testing.T) {
	tests := []struct {
		name    string
		rule    Rule
		wantErr error
	}{
		{
			name: "valid token bucket rule",
			rule: Rule{
				ID:            "user_limit",
				Dimension:     DimensionUser,
				Algorithm:     AlgorithmTokenBucket,
				Capacity:      100,
				RefillRate:    10.0,
				DefaultCost:   1,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: nil,
		},
		{
			name: "valid sliding window rule",
			rule: Rule{
				ID:            "ip_limit",
				Dimension:     DimensionIP,
				Algorithm:     AlgorithmSlidingWindow,
				Limit:         60,
				Window:        time.Minute,
				DefaultCost:   1,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: nil,
		},
		{
			name: "valid fixed window rule",
			rule: Rule{
				ID:            "api_key_limit",
				Dimension:     DimensionAPIKey,
				Algorithm:     AlgorithmFixedWindow,
				Limit:         100,
				Window:        time.Minute,
				DefaultCost:   1,
				FailurePolicy: PolicyFailClosed,
			},
			wantErr: nil,
		},
		{
			name: "missing rule ID",
			rule: Rule{
				Dimension:     DimensionIP,
				Algorithm:     AlgorithmFixedWindow,
				Limit:         10,
				Window:        time.Second,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: ErrEmptyRuleID,
		},
		{
			name: "invalid dimension",
			rule: Rule{
				ID:            "test_rule",
				Dimension:     Dimension("geolocation"),
				Algorithm:     AlgorithmFixedWindow,
				Limit:         10,
				Window:        time.Second,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: ErrInvalidDimension,
		},
		{
			name: "invalid algorithm",
			rule: Rule{
				ID:            "test_rule",
				Dimension:     DimensionUser,
				Algorithm:     AlgorithmType("leaky_bucket"),
				Limit:         10,
				Window:        time.Second,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: ErrInvalidAlgorithm,
		},
		{
			name: "invalid failure policy",
			rule: Rule{
				ID:            "test_rule",
				Dimension:     DimensionUser,
				Algorithm:     AlgorithmFixedWindow,
				Limit:         10,
				Window:        time.Second,
				FailurePolicy: FailurePolicy("retry"),
			},
			wantErr: ErrInvalidFailurePolicy,
		},
		{
			name: "fixed window zero limit",
			rule: Rule{
				ID:            "test_rule",
				Dimension:     DimensionUser,
				Algorithm:     AlgorithmFixedWindow,
				Limit:         0,
				Window:        time.Second,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: ErrInvalidLimit,
		},
		{
			name: "sliding window zero window",
			rule: Rule{
				ID:            "test_rule",
				Dimension:     DimensionUser,
				Algorithm:     AlgorithmSlidingWindow,
				Limit:         10,
				Window:        0,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: ErrInvalidWindow,
		},
		{
			name: "token bucket zero capacity",
			rule: Rule{
				ID:            "test_rule",
				Dimension:     DimensionUser,
				Algorithm:     AlgorithmTokenBucket,
				Capacity:      0,
				RefillRate:    5.0,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: ErrInvalidTokenBucketConfig,
		},
		{
			name: "token bucket zero refill rate",
			rule: Rule{
				ID:            "test_rule",
				Dimension:     DimensionUser,
				Algorithm:     AlgorithmTokenBucket,
				Capacity:      50,
				RefillRate:    0,
				FailurePolicy: PolicyFailOpen,
			},
			wantErr: ErrInvalidTokenBucketConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rule.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("expected nil error, got: %v", err)
				}
			} else {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
			}
		})
	}
}

func TestRuleSet_Validate(t *testing.T) {
	t.Run("empty rules map", func(t *testing.T) {
		rs := RuleSet{
			Version: "v1.0",
			Rules:   map[string]*Rule{},
		}
		if err := rs.Validate(); !errors.Is(err, ErrEmptyRuleSet) {
			t.Fatalf("expected ErrEmptyRuleSet, got: %v", err)
		}
	})

	t.Run("nil rule entry in map", func(t *testing.T) {
		rs := RuleSet{
			Version: "v1.0",
			Rules: map[string]*Rule{
				"broken": nil,
			},
		}
		if err := rs.Validate(); !errors.Is(err, ErrNilRule) {
			t.Fatalf("expected ErrNilRule, got: %v", err)
		}
	})

	t.Run("valid ruleset with multiple rules", func(t *testing.T) {
		rs := RuleSet{
			Version: "v1.0",
			Rules: map[string]*Rule{
				"ip": {
					ID:            "ip",
					Dimension:     DimensionIP,
					Algorithm:     AlgorithmFixedWindow,
					Limit:         100,
					Window:        time.Minute,
					DefaultCost:   1,
					FailurePolicy: PolicyFailOpen,
				},
				"user": {
					ID:            "user",
					Dimension:     DimensionUser,
					Algorithm:     AlgorithmTokenBucket,
					Capacity:      50,
					RefillRate:    10.0,
					DefaultCost:   1,
					FailurePolicy: PolicyFailOpen,
				},
			},
		}
		if err := rs.Validate(); err != nil {
			t.Fatalf("expected valid ruleset, got error: %v", err)
		}
	})
}
