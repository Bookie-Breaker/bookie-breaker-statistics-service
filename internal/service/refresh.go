package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"go.opentelemetry.io/otel"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espn"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/nba"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/repository"
)

var refreshTracer = otel.Tracer("refresh-service")

const (
	leagueNBA      = string(model.LeagueNBA)
	sourceNBA      = "nba_com"
	sourceESPN     = "espn"
	windowGamesMax = 82
)

// RefreshService pulls upstream data, normalizes it, and writes the cache.
// It is the only writer of the canonical collections.
type RefreshService struct {
	nba        *nba.Client
	espn       *espn.Client // nil when INJURY_SOURCE=none
	cache      *cache.StatsCache
	rawRepo    repository.RawResponseRepository
	publisher  *pubsub.Publisher
	seasonYear func() int
}

// NewRefreshService creates a refresh service. espnClient may be nil.
func NewRefreshService(
	nbaClient *nba.Client,
	espnClient *espn.Client,
	statsCache *cache.StatsCache,
	rawRepo repository.RawResponseRepository,
	publisher *pubsub.Publisher,
	seasonYear func() int,
) *RefreshService {
	return &RefreshService{
		nba:        nbaClient,
		espn:       espnClient,
		cache:      statsCache,
		rawRepo:    rawRepo,
		publisher:  publisher,
		seasonYear: seasonYear,
	}
}

// archiveNBA persists raw upstream bodies; failures are logged, never fatal
// (Postgres is archival-only for this service).
func (s *RefreshService) archiveNBA(ctx context.Context, fetches ...*nba.Fetch) {
	for _, f := range fetches {
		if f == nil {
			continue
		}
		err := s.rawRepo.Insert(ctx, model.RawAPIResponse{
			Service:      "statistics-service",
			Source:       sourceNBA,
			Endpoint:     f.Endpoint,
			HTTPStatus:   f.HTTPStatus,
			ResponseBody: string(f.Body),
			CapturedAt:   f.CapturedAt,
		})
		if err != nil {
			slog.Warn("failed to archive raw NBA response", "endpoint", f.Endpoint, "error", err)
		}
	}
}

// RefreshTeams writes the static team list and details. No upstream call.
func (s *RefreshService) RefreshTeams(ctx context.Context) error {
	teams := nba.NormalizeTeams()
	if err := s.cache.SetTeams(ctx, leagueNBA, teams); err != nil {
		return err
	}

	details := make(map[string]model.TeamDetail, len(teams))
	for _, t := range nba.StaticTeams {
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
	return s.cache.SetTeamDetails(ctx, leagueNBA, details)
}

// RefreshTeamStats fetches full-season stats plus home/road splits.
func (s *RefreshService) RefreshTeamStats(ctx context.Context) error {
	ctx, span := refreshTracer.Start(ctx, "refresh.TeamStats")
	defer span.End()

	seasonYear := s.seasonYear()

	stats, fetches, err := s.nba.LeagueTeamStats(ctx, seasonYear, 0)
	s.archiveNBA(ctx, fetches...)
	if err != nil {
		return fmt.Errorf("refresh team stats: %w", err)
	}

	splits, splitFetches, err := s.nba.TeamLocationSplits(ctx, seasonYear)
	s.archiveNBA(ctx, splitFetches...)
	if err != nil {
		slog.Warn("home/road splits unavailable, continuing without them", "error", err)
		splits = nil
	}

	byUUID := make(map[string]model.TeamStats)
	for _, ts := range nba.NormalizeTeamStats(stats, splits, seasonYear) {
		byUUID[ts.TeamID] = ts
	}

	previous, _, _ := s.cache.GetTeamStats(ctx, leagueNBA, seasonYear, 0, false)
	if err := s.cache.SetTeamStats(ctx, leagueNBA, seasonYear, 0, byUUID); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, sourceNBA); err != nil {
		slog.Warn("failed to record last success", "source", sourceNBA, "error", err)
	}

	if changed(previous, byUUID) {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     leagueNBA,
			UpdateType: "team_stats",
			TeamIDs:    sortedKeys(byUUID),
		})
	}
	return nil
}

