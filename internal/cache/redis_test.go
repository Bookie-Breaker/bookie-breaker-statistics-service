package cache

import (
	"context"
	"testing"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/tests/testutil"
)

func TestNewClient(t *testing.T) {
	ctx := context.Background()

	client, err := NewClient(ctx, testutil.RedisURL(t))
	if err != nil {
		t.Fatalf("NewClient against live redis: %v", err)
	}
	defer func() { _ = client.Close() }()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Errorf("returned client should be connected: %v", err)
	}
}

func TestNewClientBadURL(t *testing.T) {
	if _, err := NewClient(context.Background(), "not-a-redis-url"); err == nil {
		t.Error("unparseable URL should error")
	}
}

func TestNewClientUnreachable(t *testing.T) {
	// Valid URL, nothing listening: the startup ping must fail fast rather
	// than hand back a dead client.
	if _, err := NewClient(context.Background(), "redis://127.0.0.1:1"); err == nil {
		t.Error("unreachable redis should error on ping")
	}
}
