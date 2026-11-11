package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"distributed-rate-limiter/internal/algorithm"
	"distributed-rate-limiter/internal/engine"
	pb "distributed-rate-limiter/proto/ratelimiter/v1"
)

// RateLimiterEngine defines the interface required by the gRPC service to evaluate decisions.
type RateLimiterEngine interface {
	Allow(ctx context.Context, ruleID string, subject string, cost int64) (algorithm.Decision, error)
}

// GRPCServer implements the ratelimiter.v1.RateLimiter gRPC service.
type GRPCServer struct {
	pb.UnimplementedRateLimiterServer
	engine RateLimiterEngine
}

// NewGRPCServer creates a new GRPCServer instance bound to a rate-limiting engine.
func NewGRPCServer(eng RateLimiterEngine) (*GRPCServer, error) {
	if eng == nil {
		return nil, errors.New("server: engine cannot be nil")
	}
	return &GRPCServer{engine: eng}, nil
}

// Allow handles incoming gRPC rate limit check requests.
func (s *GRPCServer) Allow(ctx context.Context, req *pb.AllowRequest) (*pb.AllowResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request cannot be nil")
	}
	if req.Key == "" {
		return nil, status.Error(codes.InvalidArgument, "key cannot be empty")
	}

	// =========================================================================
	// LEARNER IMPLEMENTATION AREA [Brick 3.5]:
	//
	// Step A: Invoke the engine to assess the request:
	//         decision, err := s.engine.Allow(ctx, req.RuleId, req.Key, req.Cost)
	//
	// Step B: Error handling:
	//         If err matches engine.ErrRuleNotFound, return gRPC NotFound status:
	//             return nil, status.Error(codes.NotFound, err.Error())
	//         For other errors, return gRPC Internal status:
	//             return nil, status.Error(codes.Internal, err.Error())
	//
	// Step C: Map algorithm.Decision to pb.AllowResponse:
	//         - Allowed:      decision.Allowed
	//         - Limit:        decision.Limit
	//         - Remaining:    decision.Remaining
	//         - RetryAfterMs: decision.RetryAfter.Milliseconds()
	//         - ResetAtMs:    decision.ResetAt.UnixMilli()
	//         - RuleId:       decision.RuleID
	// =========================================================================

	// TODO [Learner Brick 3.5]: implement Step A, B, and C

	decision, err := s.engine.Allow(ctx, req.RuleId, req.Key, req.Cost)

	if err != nil {
		if errors.Is(err, engine.ErrRuleNotFound) {
			return nil, status.Error(codes.NotFound, err.Error())
		} else {
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	return &pb.AllowResponse{
		Allowed:      decision.Allowed,
		Limit:        decision.Limit,
		Remaining:    decision.Remaining,
		RetryAfterMs: decision.RetryAfter.Milliseconds(),
		ResetAtMs:    decision.ResetAt.UnixMilli(),
		RuleId:       decision.RuleID,
	}, nil
}
