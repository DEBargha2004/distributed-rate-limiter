package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// Worked Example (Tier W): Demonstrating SCRIPT LOAD, EVALSHA, and NOSCRIPT recovery.
func TestEvalShaDemo(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}
	defer mr.Close()

	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer rdb.Close()

	luaScript := `return ARGV[1] .. " world"`

	// 1. SCRIPT LOAD pre-compiles and caches the script, returning its SHA1 digest
	sha, err := rdb.ScriptLoad(ctx, luaScript).Result()
	if err != nil {
		t.Fatalf("ScriptLoad failed: %v", err)
	}
	t.Logf("Loaded script with SHA: %s", sha)

	// 2. EVALSHA executes the script by SHA instead of sending the full body
	res, err := rdb.EvalSha(ctx, sha, []string{}, "hello").Result()
	if err != nil {
		t.Fatalf("EvalSha failed: %v", err)
	}
	if res != "hello world" {
		t.Fatalf("expected 'hello world', got %v", res)
	}

	// 3. Simulate cache drop / Redis restart with SCRIPT FLUSH
	if err := rdb.ScriptFlush(ctx).Err(); err != nil {
		t.Fatalf("ScriptFlush failed: %v", err)
	}

	// 4. Next EVALSHA will fail with NOSCRIPT
	_, err = rdb.EvalSha(ctx, sha, []string{}, "hello").Result()
	if err == nil || !strings.HasPrefix(err.Error(), "NOSCRIPT") {
		t.Fatalf("expected NOSCRIPT error, got: %v", err)
	}
	t.Logf("Observed expected error: %v", err)

	// 5. Recovery: Reload script and retry EVALSHA once
	newSha, err := rdb.ScriptLoad(ctx, luaScript).Result()
	if err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	recoveredRes, err := rdb.EvalSha(ctx, newSha, []string{}, "recovered").Result()
	if err != nil {
		t.Fatalf("Recovered EvalSha failed: %v", err)
	}
	if recoveredRes != "recovered world" {
		t.Fatalf("expected 'recovered world', got %v", recoveredRes)
	}
	t.Log("Successfully recovered from NOSCRIPT!")
}

