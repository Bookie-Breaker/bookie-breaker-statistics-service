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
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/repository"
)

var refreshTracer = otel.Tracer("refresh-service")

const (
	sourceESPN     = "espn"
	windowGamesMax = 82
)

// RefreshService pulls upstream data through the per-league providers,
// applies the cache-coupled steps (injury folding, sorting, result
// preservation), and writes the cache. It is the only writer of the
// canonical collections.
type RefreshService struct {
	providers map[model.League]sportsdata.StatsProvider
	ordered   []sportsdata.StatsProvider // league-sorted for deterministic loops
	espn      *espn.Client               // nil when INJURY_SOURCE=none
	cache     *cache.StatsCache
	rawRepo   repository.RawResponseRepository
	publisher *pubsub.Publisher
}

// NewRefreshService creates a refresh service. espnClient may be nil.
func NewRefreshService(
	providers map[model.League]sportsdata.StatsProvider,
	espnClient *espn.Client,
	statsCache *cache.StatsCache,
	rawRepo repository.RawResponseRepository,
	publisher *pubsub.Publisher,
) *RefreshService {
	leagues := make([]string, 0, len(providers))
	for league := range providers {
		leagues = append(leagues, string(league))
	}
	sort.Strings(leagues)
	ordered := make([]sportsdata.StatsProvider, 0, len(providers))
	for _, league := range leagues {
		ordered = append(ordered, providers[model.League(league)])
	}

	return &RefreshService{
		providers: providers,
		ordered:   ordered,
		espn:      espnClient,
		cache:     statsCache,
		rawRepo:   rawRepo,
		publisher: publisher,
	}
}

// provider resolves the configured provider for one league.
func (s *RefreshService) provider(league model.League) (sportsdata.StatsProvider, error) {
	p, ok := s.providers[league]
	if !ok {
		return nil, fmt.Errorf("no provider configured for league %s", league)
	}
	return p, nil
}

// Per-league dispatchers for cache-aside reads in the query service.

func (s *RefreshService) refreshTeamsLeague(ctx context.Context, league model.League) error {
	p, err := s.provider(league)
	if err != nil {
		return err
	}
	return s.refreshTeamsFor(ctx, p)
}

func (s *RefreshService) refreshTeamStatsLeague(ctx context.Context, league model.League) error {
	p, err := s.provider(league)
	if err != nil {
		return err
	}
	return s.refreshTeamStatsFor(ctx, p)
}

func (s *RefreshService) refreshPlayersLeague(ctx context.Context, league model.League) error {
	p, err := s.provider(league)
	if err != nil {
		return err
	}
	return s.refreshPlayersFor(ctx, p)
}

func (s *RefreshService) refreshScheduleLeague(ctx context.Context, league model.League) error {
	p, err := s.provider(league)
	if err != nil {
		return err
	}
	return s.refreshScheduleFor(ctx, p)
}

// archive persists raw upstream bodies; failures are logged, never fatal
// (Postgres is archival-only for this service).
func (s *RefreshService) archive(ctx context.Context, source string, fetches ...*sportsdata.Fetch) {
	for _, f := range fetches {
		if f == nil {
			continue
		}
		err := s.rawRepo.Insert(ctx, model.RawAPIResponse{
			Service:      "statistics-service",
			Source:       source,
			Endpoint:     f.Endpoint,
			HTTPStatus:   f.HTTPStatus,
			ResponseBody: string(f.Body),
			CapturedAt:   f.CapturedAt,
		})
		if err != nil {
			slog.Warn("failed to archive raw response", "source", source, "endpoint", f.Endpoint, "error", err)
		}
	}
}

