package cbbd

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnbb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceNCAABB is the raw_api_responses source label. NCAA_BB fuses ESPN's
// site API (teams, schedule, live scoreboard) with the CollegeBasketballData
// API (season records, adjusted efficiencies, box-score aggregates); ADR-026.
const sourceNCAABB = "espn+cbbd"

// espnPathNCAABB is the ESPN site API sport/league path segment for men's
// college basketball.
const espnPathNCAABB = "basketball/mens-college-basketball"

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for NCAA Division I men's
// college basketball (ADR-026), combining the shared ESPN basketball client
// with the CBBD season-stats client.
type Provider struct {
	espn *espnbb.Client
	cbbd *Client
}

// NewProvider creates the NCAA_BB provider from the ESPN basketball client and
// a CBBD client.
func NewProvider(espn *espnbb.Client, cbbd *Client) *Provider {
	return &Provider{espn: espn, cbbd: cbbd}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return model.LeagueNCAABB }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceNCAABB }

// SeasonYear derives the current season year, identified by its starting year
// (matching the NBA convention: 2025 means the 2025-26 season). College
// basketball runs from a November tip-off through an April national
// championship, so the season year advances in November: dates from January
// through October belong to the season that started the previous calendar
// year.
func (p *Provider) SeasonYear(now time.Time) int {
	t := now.UTC()
	if t.Month() >= time.November {
		return t.Year()
	}
	return t.Year() - 1
}

// cbbdSeason converts a canonical starting-year season to the CBBD season
// value. CBBD identifies a season by its ending year (2025-26 is 2026);
// DERIVED from CBBD documented schema 2026-07-06 — validate against live API
// in the November verification session.
func cbbdSeason(seasonYear int) int { return seasonYear + 1 }

// seasonWindow bounds one season's schedule: November 1 (tip-off) through
// April 15 of the following year (covering the early-April national
// championship).
func seasonWindow(seasonYear int) (start, end time.Time) {
	return time.Date(seasonYear, time.November, 1, 0, 0, 0, 0, time.UTC),
		time.Date(seasonYear+1, time.April, 15, 0, 0, 0, 0, time.UTC)
}

// Teams fetches and normalizes the Division I team list. College teams carry
// no venue in Phase 6.
func (p *Provider) Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.espn.Teams(ctx, espnPathNCAABB)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}
	summaries, details := espnbb.NormalizeTeams(model.LeagueNCAABB, resp)
	return summaries, details, fetches, nil
}

// TeamStats builds full-season team stats from CBBD: records, the
// offensive/defensive/advanced blocks, and the adjusted efficiency margin,
// keyed to canonical ids via the ESPN team list. Rolling windows are not
// supported. The path degrades gracefully: with no CBBD key it logs once and
// returns empty stats (the league runs watcher-only off ESPN), and a CBBD
// outage or an adjusted-ratings gap degrades to the fields it can still
// source — never fatal.
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		return nil, nil, errors.New("rolling-window stats are unsupported for college basketball")
	}

	// The ESPN team list anchors CBBD's name-keyed rows to canonical ids.
	teamsResp, fetch, err := p.espn.Teams(ctx, espnPathNCAABB)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	espnByName := espnTeamIndex(teamsResp)

	if !p.cbbd.Enabled() {
		slog.Info("CBBD_API_KEY unset; NCAA_BB season stats disabled, league runs watcher-only")
		return map[string]model.TeamStats{}, fetches, nil
	}

	season := cbbdSeason(seasonYear)
	seasonStats, fetch, err := p.cbbd.SeasonStats(ctx, season)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		slog.Warn("CBBD season stats unavailable; NCAA_BB season stats empty this cycle", "season", seasonYear, "error", err)
		return map[string]model.TeamStats{}, fetches, nil
	}

	// Adjusted efficiencies are optional: a failure degrades to zero for that
	// field while the other blocks still populate (mirrors the CFBD SP+
	// fallback).
	adjusted, fetch, err := p.cbbd.AdjustedRatings(ctx, season)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		slog.Warn("CBBD adjusted ratings unavailable, adjusted_efficiency_margin stays zero", "season", seasonYear, "error", err)
		adjusted = nil
	}

	return normalizeTeamStats(seasonYear, seasonStats, adjusted, espnByName), fetches, nil
}

// Players returns empty collections: college basketball player rosters and
// stats are a documented Phase 6 descope (ADR-026).
func (p *Provider) Players(context.Context, int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	return []model.PlayerSummary{}, map[string]model.PlayerDetail{}, nil, nil
}

// Schedule fetches the season's games by walking the season window one day at
// a time (the college-basketball scoreboard has no working ranged query).
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	start, end := seasonWindow(seasonYear)
	return espnbb.SeasonGames(ctx, p.espn, model.LeagueNCAABB, espnPathNCAABB, seasonYear, start, end)
}

// Scoreboard fetches one date's live statuses as neutral updates keyed by ESPN
// event id. FINAL results carry half (plus overtime) period scores and
// Overtime for play past the second half; college basketball cannot end in a
// tie (ADR-027).
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.espn.Scoreboard(ctx, espnPathNCAABB, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	return espnbb.ScoreboardUpdates(resp, time.Now().UTC()), fetches, nil
}

// PlayerGameLog returns an empty log: college basketball player modeling is a
// documented Phase 6 descope (ADR-026).
func (p *Provider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	return []model.PlayerGameLine{}, nil, nil
}
