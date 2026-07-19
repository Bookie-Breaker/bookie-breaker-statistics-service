package nfl

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnfb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceNFL is the raw_api_responses source label. The NFL provider fuses two
// free, keyless upstreams (ADR-026): ESPN's site API for teams, schedule, and
// the live scoreboard, and nflverse's static CSV releases for season EPA and
// turnover aggregates.
const sourceNFL = "espn+nflverse"

// espnPathNFL is the ESPN site API sport/league path segment for the NFL.
const espnPathNFL = "football/nfl"

// scheduleChunkDays sizes the ranged scoreboard chunks behind Schedule. A
// month of NFL play is ~64 games — well under ESPN's observed 500-event
// scoreboard cap — so a 30-day walk keeps the schedule fetch cheap (the whole
// 2025 season returned 286 events in one call; verified live 2026-07-06).
const scheduleChunkDays = 30

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for the NFL (ADR-026),
// combining the shared ESPN football client with the nflverse CSV client.
type Provider struct {
	espn     *espnfb.Client
	nflverse *NFLVerseClient
}

// NewProvider creates the NFL provider from the shared ESPN football client
// and an nflverse CSV client.
func NewProvider(espn *espnfb.Client, nflverse *NFLVerseClient) *Provider {
	return &Provider{espn: espn, nflverse: nflverse}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return model.LeagueNFL }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceNFL }

// SeasonYear derives the current season year, identified by its starting year.
// The NFL season runs from August preseason through a February Super Bowl, so
// the season year advances in August: January/February playoff dates still
// belong to the previous starting year, and the July off-season carries the
// season that just completed (ESPN's standings for it stay available —
// verified live 2026-07-06).
func (p *Provider) SeasonYear(now time.Time) int {
	t := now.UTC()
	if t.Month() >= time.August {
		return t.Year()
	}
	return t.Year() - 1
}

// seasonWindow bounds one season's schedule: August 1 (preseason) through
// March 1 of the following year (covering the early-February Super Bowl).
func seasonWindow(seasonYear int) (start, end time.Time) {
	return time.Date(seasonYear, time.August, 1, 0, 0, 0, 0, time.UTC),
		time.Date(seasonYear+1, time.March, 1, 0, 0, 0, 0, time.UTC)
}

// Teams fetches and normalizes the NFL team list. Football teams carry no
// venue in Phase 6.
func (p *Provider) Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.espn.Teams(ctx, espnPathNFL)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}
	summaries, details := espnfb.NormalizeTeams(model.LeagueNFL, resp)
	return summaries, details, fetches, nil
}

// TeamStats builds full-season team stats from the ESPN standings (points and
// W/L/T records) fused with nflverse season aggregates (EPA per play, turnover
// margin). Rolling windows are not supported for the NFL. The nflverse fetch
// degrades gracefully: an outage leaves the EPA and turnover fields zero while
// the points-based fields (including the drive approximation) still populate.
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		return nil, nil, errors.New("rolling-window stats are unsupported for the NFL")
	}

	standings, fetch, err := p.espn.Standings(ctx, espnPathNFL, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	records := espnfb.Records(standings)

	// nflverse EPA is optional: its outage degrades to zero EPA/turnover
	// fields, never fatal (mirrors the soccer provider's optional form).
	var aggregates map[string]*teamAggregate
	rows, nflverseFetch, err := p.nflverse.TeamWeekStats(ctx, seasonYear)
	if nflverseFetch != nil {
		fetches = append(fetches, nflverseFetch)
	}
	if err != nil {
		slog.Warn("nflverse team stats unavailable, EPA and turnover margin stay zero", "season", seasonYear, "error", err)
	} else {
		aggregates = aggregateTeamWeeks(rows)
	}

	return normalizeTeamStats(seasonYear, records, aggregates), fetches, nil
}

// Players returns empty collections: NFL player rosters and stats are a
// documented Phase 6 descope (player modeling is Phase 7; ADR-026).
func (p *Provider) Players(context.Context, int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	return []model.PlayerSummary{}, map[string]model.PlayerDetail{}, nil, nil
}

// Schedule fetches the season's games by walking the season window in
// month-sized ranged scoreboard calls.
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	start, end := seasonWindow(seasonYear)
	return espnfb.SeasonGames(ctx, p.espn, model.LeagueNFL, espnPathNFL, seasonYear, start, end, scheduleChunkDays)
}

// Scoreboard fetches one date's live statuses as neutral updates keyed by ESPN
// event id. FINAL results carry quarter (plus overtime) period scores and
// Overtime for play past the fourth quarter; a final with equal scores is a
// valid tie and flows through as-is with no regulation fields (ADR-027).
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.espn.Scoreboard(ctx, espnPathNFL, date, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	return espnfb.ScoreboardUpdates(resp, time.Now().UTC()), fetches, nil
}

// PlayerGameLog returns an empty log: NFL player modeling is a documented
// Phase 6 descope (ADR-026).
func (p *Provider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	return []model.PlayerGameLine{}, nil, nil
}

// BoxScore is a tracked Phase 7 deferral: NFL box scores arrive in a later
// wave.
func (p *Provider) BoxScore(context.Context, string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	return nil, nil, sportsdata.ErrNotSupported
}
