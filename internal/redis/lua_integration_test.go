//go:build integration

package redis_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func getRealRedisClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client := redis.NewClient(&redis.Options{
		Addr: addr,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Failed to connect to real Redis at %s: %v. (Is Docker container running?)", addr, err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func loadScript(t *testing.T, filename string) string {
	t.Helper()
	path := filepath.Join("lua", filename)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read lua script %s: %v", path, err)
	}
	return string(content)
}

func TestIntegration_FixedWindow_RealRedis(t *testing.T) {
	client := getRealRedisClient(t)
	scriptContent := loadScript(t, "fixed_window.lua")
	ctx := context.Background()

	key := "{rl:integ:user1}:fixed"
	defer client.Del(ctx, key)

	limit := int64(3)
	windowSec := int64(10)
	cost := int64(1)

	// Call 1: Allow
	res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
	if err != nil {
		t.Fatalf("Lua script failed on real Redis: %v", err)
	}
	vals := res.([]interface{})
	if vals[0].(int64) != 1 {
		t.Fatalf("expected allowed=1, got %v", vals[0])
	}

	// Invariant: Server-side TTL must be > 0
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL check failed: %v", err)
	}
	if ttl <= 0 || ttl > time.Duration(windowSec)*time.Second {
		t.Errorf("expected TTL between 0 and %ds, got %v", windowSec, ttl)
	}
}

func TestIntegration_SlidingWindow_RealRedis(t *testing.T) {
	client := getRealRedisClient(t)
	scriptContent := loadScript(t, "sliding_window.lua")
	ctx := context.Background()

	key := "{rl:integ:user2}:sliding"
	defer client.Del(ctx, key)

	limit := int64(5)
	windowSec := int64(10)
	cost := int64(2)

	res, err := client.Eval(ctx, scriptContent, []string{key}, limit, windowSec, cost).Result()
	if err != nil {
		t.Fatalf("Sliding window script failed on real Redis: %v", err)
	}
	vals := res.([]interface{})
	if vals[0].(int64) != 1 {
		t.Fatalf("expected allowed=1, got %v", vals[0])
	}

	// Invariant: TTL must be set (sliding window sets 2 * windowSec)
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL check failed: %v", err)
	}
	if ttl <= 0 {
		t.Errorf("expected TTL > 0 on sliding window key, got %v", ttl)
	}
}

func TestIntegration_TokenBucket_RealRedis(t *testing.T) {
	client := getRealRedisClient(t)
	scriptContent := loadScript(t, "token_bucket.lua")
	ctx := context.Background()

	key := "{rl:integ:user3}:bucket"
	defer client.Del(ctx, key)

	capacity := 5.0
	refillRate := 1.0
	cost := int64(2)
	idleTTL := 30

	res, err := client.Eval(ctx, scriptContent, []string{key}, capacity, refillRate, cost, idleTTL).Result()
	if err != nil {
		t.Fatalf("Token bucket script failed on real Redis: %v", err)
	}
	vals := res.([]interface{})
	if vals[0].(int64) != 1 {
		t.Fatalf("expected allowed=1, got %v", vals[0])
	}

	// Invariant: TTL must be set
	ttl, err := client.TTL(ctx, key).Result()
	if err != nil {
		t.Fatalf("TTL check failed: %v", err)
	}
	if ttl <= 0 || ttl > time.Duration(idleTTL)*time.Second {
		t.Errorf("expected TTL between 0 and %ds, got %v", idleTTL, ttl)
	}
}

