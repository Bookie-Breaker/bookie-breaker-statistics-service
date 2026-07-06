package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cursor"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// ErrNotFound is returned when an entity id is unknown.
var ErrNotFound = errors.New("not found")

// ErrBadRequest wraps client input problems (bad windows, bad cursors).
var ErrBadRequest = errors.New("bad request")

// QueryService serves the read API from the cache, falling back to a
// refresh (cache-aside) when a collection is cold.
type QueryService struct {
	cache      *cache.StatsCache
	refresh    *RefreshService
	seasonYear func() int
}

// NewQueryService creates a query service.
func NewQueryService(statsCache *cache.StatsCache, refresh *RefreshService, seasonYear func() int) *QueryService {
	return &QueryService{cache: statsCache, refresh: refresh, seasonYear: seasonYear}
}

// TeamFilters narrow the team list.
type TeamFilters struct {
	Leagues    []string
	Conference string
	Division   string
	Active     *bool
	Limit      int
	Cursor     string
}

// Teams lists teams. Only the NBA is populated in Phase 1; filters on other
// leagues return empty pages.
func (q *QueryService) Teams(ctx context.Context, f TeamFilters) ([]model.TeamSummary, bool, string, error) {
	teams, err := q.teams(ctx)
	if err != nil {
		return nil, false, "", err
	}

	filtered := make([]model.TeamSummary, 0, len(teams))
	for _, t := range teams {
		if len(f.Leagues) > 0 && !containsFold(f.Leagues, string(t.League)) {
			continue
		}
		if f.Conference != "" && !strings.EqualFold(f.Conference, t.Conference) {
			continue
		}
		if f.Division != "" && !strings.EqualFold(f.Division, t.Division) {
			continue
		}
		if f.Active != nil && t.Active != *f.Active {
			continue
		}
		filtered = append(filtered, t)
	}

	page, hasMore, next, err := cursor.Page(filtered, "teams", f.Cursor, f.Limit)
	if err != nil {
		return nil, false, "", fmt.Errorf("%w: %s", ErrBadRequest, err)
	}
	return page, hasMore, next, nil
}

// TeamByID returns team details with the current season summary attached.
func (q *QueryService) TeamByID(ctx context.Context, id string) (*model.TeamDetail, error) {
	details, ok, err := q.cache.GetTeamDetails(ctx, leagueNBA, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := q.refresh.RefreshTeams(ctx); err != nil {
			return nil, err
		}
		details, _, err = q.cache.GetTeamDetails(ctx, leagueNBA, true)
		if err != nil {
			return nil, err
		}
	}

	detail, ok := details[id]
	if !ok {
		return nil, ErrNotFound
	}

	seasonYear := q.seasonYear()
	if stats, ok, _ := q.cache.GetTeamStats(ctx, leagueNBA, seasonYear, 0, true); ok {
		if ts, ok := stats[id]; ok {
			summary := &model.SeasonSummary{
				Season: seasonYear,
				Wins:   ts.Wins,
				Losses: ts.Losses,
			}
			if games := ts.Wins + ts.Losses; games > 0 {
				summary.WinPct = float64(ts.Wins) / float64(games)
			}
			if ts.Stats.Offensive != nil {
				summary.PointsPerGame = ts.Stats.Offensive.PointsPerGame
				summary.OffensiveRating = ts.Stats.Offensive.OffensiveRating
			}
			if ts.Stats.Defensive != nil {
				summary.PointsAllowedPerGame = ts.Stats.Defensive.PointsAllowedPerGame
				summary.DefensiveRating = ts.Stats.Defensive.DefensiveRating
			}
			detail.SeasonSummary = summary
		}
	}

	return &detail, nil
}

// TeamStats returns one team's stats shaped by stat_type, optionally over a
// rolling window.
func (q *QueryService) TeamStats(ctx context.Context, teamID string, seasonParam int, statType model.StatType, window int) (*model.TeamStats, error) {
	seasonYear := q.seasonYear()
	if seasonParam > 0 && seasonParam != seasonYear {
		// Historical seasons are not cached in Phase 1.
		return nil, fmt.Errorf("%w: only the current season (%d) is available", ErrBadRequest, seasonYear)
	}

	var stats map[string]model.TeamStats
	var err error
	if window > 0 {
		stats, err = q.refresh.FetchTeamStatsWindow(ctx, model.LeagueNBA, window)
		if err != nil {
			return nil, err
		}
	} else {
		var ok bool
		stats, ok, err = q.cache.GetTeamStats(ctx, leagueNBA, seasonYear, 0, true)
		if err != nil {
			return nil, err
		}
		if !ok {
			if err := q.refresh.RefreshTeamStats(ctx); err != nil {
				return nil, err
			}
			stats, _, err = q.cache.GetTeamStats(ctx, leagueNBA, seasonYear, 0, true)
			if err != nil {
				return nil, err
			}
		}
	}

	ts, ok := stats[teamID]
	if !ok {
		return nil, ErrNotFound
	}
	shaped := shapeStatBlocks(ts, statType)
	return &shaped, nil
}

