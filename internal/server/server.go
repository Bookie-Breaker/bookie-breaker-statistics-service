package server

import (
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/contrib/instrumentation/github.com/labstack/echo/otelecho"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/handler"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
)

// Deps holds the constructed dependencies the HTTP layer needs.
type Deps struct {
	DB        *pgxpool.Pool
	Redis     *redis.Client
	Cache     *cache.StatsCache
	Query     *service.QueryService
	Upstreams []handler.UpstreamCheck
}

func New(deps Deps) *echo.Echo {
	e := echo.New()
	e.HideBanner = true

	e.Use(otelecho.Middleware("statistics-service"))
	e.Use(handler.RequestIDMiddleware())
	e.Use(middleware.Recover())
	e.Use(middleware.CORSWithConfig(middleware.CORSConfig{
		AllowOrigins: []string{"*"},
	}))
	e.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogStatus:   true,
		LogURI:      true,
		LogError:    true,
		LogMethod:   true,
		LogLatency:  true,
		HandleError: true,
		LogValuesFunc: func(_ echo.Context, v middleware.RequestLoggerValues) error {
			attrs := []slog.Attr{
				slog.String("method", v.Method),
				slog.String("uri", v.URI),
				slog.Int("status", v.Status),
				slog.Duration("latency", v.Latency),
			}
			if v.Error != nil {
				attrs = append(attrs, slog.String("error", v.Error.Error()))
			}
			slog.LogAttrs(nil, slog.LevelInfo, "request", attrs...) //nolint:staticcheck // nil context is intentional for request logging
			return nil
		},
	}))

	registerRoutes(e, deps)

	return e
}
