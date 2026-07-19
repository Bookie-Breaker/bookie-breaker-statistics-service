package nhl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceNHL is the raw_api_responses source label for the official NHL APIs
// (ADR-026).
const sourceNHL = "nhl_api"

// regularSeasonGameType is the stats-API game type for the regular season,
// the window team-summary season stats are scoped to.
const regularSeasonGameType = 2

// scheduleChunkDays sizes the schedule walk: the web schedule endpoint returns
// one week of games per call, so the season window is walked a week at a time.
const scheduleChunkDays = 7

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for the NHL on the official NHL
// APIs (ADR-026).
type Provider struct {
	client *Client
}

// NewProvider creates the NHL provider.
func NewProvider(client *Client) *Provider {
	return &Provider{client: client}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return model.LeagueNHL }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceNHL }

// SeasonYear derives the current season year, identified by its starting year.
// The NHL season runs from an October opening through a June Stanley Cup
// Final, so the season year advances in July: January–June dates belong to the
// season that started the previous calendar year, and the summer off-season
// carries the season that just completed.
func (p *Provider) SeasonYear(now time.Time) int {
	t := now.UTC()
	if t.Month() >= time.July {
		return t.Year()
	}
	return t.Year() - 1
}

// seasonWindow bounds one season's schedule: September 1 (preseason) through
// July 15 of the following year (covering a late June Stanley Cup Final with
// margin).
func seasonWindow(seasonYear int) (start, end time.Time) {
	return time.Date(seasonYear, time.September, 1, 0, 0, 0, 0, time.UTC),
		time.Date(seasonYear+1, time.July, 15, 0, 0, 0, 0, time.UTC)
}

// Teams fetches and normalizes the team list from the standings.
func (p *Provider) Teams(ctx context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Standings(ctx)
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

// TeamStats builds full-season HockeyStats from the team-summary endpoint,
// resolving each row's numeric team id to its tricode via the team reference
// list. Rolling windows are not supported.
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	if window > 0 {
		return nil, nil, errors.New("rolling-window stats are unsupported for NHL")
	}

	var fetches []*sportsdata.Fetch
	summary, fetch, err := p.client.TeamSummary(ctx, seasonID(seasonYear), regularSeasonGameType)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, fmt.Errorf("fetch team summary: %w", err)
	}

	ref, fetch, err := p.client.TeamRef(ctx)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, fmt.Errorf("fetch team reference: %w", err)
	}
	triByID := make(map[int]string, len(ref.Data))
	for _, r := range ref.Data {
		triByID[r.ID] = r.TriCode
	}

	return normalizeTeamStats(seasonYear, summary, triByID), fetches, nil
}

// Players returns empty collections: NHL player rosters and stats are a
// documented Phase 6 descope (ADR-026).
func (p *Provider) Players(context.Context, int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	return []model.PlayerSummary{}, map[string]model.PlayerDetail{}, nil, nil
}

// Schedule fetches the season's games by walking the season window in
// week-sized calls to the web schedule endpoint, deduplicating by game id.
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	start, end := seasonWindow(seasonYear)
	var fetches []*sportsdata.Fetch
	var games []model.Game
	seen := make(map[int]bool)
	for from := start; !from.After(end); from = from.AddDate(0, 0, scheduleChunkDays) {
		resp, fetch, err := p.client.Schedule(ctx, from)
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			return nil, fetches, fmt.Errorf("fetch schedule %s: %w", from.Format("2006-01-02"), err)
		}
		for _, day := range resp.GameWeek {
			for _, g := range day.Games {
				if seen[g.ID] {
					continue
				}
				seen[g.ID] = true
				if game, ok := normalizeGame(g); ok {
					games = append(games, game)
				}
			}
		}
	}
	return games, fetches, nil
}

// Scoreboard fetches one date's live statuses as neutral updates keyed by NHL
// game id. FINAL results carry per-period scores (from the score endpoint's
// goal detail), the final including overtime and the shootout, and Overtime
// for any game past regulation (ADR-027).
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	resp, fetch, err := p.client.Score(ctx, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	return scoreboardUpdates(resp, time.Now().UTC()), fetches, nil
}

// PlayerGameLog returns an empty log: NHL player modeling is a documented
// Phase 6 descope (ADR-026).
func (p *Provider) PlayerGameLog(context.Context, model.PlayerDetail, int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	return []model.PlayerGameLine{}, nil, nil
}

// BoxScore is a tracked Phase 7 deferral: NHL box scores arrive in a later
// wave.
func (p *Provider) BoxScore(context.Context, string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	return nil, nil, sportsdata.ErrNotSupported
}
