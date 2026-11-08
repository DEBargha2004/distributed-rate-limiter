package redis_test

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func loadLuaScript(t *testing.T, filename string) string {
	t.Helper()
	// Navigate relative to current directory or module root
	path := filepath.Join("lua", filename)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read lua script %s: %v", path, err)
	}
	return string(content)
}

func setupTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	t.Cleanup(func() {
		_ = client.Close()
	})
	return mr, client
}

func TestFixedWindowLuaScript(t *testing.T) {
	scriptContent := loadLuaScript(t, "fixed_window.lua")
	ctx := context.Background()

	t.Run("first request initializes counter and allows execution", func(t *testing.T) {
		mr, client := setupTestRedis(t)
		key := "{rl:test:user1}:fixed"
		limit := int64(10)
		windowSec := int64(60)
		cost := int64(1)

		res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
		if err != nil {
			t.Fatalf("Lua script failed: %v", err)
		}

		vals, ok := res.([]interface{})
		if !ok || len(vals) < 4 {
			t.Fatalf("expected 4 return values [allowed, remaining, retry_after, reset_at], got %#v", res)
		}

		allowed := vals[0].(int64)
		remaining := vals[1].(int64)
		retryAfter := vals[2].(int64)

		if allowed != 1 {
			t.Errorf("expected allowed=1, got %d", allowed)
		}
		if remaining != limit-cost {
			t.Errorf("expected remaining=%d, got %d", limit-cost, remaining)
		}
		if retryAfter != 0 {
			t.Errorf("expected retryAfter=0, got %d", retryAfter)
		}

		// Ensure TTL was set on the key
		ttl := mr.TTL(key)
		if ttl <= 0 || ttl > time.Duration(windowSec)*time.Second {
			t.Errorf("expected key TTL around %ds, got %v", windowSec, ttl)
		}
	})

	t.Run("allows requests until limit is reached and then rejects", func(t *testing.T) {
		_, client := setupTestRedis(t)
		key := "{rl:test:user2}:fixed"
		limit := int64(3)
		windowSec := int64(60)
		cost := int64(1)

		// 3 allowed requests
		for i := int64(1); i <= limit; i++ {
			res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
			if err != nil {
				t.Fatalf("iteration %d failed: %v", i, err)
			}
			vals := res.([]interface{})
			if vals[0].(int64) != 1 {
				t.Fatalf("iteration %d: expected allowed=1, got %d", i, vals[0].(int64))
			}
			expectedRemaining := limit - i
			if vals[1].(int64) != expectedRemaining {
				t.Errorf("iteration %d: expected remaining=%d, got %d", i, expectedRemaining, vals[1].(int64))
			}
		}

		// 4th request must be rejected
		res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
		if err != nil {
			t.Fatalf("rejected request eval failed: %v", err)
		}
		vals := res.([]interface{})
		allowed := vals[0].(int64)
		remaining := vals[1].(int64)
		retryAfter := vals[2].(int64)

		if allowed != 0 {
			t.Errorf("expected allowed=0 for exceeded quota, got %d", allowed)
		}
		if remaining != 0 {
			t.Errorf("expected remaining=0, got %d", remaining)
		}
		if retryAfter <= 0 {
			t.Errorf("expected retryAfter > 0, got %d", retryAfter)
		}
	})

	t.Run("rejects request if cost exceeds available quota", func(t *testing.T) {
		_, client := setupTestRedis(t)
		key := "{rl:test:user3}:fixed"
		limit := int64(5)
		windowSec := int64(60)

		// Spend 3 tokens
		_, _ = client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, 3).Result()

		// Request costing 3 more should be rejected (3 + 3 = 6 > 5)
		res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, 3).Result()
		if err != nil {
			t.Fatalf("eval failed: %v", err)
		}
		vals := res.([]interface{})
		if vals[0].(int64) != 0 {
			t.Errorf("expected rejected request for cost exceeding quota")
		}
		if vals[1].(int64) != 2 {
			t.Errorf("expected remaining=2 tokens, got %d", vals[1].(int64))
		}
	})
}

