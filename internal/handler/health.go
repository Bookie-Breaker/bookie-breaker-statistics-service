package handler

import (
	"context"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
)

// UpstreamCheck describes how to report one external data source: it is
// healthy while its last successful fetch is within the freshness window.
// Health checks never call external APIs directly (they are slow and
// rate-limited); they report cached fetch status instead.
type UpstreamCheck struct {
	Source    string
	Freshness time.Duration
}

type HealthHandler struct {
	db        *pgxpool.Pool
	rdb       *redis.Client
	cache     *cache.StatsCache
	upstreams []UpstreamCheck
	startTime time.Time
}

func NewHealthHandler(db *pgxpool.Pool, rdb *redis.Client, statsCache *cache.StatsCache, upstreams []UpstreamCheck) *HealthHandler {
	return &HealthHandler{
		db:        db,
		rdb:       rdb,
		cache:     statsCache,
		upstreams: upstreams,
		startTime: time.Now(),
	}
}

type DependencyStatus struct {
	Status    string  `json:"status"`
	LatencyMs float64 `json:"latency_ms"`
}

type HealthResponse struct {
	Status        string                       `json:"status"`
	Service       string                       `json:"service"`
	Version       string                       `json:"version"`
	UptimeSeconds float64                      `json:"uptime_seconds"`
	Dependencies  map[string]*DependencyStatus `json:"dependencies"`
}

func (h *HealthHandler) GetHealth(c echo.Context) error {
	ctx, cancel := context.WithTimeout(c.Request().Context(), 3*time.Second)
	defer cancel()

	deps := make(map[string]*DependencyStatus)
	overallStatus := "healthy"

	// Redis is the primary store: unreachable means the read API cannot
	// serve anything.
	redisStart := time.Now()
	if err := h.rdb.Ping(ctx).Err(); err != nil {
		deps["redis"] = &DependencyStatus{Status: "unhealthy", LatencyMs: float64(time.Since(redisStart).Milliseconds())}
		overallStatus = "unhealthy"
	} else {
		deps["redis"] = &DependencyStatus{Status: "healthy", LatencyMs: float64(time.Since(redisStart).Milliseconds())}
	}

	// Postgres is archival-only: losing it degrades, not breaks, the service.
	pgStart := time.Now()
	if err := h.db.Ping(ctx); err != nil {
		deps["postgres"] = &DependencyStatus{Status: "unhealthy", LatencyMs: float64(time.Since(pgStart).Milliseconds())}
		if overallStatus == "healthy" {
			overallStatus = "degraded"
		}
	} else {
		deps["postgres"] = &DependencyStatus{Status: "healthy", LatencyMs: float64(time.Since(pgStart).Milliseconds())}
	}

	// Upstream sources are informational only: a stale upstream must not
	// flip the container unhealthy (data is still served from cache).
	for _, u := range h.upstreams {
		last, ok, err := h.cache.GetLastSuccess(ctx, u.Source)
		status := "unhealthy"
		if err == nil && ok && time.Since(last) < u.Freshness {
			status = "healthy"
		}
		deps[u.Source] = &DependencyStatus{Status: status}
	}

	resp := HealthResponse{
		Status:        overallStatus,
		Service:       "statistics-service",
		Version:       "0.1.0",
		UptimeSeconds: time.Since(h.startTime).Seconds(),
		Dependencies:  deps,
	}

	status := http.StatusOK
	if overallStatus != "healthy" {
		status = http.StatusServiceUnavailable
	}
	return c.JSON(status, resp)
}
