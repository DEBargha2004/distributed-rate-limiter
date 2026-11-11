package server_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"distributed-rate-limiter/internal/algorithm"
	"distributed-rate-limiter/internal/engine"
	"distributed-rate-limiter/internal/server"
	pb "distributed-rate-limiter/proto/ratelimiter/v1"
)

type mockEngine struct {
	decision algorithm.Decision
	err      error
}

func (m *mockEngine) Allow(ctx context.Context, ruleID string, subject string, cost int64) (algorithm.Decision, error) {
	if m.err != nil {
		return algorithm.Decision{}, m.err
	}
	return m.decision, nil
}

func TestGRPCServer_Validation(t *testing.T) {
	srv, _ := server.NewGRPCServer(&mockEngine{})

	t.Run("nil engine constructor", func(t *testing.T) {
		_, err := server.NewGRPCServer(nil)
		if err == nil {
			t.Errorf("expected error for nil engine")
		}
	})

	t.Run("nil request", func(t *testing.T) {
		_, err := srv.Allow(context.Background(), nil)
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got %v", err)
		}
	})

	t.Run("empty key", func(t *testing.T) {
		req := &pb.AllowRequest{Key: "", RuleId: "rule1"}
		_, err := srv.Allow(context.Background(), req)
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.InvalidArgument {
			t.Errorf("expected InvalidArgument, got %v", err)
		}
	})
}

func TestGRPCServer_ErrorMapping(t *testing.T) {
	t.Run("rule not found maps to codes.NotFound", func(t *testing.T) {
		srv, _ := server.NewGRPCServer(&mockEngine{
			err: engine.ErrRuleNotFound,
		})
		_, err := srv.Allow(context.Background(), &pb.AllowRequest{Key: "user1", RuleId: "missing"})
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.NotFound {
			t.Errorf("expected NotFound, got %v", err)
		}
	})

	t.Run("internal error maps to codes.Internal", func(t *testing.T) {
		srv, _ := server.NewGRPCServer(&mockEngine{
			err: errors.New("redis connection timeout"),
		})
		_, err := srv.Allow(context.Background(), &pb.AllowRequest{Key: "user1", RuleId: "rule1"})
		st, ok := status.FromError(err)
		if !ok || st.Code() != codes.Internal {
			t.Errorf("expected Internal, got %v", err)
		}
	})
}

func TestGRPCServer_AllowMapping(t *testing.T) {
	resetTime := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	t.Run("allowed request mapping", func(t *testing.T) {
		srv, _ := server.NewGRPCServer(&mockEngine{
			decision: algorithm.Decision{
				Allowed:    true,
				Limit:      10,
				Remaining:  9,
				RetryAfter: 0,
				ResetAt:    resetTime,
				RuleID:     "login-limit",
			},
		})

		resp, err := srv.Allow(context.Background(), &pb.AllowRequest{
			Key:    "user123",
			RuleId: "login-limit",
			Cost:   1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !resp.Allowed {
			t.Errorf("expected Allowed = true")
		}
		if resp.Limit != 10 {
			t.Errorf("expected Limit = 10, got %d", resp.Limit)
		}
		if resp.Remaining != 9 {
			t.Errorf("expected Remaining = 9, got %d", resp.Remaining)
		}
		if resp.RetryAfterMs != 0 {
			t.Errorf("expected RetryAfterMs = 0, got %d", resp.RetryAfterMs)
		}
		if resp.ResetAtMs != resetTime.UnixMilli() {
			t.Errorf("expected ResetAtMs = %d, got %d", resetTime.UnixMilli(), resp.ResetAtMs)
		}
		if resp.RuleId != "login-limit" {
			t.Errorf("expected RuleId = 'login-limit', got %q", resp.RuleId)
		}
	})

	t.Run("denied request mapping", func(t *testing.T) {
		retryDuration := 3500 * time.Millisecond
		srv, _ := server.NewGRPCServer(&mockEngine{
			decision: algorithm.Decision{
				Allowed:    false,
				Limit:      5,
				Remaining:  0,
				RetryAfter: retryDuration,
				ResetAt:    resetTime,
				RuleID:     "api-tier-1",
			},
		})

		resp, err := srv.Allow(context.Background(), &pb.AllowRequest{
			Key:    "key_abc",
			RuleId: "api-tier-1",
			Cost:   2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.Allowed {
			t.Errorf("expected Allowed = false")
		}
		if resp.Remaining != 0 {
			t.Errorf("expected Remaining = 0, got %d", resp.Remaining)
		}
		if resp.RetryAfterMs != 3500 {
			t.Errorf("expected RetryAfterMs = 3500, got %d", resp.RetryAfterMs)
		}
	})
}