func TestSlidingWindowLuaScript(t *testing.T) {
	scriptContent := loadLuaScript(t, "sliding_window.lua")
	ctx := context.Background()

	t.Run("first request initializes hash and allows execution", func(t *testing.T) {
		mr, client := setupTestRedis(t)
		key := "{rl:test:user1}:sliding"
		limit := int64(10)
		windowSec := int64(60)
		cost := int64(1)

		res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
		if err != nil {
			t.Fatalf("Lua script failed: %v", err)
		}

		vals, ok := res.([]interface{})
		if !ok || len(vals) < 4 {
			t.Fatalf("expected 4 return values [allowed, remaining, retry_after, reset_at], got %#v", res)
		}

		allowed := vals[0].(int64)
		remaining := vals[1].(int64)
		retryAfter := vals[2].(int64)

		if allowed != 1 {
			t.Errorf("expected allowed=1, got %d", allowed)
		}
		if remaining != limit-cost {
			t.Errorf("expected remaining=%d, got %d", limit-cost, remaining)
		}
		if retryAfter != 0 {
			t.Errorf("expected retryAfter=0, got %d", retryAfter)
		}

		// Ensure TTL was set on the key (should be ~2x window)
		ttl := mr.TTL(key)
		if ttl <= time.Duration(windowSec)*time.Second {
			t.Errorf("expected key TTL around 2x window (%ds), got %v", windowSec*2, ttl)
		}
	})

	t.Run("allows requests until limit is reached and then rejects", func(t *testing.T) {
		_, client := setupTestRedis(t)
		key := "{rl:test:user2}:sliding"
		limit := int64(3)
		windowSec := int64(60)
		cost := int64(1)

		// 3 allowed requests
		for i := int64(1); i <= limit; i++ {
			res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
			if err != nil {
				t.Fatalf("iteration %d failed: %v", i, err)
			}
			vals := res.([]interface{})
			if vals[0].(int64) != 1 {
				t.Fatalf("iteration %d: expected allowed=1, got %d", i, vals[0].(int64))
			}
		}

		// 4th request must be rejected
		res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
		if err != nil {
			t.Fatalf("rejected request eval failed: %v", err)
		}
		vals := res.([]interface{})
		if vals[0].(int64) != 0 {
			t.Errorf("expected allowed=0 for exceeded quota, got %d", vals[0].(int64))
		}
		if vals[2].(int64) <= 0 {
			t.Errorf("expected retryAfter > 0, got %d", vals[2].(int64))
		}
	})

	t.Run("rejects request if cost exceeds available quota", func(t *testing.T) {
		_, client := setupTestRedis(t)
		key := "{rl:test:user3}:sliding"
		limit := int64(5)
		windowSec := int64(60)

		// Spend 3 tokens
		_, _ = client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, 3).Result()

		// Request costing 3 more should be rejected (3 + 3 = 6 > 5)
		res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, 3).Result()
		if err != nil {
			t.Fatalf("eval failed: %v", err)
		}
		vals := res.([]interface{})
		if vals[0].(int64) != 0 {
			t.Errorf("expected rejected request for cost exceeding quota")
		}
	})
}

func TestTokenBucketLuaScript(t *testing.T) {
	scriptContent := loadLuaScript(t, "token_bucket.lua")
	ctx := context.Background()

	t.Run("first request initializes bucket with full capacity and allows cost", func(t *testing.T) {
		mr, client := setupTestRedis(t)
		key := "{rl:test:user1}:bucket"
		capacity := 10.0
		refillRate := 2.0 // 2 tokens per second
		cost := int64(3)
		idleTTL := 60

		res, err := client.Eval(ctx, scriptContent, []string{key}, capacity, refillRate, cost, idleTTL).Result()
		if err != nil {
			t.Fatalf("Lua script failed: %v", err)
		}

		vals, ok := res.([]interface{})
		if !ok || len(vals) < 4 {
			t.Fatalf("expected 4 return values, got %#v", res)
		}

		allowed := vals[0].(int64)
		remaining := vals[1].(int64)
		retryAfter := vals[2].(int64)

		if allowed != 1 {
			t.Errorf("expected allowed=1, got %d", allowed)
		}
		if remaining != int64(capacity)-cost {
			t.Errorf("expected remaining=%d, got %d", int64(capacity)-cost, remaining)
		}
		if retryAfter != 0 {
			t.Errorf("expected retryAfter=0, got %d", retryAfter)
		}

		ttl := mr.TTL(key)
		if ttl <= 0 || ttl > time.Duration(idleTTL)*time.Second {
			t.Errorf("expected key TTL around %ds, got %v", idleTTL, ttl)
		}
	})

	t.Run("allows bursts up to capacity and rejects when exhausted", func(t *testing.T) {
		_, client := setupTestRedis(t)
		key := "{rl:test:user2}:bucket"
		capacity := 3.0
		refillRate := 0.1 // very slow refill to test burst capacity
		cost := int64(1)
		idleTTL := 60

		// 3 allowed bursts
		for i := 1; i <= int(capacity); i++ {
			res, err := client.Eval(ctx, scriptContent, []string{key}, capacity, refillRate, cost, idleTTL).Result()
			if err != nil {
				t.Fatalf("burst %d failed: %v", i, err)
			}
			vals := res.([]interface{})
			if vals[0].(int64) != 1 {
				t.Fatalf("burst %d: expected allowed=1, got %d", i, vals[0].(int64))
			}
		}

		// 4th request must be rejected
		res, err := client.Eval(ctx, scriptContent, []string{key}, capacity, refillRate, cost, idleTTL).Result()
		if err != nil {
			t.Fatalf("rejected eval failed: %v", err)
		}
		vals := res.([]interface{})
		if vals[0].(int64) != 0 {
			t.Errorf("expected allowed=0 for exhausted bucket, got %d", vals[0].(int64))
		}
		if vals[2].(int64) <= 0 {
			t.Errorf("expected retryAfter > 0, got %d", vals[2].(int64))
		}
	})

	t.Run("rejects request if cost is larger than capacity", func(t *testing.T) {
		_, client := setupTestRedis(t)
		key := "{rl:test:user3}:bucket"
		capacity := 5.0
		refillRate := 1.0
		cost := int64(10) // larger than full bucket
		idleTTL := 60

		res, err := client.Eval(ctx, scriptContent, []string{key}, capacity, refillRate, cost, idleTTL).Result()
		if err != nil {
			t.Fatalf("eval failed: %v", err)
		}
		vals := res.([]interface{})
		if vals[0].(int64) != 0 {
			t.Errorf("expected allowed=0 when cost > capacity")
		}
		if vals[2].(int64) <= 0 {
			t.Errorf("expected retryAfter > 0")
		}
	})
}



