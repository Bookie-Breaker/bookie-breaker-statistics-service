package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espn"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/mlb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/nba"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/ncaabsb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/soccer"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/config"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/database"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/handler"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/repository/postgres"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/season"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/server"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/telemetry"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	setupLogger(cfg.LogLevel)
	slog.Info("starting statistics-service", "port", cfg.Port, "leagues", cfg.LeaguesEnabled)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	otelShutdown, err := telemetry.Init(ctx, cfg.OTELServiceName, cfg.OTELExporterEndpoint)
	if err != nil {
		slog.Warn("failed to init telemetry, continuing without it", "error", err)
	}

	db, err := database.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	rdb, err := cache.NewClient(ctx, cfg.RedisURL)
	if err != nil {
		slog.Error("failed to connect to Redis", "error", err)
		os.Exit(1)
	}
	defer func() { _ = rdb.Close() }()

	statsCache := cache.NewStatsCache(rdb, cache.TTLs{
		Teams:     cfg.TTLTeams,
		TeamStats: cfg.TTLTeamStats,
		Players:   cfg.TTLPlayers,
		Games:     cfg.TTLGames,
		Injuries:  cfg.TTLInjuries,
		Stale:     cfg.TTLStale,
	})

	nbaClient := nba.NewClient(nba.Options{
		BaseURL:            cfg.NBAStatsBaseURL,
		UserAgent:          cfg.NBAUserAgent,
		MinRequestInterval: cfg.NBAMinRequestInterval,
		Timeout:            cfg.NBAHTTPTimeout,
		FailureThreshold:   cfg.CircuitFailureThreshold,
		OpenDuration:       cfg.CircuitOpenDuration,
	})

	seasonFor := func(now time.Time) int {
		if cfg.CurrentSeason > 0 {
			return cfg.CurrentSeason
		}
		return season.Current(now)
	}
	seasonYear := func() int { return seasonFor(time.Now().UTC()) }

	// NBA (Wave 0), soccer (Wave 1), and baseball (Wave 2) adapters exist;
	// later waves add the remaining leagues (ADR-026).
	var soccerClient *soccer.Client
	providers := make(map[model.League]sportsdata.StatsProvider, len(cfg.LeaguesEnabled))
	for _, league := range cfg.LeaguesEnabled {
		switch league {
		case model.LeagueNBA:
			providers[league] = nba.NewProvider(nbaClient, seasonFor)
		case model.LeagueFIFAWC, model.LeagueEPL:
			if soccerClient == nil {
				soccerClient = soccer.NewClient(cfg.ESPNBaseURL, cfg.NBAHTTPTimeout)
			}
			comp, err := soccer.CompetitionForLeague(league)
			if err != nil {
				slog.Error("invalid soccer league", "league", league, "error", err)
				os.Exit(1)
			}
			providers[league] = soccer.NewProvider(soccerClient, comp)
		case model.LeagueMLB:
			providers[league] = mlb.NewProvider(mlb.NewClient(cfg.MLBStatsBaseURL, cfg.NBAHTTPTimeout))
		case model.LeagueNCAABSB:
			providers[league] = ncaabsb.NewProvider(ncaabsb.NewClient(cfg.ESPNBaseURL, cfg.NBAHTTPTimeout))
		default:
			slog.Error(fmt.Sprintf("no adapter for league %s yet", league))
			os.Exit(1)
		}
	}

	upstreams := make([]handler.UpstreamCheck, 0, len(cfg.LeaguesEnabled)+1)
	for _, league := range cfg.LeaguesEnabled {
		upstreams = append(upstreams, handler.UpstreamCheck{
			Source:    providers[league].Source(),
			Freshness: 2 * cfg.RefreshTeamStatsInterval,
		})
	}

	// Injuries run per-league through ESPN for every enabled league with an
	// injuries path (ADR-026 generalizes the ESPN client across leagues).
	espnInjuryPaths := map[model.League]string{
		model.LeagueNBA: "basketball/nba",
		model.LeagueMLB: "baseball/mlb",
	}
	espnInjuries := make(map[model.League]*espn.Client)
	if cfg.InjurySource == "espn" {
		for _, league := range cfg.LeaguesEnabled {
			path, ok := espnInjuryPaths[league]
			if !ok {
				continue
			}
			espnInjuries[league] = espn.NewClient(cfg.ESPNBaseURL, path, cfg.NBAHTTPTimeout)
		}
		if len(espnInjuries) > 0 {
			upstreams = append(upstreams, handler.UpstreamCheck{Source: "espn", Freshness: 2 * cfg.RefreshInjuriesInterval})
		}
	} else {
		slog.Info("injury source disabled; /injuries will return empty reports", "INJURY_SOURCE", cfg.InjurySource)
	}

	rawRepo := postgres.NewRawResponseRepo(db)
	publisher := pubsub.NewPublisher(rdb)
	refresh := service.NewRefreshService(providers, espnInjuries, statsCache, rawRepo, publisher)
	watcher := service.NewGameWatcher(refresh, statsCache, publisher)
	query := service.NewQueryService(statsCache, refresh, cfg.LeaguesEnabled, seasonYear)

	scheduler := service.NewScheduler(refresh, watcher, service.Intervals{
		TeamStats:  cfg.RefreshTeamStatsInterval,
		Rosters:    cfg.RefreshRostersInterval,
		Schedule:   cfg.RefreshScheduleInterval,
		Injuries:   cfg.RefreshInjuriesInterval,
		Scoreboard: cfg.ScoreboardPollInterval,
	})
	go scheduler.Start(ctx)

	e := server.New(server.Deps{
		DB:        db,
		Redis:     rdb,
		Cache:     statsCache,
		Query:     query,
		Upstreams: upstreams,
	})

	go func() {
		addr := fmt.Sprintf(":%d", cfg.Port)
		if err := e.Start(addr); err != nil {
			slog.Info("server stopped", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down gracefully")
	cancel() // stop the refresh scheduler before the HTTP server drains

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := e.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	if otelShutdown != nil {
		if err := otelShutdown(shutdownCtx); err != nil {
			slog.Error("telemetry shutdown error", "error", err)
		}
	}
}

func setupLogger(level string) {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	slog.SetDefault(slog.New(handler))
}