// RefreshTeams writes the team lists and details for every enabled league.
func (s *RefreshService) RefreshTeams(ctx context.Context) error {
	for _, p := range s.ordered {
		if err := s.refreshTeamsFor(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *RefreshService) refreshTeamsFor(ctx context.Context, p sportsdata.StatsProvider) error {
	league := string(p.League())

	teams, details, fetches, err := p.Teams(ctx)
	s.archive(ctx, p.Source(), fetches...)
	if err != nil {
		return err
	}

	if err := s.cache.SetTeams(ctx, league, teams); err != nil {
		return err
	}
	return s.cache.SetTeamDetails(ctx, league, details)
}

// RefreshTeamStats fetches full-season stats for every enabled league.
func (s *RefreshService) RefreshTeamStats(ctx context.Context) error {
	ctx, span := refreshTracer.Start(ctx, "refresh.TeamStats")
	defer span.End()

	for _, p := range s.ordered {
		if err := s.refreshTeamStatsFor(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *RefreshService) refreshTeamStatsFor(ctx context.Context, p sportsdata.StatsProvider) error {
	league := string(p.League())
	seasonYear := p.SeasonYear(time.Now().UTC())

	byUUID, fetches, err := p.TeamStats(ctx, seasonYear, 0)
	s.archive(ctx, p.Source(), fetches...)
	if err != nil {
		return fmt.Errorf("refresh team stats: %w", err)
	}

	previous, _, _ := s.cache.GetTeamStats(ctx, league, seasonYear, 0, false)
	if err := s.cache.SetTeamStats(ctx, league, seasonYear, 0, byUUID); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, p.Source()); err != nil {
		slog.Warn("failed to record last success", "source", p.Source(), "error", err)
	}

	if changed(previous, byUUID) {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     league,
			UpdateType: "team_stats",
			TeamIDs:    sortedKeys(byUUID),
		})
	}
	return nil
}

// FetchTeamStatsWindow serves rolling_window requests cache-aside for one
// league.
func (s *RefreshService) FetchTeamStatsWindow(ctx context.Context, league model.League, window int) (map[string]model.TeamStats, error) {
	if window <= 0 || window > windowGamesMax {
		return nil, fmt.Errorf("rolling window must be 1-%d", windowGamesMax)
	}

	p, err := s.provider(league)
	if err != nil {
		return nil, err
	}

	seasonYear := p.SeasonYear(time.Now().UTC())
	if cached, ok, err := s.cache.GetTeamStats(ctx, string(league), seasonYear, window, false); err == nil && ok {
		return cached, nil
	}

	byUUID, fetches, err := p.TeamStats(ctx, seasonYear, window)
	s.archive(ctx, p.Source(), fetches...)
	if err != nil {
		return nil, fmt.Errorf("fetch %d-game window stats: %w", window, err)
	}

	if err := s.cache.SetTeamStats(ctx, string(league), seasonYear, window, byUUID); err != nil {
		slog.Warn("failed to cache window stats", "window", window, "error", err)
	}
	return byUUID, nil
}

// RefreshPlayers refreshes the player collections for every enabled league.
func (s *RefreshService) RefreshPlayers(ctx context.Context) error {
	ctx, span := refreshTracer.Start(ctx, "refresh.Players")
	defer span.End()

	for _, p := range s.ordered {
		if err := s.refreshPlayersFor(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *RefreshService) refreshPlayersFor(ctx context.Context, p sportsdata.StatsProvider) error {
	league := string(p.League())
	seasonYear := p.SeasonYear(time.Now().UTC())

	summaries, details, fetches, err := p.Players(ctx, seasonYear)
	s.archive(ctx, p.Source(), fetches...)
	if err != nil {
		return fmt.Errorf("refresh players: %w", err)
	}

	applyInjuryStatuses(ctx, s.cache, league, summaries, details)
	sortPlayers(summaries)

	previous, _, _ := s.cache.GetPlayers(ctx, league, false)
	if err := s.cache.SetPlayers(ctx, league, summaries); err != nil {
		return err
	}
	if err := s.cache.SetPlayerDetails(ctx, league, details); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, p.Source()); err != nil {
		slog.Warn("failed to record last success", "source", p.Source(), "error", err)
	}

	if changed(previous, summaries) {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     league,
			UpdateType: "player_stats",
		})
	}
	return nil
}

