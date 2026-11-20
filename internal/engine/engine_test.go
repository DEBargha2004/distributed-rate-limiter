package engine_test

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"distributed-rate-limiter/internal/algorithm"
	"distributed-rate-limiter/internal/config"
	"distributed-rate-limiter/internal/engine"
	"distributed-rate-limiter/internal/redis"
)

func createTestConfigFile(t *testing.T, yamlContent string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("failed to write test config file: %v", err)
	}
	return path
}

func setupTestEngine(t *testing.T, yamlContent string) (*engine.Engine, *config.Watcher, string, *miniredis.Miniredis) {
	t.Helper()

	// Setup Redis
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	sm, err := redis.NewScriptManager(rdb)
	if err != nil {
		t.Fatalf("failed to init script manager: %v", err)
	}

	// Setup Config Watcher
	configPath := createTestConfigFile(t, yamlContent)
	watcher, err := config.NewWatcher(configPath)
	if err != nil {
		t.Fatalf("failed to init watcher: %v", err)
	}

	eng, err := engine.NewEngine(watcher, sm)
	if err != nil {
		t.Fatalf("failed to init engine: %v", err)
	}

	return eng, watcher, configPath, mr
}

const sampleConfigYAML = `
rules:
  login-limit:
    id: "login-limit"
    dimension: "ip"
    algorithm: "fixed_window"
    limit: 2
    window: 10s
    default_cost: 1
    failure_policy: "fail_closed"
  search-limit:
    id: "search-limit"
    dimension: "user"
    algorithm: "token_bucket"
    capacity: 5
    refill_rate: 1.0
    limit: 5
    window: 60s
    default_cost: 1
    failure_policy: "fail_closed"
`

func TestFormatKey_BasicAndSuffix(t *testing.T) {
	tests := []struct {
		name     string
		ruleID   string
		subject  string
		suffix   []string
		expected string
	}{
		{
			name:     "standard rule and ip",
			ruleID:   "login",
			subject:  "192.168.1.1",
			expected: "rl:{login:192.168.1.1}",
		},
		{
			name:     "standard with spaces trimmed",
			ruleID:   "  spaces  ",
			subject:  "  user123  ",
			expected: "rl:{spaces:user123}",
		},
		{
			name:     "with single suffix",
			ruleID:   "login",
			subject:  "usr_48291",
			suffix:   []string{"ts"},
			expected: "rl:{login:usr_48291}:ts",
		},
		{
			name:     "with empty suffix ignored",
			ruleID:   "login",
			subject:  "usr_48291",
			suffix:   []string{""},
			expected: "rl:{login:usr_48291}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.FormatKey(tt.ruleID, tt.subject, tt.suffix...)
			if got != tt.expected {
				t.Errorf("FormatKey(%q, %q, %v) = %q; want %q", tt.ruleID, tt.subject, tt.suffix, got, tt.expected)
			}
		})
	}
}

func TestFormatKey_DelimiterEscaping(t *testing.T) {
	tests := []struct {
		name     string
		ruleID   string
		subject  string
		expected string
	}{
		{
			name:     "colons in subject",
			ruleID:   "auth",
			subject:  "user:admin:99",
			expected: "rl:{auth:user%3Aadmin%3A99}",
		},
		{
			name:     "colons in rule ID",
			ruleID:   "api:v1:login",
			subject:  "10.0.0.1",
			expected: "rl:{api%3Av1%3Alogin:10.0.0.1}",
		},
		{
			name:     "braces in subject",
			ruleID:   "login",
			subject:  "guest{vip}user",
			expected: "rl:{login:guest%7Bvip%7Duser}",
		},
		{
			name:     "percent signs escaped",
			ruleID:   "rule%1",
			subject:  "user%3A1",
			expected: "rl:{rule%251:user%253A1}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := engine.FormatKey(tt.ruleID, tt.subject)
			if got != tt.expected {
				t.Errorf("FormatKey(%q, %q) = %q; want %q", tt.ruleID, tt.subject, got, tt.expected)
			}
		})
	}
}

