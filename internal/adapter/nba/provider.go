package nba

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// sourceNBA is the raw_api_responses source label for stats.nba.com.
const sourceNBA = "nba_com"

var _ sportsdata.StatsProvider = (*Provider)(nil)

// Provider implements sportsdata.StatsProvider for the NBA by wrapping the
// stats.nba.com client with this package's normalizers.
type Provider struct {
	client *Client
	season func(now time.Time) int
}

// NewProvider creates the NBA provider. season derives the season year from
// a moment in time (allowing a config override upstream).
func NewProvider(client *Client, season func(now time.Time) int) *Provider {
	return &Provider{client: client, season: season}
}

// League identifies the league this provider serves.
func (p *Provider) League() model.League { return model.LeagueNBA }

// Source is the raw_api_responses source label.
func (p *Provider) Source() string { return sourceNBA }

// SeasonYear derives the current NBA season year.
func (p *Provider) SeasonYear(now time.Time) int { return p.season(now) }

// Teams builds the canonical team list and details from the static seed.
// No upstream call.
func (p *Provider) Teams(_ context.Context) ([]model.TeamSummary, map[string]model.TeamDetail, []*sportsdata.Fetch, error) {
	teams := NormalizeTeams()

	details := make(map[string]model.TeamDetail, len(teams))
	for _, t := range StaticTeams {
		id := ids.Team(leagueNBA, t.NBAID)
		var summary model.TeamSummary
		for _, ts := range teams {
			if ts.ID == id {
				summary = ts
				break
			}
		}
		details[id] = model.TeamDetail{
			TeamSummary: summary,
			Venue: &model.VenueSummary{
				ID:    ids.Venue(leagueNBA, t.Arena),
				Name:  t.Arena,
				City:  t.ArenaCity,
				State: t.ArenaState,
			},
		}
	}
	return teams, details, nil, nil
}

// TeamStats fetches full-season stats (window == 0, with home/road splits)
// or a rolling window (window > 0, no splits) and normalizes them keyed by
// canonical team id. Split failures are logged and skipped, never fatal.
func (p *Provider) TeamStats(ctx context.Context, seasonYear, window int) (map[string]model.TeamStats, []*sportsdata.Fetch, error) {
	stats, fetches, err := p.client.LeagueTeamStats(ctx, seasonYear, window)
	if err != nil {
		return nil, fetches, err
	}

	var splits map[string]*TeamSplits
	if window == 0 {
		s, splitFetches, err := p.client.TeamLocationSplits(ctx, seasonYear)
		fetches = append(fetches, splitFetches...)
		if err != nil {
			slog.Warn("home/road splits unavailable, continuing without them", "error", err)
		} else {
			splits = s
		}
	}

	byUUID := make(map[string]model.TeamStats)
	for _, ts := range NormalizeTeamStats(stats, splits, seasonYear) {
		byUUID[ts.TeamID] = ts
	}
	return byUUID, fetches, nil
}

// Players fetches rosters (one call per team) and league-wide season stats,
// then merges them into the canonical player collections.
func (p *Provider) Players(ctx context.Context, seasonYear int) ([]model.PlayerSummary, map[string]model.PlayerDetail, []*sportsdata.Fetch, error) {
	var fetches []*sportsdata.Fetch

	rosterByTeam := make(map[string][]RosterEntry, len(StaticTeams))
	for _, team := range StaticTeams {
		roster, fetch, err := p.client.TeamRoster(ctx, team.NBAID, seasonYear)
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			return nil, nil, fetches, err
		}
		rosterByTeam[team.NBAID] = roster
	}

	seasons, fetch, err := p.client.LeaguePlayerStats(ctx, seasonYear)
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, nil, fetches, err
	}

	summaries, details := NormalizePlayers(rosterByTeam, seasons, seasonYear)
	return summaries, details, fetches, nil
}

// Schedule fetches and normalizes the season schedule.
func (p *Provider) Schedule(ctx context.Context, seasonYear int) ([]model.Game, []*sportsdata.Fetch, error) {
	scheduled, fetch, err := p.client.Schedule(ctx, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}
	return NormalizeSchedule(scheduled, seasonYear), fetches, nil
}

// Scoreboard fetches live statuses for one date as neutral updates keyed by
// NBA game id. FINAL results are built with an empty ID; the service sets it
// after matching the update to a canonical game.
func (p *Provider) Scoreboard(ctx context.Context, date time.Time) (map[string]sportsdata.ScoreboardUpdate, []*sportsdata.Fetch, error) {
	entries, fetch, err := p.client.Scoreboard(ctx, date)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	updates := make(map[string]sportsdata.ScoreboardUpdate, len(entries))
	for _, e := range entries {
		update := sportsdata.ScoreboardUpdate{
			Status:    statusFromCode(e.Status),
			HomeScore: e.HomeScore,
			AwayScore: e.AwayScore,
		}
		if e.Status == statusFinal {
			update.Result = buildResult("", e.HomeScore, e.AwayScore, e.HomePeriods, e.AwayPeriods, time.Now().UTC())
		}
		updates[e.NBAGameID] = update
	}
	return updates, fetches, nil
}

// PlayerGameLog fetches one player's per-game log for a season.
func (p *Provider) PlayerGameLog(ctx context.Context, player model.PlayerDetail, seasonYear int) ([]model.PlayerGameLine, []*sportsdata.Fetch, error) {
	nbaID := player.ExternalIDs["nba"]
	if nbaID == "" {
		return nil, nil, fmt.Errorf("player %s has no NBA external id", player.ID)
	}

	entries, fetch, err := p.client.PlayerGameLog(ctx, nbaID, seasonYear)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, fmt.Errorf("fetch game log: %w", err)
	}

	log := make([]model.PlayerGameLine, 0, len(entries))
	for _, e := range entries {
		line := model.PlayerGameLine{
			GameID:   ids.Game(leagueNBA, e.NBAGameID),
			Matchup:  e.Matchup,
			Result:   e.Result,
			Minutes:  e.MIN,
			Points:   e.PTS,
			Rebounds: e.REB,
			Assists:  e.AST,
			Steals:   e.STL,
			Blocks:   e.BLK,
		}
		if t, err := time.Parse("Jan 2, 2006", capitalizeDate(e.GameDate)); err == nil {
			line.GameDate = t
		}
		log = append(log, line)
	}
	return log, fetches, nil
}

// BoxScore fetches one game's traditional box score by NBA game id. Teams
// come back in response order; the service orients home/away against the
// canonical game.
func (p *Provider) BoxScore(ctx context.Context, gameExternalID string) (*model.BoxScore, []*sportsdata.Fetch, error) {
	players, teams, fetch, err := p.client.BoxScoreTraditional(ctx, gameExternalID)
	var fetches []*sportsdata.Fetch
	if fetch != nil {
		fetches = append(fetches, fetch)
	}
	if err != nil {
		return nil, fetches, err
	}

	box := NormalizeBoxScore(players, teams)
	if box == nil {
		return nil, fetches, fmt.Errorf("no box score data for game %s", gameExternalID)
	}
	return box, fetches, nil
}

// capitalizeDate converts "JAN 15, 2026" to "Jan 15, 2026" for time.Parse.
func capitalizeDate(d string) string {
	if len(d) < 3 {
		return d
	}
	return d[:1] + strings.ToLower(d[1:3]) + d[3:]
}
