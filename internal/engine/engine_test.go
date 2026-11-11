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

func TestFormatKey(t *testing.T) {
	tests := []struct {
		ruleID   string
		subject  string
		expected string
	}{
		{
			ruleID:   "login",
			subject:  "192.168.1.1",
			expected: "rl:{login:192.168.1.1}",
		},
		{
			ruleID:   "api-key-tier1",
			subject:  "usr_48291",
			expected: "rl:{api-key-tier1:usr_48291}",
		},
		{
			ruleID:   "  spaces  ",
			subject:  "  user123  ",
			expected: "rl:{spaces:user123}",
		},
	}

	for _, tt := range tests {
		got := engine.FormatKey(tt.ruleID, tt.subject)
		if got != tt.expected {
			t.Errorf("FormatKey(%q, %q) = %q; expected %q", tt.ruleID, tt.subject, got, tt.expected)
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
