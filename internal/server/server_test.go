package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/handler"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/tests/testutil"
)

type nopRawRepo struct{}

func (nopRawRepo) Insert(context.Context, model.RawAPIResponse) error { return nil }

// newTestServer wires the full HTTP layer over a real Redis with a
// pre-populated cache (no providers: everything serves cache-side) and a
// lazily-failing Postgres pool.
func newTestServer(t *testing.T) *echo.Echo {
	t.Helper()
	ctx := context.Background()

	rdb := testutil.RedisClient(t)
	statsCache := cache.NewStatsCache(rdb, cache.TTLs{
		Teams: time.Hour, TeamStats: time.Hour, Players: time.Hour,
		Games: time.Hour, Injuries: time.Hour, BoxScore: time.Hour, Stale: 24 * time.Hour,
	})
	if err := statsCache.SetTeams(ctx, "NBA", []model.TeamSummary{
		{ID: "team-lal", League: model.LeagueNBA, Name: "Los Angeles Lakers", Abbreviation: "LAL", Active: true},
	}); err != nil {
		t.Fatal(err)
	}

	refresh := service.NewRefreshService(
		map[model.League]sportsdata.StatsProvider{}, nil, statsCache, nopRawRepo{}, pubsub.NewPublisher(rdb),
	)
	query := service.NewQueryService(statsCache, refresh, []model.League{model.LeagueNBA}, func() int { return 2025 })

	pool, err := pgxpool.New(ctx, "postgres://u:p@127.0.0.1:1/db?connect_timeout=1")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	return New(Deps{
		DB:    pool,
		Redis: rdb,
		Cache: statsCache,
		Query: query,
		Upstreams: []handler.UpstreamCheck{
			{Source: "nba_com", Freshness: time.Hour},
		},
	})
}

// TestServerServesRegisteredRoutes covers New and registerRoutes end to
// end: a cache-backed read route, the health route, and the middleware
// chain (request id header, JSON envelope).
func TestServerServesRegisteredRoutes(t *testing.T) {
	e := newTestServer(t)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stats/teams", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("teams status = %d\n%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get(echo.HeaderXRequestID) == "" {
		t.Error("request id middleware did not stamp the response")
	}
	var envelope struct {
		Data []model.TeamSummary `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0].Abbreviation != "LAL" {
		t.Errorf("teams payload wrong: %+v", envelope.Data)
	}

	// Health is registered and reports degraded (dead Postgres) as 503.
	rec = httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stats/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d", rec.Code)
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "degraded" {
		t.Errorf("health = %q, want degraded", health.Status)
	}
}

// TestServerUnknownRoute covers the error branch of the request logger: an
// unregistered path flows through HandleError as a 404.
func TestServerUnknownRoute(t *testing.T) {
	e := newTestServer(t)

	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/stats/nope", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