func TestFormatKey_CollisionSafetyProperty(t *testing.T) {
	// Ambiguous pairs that would collide under naive "rl:{" + ruleID + ":" + subject + "}"
	collisionPairs := []struct {
		pairA [2]string
		pairB [2]string
	}{
		{
			pairA: [2]string{"api:v1", "user10"},
			pairB: [2]string{"api", "v1:user10"},
		},
		{
			pairA: [2]string{"user", "1:2:3"},
			pairB: [2]string{"user:1", "2:3"},
		},
		{
			pairA: [2]string{"rule", "user%3Atest"},
			pairB: [2]string{"rule:user", "test"},
		},
		{
			pairA: [2]string{"rule{1}", "user"},
			pairB: [2]string{"rule", "{1}:user"},
		},
	}

	for i, cp := range collisionPairs {
		keyA := engine.FormatKey(cp.pairA[0], cp.pairA[1])
		keyB := engine.FormatKey(cp.pairB[0], cp.pairB[1])

		if keyA == keyB {
			t.Fatalf("Collision detected for pair %d! Both produced %q: pairA=%v, pairB=%v",
				i, keyA, cp.pairA, cp.pairB)
		}
	}
}

func TestFormatKey_RedisClusterHashTagIntegrity(t *testing.T) {
	// Verify that generated keys contain exactly one '{' and one '}'
	// so Redis Cluster CRC16 extracts the exact intended tag.
	inputs := [][2]string{
		{"auth:login", "user{evil}123"},
		{"{rule}", "{subject}"},
		{"nested{{}}", "user::123"},
	}

	for _, in := range inputs {
		key := engine.FormatKey(in[0], in[1], "suffix")
		// Count occurrences of '{' and '}'
		openBraces := 0
		closeBraces := 0
		for _, ch := range key {
			if ch == '{' {
				openBraces++
			}
			if ch == '}' {
				closeBraces++
			}
		}

		if openBraces != 1 || closeBraces != 1 {
			t.Errorf("FormatKey(%q, %q) produced key %q with %d '{' and %d '}'; expected exactly 1 of each",
				in[0], in[1], key, openBraces, closeBraces)
		}
	}
}

func TestEngine_Validation(t *testing.T) {
	eng, _, _, _ := setupTestEngine(t, sampleConfigYAML)

	t.Run("nil dependencies", func(t *testing.T) {
		_, err := engine.NewEngine(nil, nil)
		if err == nil {
			t.Errorf("expected error for nil dependencies")
		}
	})

	t.Run("empty subject in Allow", func(t *testing.T) {
		_, err := eng.Allow(context.Background(), "login-limit", "", 1)
		if err == nil {
			t.Errorf("expected error for empty subject")
		}
	})

	t.Run("empty ruleID in Allow", func(t *testing.T) {
		_, err := eng.Allow(context.Background(), "", "127.0.0.1", 1)
		if err == nil {
			t.Errorf("expected error for empty ruleID")
		}
	})

	t.Run("non-existent ruleID", func(t *testing.T) {
		_, err := eng.Allow(context.Background(), "non-existent", "127.0.0.1", 1)
		if err == nil {
			t.Errorf("expected error for non-existent ruleID")
		}
	})
}

