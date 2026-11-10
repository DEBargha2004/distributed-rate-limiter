package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"distributed-rate-limiter/internal/config"
	"distributed-rate-limiter/internal/redis"
)

func setupTestLimiter(t *testing.T, rule config.Rule) (*redis.Limiter, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	sm, err := redis.NewScriptManager(rdb)
	if err != nil {
		t.Fatalf("failed to create script manager: %v", err)
	}

	limiter, err := redis.NewLimiter(sm, rule)
	if err != nil {
		t.Fatalf("failed to create limiter: %v", err)
	}

	return limiter, mr
}

func TestLimiter_Validation(t *testing.T) {
	rule := config.Rule{
		ID:            "test-rule",
		Dimension:     config.DimensionUser,
		Algorithm:     config.AlgorithmFixedWindow,
		Limit:         10,
		Window:        time.Minute,
		FailurePolicy: config.PolicyFailClosed,
	}

	t.Run("nil script manager", func(t *testing.T) {
		_, err := redis.NewLimiter(nil, rule)
		if err == nil {
			t.Errorf("expected error for nil script manager")
		}
	})

	t.Run("invalid rule", func(t *testing.T) {
		mr := miniredis.RunT(t)
		rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
		t.Cleanup(func() { _ = rdb.Close() })
		sm, _ := redis.NewScriptManager(rdb)

		invalidRule := rule
		invalidRule.Limit = -1
		_, err := redis.NewLimiter(sm, invalidRule)
		if err == nil {
			t.Errorf("expected error for invalid rule config")
		}
	})

	t.Run("empty key returns ErrEmptyKey", func(t *testing.T) {
		limiter, _ := setupTestLimiter(t, rule)
		_, err := limiter.Check(context.Background(), "", 1)
		if err == nil {
			t.Errorf("expected error for empty key")
		}
	})
}

func TestLimiter_FixedWindow(t *testing.T) {
	rule := config.Rule{
		ID:            "fw-rule",
		Dimension:     config.DimensionUser,
		Algorithm:     config.AlgorithmFixedWindow,
		Limit:         3,
		Window:        10 * time.Second,
		DefaultCost:   1,
		FailurePolicy: config.PolicyFailClosed,
	}

	limiter, _ := setupTestLimiter(t, rule)
	ctx := context.Background()
	key := "{rl:fw:user1}"

	// Request 1: Cost 1 -> Allowed (remaining 2)
	d1, err := limiter.Check(ctx, key, 1)
	if err != nil {
		t.Fatalf("Check 1 failed: %v", err)
	}
	if !d1.Allowed || d1.Remaining != 2 || d1.Limit != 3 || d1.RuleID != "fw-rule" {
		t.Errorf("unexpected decision 1: %+v", d1)
	}

	// Request 2: Cost 2 -> Allowed (remaining 0)
	d2, err := limiter.Check(ctx, key, 2)
	if err != nil {
		t.Fatalf("Check 2 failed: %v", err)
	}
	if !d2.Allowed || d2.Remaining != 0 {
		t.Errorf("unexpected decision 2: %+v", d2)
	}

	// Request 3: Cost 1 -> Denied (remaining 0, retryAfter > 0)
	d3, err := limiter.Check(ctx, key, 1)
	if err != nil {
		t.Fatalf("Check 3 failed: %v", err)
	}
	if d3.Allowed {
		t.Errorf("expected denied on request 3, got allowed: %+v", d3)
	}
	if d3.RetryAfter <= 0 {
		t.Errorf("expected RetryAfter > 0 on denial, got %v", d3.RetryAfter)
	}
	if d3.ResetAt.Before(time.Now()) {
		t.Errorf("expected ResetAt in future, got %v", d3.ResetAt)
	}
}

func TestLimiter_SlidingWindow(t *testing.T) {
	rule := config.Rule{
		ID:            "sw-rule",
		Dimension:     config.DimensionIP,
		Algorithm:     config.AlgorithmSlidingWindow,
		Limit:         5,
		Window:        10 * time.Second,
		DefaultCost:   1,
		FailurePolicy: config.PolicyFailClosed,
	}

	limiter, _ := setupTestLimiter(t, rule)
	ctx := context.Background()
	key := "{rl:sw:ip1}:sliding"

	// Request 1: Cost 3 -> Allowed (remaining 2)
	d1, err := limiter.Check(ctx, key, 3)
	if err != nil {
		t.Fatalf("Check 1 failed: %v", err)
	}
	if !d1.Allowed || d1.Remaining != 2 {
		t.Errorf("unexpected decision 1: %+v", d1)
	}

	// Request 2: Cost 3 -> Exceeds limit (5) -> Denied
	d2, err := limiter.Check(ctx, key, 3)
	if err != nil {
		t.Fatalf("Check 2 failed: %v", err)
	}
	if d2.Allowed {
		t.Errorf("expected denial for cost 3 exceeding remaining 2, got allowed")
	}
	if d2.RetryAfter <= 0 {
		t.Errorf("expected positive retry after, got %v", d2.RetryAfter)
	}
}

func TestLimiter_TokenBucket(t *testing.T) {
	rule := config.Rule{
		ID:            "tb-rule",
		Dimension:     config.DimensionAPIKey,
		Algorithm:     config.AlgorithmTokenBucket,
		Capacity:      10,
		RefillRate:    2.5,
		Limit:         10,
		Window:        60 * time.Second,
		DefaultCost:   1,
		FailurePolicy: config.PolicyFailClosed,
	}

	limiter, _ := setupTestLimiter(t, rule)
	ctx := context.Background()
	key := "{rl:tb:key1}:bucket"

	// Request 1: Cost 4 -> Allowed (remaining 6)
	d1, err := limiter.Check(ctx, key, 4)
	if err != nil {
		t.Fatalf("Check 1 failed: %v", err)
	}
	if !d1.Allowed || d1.Remaining != 6 || d1.Limit != 10 {
		t.Errorf("unexpected decision 1: %+v", d1)
	}

	// Request 2: Default cost (0 passed -> defaults to rule.DefaultCost = 1) -> Allowed (remaining 5)
	d2, err := limiter.Check(ctx, key, 0)
	if err != nil {
		t.Fatalf("Check 2 failed: %v", err)
	}
	if !d2.Allowed || d2.Remaining != 5 {
		t.Errorf("unexpected decision 2: %+v", d2)
	}

	// Request 3: Cost 8 (exceeds remaining 5) -> Denied
	d3, err := limiter.Check(ctx, key, 8)
	if err != nil {
		t.Fatalf("Check 3 failed: %v", err)
	}
	if d3.Allowed {
		t.Errorf("expected denial when cost exceeds remaining tokens")
	}
	if d3.RetryAfter <= 0 {
		t.Errorf("expected positive RetryAfter, got %v", d3.RetryAfter)
	}
}

