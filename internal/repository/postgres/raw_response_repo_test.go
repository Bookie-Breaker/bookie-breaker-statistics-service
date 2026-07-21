package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// TestInsertSurfacesDatabaseError covers the archival write's error path:
// Postgres being down must yield a wrapped error the caller can log (the
// refresh service treats archival as best-effort). pgxpool connects lazily,
// so a pool against a closed port constructs fine and fails on Exec. The
// successful-insert path runs against real Postgres in tests/integration.
func TestInsertSurfacesDatabaseError(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err != nil {
		t.Fatalf("build lazy pool: %v", err)
	}
	t.Cleanup(pool.Close)

	repo := NewRawResponseRepo(pool)
	err = repo.Insert(context.Background(), model.RawAPIResponse{
		Service:      "statistics-service",
		Source:       "nba_com",
		Endpoint:     "/teams",
		HTTPStatus:   200,
		ResponseBody: "{}",
		CapturedAt:   time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("insert against dead postgres should error")
	}
	if !strings.Contains(err.Error(), "insert raw api response") {
		t.Errorf("error not wrapped with context: %v", err)
	}
}
