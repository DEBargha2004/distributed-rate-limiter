package redis_test

import (
	"context"
	"testing"
	"time"

	"distributed-rate-limiter/internal/redis"

	"github.com/alicebob/miniredis/v2"
)

func TestNewClient_Validation(t *testing.T) {
	_, err := redis.NewClient(redis.Config{Addrs: []string{}})
	if err != redis.ErrEmptyAddresses {
		t.Fatalf("expected ErrEmptyAddresses, got %v", err)
	}
}

func TestClient_PingAndClose(t *testing.T) {
	mr := miniredis.RunT(t)

	client, err := redis.NewClient(redis.Config{
		Addrs: []string{mr.Addr()},
	})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("Ping failed: %v", err)
	}

	if client.Universal() == nil {
		t.Errorf("expected UniversalClient to be non-nil")
	}

	if err := client.Close(); err != nil {
		t.Errorf("Close failed: %v", err)
	}
}

func TestClient_PingTimeout(t *testing.T) {
	mr := miniredis.RunT(t)

	client, err := redis.NewClient(redis.Config{
		Addrs: []string{mr.Addr()},
	})
	if err != nil {
		t.Fatalf("unexpected error creating client: %v", err)
	}
	defer client.Close()

	// An already cancelled context must fail Ping immediately
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = client.Ping(ctx)
	if err == nil {
		t.Errorf("expected error when pinging with cancelled context, got nil")
	}
}

