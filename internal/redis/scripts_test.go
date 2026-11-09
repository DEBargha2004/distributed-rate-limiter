package redis_test

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"distributed-rate-limiter/internal/redis"
)

func TestScriptManager_LoadAndRun(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()

	sm, err := redis.NewScriptManager(rdb)
	if err != nil {
		t.Fatalf("NewScriptManager failed: %v", err)
	}

	// Verify SHAs were populated for all 3 algorithms
	for _, name := range []redis.ScriptName{
		redis.ScriptFixedWindow,
		redis.ScriptSlidingWindow,
		redis.ScriptTokenBucket,
	} {
		sha, ok := sm.GetSHA(name)
		if !ok || len(sha) == 0 {
			t.Fatalf("expected cached SHA for script %s, got %q", name, sha)
		}
	}

	// Execute Fixed Window script via Run
	// fixed_window ARGV: [1]=limit, [2]=window_seconds, [3]=cost
	keys := []string{"test:load_run"}
	res, err := sm.Run(ctx, redis.ScriptFixedWindow, keys, 10, 60, 1)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	arr, ok := res.([]any)
	if !ok || len(arr) < 2 {
		t.Fatalf("unexpected result format: %v", res)
	}
	// allowed = 1, remaining = 9
	if arr[0] != int64(1) {
		t.Errorf("expected allowed=1, got %v", arr[0])
	}
	if arr[1] != int64(9) {
		t.Errorf("expected remaining=9, got %v", arr[1])
	}
}

func TestScriptManager_NoScriptRecovery(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()

	sm, err := redis.NewScriptManager(rdb)
	if err != nil {
		t.Fatalf("NewScriptManager failed: %v", err)
	}

	keys := []string{"test:noscript_recovery"}
	// First run should succeed: limit=5, window=60s, cost=1
	res, err := sm.Run(ctx, redis.ScriptFixedWindow, keys, 5, 60, 1)
	if err != nil {
		t.Fatalf("initial Run failed: %v", err)
	}
	arr := res.([]any)
	if arr[0] != int64(1) || arr[1] != int64(4) {
		t.Fatalf("expected allowed=1, remaining=4, got %v", arr)
	}

	// Mid-run cache flush on Redis to simulate Redis restart or SCRIPT FLUSH
	if err := rdb.ScriptFlush(ctx).Err(); err != nil {
		t.Fatalf("ScriptFlush failed: %v", err)
	}

	// Next call MUST succeed via transparent reload and retry: cost=1
	res2, err := sm.Run(ctx, redis.ScriptFixedWindow, keys, 5, 60, 1)
	if err != nil {
		t.Fatalf("Run after SCRIPT FLUSH failed to recover: %v", err)
	}
	arr2 := res2.([]any)
	if arr2[0] != int64(1) || arr2[1] != int64(3) {
		t.Fatalf("expected allowed=1, remaining=3 after recovery, got %v", arr2)
	}
}

func TestScriptManager_UnknownScript(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	sm, err := redis.NewScriptManager(rdb)
	if err != nil {
		t.Fatalf("NewScriptManager failed: %v", err)
	}

	_, err = sm.Run(context.Background(), redis.ScriptName("non_existent"), []string{"k"}, 1)
	if err != redis.ErrUnknownScript {
		t.Fatalf("expected ErrUnknownScript, got %v", err)
	}
}

