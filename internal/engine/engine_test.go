package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

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