// FetchTeamStatsWindow serves rolling_window requests cache-aside.
func (s *RefreshService) FetchTeamStatsWindow(ctx context.Context, window int) (map[string]model.TeamStats, error) {
	if window <= 0 || window > windowGamesMax {
		return nil, fmt.Errorf("rolling window must be 1-%d", windowGamesMax)
	}

	seasonYear := s.seasonYear()
	if cached, ok, err := s.cache.GetTeamStats(ctx, leagueNBA, seasonYear, window, false); err == nil && ok {
		return cached, nil
	}

	stats, fetches, err := s.nba.LeagueTeamStats(ctx, seasonYear, window)
	s.archiveNBA(ctx, fetches...)
	if err != nil {
		return nil, fmt.Errorf("fetch %d-game window stats: %w", window, err)
	}

	byUUID := make(map[string]model.TeamStats)
	for _, ts := range nba.NormalizeTeamStats(stats, nil, seasonYear) {
		byUUID[ts.TeamID] = ts
	}
	if err := s.cache.SetTeamStats(ctx, leagueNBA, seasonYear, window, byUUID); err != nil {
		slog.Warn("failed to cache window stats", "window", window, "error", err)
	}
	return byUUID, nil
}

// RefreshPlayers fetches rosters (one call per team) and league-wide season
// stats, then merges them into the player collections.
func (s *RefreshService) RefreshPlayers(ctx context.Context) error {
	ctx, span := refreshTracer.Start(ctx, "refresh.Players")
	defer span.End()

	seasonYear := s.seasonYear()

	rosterByTeam := make(map[string][]nba.RosterEntry, len(nba.StaticTeams))
	for _, team := range nba.StaticTeams {
		roster, fetch, err := s.nba.TeamRoster(ctx, team.NBAID, seasonYear)
		s.archiveNBA(ctx, fetch)
		if err != nil {
			return fmt.Errorf("refresh players: %w", err)
		}
		rosterByTeam[team.NBAID] = roster
	}

	seasons, fetch, err := s.nba.LeaguePlayerStats(ctx, seasonYear)
	s.archiveNBA(ctx, fetch)
	if err != nil {
		return fmt.Errorf("refresh players: %w", err)
	}

	summaries, details := nba.NormalizePlayers(rosterByTeam, seasons, seasonYear)
	applyInjuryStatuses(ctx, s.cache, summaries, details)
	sortPlayers(summaries)

	previous, _, _ := s.cache.GetPlayers(ctx, leagueNBA, false)
	if err := s.cache.SetPlayers(ctx, leagueNBA, summaries); err != nil {
		return err
	}
	if err := s.cache.SetPlayerDetails(ctx, leagueNBA, details); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, sourceNBA); err != nil {
		slog.Warn("failed to record last success", "source", sourceNBA, "error", err)
	}

	if changed(previous, summaries) {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     leagueNBA,
			UpdateType: "player_stats",
		})
	}
	return nil
}

// RefreshSchedule fetches the season schedule, preserving richer results
// (period scores) already recorded by the game watcher.
func (s *RefreshService) RefreshSchedule(ctx context.Context) error {
	ctx, span := refreshTracer.Start(ctx, "refresh.Schedule")
	defer span.End()

	seasonYear := s.seasonYear()

	scheduled, fetch, err := s.nba.Schedule(ctx, seasonYear)
	s.archiveNBA(ctx, fetch)
	if err != nil {
		return fmt.Errorf("refresh schedule: %w", err)
	}

	games := nba.NormalizeSchedule(scheduled, seasonYear)
	sortGames(games)

	if existing, ok, _ := s.cache.GetGames(ctx, leagueNBA, seasonYear, false); ok {
		resultByID := make(map[string]*model.GameResult)
		for _, g := range existing {
			if g.Result != nil && len(g.Result.PeriodScores) > 0 {
				resultByID[g.ID] = g.Result
			}
		}
		for i := range games {
			if r, ok := resultByID[games[i].ID]; ok {
				games[i].Result = r
			}
		}
	}

	previous, _, _ := s.cache.GetGames(ctx, leagueNBA, seasonYear, false)
	if err := s.cache.SetGames(ctx, leagueNBA, seasonYear, games); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, sourceNBA); err != nil {
		slog.Warn("failed to record last success", "source", sourceNBA, "error", err)
	}

	if changed(previous, games) {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     leagueNBA,
			UpdateType: "schedule",
		})
	}
	return nil
}