// RefreshSchedule refreshes the season schedule for every enabled league,
// preserving richer results (period scores) already recorded by the game
// watcher.
func (s *RefreshService) RefreshSchedule(ctx context.Context) error {
	ctx, span := refreshTracer.Start(ctx, "refresh.Schedule")
	defer span.End()

	for _, p := range s.ordered {
		if err := s.refreshScheduleFor(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *RefreshService) refreshScheduleFor(ctx context.Context, p sportsdata.StatsProvider) error {
	league := string(p.League())
	seasonYear := p.SeasonYear(time.Now().UTC())

	games, fetches, err := p.Schedule(ctx, seasonYear)
	s.archive(ctx, p.Source(), fetches...)
	if err != nil {
		return fmt.Errorf("refresh schedule: %w", err)
	}
	sortGames(games)

	if existing, ok, _ := s.cache.GetGames(ctx, league, seasonYear, false); ok {
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

	previous, _, _ := s.cache.GetGames(ctx, league, seasonYear, false)
	if err := s.cache.SetGames(ctx, league, seasonYear, games); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, p.Source()); err != nil {
		slog.Warn("failed to record last success", "source", p.Source(), "error", err)
	}

	if changed(previous, games) {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     league,
			UpdateType: "schedule",
		})
	}
	return nil
}

// RefreshInjuries fetches the ESPN injury report and updates player
// statuses. Injuries stay NBA-wired in Phase 6 Wave 0; a nil ESPN client
// (INJURY_SOURCE=none) or a disabled NBA league is a no-op.
func (s *RefreshService) RefreshInjuries(ctx context.Context) error {
	if s.espn == nil {
		return nil
	}
	if _, ok := s.providers[model.LeagueNBA]; !ok {
		return nil
	}
	league := string(model.LeagueNBA)
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

	teams, _, _ := s.cache.GetTeams(ctx, league, true)
	teamIDByName := make(map[string]string, len(teams))
	abbrevByName := make(map[string]string, len(teams))
	for _, t := range teams {
		key := normalizeKey(t.Name)
		teamIDByName[key] = t.ID
		abbrevByName[key] = t.Abbreviation
	}

	players, _, _ := s.cache.GetPlayers(ctx, league, true)
	playerIDByKey := make(map[string]string, len(players))
	for _, p := range players {
		playerIDByKey[normalizeKey(p.FirstName+" "+p.LastName)+"|"+p.TeamAbbreviation] = p.ID
	}

	reports := espn.Normalize(resp, model.LeagueNBA, teamIDByName, abbrevByName, playerIDByKey)

	previous, _, _ := s.cache.GetInjuries(ctx, league, false)
	if err := s.cache.SetInjuries(ctx, league, reports); err != nil {
		return err
	}
	if err := s.cache.SetLastSuccess(ctx, sourceESPN); err != nil {
		slog.Warn("failed to record last success", "source", sourceESPN, "error", err)
	}

	if changes := diffInjuries(previous, reports); len(changes) > 0 {
		s.publish(ctx, pubsub.StatsUpdatedEvent{
			League:     league,
			UpdateType: "injuries",
			Changes:    changes,
		})
	}

	// Fold statuses into the player collections so /players reflects them.
	if len(players) > 0 {
		details, _, _ := s.cache.GetPlayerDetails(ctx, league, true)
		summaries := players
		applyInjuryStatuses(ctx, s.cache, league, summaries, details)
		if err := s.cache.SetPlayers(ctx, league, summaries); err != nil {
			slog.Warn("failed to update player statuses", "error", err)
		}
		if details != nil {
			if err := s.cache.SetPlayerDetails(ctx, league, details); err != nil {
				slog.Warn("failed to update player detail statuses", "error", err)
			}
		}
	}
	return nil
}

// FetchGameLog serves game_log=true requests cache-aside for one league.
func (s *RefreshService) FetchGameLog(ctx context.Context, league model.League, player model.PlayerDetail) ([]model.PlayerGameLine, error) {
	p, err := s.provider(league)
	if err != nil {
		return nil, err
	}

	seasonYear := p.SeasonYear(time.Now().UTC())
	if cached, ok, err := s.cache.GetGameLog(ctx, player.ID, seasonYear); err == nil && ok {
		return cached, nil
	}

	log, fetches, err := p.PlayerGameLog(ctx, player, seasonYear)
	s.archive(ctx, p.Source(), fetches...)
	if err != nil {
		return nil, err
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
func applyInjuryStatuses(ctx context.Context, statsCache *cache.StatsCache, league string, summaries []model.PlayerSummary, details map[string]model.PlayerDetail) {
	injuries, ok, err := statsCache.GetInjuries(ctx, league, true)
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
