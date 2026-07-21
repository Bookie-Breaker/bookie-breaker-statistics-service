package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/tests/testutil"
)

// deadPool builds a pool whose Ping fails: pgxpool connects lazily, so a
// valid DSN pointing at a closed port constructs fine. Postgres is
// archival-only for this service, so tests exercise the degraded posture;
// the fully-healthy branch needs a live Postgres and is covered by the
// integration suite's environment.
func deadPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err != nil {
		t.Fatalf("build lazy pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func getHealth(t *testing.T, h *HealthHandler) (*httptest.ResponseRecorder, HealthResponse) {
	t.Helper()
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/v1/stats/health", nil), rec)
	if err := h.GetHealth(c); err != nil {
		t.Fatal(err)
	}
	var resp HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("health response malformed: %v\n%s", err, rec.Body.String())
	}
	return rec, resp
}

// TestGetHealthDegradedWithoutPostgres covers the degraded posture: Redis up
// and Postgres down keeps the service serving (archival-only Postgres) but
// reports 503 degraded.
func TestGetHealthDegradedWithoutPostgres(t *testing.T) {
	rdb := testutil.RedisClient(t)
	statsCache := cache.NewStatsCache(rdb, cache.TTLs{Stale: time.Hour})

	// One fresh upstream, one that never fetched.
	if err := statsCache.SetLastSuccess(context.Background(), "nba_com"); err != nil {
		t.Fatal(err)
	}
	h := NewHealthHandler(deadPool(t), rdb, statsCache, []UpstreamCheck{
		{Source: "nba_com", Freshness: time.Hour},
		{Source: "espn", Freshness: time.Hour},
	})

	rec, resp := getHealth(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if resp.Status != "degraded" || resp.Service != "statistics-service" {
		t.Errorf("response wrong: %+v", resp)
	}
	if resp.Dependencies["redis"].Status != "healthy" {
		t.Errorf("redis dep = %+v, want healthy", resp.Dependencies["redis"])
	}
	if resp.Dependencies["postgres"].Status != "unhealthy" {
		t.Errorf("postgres dep = %+v, want unhealthy", resp.Dependencies["postgres"])
	}
	// Upstreams are informational: fresh is healthy, missing is unhealthy.
	if resp.Dependencies["nba_com"].Status != "healthy" {
		t.Errorf("nba_com dep = %+v, want healthy", resp.Dependencies["nba_com"])
	}
	if resp.Dependencies["espn"].Status != "unhealthy" {
		t.Errorf("espn dep = %+v, want unhealthy", resp.Dependencies["espn"])
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("uptime negative: %v", resp.UptimeSeconds)
	}
}

// TestGetHealthUnhealthyWithoutRedis covers the hard-down posture: Redis is
// the primary store, so losing it flips the whole service unhealthy.
func TestGetHealthUnhealthyWithoutRedis(t *testing.T) {
	deadRedis := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = deadRedis.Close() })
	statsCache := cache.NewStatsCache(deadRedis, cache.TTLs{Stale: time.Hour})

	h := NewHealthHandler(deadPool(t), deadRedis, statsCache, nil)

	rec, resp := getHealth(t, h)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if resp.Status != "unhealthy" {
		t.Errorf("status = %q, want unhealthy", resp.Status)
	}
	if resp.Dependencies["redis"].Status != "unhealthy" {
		t.Errorf("redis dep = %+v, want unhealthy", resp.Dependencies["redis"])
	}
}

// TestGetHealthStaleUpstream covers the freshness window: a recorded success
// outside the window reports unhealthy without flipping the container.
func TestGetHealthStaleUpstream(t *testing.T) {
	rdb := testutil.RedisClient(t)
	statsCache := cache.NewStatsCache(rdb, cache.TTLs{Stale: time.Hour})
	if err := statsCache.SetLastSuccess(context.Background(), "nba_com"); err != nil {
		t.Fatal(err)
	}

	// A zero freshness window makes the just-written marker already stale.
	h := NewHealthHandler(deadPool(t), rdb, statsCache, []UpstreamCheck{
		{Source: "nba_com", Freshness: 0},
	})

	_, resp := getHealth(t, h)
	if resp.Dependencies["nba_com"].Status != "unhealthy" {
		t.Errorf("stale upstream = %+v, want unhealthy", resp.Dependencies["nba_com"])
	}
	// The stale upstream alone does not decide the overall status.
	if resp.Status != "degraded" {
		t.Errorf("overall = %q, want degraded (postgres only)", resp.Status)
	}
}
