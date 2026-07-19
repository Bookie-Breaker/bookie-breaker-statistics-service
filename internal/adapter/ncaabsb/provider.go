package ncaabsb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceESPN is the raw_api_responses source label for ESPN's site API.
const sourceESPN = "espn"

// scheduleChunkDays sizes the ranged scoreboard chunks behind Schedule.
// ESPN caps a scoreboard response at 1000 events and a month of college
// baseball exceeds that (verified live), so the season window is walked in
// week-sized ranges.
const scheduleChunkDays = 7

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for NCAA Division I baseball
// (ADR-026). The league ships dormant (not in the LEAGUES_ENABLED defaults);
// the adapter covers teams, schedule/scoreboard, and runs-based team stats.
type Provider struct {
	client *Client
}

// NewProvider creates the NCAA baseball provider.
func NewProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return model.LeagueNCAABSB }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceESPN }

// SeasonYear derives the current season year. A college baseball season
// (February through the June College World Series) sits wholly inside one
// calendar year, so the season year is the calendar year: during the season
// it is the season in progress, and in the July–December off-season it is
// the season just completed (ESPN's standings for it stay available).
func (p *Provider) SeasonYear(now time.Time) int { return now.UTC().Year() }

// seasonWindow bounds one season's schedule: February 1 through July 1
// covers opening weekend and the College World Series finals.
func seasonWindow(seasonYear int) (start, end time.Time) {
	return time.Date(seasonYear, time.February, 1, 0, 0, 0, 0, time.UTC),
		time.Date(seasonYear, time.July, 1, 0, 0, 0, 0, time.UTC)
}

// Teams fetches and normalizes the Division I team list.
func (p *Provider) Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Teams(ctx)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}
	summaries, details := normalizeTeams(resp)
	return summaries, details, fetches, nil
}

// TeamStats builds minimal full-season team stats from the ESPN standings:
// W/L records plus runs scored/allowed per game. Rolling windows are not
// supported for college baseball.
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		return nil, nil, errors.New("rolling-window stats are unsupported for college baseball")
	}

	resp, fetch, err := p.client.Standings(ctx, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	return normalizeTeamStats(seasonYear, resp), fetches, nil
}

// Players returns empty collections: college baseball player modeling is a
// documented Phase 6 descope (ADR-026).
func (p *Provider) Players(context.Context, int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	return []model.PlayerSummary{}, map[string]model.PlayerDetail{}, nil, nil
}

// Schedule fetches the season's games by walking the season window in
// week-sized ranged scoreboard calls (ESPN truncates larger ranges at 1000
// events; verified live).
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	start, end := seasonWindow(seasonYear)

	var fetches []*sportsdata.Fetch
	var games []model.Game
	seen := make(map[string]bool)
	for from := start; !from.After(end); from = from.AddDate(0, 0, scheduleChunkDays) {
		to := from.AddDate(0, 0, scheduleChunkDays-1)
		if to.After(end) {
			to = end
		}
		resp, fetch, err := p.client.Scoreboard(ctx, from, to)
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			return nil, fetches, fmt.Errorf("fetch scoreboard %s: %w", from.Format("2006-01-02"), err)
		}
		for _, ev := range resp.Events {
			if seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true
			if game, ok := normalizeEvent(ev, seasonYear); ok {
				games = append(games, game)
			}
		}
	}
	return games, fetches, nil
}

// Scoreboard fetches one date's live statuses as neutral updates keyed by
// ESPN event id. FINAL results carry per-inning period scores and Overtime
// for extra innings (no regulation-score semantics — ADR-027).
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Scoreboard(ctx, date, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	updates := make(map[string]sportsdata.ScoreboardUpdate, len(resp.Events))
	for _, ev := range resp.Events {
		home, away, ok := homeAwayCompetitors(ev)
		if !ok {
			continue
		}
		update := sportsdata.ScoreboardUpdate{
			Status:    mapStatus(ev.Status.Type.Name, ev.Status.Type.State, ev.Status.Type.Completed),
			HomeScore: parseScore(home.Score),
			AwayScore: parseScore(away.Score),
		}
		if update.Status == model.GameFinal {
			update.Result = buildResult(ev, home, away, time.Now().UTC())
		}
		updates[ev.ID] = update
	}
	return updates, fetches, nil
}

// PlayerGameLog returns an empty log: college baseball player modeling is a
// documented Phase 6 descope (ADR-026).
func (p *Provider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	return []model.PlayerGameLine{}, nil, nil
}

// BoxScore is a tracked Phase 7 deferral: college baseball box scores
// arrive in a later wave.
func (p *Provider) BoxScore(context.Context, string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	return nil, nil, sportsdata.ErrNotSupported
}