// RefreshInjuries fetches the ESPN injury report and updates player
// statuses. A nil ESPN client (INJURY_SOURCE=none) is a no-op.
func (s *RefreshService) RefreshInjuries(ctx context.Context) error {
	if s.espn == nil {
		return nil
	}
	ctx, span := refreshTracer.Start(ctx, "refresh.Injuries")
	defer span.End()

	resp, fetch, err := s.espn.Injuries(ctx)
	if fetch != nil {
		archiveErr := s.rawRepo.Insert(ctx, model.RawAPIResponse{
			Service:      "statistics-service",
			Source:       sourceESPN,
			Endpoint:     fetch.Endpoint,
			HTTPStatus:   fetch.HTTPStatus,
			ResponseBody: string(fetch.Body),
			CapturedAt:   fetch.CapturedAt,
		})
		if archiveErr != nil {
			slog.Warn("failed to archive raw ESPN response", "error", archiveErr)
		}
	}
	if err != nil {
		return fmt.Errorf("refresh injuries: %w", err)
	}

	teams, _, _ := s.cache.GetTeams(ctx, leagueNBA, true)
	teamIDByName := make(map[string]string, len(teams))
	abbrevByName := make(map[string]string, len(teams))
	for _, t := range teams {
		key := normalizeKey(t.Name)
		teamIDByName[key] = t.ID
		abbrevByName[key] = t.Abbreviation
	}

	players, _, _ := s.cache.GetPlayers(ctx, leagueNBA, true)
	playerIDByKey := make(map[string]string, len(players))
	for _, p := range players {
		playerIDByKey[normalizeKey(p.FirstName+" "+p.LastName)+"|"+p.TeamAbbreviation] = p.ID
	}

	reports := espn.Normalize(resp, teamIDByName, abbrevByName, playerIDByKey)

	previous, _, _ := s.cache.GetInjuries(ctx, leagueNBA, false)
	if err := s.cache.SetInjuries(ctx, leagueNBA, reports); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, sourceESPN); err != nil {
		slog.Warn("failed to record last success", "source", sourceESPN, "error", err)
	}

	if changes := diffInjuries(previous, reports); len(changes) > 0 {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     leagueNBA,
			UpdateType: "injuries",
			Changes:    changes,
		})
	}

	// Fold statuses into the player collections so /players reflects them.
	if len(players) > 0 {
		details, _, _ := s.cache.GetPlayerDetails(ctx, leagueNBA, true)
		summaries := players
		applyInjuryStatuses(ctx, s.cache, summaries, details)
		if err := s.cache.SetPlayers(ctx, leagueNBA, summaries); err != nil {
			slog.Warn("failed to update player statuses", "error", err)
		}
		if details != nil {
			if err := s.cache.SetPlayerDetails(ctx, leagueNBA, details); err != nil {
				slog.Warn("failed to update player detail statuses", "error", err)
			}
		}
	}
	return nil
}

