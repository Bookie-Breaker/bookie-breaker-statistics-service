package service

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/nba"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
)

var watcherTracer = otel.Tracer("game-watcher")

// GameWatcher polls the scoreboard for dates with unfinished games and
// publishes game.completed on transitions to FINAL. It stays idle when the
// schedule shows nothing to watch (e.g. the offseason).
type GameWatcher struct {
	refresh    *RefreshService
	nba        *nba.Client
	cache      *cache.StatsCache
	publisher  *pubsub.Publisher
	seasonYear func() int
}

// NewGameWatcher creates a game watcher.
func NewGameWatcher(refresh *RefreshService, nbaClient *nba.Client, statsCache *cache.StatsCache, publisher *pubsub.Publisher, seasonYear func() int) *GameWatcher {
	return &GameWatcher{
		refresh:    refresh,
		nba:        nbaClient,
		cache:      statsCache,
		publisher:  publisher,
		seasonYear: seasonYear,
	}
}

// Tick runs one watch cycle.
func (w *GameWatcher) Tick(ctx context.Context) error {
	ctx, span := watcherTracer.Start(ctx, "watcher.Tick")
	defer span.End()

	seasonYear := w.seasonYear()
	games, ok, err := w.cache.GetGames(ctx, leagueNBA, seasonYear, true)
	if err != nil || !ok {
		return err // nothing to watch until the schedule refresh lands
	}

	dates := watchDates(games, time.Now().UTC())
	span.SetAttributes(attribute.Int("watcher.dates", len(dates)))
	if len(dates) == 0 {
		return nil
	}

	entriesByGame := make(map[string]nba.ScoreboardEntry)
	for _, date := range dates {
		entries, fetch, err := w.nba.Scoreboard(ctx, date)
		w.refresh.archiveNBA(ctx, fetch)
		if err != nil {
			return err
		}
		for _, e := range entries {
			entriesByGame[e.NBAGameID] = e
		}
	}

	updated := false
	for i := range games {
		entry, ok := entriesByGame[games[i].ExternalID]
		if !ok {
			continue
		}
		before := games[i].Status
		games[i] = nba.ApplyScoreboard(games[i], entry)
		if games[i].Status != before {
			updated = true
		}

		if before != model.GameFinal && games[i].Status == model.GameFinal {
			w.publishCompleted(ctx, games[i])
		}
	}

	if updated {
		if err := w.cache.SetGames(ctx, leagueNBA, seasonYear, games); err != nil {
			return err
		}
	}
	return nil
}

func (w *GameWatcher) publishCompleted(ctx context.Context, game model.Game) {
	// The dedup marker survives restarts so a re-observed FINAL does not
	// re-publish.
	first, err := w.cache.MarkGameCompleted(ctx, game.ID)
	if err != nil {
		slog.Warn("failed to mark game completed", "game_id", game.ID, "error", err)
		return
	}
	if !first || game.Result == nil {
		return
	}

	event := pubsub.GameCompletedEvent{
		GameID:         game.ID,
		GameExternalID: game.ExternalID,
		League:         string(game.League),
		HomeTeam:       game.HomeTeam.Abbreviation,
		AwayTeam:       game.AwayTeam.Abbreviation,
		HomeScore:      game.Result.HomeScore,
		AwayScore:      game.Result.AwayScore,
		Total:          game.Result.TotalScore,
		Margin:         game.Result.Margin,
		Overtime:       game.Result.Overtime,
	}
	if err := w.publisher.PublishGameCompleted(ctx, event); err != nil {
		slog.Warn("failed to publish game.completed", "game_id", game.ID, "error", err)
	}
}

// watchDates returns the scoreboard dates (UTC) worth polling: dates of
// games that started already (or start today) and are not yet terminal,
// looking back one day for late finishes.
func watchDates(games []model.Game, now time.Time) []time.Time {
	today := now.Truncate(24 * time.Hour)
	yesterday := today.Add(-24 * time.Hour)

	seen := make(map[string]time.Time)
	for _, g := range games {
		switch g.Status {
		case model.GameFinal, model.GameCancelled:
			continue
		default:
		}
		day := g.ScheduledStart.UTC().Truncate(24 * time.Hour)
		if day.Equal(today) || day.Equal(yesterday) {
			seen[day.Format("2006-01-02")] = day
		}
	}

	dates := make([]time.Time, 0, len(seen))
	for _, d := range seen {
		dates = append(dates, d)
	}
	return dates
}
