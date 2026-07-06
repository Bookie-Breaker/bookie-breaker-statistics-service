package service

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
)

var watcherTracer = otel.Tracer("game-watcher")

// GameWatcher polls each enabled league's scoreboard for dates with
// unfinished games and publishes game.completed on transitions to FINAL. It
// stays idle when the schedule shows nothing to watch (e.g. the offseason).
type GameWatcher struct {
	refresh   *RefreshService
	cache     *cache.StatsCache
	publisher *pubsub.Publisher
}

// NewGameWatcher creates a game watcher over the refresh service's
// providers.
func NewGameWatcher(refresh *RefreshService, statsCache *cache.StatsCache, publisher *pubsub.Publisher) *GameWatcher {
	return &GameWatcher{
		refresh:   refresh,
		cache:     statsCache,
		publisher: publisher,
	}
}

// Tick runs one watch cycle across every enabled league.
func (w *GameWatcher) Tick(ctx context.Context) error {
	ctx, span := watcherTracer.Start(ctx, "watcher.Tick")
	defer span.End()

	for _, p := range w.refresh.ordered {
		if err := w.tickLeague(ctx, span, p); err != nil {
			return err
		}
	}
	return nil
}

func (w *GameWatcher) tickLeague(ctx context.Context, span trace.Span, p sportsdata.StatsProvider) error {
	league := string(p.League())
	seasonYear := p.SeasonYear(time.Now().UTC())

	games, ok, err := w.cache.GetGames(ctx, league, seasonYear, true)
	if err != nil || !ok {
		return err // nothing to watch until the schedule refresh lands
	}

	dates := watchDates(games, time.Now().UTC())
	span.SetAttributes(attribute.Int("watcher.dates", len(dates)))
	if len(dates) == 0 {
		return nil
	}

	updatesByGame := make(map[string]sportsdata.ScoreboardUpdate)
	for _, date := range dates {
		updates, fetches, err := p.Scoreboard(ctx, date)
		w.refresh.archive(ctx, p.Source(), fetches...)
		if err != nil {
			return err
		}
		for id, u := range updates {
			updatesByGame[id] = u
		}
	}

	updated := false
	for i := range games {
		update, ok := updatesByGame[games[i].ExternalID]
		if !ok {
			continue
		}
		before := games[i].Status
		applyScoreboardUpdate(&games[i], update)
		if games[i].Status != before {
			updated = true
		}

		if before != model.GameFinal && games[i].Status == model.GameFinal {
			w.publishCompleted(ctx, games[i])
		}
	}

	if updated {
		if err := w.cache.SetGames(ctx, league, seasonYear, games); err != nil {
			return err
		}
	}
	return nil
}

// applyScoreboardUpdate folds a neutral scoreboard update into a canonical
// game, adopting the provider-built result on FINAL.
func applyScoreboardUpdate(game *model.Game, update sportsdata.ScoreboardUpdate) {
	game.Status = update.Status
	if update.Status != model.GameScheduled {
		home, away := update.HomeScore, update.AwayScore
		game.HomeScore = &home
		game.AwayScore = &away
	}
	if update.Status == model.GameFinal && update.Result != nil {
		update.Result.ID = game.ID
		game.Result = update.Result
	}
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
		GameID:              game.ID,
		GameExternalID:      game.ExternalID,
		League:              string(game.League),
		HomeTeam:            game.HomeTeam.Abbreviation,
		AwayTeam:            game.AwayTeam.Abbreviation,
		HomeScore:           game.Result.HomeScore,
		AwayScore:           game.Result.AwayScore,
		Total:               game.Result.TotalScore,
		Margin:              game.Result.Margin,
		Overtime:            game.Result.Overtime,
		RegulationHomeScore: game.Result.RegulationHomeScore,
		RegulationAwayScore: game.Result.RegulationAwayScore,
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
