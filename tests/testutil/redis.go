// Package testutil provides shared infrastructure helpers for co-located
// unit tests that need a real Redis (the service's primary store). It lives
// under tests/ so coverage tooling ignores it. Tests are skipped when Docker
// is unavailable so local pre-push hooks still pass on Docker-less machines,
// mirroring tests/integration.
package testutil

import (
	"context"
	"sync"
	"testing"

	goredis "github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

var (
	redisOnce sync.Once
	redisURL  string
	redisErr  error
)

func dockerAvailable(ctx context.Context) bool {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return false
	}
	defer func() { _ = provider.Close() }()
	return provider.Health(ctx) == nil
}

// RedisURL starts one Redis container per test binary (cleaned up by the
// testcontainers reaper) and returns its connection URL, skipping the test
// when Docker is unavailable.
func RedisURL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	if !dockerAvailable(ctx) {
		t.Skip("skipping: Docker is not available")
	}

	redisOnce.Do(func() {
		container, err := tcredis.Run(ctx, "redis:7-alpine")
		if err != nil {
			redisErr = err
			return
		}
		redisURL, redisErr = container.ConnectionString(ctx)
	})
	if redisErr != nil {
		t.Fatalf("start redis container: %v", redisErr)
	}
	return redisURL
}

// RedisClient returns a client against the shared test container with a
// flushed database, so each test starts from a clean keyspace.
func RedisClient(t *testing.T) *goredis.Client {
	t.Helper()

	opts, err := goredis.ParseURL(RedisURL(t))
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	client := goredis.NewClient(opts)
	t.Cleanup(func() { _ = client.Close() })

	if err := client.FlushDB(context.Background()).Err(); err != nil {
		t.Fatalf("flush redis: %v", err)
	}
	return client
}