// PlayerFilters narrow the player list.
type PlayerFilters struct {
	TeamID   string
	League   string
	Position string
	Status   string
	Limit    int
	Cursor   string
}

// Players lists players.
func (q *QueryService) Players(ctx context.Context, f PlayerFilters) ([]model.PlayerSummary, bool, string, error) {
	players, err := q.players(ctx)
	if err != nil {
		return nil, false, "", err
	}

	filtered := make([]model.PlayerSummary, 0, len(players))
	for _, p := range players {
		if f.League != "" && !strings.EqualFold(f.League, string(p.League)) {
			continue
		}
		if f.TeamID != "" && p.TeamID != f.TeamID {
			continue
		}
		if f.Position != "" && !strings.EqualFold(f.Position, p.Position) {
			continue
		}
		if f.Status != "" && !strings.EqualFold(f.Status, string(p.Status)) {
			continue
		}
		filtered = append(filtered, p)
	}

	page, hasMore, next, err := cursor.Page(filtered, "players", f.Cursor, f.Limit)
	if err != nil {
		return nil, false, "", fmt.Errorf("%w: %s", ErrBadRequest, err)
	}
	return page, hasMore, next, nil
}

// PlayerByID returns player details, optionally with the season game log.
func (q *QueryService) PlayerByID(ctx context.Context, id string, gameLog bool) (*model.PlayerDetail, error) {
	details, ok, err := q.cache.GetPlayerDetails(ctx, leagueNBA, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := q.refresh.RefreshPlayers(ctx); err != nil {
			return nil, err
		}
		details, _, err = q.cache.GetPlayerDetails(ctx, leagueNBA, true)
		if err != nil {
			return nil, err
		}
	}

	detail, ok := details[id]
	if !ok {
		return nil, ErrNotFound
	}

	if gameLog {
		log, err := q.refresh.FetchGameLog(ctx, detail.League, detail)
		if err != nil {
			return nil, err
		}
		detail.GameLog = log
	}
	return &detail, nil
}

// GameFilters narrow the game list.
type GameFilters struct {
	League   string
	DateFrom string
	DateTo   string
	Team     string // canonical UUID or abbreviation
	Statuses []string
	Season   int
	Limit    int
	Cursor   string
}

// Games lists games for a season (default current).
func (q *QueryService) Games(ctx context.Context, f GameFilters) ([]model.Game, bool, string, error) {
	games, err := q.games(ctx, f.Season)
	if err != nil {
		return nil, false, "", err
	}

	dateFrom, dateTo, err := parseDateRange(f.DateFrom, f.DateTo)
	if err != nil {
		return nil, false, "", err
	}

	filtered := make([]model.Game, 0, len(games))
	for _, g := range games {
		if f.League != "" && !strings.EqualFold(f.League, string(g.League)) {
			continue
		}
		if !dateFrom.IsZero() && g.ScheduledStart.Before(dateFrom) {
			continue
		}
		if !dateTo.IsZero() && !g.ScheduledStart.Before(dateTo) {
			continue
		}
		if f.Team != "" && !gameHasTeam(g, f.Team) {
			continue
		}
		if len(f.Statuses) > 0 && !containsFold(f.Statuses, string(g.Status)) {
			continue
		}
		filtered = append(filtered, g)
	}

	page, hasMore, next, err := cursor.Page(filtered, "games", f.Cursor, f.Limit)
	if err != nil {
		return nil, false, "", fmt.Errorf("%w: %s", ErrBadRequest, err)
	}
	return page, hasMore, next, nil
}

// GameByID returns one game.
func (q *QueryService) GameByID(ctx context.Context, id string) (*model.Game, error) {
	games, err := q.games(ctx, 0)
	if err != nil {
		return nil, err
	}
	for i := range games {
		if games[i].ID == id {
			return &games[i], nil
		}
	}
	return nil, ErrNotFound
}

