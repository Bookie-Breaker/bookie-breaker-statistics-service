// Package database tests cover NewPool's error branches directly; the happy
// path (a live Postgres reachable and pingable) is additionally exercised
// here via testcontainers-go when Docker is available, mirroring
// tests/integration/setup_test.go's skip guard so Docker-less runs still
// pass.
package database

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestNewPoolRejectsUnparseableURL(t *testing.T) {
	pool, err := NewPool(context.Background(), "not a valid dsn with spaces")
	if err == nil {
		t.Fatal("expected an error for an unparseable database URL")
	}
	if pool != nil {
		t.Error("expected a nil pool when ParseConfig fails")
	}
}

func TestNewPoolFailsOnUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	pool, err := NewPool(ctx, "postgres://user:pw@127.0.0.1:1/db")
	if err == nil {
		t.Fatal("expected an error connecting to an unreachable host")
	}
	if pool != nil {
		t.Error("expected a nil pool when Ping fails")
	}
}

func dockerAvailable(ctx context.Context) bool {
	provider, err := testcontainers.NewDockerProvider()
	if err != nil {
		return false
	}
	defer func() { _ = provider.Close() }()
	return provider.Health(ctx) == nil
}

func TestNewPoolSucceedsAgainstLivePostgres(t *testing.T) {
	ctx := context.Background()
	if !dockerAvailable(ctx) {
		t.Skip("skipping: Docker is not available")
	}

	pgContainer, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("bookiebreaker"),
		tcpostgres.WithUsername("bookiebreaker"),
		tcpostgres.WithPassword("localdev"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(pgContainer) }()

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}

	poolCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	pool, err := NewPool(poolCtx, connStr)
	if err != nil {
		t.Fatalf("NewPool unexpected error: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		t.Errorf("expected pool to be pingable after NewPool succeeded: %v", err)
	}
}
