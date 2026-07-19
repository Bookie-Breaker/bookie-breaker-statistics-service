package cfbd

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnfb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceNCAAFB is the raw_api_responses source label. NCAA_FB fuses ESPN's
// site API (teams, schedule, live scoreboard) with the CollegeFootballData API
// (season records, SP+ ratings, scoring); ADR-026.
const sourceNCAAFB = "espn+cfbd"

// espnPathNCAAFB is the ESPN site API sport/league path segment for college
// football.
const espnPathNCAAFB = "football/college-football"

// scheduleChunkDays sizes the ranged scoreboard chunks behind Schedule. A
// college football Saturday alone runs ~50 FBS events, so the season window is
// walked a week at a time to stay well under ESPN's 500-event cap.
const scheduleChunkDays = 7

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for NCAA Division I FBS
// football (ADR-026), combining the shared ESPN football client with the CFBD
// season-stats client.
type Provider struct {
	espn *espnfb.Client
	cfbd *Client
}

// NewProvider creates the NCAA_FB provider from the shared ESPN football
// client and a CFBD client.
func NewProvider(espn *espnfb.Client, cfbd *Client) *Provider {
	return &Provider{espn: espn, cfbd: cfbd}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return model.LeagueNCAAFB }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceNCAAFB }

// SeasonYear derives the current season year, identified by its starting year.
// The college season runs from late-August kickoff through a mid-January
// national championship, so the season year advances in August: January
// playoff dates still belong to the previous starting year, and the summer
// off-season carries the season that just completed.
func (p *Provider) SeasonYear(now time.Time) int {
	t := now.UTC()
	if t.Month() >= time.August {
		return t.Year()
	}
	return t.Year() - 1
}

// seasonWindow bounds one season's schedule: August 1 (Week 0 / preseason)
// through February 1 of the following year (covering the January national
// championship).
func seasonWindow(seasonYear int) (start, end time.Time) {
	return time.Date(seasonYear, time.August, 1, 0, 0, 0, 0, time.UTC),
		time.Date(seasonYear+1, time.February, 1, 0, 0, 0, 0, time.UTC)
}

// Teams fetches and normalizes the FBS team list. College teams carry no venue
// in Phase 6.
func (p *Provider) Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.espn.Teams(ctx, espnPathNCAAFB)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}
	summaries, details := espnfb.NormalizeTeams(model.LeagueNCAAFB, resp)
	return summaries, details, fetches, nil
}

// TeamStats builds full-season team stats from CFBD: win/loss records, SP+
// ratings, and season scoring, keyed to canonical ids via the ESPN team list.
// Rolling windows are not supported. The path degrades gracefully: with no
// CFBD key it logs once and returns empty stats (the league runs watcher-only
// off ESPN), and a CFBD outage or an SP+/scoring gap degrades to the fields it
// can still source — never fatal.
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		return nil, nil, errors.New("rolling-window stats are unsupported for college football")
	}

	// The ESPN team list anchors CFBD's name-keyed rows to canonical ids.
	teamsResp, fetch, err := p.espn.Teams(ctx, espnPathNCAAFB)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	espnByName := espnTeamIndex(teamsResp)

	if !p.cfbd.Enabled() {
		slog.Info("CFBD_API_KEY unset; NCAA_FB season stats disabled, league runs watcher-only")
		return map[string]model.TeamStats{}, fetches, nil
	}

	records, fetch, err := p.cfbd.Records(ctx, seasonYear)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		slog.Warn("CFBD records unavailable; NCAA_FB season stats empty this cycle", "season", seasonYear, "error", err)
		return map[string]model.TeamStats{}, fetches, nil
	}

	// SP+ and scoring are optional: a failure degrades to zero for that field
	// while records still produce stats rows (mirrors the MLB bullpen
	// fallback).
	sp, fetch, err := p.cfbd.SPRatings(ctx, seasonYear)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		slog.Warn("CFBD SP+ ratings unavailable, sp_plus_rating stays zero", "season", seasonYear, "error", err)
		sp = nil
	}

	stats, fetch, err := p.cfbd.SeasonStats(ctx, seasonYear)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		slog.Warn("CFBD season stats unavailable, points stay zero", "season", seasonYear, "error", err)
		stats = nil
	}

	return normalizeTeamStats(seasonYear, records, sp, stats, espnByName), fetches, nil
}

// Players returns empty collections: college football player rosters and stats
// are a documented Phase 6 descope (ADR-026).
func (p *Provider) Players(context.Context, int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	return []model.PlayerSummary{}, map[string]model.PlayerDetail{}, nil, nil
}

// Schedule fetches the season's games by walking the season window in
// week-sized ranged scoreboard calls.
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	start, end := seasonWindow(seasonYear)
	return espnfb.SeasonGames(ctx, p.espn, model.LeagueNCAAFB, espnPathNCAAFB, seasonYear, start, end, scheduleChunkDays)
}

// Scoreboard fetches one date's live statuses as neutral updates keyed by ESPN
// event id. FINAL results carry quarter (plus overtime) period scores and
// Overtime for play past the fourth quarter; college overtime alternates
// possessions and cannot end in a tie (ADR-027).
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.espn.Scoreboard(ctx, espnPathNCAAFB, date, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	return espnfb.ScoreboardUpdates(resp, time.Now().UTC()), fetches, nil
}

// PlayerGameLog returns an empty log: college football player modeling is a
// documented Phase 6 descope (ADR-026).
func (p *Provider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	return []model.PlayerGameLine{}, nil, nil
}

// BoxScore is a tracked Phase 7 deferral: college football box scores
// arrive in a later wave.
func (p *Provider) BoxScore(context.Context, string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	return nil, nil, sportsdata.ErrNotSupported
}
