// Package sportsdata defines the per-league provider seam between the
// refresh/watcher services and the league-specific adapters (nba today;
// soccer, MLB, NFL, and NHL in later Phase 6 waves — see ADR-026). Providers
// own all league-specific fetch-and-normalize knowledge; the services own
// everything cache-coupled (injury folding, sorting, result preservation,
// event publication).
package sportsdata

import (
	"context"
	"errors"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// ErrNotSupported is returned by providers for surfaces their league does
// not implement yet (e.g. box scores outside NBA and soccer in Phase 7
// Wave 0). The handler layer maps it to a 404.
var ErrNotSupported = errors.New("not supported for this league")

// Fetch carries the raw response of one upstream call for archival into
// raw_api_responses.
type Fetch struct {
	Endpoint   string
	Body       []byte
	HTTPStatus int
	CapturedAt time.Time
}

// ScoreboardUpdate is one game's league-neutral live-scoreboard state.
// Result is nil unless the game is FINAL; the provider builds it (including
// period scores and overtime — league-specific knowledge stays in the
// adapter) with an empty ID, and the service sets Result.ID after matching
// the update to a canonical game.
type ScoreboardUpdate struct {
	Status    model.GameStatus
	HomeScore int
	AwayScore int
	Result    *model.GameResult
}

// StatsProvider is one league's data source. Implementations fetch upstream
// data and normalize it into canonical models, returning every raw upstream
// body as a Fetch for archival (including on error, so failed calls are
// still archived).
type StatsProvider interface {
	// League identifies the league this provider serves.
	League() model.League
	// Source is the raw_api_responses source label, e.g. "nba_com".
	Source() string
	// SeasonYear derives the current season year from a moment in time.
	SeasonYear(now time.Time) int
	// Teams returns the canonical team list and per-team details.
	Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*Fetch, error)
	// TeamStats returns team stats keyed by canonical team id. window == 0
	// means full season; window > 0 restricts to a rolling game window.
	TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*Fetch, error)
	// Players returns the canonical player list and per-player details.
	Players(ctx context.Context, seasonYear int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*Fetch, error)
	// Schedule returns the season's canonical games.
	Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*Fetch, error)
	// Scoreboard returns live game updates for one date keyed by the
	// source's external game id.
	Scoreboard(ctx context.Context, date time.Time) (map[string]ScoreboardUpdate, []*Fetch, error)
	// PlayerGameLog returns one player's per-game lines for a season.
	PlayerGameLog(ctx context.Context, player model.PlayerDetail, seasonYear int) ([]model.PlayerGameLine, []*Fetch, error)
	// BoxScore returns one game's per-player box score by the source's
	// external game id. The provider fills teams in source order with
	// canonical ids; the service orients home/away against the canonical
	// game and owns game_id/sport/status. Leagues without box-score support
	// return ErrNotSupported (tracked Phase 7 deferrals).
	BoxScore(ctx context.Context, gameExternalID string) (*model.BoxScore, []*Fetch, error)
}
