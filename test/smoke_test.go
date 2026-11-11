package test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"distributed-rate-limiter/internal/config"
	"distributed-rate-limiter/internal/engine"
	"distributed-rate-limiter/internal/redis"
	"distributed-rate-limiter/internal/server"
	pb "distributed-rate-limiter/proto/ratelimiter/v1"
)

const bufSize = 1024 * 1024

func setupRealRedis(t *testing.T) *goredis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}

	client := goredis.NewClient(&goredis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		if os.Getenv("REQUIRE_REDIS") == "1" {
			t.Fatalf("real Redis required at %s, ping failed: %v", addr, err)
		}
		t.Skipf("Skipping smoke test: real Redis not reachable at %s: %v", addr, err)
	}

	t.Cleanup(func() {
		_ = client.Close()
	})
	return client
}

func setupSmokeHarness(t *testing.T, ruleYAML string) pb.RateLimiterClient {
	t.Helper()

	// 1. Connect to Redis
	rdb := setupRealRedis(t)

	// 2. Preload Lua scripts
	sm, err := redis.NewScriptManager(rdb)
	if err != nil {
		t.Fatalf("failed to init script manager: %v", err)
	}

	// 3. Setup dynamic config watcher with test rule
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "rules.yaml")
	if err := os.WriteFile(configPath, []byte(ruleYAML), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	watcher, err := config.NewWatcher(configPath)
	if err != nil {
		t.Fatalf("failed to init watcher: %v", err)
	}

	// 4. Instantiate Engine
	eng, err := engine.NewEngine(watcher, sm)
	if err != nil {
		t.Fatalf("failed to init engine: %v", err)
	}

	// 5. Setup in-process gRPC Server via bufconn
	grpcSrv, err := server.NewGRPCServer(eng)
	if err != nil {
		t.Fatalf("failed to init grpc server: %v", err)
	}

	lis := bufconn.Listen(bufSize)
	baseServer := grpc.NewServer()
	pb.RegisterRateLimiterServer(baseServer, grpcSrv)

	go func() {
		if err := baseServer.Serve(lis); err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Logf("bufconn server stopped: %v", err)
		}
	}()

	t.Cleanup(func() {
		baseServer.GracefulStop()
		_ = lis.Close()
	})

	// 6. Connect gRPC client to the in-memory pipe
	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("failed to connect to bufnet: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
	})

	return pb.NewRateLimiterClient(conn)
}

func TestSmoke_ThinSlice_EndToEnd(t *testing.T) {
	ruleYAML := `
rules:
  smoke-rule:
    id: smoke-rule
    dimension: user
    algorithm: token_bucket
    capacity: 3
    refill_rate: 0.5
    failure_policy: fail_closed
`
	client := setupSmokeHarness(t, ruleYAML)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Unique subject per test run; Redis script TTL handles cleanup automatically
	testSubject := fmt.Sprintf("smoke-%d", time.Now().UnixNano())

	// =========================================================================
	// LEARNER IMPLEMENTATION AREA [Brick 3.6]:
	//
	// Make 4 consecutive client.Allow calls for testSubject with Cost: 1.
	//
	// Calls 1 to 3:
	// - Must be Allowed == true
	// - Remaining must decrement (2 -> 1 -> 0)
	// - RetryAfterMs must be 0
	//
	// Call 4:
	// - Must be Allowed == false
	// - Remaining must be 0
	// - RetryAfterMs must be within the expected range for refill_rate: 0.5
	//   (with 1 missing token at 0.5 tokens/sec, expect ~2000ms)
	// - Limit must be 3, RuleId must be "smoke-rule"
	// =========================================================================

	for i := 0; i < 4; i++ {
		res, err := client.Allow(ctx, &pb.AllowRequest{Key: testSubject, RuleId: "smoke-rule", Cost: 1})
		if err != nil {
			t.Fatalf("call %d error: %v", i+1, err)
		}

		if res.Limit != 3 {
			t.Errorf("call %d: expected Limit=3, got %d", i+1, res.Limit)
		}
		if res.RuleId != "smoke-rule" {
			t.Errorf("call %d: expected RuleId=%q, got %q", i+1, "smoke-rule", res.RuleId)
		}
		if res.ResetAtMs <= 0 {
			t.Errorf("call %d: expected ResetAtMs > 0, got %d", i+1, res.ResetAtMs)
		}

		if i <= 2 {
			wantRemaining := int64(3 - i - 1)
			if !res.Allowed {
				t.Errorf("call %d: expected Allowed=true, got false", i+1)
			}
			if res.Remaining != wantRemaining {
				t.Errorf("call %d: expected Remaining=%d, got %d", i+1, wantRemaining, res.Remaining)
			}
			if res.RetryAfterMs != 0 {
				t.Errorf("call %d: expected RetryAfterMs=0, got %d", i+1, res.RetryAfterMs)
			}
		} else {
			if res.Allowed {
				t.Errorf("call %d: expected Allowed=false, got true", i+1)
			}
			if res.Remaining != 0 {
				t.Errorf("call %d: expected Remaining=0, got %d", i+1, res.Remaining)
			}
			if res.RetryAfterMs < 1000 || res.RetryAfterMs > 2000 {
				t.Errorf("call %d: expected RetryAfterMs in [1000, 2000], got %d", i+1, res.RetryAfterMs)
			}
		}
	}
}