func TestEngine_Allow_EndToEnd(t *testing.T) {
	eng, _, _, _ := setupTestEngine(t, sampleConfigYAML)
	ctx := context.Background()

	// Test 1: Fixed Window rule ("login-limit", limit: 2, window: 10s)
	// First request: Allowed (remaining 1)
	d1, err := eng.Allow(ctx, "login-limit", "10.0.0.1", 1)
	if err != nil {
		t.Fatalf("Allow 1 failed: %v", err)
	}
	if !d1.Allowed || d1.Remaining != 1 || d1.Limit != 2 {
		t.Errorf("unexpected decision 1: %+v", d1)
	}

	// Second request: Allowed (remaining 0)
	d2, err := eng.Allow(ctx, "login-limit", "10.0.0.1", 1)
	if err != nil {
		t.Fatalf("Allow 2 failed: %v", err)
	}
	if !d2.Allowed || d2.Remaining != 0 {
		t.Errorf("unexpected decision 2: %+v", d2)
	}

	// Third request: Denied (quota exhausted)
	d3, err := eng.Allow(ctx, "login-limit", "10.0.0.1", 1)
	if err != nil {
		t.Fatalf("Allow 3 failed: %v", err)
	}
	if d3.Allowed {
		t.Errorf("expected denial on request 3, got allowed")
	}
	if d3.RetryAfter <= 0 {
		t.Errorf("expected RetryAfter > 0 on denial, got %v", d3.RetryAfter)
	}

	// Test 2: Different subject has its own independent quota
	dOther, err := eng.Allow(ctx, "login-limit", "10.0.0.2", 1)
	if err != nil {
		t.Fatalf("Allow other subject failed: %v", err)
	}
	if !dOther.Allowed || dOther.Remaining != 1 {
		t.Errorf("other subject should have full remaining quota, got %+v", dOther)
	}
}

func TestEngine_Allow_WithWatcherReload(t *testing.T) {
	eng, watcher, configPath, _ := setupTestEngine(t, sampleConfigYAML)
	ctx := context.Background()

	// Initial rule allows limit 2
	d1, _ := eng.Allow(ctx, "login-limit", "192.168.1.100", 2)
	if !d1.Allowed || d1.Remaining != 0 {
		t.Fatalf("initial request should consume all 2 tokens, got %+v", d1)
	}

	// Immediately denied on 3rd request
	dDeny, _ := eng.Allow(ctx, "login-limit", "192.168.1.100", 1)
	if dDeny.Allowed {
		t.Fatalf("expected denial before reload")
	}

	// Dynamically update config to increase limit to 10
	updatedYAML := `
rules:
  login-limit:
    id: "login-limit"
    dimension: "ip"
    algorithm: "fixed_window"
    limit: 10
    window: 10s
    default_cost: 1
    failure_policy: "fail_closed"
`
	if err := os.WriteFile(configPath, []byte(updatedYAML), 0644); err != nil {
		t.Fatalf("failed to update config file: %v", err)
	}

	if err := watcher.Reload(); err != nil {
		t.Fatalf("watcher reload failed: %v", err)
	}

	// Engine immediately reflects the new limit (10) without restarting
	rule, err := eng.GetRule("login-limit")
	if err != nil {
		t.Fatalf("GetRule failed after reload: %v", err)
	}
	if rule.Limit != 10 {
		t.Fatalf("expected reloaded limit to be 10, got %d", rule.Limit)
	}

	// Next request against the higher limit is allowed!
	dAfterReload, err := eng.Allow(ctx, "login-limit", "192.168.1.100", 1)
	if err != nil {
		t.Fatalf("Allow after reload failed: %v", err)
	}
	if !dAfterReload.Allowed {
		t.Errorf("expected allowed after limit increased to 10, got denied: %+v", dAfterReload)
	}
}

const multiRuleConfigYAML = `
rules:
  rule-ip:
    id: "rule-ip"
    dimension: "ip"
    algorithm: "fixed_window"
    limit: 100
    window: 60s
    default_cost: 1
    failure_policy: "fail_closed"
  rule-user:
    id: "rule-user"
    dimension: "user"
    algorithm: "token_bucket"
    capacity: 50
    refill_rate: 5.0
    limit: 50
    window: 60s
    default_cost: 1
    failure_policy: "fail_closed"
  rule-apikey:
    id: "rule-apikey"
    dimension: "api_key"
    algorithm: "sliding_window"
    limit: 1000
    window: 3600s
    default_cost: 1
    failure_policy: "fail_closed"
  rule-route:
    id: "rule-route"
    dimension: "route"
    algorithm: "fixed_window"
    limit: 10
    window: 1s
    default_cost: 1
    failure_policy: "fail_closed"
`