// FetchGameLog serves game_log=true requests cache-aside.
func (s *RefreshService) FetchGameLog(ctx context.Context, player model.PlayerDetail) ([]model.PlayerGameLine, error) {
	seasonYear := s.seasonYear()
	if cached, ok, err := s.cache.GetGameLog(ctx, player.ID, seasonYear); err == nil && ok {
		return cached, nil
	}

	nbaID := player.ExternalIDs["nba"]
	if nbaID == "" {
		return nil, fmt.Errorf("player %s has no NBA external id", player.ID)
	}

	entries, fetch, err := s.nba.PlayerGameLog(ctx, nbaID, seasonYear)
	s.archiveNBA(ctx, fetch)
	if err != nil {
		return nil, fmt.Errorf("fetch game log: %w", err)
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

	if err := s.cache.SetGameLog(ctx, player.ID, seasonYear, log); err != nil {
		slog.Warn("failed to cache game log", "player", player.ID, "error", err)
	}
	return log, nil
}

func (s *RefreshService) publish(ctx context.Context, event pubsub.StatsUpdatedEvent) {
	if err := s.publisher.PublishStatsUpdated(ctx, event); err != nil {
		slog.Warn("failed to publish stats.updated", "update_type", event.UpdateType, "error", err)
	}
}

// applyInjuryStatuses folds cached injury reports into player statuses.
func applyInjuryStatuses(ctx context.Context, statsCache *cache.StatsCache, summaries []model.PlayerSummary, details map[string]model.PlayerDetail) {
	injuries, ok, err := statsCache.GetInjuries(ctx, leagueNBA, true)
	if err != nil || !ok {
		return
	}
	statusByPlayer := make(map[string]model.InjuryReport, len(injuries))
	for _, inj := range injuries {
		if inj.PlayerID != "" {
			statusByPlayer[inj.PlayerID] = inj
		}
	}
	for i := range summaries {
		if inj, ok := statusByPlayer[summaries[i].ID]; ok {
			summaries[i].Status = model.PlayerStatus(inj.Status)
			if inj.Description != "" {
				desc := inj.Description
				summaries[i].InjuryDescription = &desc
			}
			if d, ok := details[summaries[i].ID]; ok {
				d.Status = summaries[i].Status
				d.InjuryDescription = summaries[i].InjuryDescription
				details[summaries[i].ID] = d
			}
		}
	}
}

func diffInjuries(old, current []model.InjuryReport) []pubsub.StatsChange {
	oldStatus := make(map[string]string, len(old))
	for _, r := range old {
		oldStatus[injuryKey(r)] = r.Status
	}
	var changes []pubsub.StatsChange
	for _, r := range current {
		if prev, ok := oldStatus[injuryKey(r)]; !ok || prev != r.Status {
			changes = append(changes, pubsub.StatsChange{
				PlayerID: r.PlayerID,
				Field:    "status",
				Old:      prev,
				New:      r.Status,
			})
		}
	}
	return changes
}

func injuryKey(r model.InjuryReport) string {
	if r.PlayerID != "" {
		return r.PlayerID
	}
	return normalizeKey(r.PlayerName) + "|" + r.TeamAbbreviation
}

func changed(old, current any) bool {
	if old == nil {
		return true
	}
	a, errA := json.Marshal(old)
	b, errB := json.Marshal(current)
	if errA != nil || errB != nil {
		return true
	}
	return !bytes.Equal(a, b)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortPlayers(players []model.PlayerSummary) {
	sort.Slice(players, func(i, j int) bool {
		a, b := players[i], players[j]
		if a.LastName != b.LastName {
			return a.LastName < b.LastName
		}
		if a.FirstName != b.FirstName {
			return a.FirstName < b.FirstName
		}
		return a.ID < b.ID
	})
}

func sortGames(games []model.Game) {
	sort.Slice(games, func(i, j int) bool {
		if !games[i].ScheduledStart.Equal(games[j].ScheduledStart) {
			return games[i].ScheduledStart.Before(games[j].ScheduledStart)
		}
		return games[i].ID < games[j].ID
	})
}

func normalizeKey(name string) string {
	fields := bytes.Fields([]byte(name))
	return string(bytes.ToLower(bytes.Join(fields, []byte(" "))))
}

// capitalizeDate converts "JAN 15, 2026" to "Jan 15, 2026" for time.Parse.
func capitalizeDate(d string) string {
	if len(d) < 3 {
		return d
	}
	return d[:1] + string(bytes.ToLower([]byte(d[1:3]))) + d[3:]
}