// GameResult returns the final result of a completed game.
func (q *QueryService) GameResult(ctx context.Context, id string) (*model.GameResult, error) {
	game, err := q.GameByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if game.Status != model.GameFinal || game.Result == nil {
		return nil, fmt.Errorf("%w: game has not completed", ErrNotFound)
	}
	return game.Result, nil
}

// Injuries lists current injury reports.
func (q *QueryService) Injuries(ctx context.Context, league, teamID, status string) ([]model.InjuryReport, error) {
	if league != "" && !strings.EqualFold(league, leagueNBA) {
		return []model.InjuryReport{}, nil
	}

	injuries, ok, err := q.cache.GetInjuries(ctx, leagueNBA, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := q.refresh.RefreshInjuries(ctx); err != nil {
			// Degrade to empty: injuries are best-effort by design.
			return []model.InjuryReport{}, nil //nolint:nilerr // documented degradation
		}
		injuries, _, _ = q.cache.GetInjuries(ctx, leagueNBA, true)
	}

	filtered := make([]model.InjuryReport, 0, len(injuries))
	for _, r := range injuries {
		if teamID != "" && r.TeamID != teamID {
			continue
		}
		if status != "" && !strings.EqualFold(status, r.Status) {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered, nil
}

func (q *QueryService) teams(ctx context.Context) ([]model.TeamSummary, error) {
	teams, ok, err := q.cache.GetTeams(ctx, leagueNBA, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := q.refresh.RefreshTeams(ctx); err != nil {
			return nil, err
		}
		teams, _, err = q.cache.GetTeams(ctx, leagueNBA, true)
		if err != nil {
			return nil, err
		}
	}
	slices.SortFunc(teams, func(a, b model.TeamSummary) int {
		return strings.Compare(a.Abbreviation, b.Abbreviation)
	})
	return teams, nil
}

func (q *QueryService) players(ctx context.Context) ([]model.PlayerSummary, error) {
	players, ok, err := q.cache.GetPlayers(ctx, leagueNBA, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		if err := q.refresh.RefreshPlayers(ctx); err != nil {
			return nil, err
		}
		players, _, err = q.cache.GetPlayers(ctx, leagueNBA, true)
		if err != nil {
			return nil, err
		}
	}
	return players, nil
}

func (q *QueryService) games(ctx context.Context, seasonParam int) ([]model.Game, error) {
	seasonYear := q.seasonYear()
	if seasonParam > 0 {
		seasonYear = seasonParam
	}

	games, ok, err := q.cache.GetGames(ctx, leagueNBA, seasonYear, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		if seasonParam > 0 && seasonParam != q.seasonYear() {
			// Only the current season is refreshed on demand.
			return []model.Game{}, nil
		}
		if err := q.refresh.RefreshSchedule(ctx); err != nil {
			return nil, err
		}
		games, _, err = q.cache.GetGames(ctx, leagueNBA, seasonYear, true)
		if err != nil {
			return nil, err
		}
	}
	return games, nil
}

func shapeStatBlocks(ts model.TeamStats, statType model.StatType) model.TeamStats {
	switch statType {
	case model.StatOffensive:
		ts.Stats.Defensive = nil
		ts.Stats.Advanced = nil
		ts.HomeAwaySplits = nil
	case model.StatDefensive:
		ts.Stats.Offensive = nil
		ts.Stats.Advanced = nil
		ts.HomeAwaySplits = nil
	case model.StatOverall:
		ts.Stats.Advanced = nil
	case model.StatAdvanced:
		ts.Stats.Offensive = nil
		ts.Stats.Defensive = nil
		ts.HomeAwaySplits = nil
	default: // all
	}
	return ts
}

func gameHasTeam(g model.Game, team string) bool {
	return g.HomeTeam.ID == team || g.AwayTeam.ID == team ||
		strings.EqualFold(g.HomeTeam.Abbreviation, team) ||
		strings.EqualFold(g.AwayTeam.Abbreviation, team)
}

func parseDateRange(from, to string) (time.Time, time.Time, error) {
	var dateFrom, dateTo time.Time
	var err error
	if from != "" {
		dateFrom, err = time.Parse("2006-01-02", from)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: date_from must be YYYY-MM-DD", ErrBadRequest)
		}
	}
	if to != "" {
		dateTo, err = time.Parse("2006-01-02", to)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: date_to must be YYYY-MM-DD", ErrBadRequest)
		}
		dateTo = dateTo.Add(24 * time.Hour) // inclusive end date
	}
	return dateFrom, dateTo, nil
}

func containsFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}