func TestEngine_MatchRules(t *testing.T) {
	eng, _, _, _ := setupTestEngine(t, multiRuleConfigYAML)

	t.Run("matches all provided dimensions and binds subjects", func(t *testing.T) {
		req := engine.Request{
			Dimensions: map[config.Dimension]string{
				config.DimensionIP:    "192.168.1.1",
				config.DimensionUser:  "user_42",
				config.DimensionRoute: "/api/v1/pay",
			},
		}

		matches, err := eng.MatchRules(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// rule-apikey should NOT match because api_key was not provided
		if len(matches) != 3 {
			t.Fatalf("expected 3 matches, got %d", len(matches))
		}

		// Output must be deterministically sorted alphabetically by Rule.ID
		expectedRules := []struct {
			id      string
			subject string
		}{
			{"rule-ip", "192.168.1.1"},
			{"rule-route", "/api/v1/pay"},
			{"rule-user", "user_42"},
		}

		for i, expected := range expectedRules {
			if matches[i].Rule.ID != expected.id {
				t.Errorf("match[%d].Rule.ID = %q; want %q", i, matches[i].Rule.ID, expected.id)
			}
			if matches[i].Subject != expected.subject {
				t.Errorf("match[%d].Subject = %q; want %q", i, matches[i].Subject, expected.subject)
			}
		}
	})

	t.Run("ignores empty or whitespace dimension values", func(t *testing.T) {
		req := engine.Request{
			Dimensions: map[config.Dimension]string{
				config.DimensionIP:    "10.0.0.1",
				config.DimensionUser:  "   ",
				config.DimensionRoute: "",
			},
		}

		matches, err := eng.MatchRules(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(matches) != 1 {
			t.Fatalf("expected 1 match (ip only), got %d", len(matches))
		}
		if matches[0].Rule.ID != "rule-ip" || matches[0].Subject != "10.0.0.1" {
			t.Errorf("expected rule-ip with subject 10.0.0.1, got %+v", matches[0])
		}
	})

	t.Run("filters by specific RuleID when provided", func(t *testing.T) {
		req := engine.Request{
			RuleID: "rule-ip",
			Dimensions: map[config.Dimension]string{
				config.DimensionIP:   "10.0.0.1",
				config.DimensionUser: "user_42",
			},
		}

		matches, err := eng.MatchRules(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(matches) != 1 {
			t.Fatalf("expected 1 match, got %d", len(matches))
		}
		if matches[0].Rule.ID != "rule-ip" {
			t.Errorf("expected rule-ip, got %s", matches[0].Rule.ID)
		}
	})

	t.Run("returns ErrRuleNotFound for non-existent RuleID", func(t *testing.T) {
		req := engine.Request{
			RuleID: "unknown-rule",
			Dimensions: map[config.Dimension]string{
				config.DimensionIP: "10.0.0.1",
			},
		}

		_, err := eng.MatchRules(req)
		if err == nil {
			t.Fatalf("expected error for unknown RuleID")
		}
	})

	t.Run("returns empty matches when specific RuleID dimension is missing", func(t *testing.T) {
		req := engine.Request{
			RuleID: "rule-apikey",
			Dimensions: map[config.Dimension]string{
				config.DimensionIP: "10.0.0.1",
			},
		}

		matches, err := eng.MatchRules(req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(matches) != 0 {
			t.Fatalf("expected 0 matches when required dimension missing, got %d", len(matches))
		}
	})

	t.Run("order is strictly deterministic across multiple runs", func(t *testing.T) {
		req := engine.Request{
			Dimensions: map[config.Dimension]string{
				config.DimensionIP:     "1.1.1.1",
				config.DimensionUser:   "u",
				config.DimensionAPIKey: "k",
				config.DimensionRoute:  "r",
			},
		}

		for i := 0; i < 50; i++ {
			matches, err := eng.MatchRules(req)
			if err != nil {
				t.Fatalf("unexpected error on run %d: %v", i, err)
			}
			if len(matches) != 4 {
				t.Fatalf("expected 4 matches, got %d", len(matches))
			}
			// Verify exact alphabetical ordering: rule-apikey, rule-ip, rule-route, rule-user
			expectedOrder := []string{"rule-apikey", "rule-ip", "rule-route", "rule-user"}
			for idx, id := range expectedOrder {
				if matches[idx].Rule.ID != id {
					t.Fatalf("run %d failed ordering at [%d]: got %s, want %s", i, idx, matches[idx].Rule.ID, id)
				}
			}
		}
	})
}

func TestCombine(t *testing.T) {
	now := time.Now()

	t.Run("empty decisions returns allowed with max remaining", func(t *testing.T) {
		got := engine.Combine(nil)
		if !got.Allowed {
			t.Errorf("expected allowed=true for empty decisions, got %v", got.Allowed)
		}
		if got.Remaining != math.MaxInt64 {
			t.Errorf("expected remaining=MaxInt64, got %d", got.Remaining)
		}
		if got.RetryAfter != 0 {
			t.Errorf("expected retryAfter=0, got %v", got.RetryAfter)
		}
	})

	t.Run("all allowed returns minimum remaining", func(t *testing.T) {
		decisions := []algorithm.Decision{
			{
				Allowed:    true,
				Limit:      100,
				Remaining:  50,
				RetryAfter: 0,
				ResetAt:    now.Add(10 * time.Second),
				RuleID:     "rule-ip",
			},
			{
				Allowed:    true,
				Limit:      10,
				Remaining:  3,
				RetryAfter: 0,
				ResetAt:    now.Add(30 * time.Second),
				RuleID:     "rule-user",
			},
		}

		got := engine.Combine(decisions)
		if !got.Allowed {
			t.Errorf("expected allowed=true, got false")
		}
		if got.Remaining != 3 {
			t.Errorf("expected remaining=3 (bottleneck), got %d", got.Remaining)
		}
		if got.RuleID != "rule-user" {
			t.Errorf("expected RuleID=rule-user (bottleneck rule), got %s", got.RuleID)
		}
		if got.RetryAfter != 0 {
			t.Errorf("expected retryAfter=0, got %v", got.RetryAfter)
		}
	})

	t.Run("one denied returns allowed=false and denying rule retryAfter", func(t *testing.T) {
		decisions := []algorithm.Decision{
			{
				Allowed:    true,
				Limit:      100,
				Remaining:  40,
				RetryAfter: 0,
				ResetAt:    now.Add(5 * time.Second),
				RuleID:     "rule-ip",
			},
			{
				Allowed:    false,
				Limit:      10,
				Remaining:  0,
				RetryAfter: 15 * time.Second,
				ResetAt:    now.Add(15 * time.Second),
				RuleID:     "rule-user",
			},
		}

		got := engine.Combine(decisions)
		if got.Allowed {
			t.Errorf("expected allowed=false when one denies, got true")
		}
		if got.Remaining != 0 {
			t.Errorf("expected remaining=0, got %d", got.Remaining)
		}
		if got.RetryAfter != 15*time.Second {
			t.Errorf("expected retryAfter=15s, got %v", got.RetryAfter)
		}
		if got.RuleID != "rule-user" {
			t.Errorf("expected RuleID=rule-user, got %s", got.RuleID)
		}
	})

	t.Run("multiple denied returns maximum retryAfter (most restrictive)", func(t *testing.T) {
		decisions := []algorithm.Decision{
			{
				Allowed:    false,
				Limit:      50,
				Remaining:  0,
				RetryAfter: 50 * time.Millisecond,
				ResetAt:    now.Add(50 * time.Millisecond),
				RuleID:     "rule-ip",
			},
			{
				Allowed:    false,
				Limit:      10,
				Remaining:  0,
				RetryAfter: 45 * time.Second,
				ResetAt:    now.Add(45 * time.Second),
				RuleID:     "rule-route",
			},
			{
				Allowed:    false,
				Limit:      5,
				Remaining:  0,
				RetryAfter: 5 * time.Second,
				ResetAt:    now.Add(5 * time.Second),
				RuleID:     "rule-user",
			},
		}

		got := engine.Combine(decisions)
		if got.Allowed {
			t.Errorf("expected allowed=false, got true")
		}
		if got.RetryAfter != 45*time.Second {
			t.Errorf("expected retryAfter=45s (max wait), got %v", got.RetryAfter)
		}
		if got.RuleID != "rule-route" {
			t.Errorf("expected RuleID=rule-route (most restrictive rule), got %s", got.RuleID)
		}
	})
}

const combineTestConfigYAML = `
rules:
  ip-rule:
    id: "ip-rule"
    dimension: "ip"
    algorithm: "fixed_window"
    limit: 5
    window: 10s
    default_cost: 1
    failure_policy: "fail_closed"
  user-rule:
    id: "user-rule"
    dimension: "user"
    algorithm: "fixed_window"
    limit: 2
    window: 10s
    default_cost: 1
    failure_policy: "fail_closed"
`

func TestEngine_AllowRequest(t *testing.T) {
	ctx := context.Background()
	eng, _, _, _ := setupTestEngine(t, combineTestConfigYAML)

	req := engine.Request{
		Dimensions: map[config.Dimension]string{
			config.DimensionIP:   "192.168.1.1",
			config.DimensionUser: "alice",
		},
	}

	// Request 1: both have capacity (ip remaining 4, user remaining 1) -> Allowed: true, Remaining: 1
	d1, err := eng.AllowRequest(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on req 1: %v", err)
	}
	if !d1.Allowed || d1.Remaining != 1 {
		t.Fatalf("req 1: expected allowed=true, remaining=1; got allowed=%v, remaining=%d", d1.Allowed, d1.Remaining)
	}

	// Request 2: both have capacity (ip remaining 3, user remaining 0) -> Allowed: true, Remaining: 0
	d2, err := eng.AllowRequest(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on req 2: %v", err)
	}
	if !d2.Allowed || d2.Remaining != 0 {
		t.Fatalf("req 2: expected allowed=true, remaining=0; got allowed=%v, remaining=%d", d2.Allowed, d2.Remaining)
	}

	// Request 3: IP has capacity (3 left), but User is exhausted -> Denied!
	d3, err := eng.AllowRequest(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error on req 3: %v", err)
	}
	if d3.Allowed {
		t.Fatalf("req 3: expected allowed=false (user exhausted), got true")
	}
	if d3.RuleID != "user-rule" {
		t.Errorf("req 3: expected RuleID=user-rule, got %s", d3.RuleID)
	}
	if d3.RetryAfter <= 0 {
		t.Errorf("req 3: expected retryAfter > 0, got %v", d3.RetryAfter)
	}

	// Verify Partial Consumption (ADR 003):
	// IP rule still evaluated and consumed its token during Request 3 (ip capacity was 3, should now be 2).
	// A request for the same IP under an IP-only request must see remaining == 1 (since 5 limit - 3 consumed = 2 left, consuming 1 leaves 1).
	ipReq := engine.Request{
		RuleID: "ip-rule",
		Dimensions: map[config.Dimension]string{
			config.DimensionIP: "192.168.1.1",
		},
	}
	dIP, err := eng.AllowRequest(ctx, ipReq)
	if err != nil {
		t.Fatalf("unexpected error on ipReq: %v", err)
	}
	if !dIP.Allowed || dIP.Remaining != 1 {
		t.Fatalf("expected IP remaining=1 after partial consumption, got allowed=%v, remaining=%d", dIP.Allowed, dIP.Remaining)
	}
}
