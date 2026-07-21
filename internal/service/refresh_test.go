package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espn"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
)

func TestRefreshTeamsCachesAndArchives(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()

	if err := f.refresh.RefreshTeams(ctx); err != nil {
		t.Fatal(err)
	}

	teams, ok, err := f.cache.GetTeams(ctx, "NBA", false)
	if err != nil || !ok || len(teams) != 3 {
		t.Fatalf("teams not cached: ok=%v err=%v len=%d", ok, err, len(teams))
	}
	details, ok, err := f.cache.GetTeamDetails(ctx, "NBA", false)
	if err != nil || !ok || details["team-lal"].Name != "Los Angeles Lakers" {
		t.Fatalf("team details not cached: ok=%v err=%v", ok, err)
	}

	// The raw upstream body was archived with its fetch metadata.
	if f.rawRepo.count() != 1 {
		t.Fatalf("archived responses = %d, want 1", f.rawRepo.count())
	}
	raw := f.rawRepo.last()
	if raw.Service != "statistics-service" || raw.Source != "fake_nba" || raw.Endpoint != "/teams" || raw.HTTPStatus != 200 {
		t.Errorf("archived record wrong: %+v", raw)
	}
}

func TestRefreshTeamsProviderErrorStillArchives(t *testing.T) {
	f := newFixture(t)
	f.provider.fail("teams", errors.New("upstream down"))

	if err := f.refresh.RefreshTeams(context.Background()); err == nil {
		t.Fatal("provider failure should propagate")
	}
	// Failed calls are still archived (the provider returns the Fetch with
	// the error).
	if f.rawRepo.count() != 1 {
		t.Errorf("archived responses = %d, want 1 even on error", f.rawRepo.count())
	}
}

func TestArchiveFailureIsNonFatal(t *testing.T) {
	f := newFixture(t)
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()
	f.rawRepo.err = errors.New("postgres down")

	// Postgres is archival-only: its failure must not block the refresh.
	if err := f.refresh.RefreshTeams(context.Background()); err != nil {
		t.Errorf("refresh must survive archive failure, got %v", err)
	}
	if _, ok, _ := f.cache.GetTeams(context.Background(), "NBA", false); !ok {
		t.Error("teams should still be cached")
	}
}

func TestRefreshTeamStatsPublishesOnChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.teamStats = sampleTeamStats()
	events := f.subscribe(t, "events:stats.updated")

	if err := f.refresh.RefreshTeamStats(ctx); err != nil {
		t.Fatal(err)
	}

	var event pubsub.StatsUpdatedEvent
	if err := json.Unmarshal([]byte(waitForEvent(t, events)), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "stats.updated" || event.League != "NBA" || event.UpdateType != "team_stats" {
		t.Errorf("event wrong: %+v", event)
	}
	if len(event.TeamIDs) != 1 || event.TeamIDs[0] != "team-lal" {
		t.Errorf("team ids wrong: %v", event.TeamIDs)
	}

	// The upstream success marker feeds health reporting.
	if _, ok, err := f.cache.GetLastSuccess(ctx, "fake_nba"); err != nil || !ok {
		t.Errorf("last success not recorded: ok=%v err=%v", ok, err)
	}

	// An identical second refresh must not re-publish.
	if err := f.refresh.RefreshTeamStats(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoEvent(t, events)

	// A data change publishes again.
	changed := sampleTeamStats()
	stats := changed["team-lal"]
	stats.Wins = 31
	changed["team-lal"] = stats
	f.provider.mu.Lock()
	f.provider.teamStats = changed
	f.provider.mu.Unlock()
	if err := f.refresh.RefreshTeamStats(ctx); err != nil {
		t.Fatal(err)
	}
	waitForEvent(t, events)
}

func TestRefreshTeamStatsProviderError(t *testing.T) {
	f := newFixture(t)
	f.provider.fail("teamstats", errors.New("upstream down"))
	if err := f.refresh.RefreshTeamStats(context.Background()); err == nil {
		t.Error("provider failure should propagate")
	}
}

func TestRefreshPlayersFoldsInjuryStatuses(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.players = samplePlayers()
	f.provider.playerDetails = samplePlayerDetails()
	// A cached injury report for LeBron must fold into his player status.
	if err := f.cache.SetInjuries(ctx, "NBA", []model.InjuryReport{
		{PlayerID: "player-lbj", Status: "OUT", Description: "knee"},
	}); err != nil {
		t.Fatal(err)
	}
	events := f.subscribe(t, "events:stats.updated")

	if err := f.refresh.RefreshPlayers(ctx); err != nil {
		t.Fatal(err)
	}

	players, ok, err := f.cache.GetPlayers(ctx, "NBA", false)
	if err != nil || !ok {
		t.Fatalf("players not cached: ok=%v err=%v", ok, err)
	}
	if players[0].LastName != "James" || players[0].Status != model.PlayerStatus("OUT") {
		t.Errorf("injury status not folded: %+v", players[0])
	}
	if players[0].InjuryDescription == nil || *players[0].InjuryDescription != "knee" {
		t.Errorf("injury description not folded: %+v", players[0].InjuryDescription)
	}
	if players[1].Status != model.PlayerActive {
		t.Errorf("uninjured player status changed: %+v", players[1])
	}

	details, _, err := f.cache.GetPlayerDetails(ctx, "NBA", false)
	if err != nil || details["player-lbj"].Status != model.PlayerStatus("OUT") {
		t.Errorf("details status not folded: %+v err=%v", details["player-lbj"], err)
	}

	var event pubsub.StatsUpdatedEvent
	if err := json.Unmarshal([]byte(waitForEvent(t, events)), &event); err != nil {
		t.Fatal(err)
	}
	if event.UpdateType != "player_stats" || event.League != "NBA" {
		t.Errorf("event wrong: %+v", event)
	}
}

func TestRefreshPlayersProviderError(t *testing.T) {
	f := newFixture(t)
	f.provider.fail("players", errors.New("upstream down"))
	if err := f.refresh.RefreshPlayers(context.Background()); err == nil {
		t.Error("provider failure should propagate")
	}
}

// TestRefreshSchedulePreservesRicherResults covers the watcher/refresh
// interplay: a schedule refresh must not clobber period scores the watcher
// already recorded.
func TestRefreshSchedulePreservesRicherResults(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	existing := sampleGames(now)
	existing[0].Status = model.GameFinal
	existing[0].Result = &model.GameResult{
		ID: "game-1", HomeScore: 112, AwayScore: 104, TotalScore: 216, Margin: 8,
		PeriodScores: []model.PeriodScore{{Period: 1, Home: 30, Away: 25}},
	}
	if err := f.cache.SetGames(ctx, "NBA", testSeason, existing); err != nil {
		t.Fatal(err)
	}

	// The provider's schedule has the same game, final but result-less, and
	// deliberately out of order to exercise sorting.
	refreshed := sampleGames(now)
	refreshed[0].Status = model.GameFinal
	refreshed[0], refreshed[1] = refreshed[1], refreshed[0]
	f.provider.schedule = refreshed

	if err := f.refresh.RefreshSchedule(ctx); err != nil {
		t.Fatal(err)
	}

	games, ok, err := f.cache.GetGames(ctx, "NBA", testSeason, false)
	if err != nil || !ok {
		t.Fatalf("games not cached: ok=%v err=%v", ok, err)
	}
	if games[0].ID != "game-1" || games[1].ID != "game-2" {
		t.Errorf("games not sorted by start time: %v, %v", games[0].ID, games[1].ID)
	}
	if games[0].Result == nil || len(games[0].Result.PeriodScores) != 1 {
		t.Errorf("richer result clobbered by refresh: %+v", games[0].Result)
	}
}

func TestRefreshScheduleProviderError(t *testing.T) {
	f := newFixture(t)
	f.provider.fail("schedule", errors.New("upstream down"))
	if err := f.refresh.RefreshSchedule(context.Background()); err == nil {
		t.Error("provider failure should propagate")
	}
}

// espnInjuriesPayload is a minimal ESPN injuries response whose team and
// athlete names match the sample fixtures, so normalization can resolve
// canonical ids.
const espnInjuriesPayload = `{
  "injuries": [
    {
      "id": "13",
      "displayName": "Los Angeles Lakers",
      "injuries": [
        {
          "status": "Out",
          "date": "2026-01-15T18:00:00Z",
          "shortComment": "Knee soreness",
          "athlete": {
            "id": "1966",
            "displayName": "LeBron James",
            "position": {"abbreviation": "F"}
          }
        }
      ]
    }
  ]
}`

func newInjuriesFixture(t *testing.T, handler http.HandlerFunc) *fixture {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return newFixtureWithInjuries(t, map[model.League]*espn.Client{
		model.LeagueNBA: espn.NewClient(srv.URL, "basketball/nba", time.Second),
	})
}

func TestRefreshInjuriesEndToEnd(t *testing.T) {
	f := newInjuriesFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(espnInjuriesPayload))
	})
	ctx := context.Background()

	// Cached teams and players give the normalizer its id mappings.
	if err := f.cache.SetTeams(ctx, "NBA", sampleTeams()); err != nil {
		t.Fatal(err)
	}
	if err := f.cache.SetPlayers(ctx, "NBA", samplePlayers()); err != nil {
		t.Fatal(err)
	}
	if err := f.cache.SetPlayerDetails(ctx, "NBA", samplePlayerDetails()); err != nil {
		t.Fatal(err)
	}
	events := f.subscribe(t, "events:stats.updated")

	if err := f.refresh.RefreshInjuries(ctx); err != nil {
		t.Fatal(err)
	}

	injuries, ok, err := f.cache.GetInjuries(ctx, "NBA", false)
	if err != nil || !ok || len(injuries) != 1 {
		t.Fatalf("injuries not cached: ok=%v err=%v got=%+v", ok, err, injuries)
	}
	report := injuries[0]
	if report.PlayerID != "player-lbj" || report.TeamID != "team-lal" || report.TeamAbbreviation != "LAL" {
		t.Errorf("ids not resolved: %+v", report)
	}
	if report.Status != "OUT" || report.Description != "Knee soreness" {
		t.Errorf("report content wrong: %+v", report)
	}

	// The change event carries the status transition.
	var event pubsub.StatsUpdatedEvent
	if err := json.Unmarshal([]byte(waitForEvent(t, events)), &event); err != nil {
		t.Fatal(err)
	}
	if event.UpdateType != "injuries" || len(event.Changes) != 1 {
		t.Fatalf("event wrong: %+v", event)
	}
	if event.Changes[0].PlayerID != "player-lbj" || event.Changes[0].New != "OUT" {
		t.Errorf("change wrong: %+v", event.Changes[0])
	}

	// Statuses were folded into the player collection.
	players, _, err := f.cache.GetPlayers(ctx, "NBA", false)
	if err != nil {
		t.Fatal(err)
	}
	if players[0].LastName != "James" || players[0].Status != model.PlayerStatus("OUT") {
		t.Errorf("player status not folded: %+v", players[0])
	}
	if _, ok, _ := f.cache.GetLastSuccess(ctx, "espn"); !ok {
		t.Error("espn last success not recorded")
	}

	// An identical second refresh publishes no event.
	if err := f.refresh.RefreshInjuries(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoEvent(t, events)
}

func TestRefreshInjuriesUpstreamError(t *testing.T) {
	f := newInjuriesFixture(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	if err := f.refresh.RefreshInjuries(context.Background()); err == nil {
		t.Error("upstream failure should propagate")
	}
	// The failed body was still archived.
	if f.rawRepo.count() != 1 {
		t.Errorf("archived responses = %d, want 1", f.rawRepo.count())
	}
}

func TestRefreshInjuriesNoClientsIsNoop(t *testing.T) {
	f := newFixture(t)
	if err := f.refresh.RefreshInjuries(context.Background()); err != nil {
		t.Errorf("no-client refresh should be a no-op, got %v", err)
	}
	if f.rawRepo.count() != 0 {
		t.Errorf("no-op refresh archived %d responses", f.rawRepo.count())
	}
}

// TestUnknownLeagueProvider covers the provider-resolution error surfaced
// through every dispatcher when a league has no configured provider.
func TestUnknownLeagueProvider(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if _, err := f.refresh.FetchTeamStatsWindow(ctx, model.LeagueMLB, 5); err == nil {
		t.Error("window fetch for unconfigured league should error")
	}
	if _, err := f.refresh.FetchGameLog(ctx, model.LeagueMLB, model.PlayerDetail{}); err == nil {
		t.Error("game log for unconfigured league should error")
	}
	if _, err := f.refresh.FetchBoxScore(ctx, &model.Game{League: model.LeagueMLB}); err == nil {
		t.Error("box score for unconfigured league should error")
	}

	// A query service enabled for a league without a provider surfaces the
	// same error on the cold path.
	q := NewQueryService(f.cache, f.refresh, []model.League{model.LeagueMLB}, func() int { return testSeason })
	if _, _, _, err := q.Teams(ctx, TeamFilters{}); err == nil {
		t.Error("cold teams read for unconfigured league should error")
	}
	if _, _, _, err := q.Players(ctx, PlayerFilters{}); err == nil {
		t.Error("cold players read for unconfigured league should error")
	}
	if _, _, _, err := q.Games(ctx, GameFilters{}); err == nil {
		t.Error("cold games read for unconfigured league should error")
	}
	if _, err := q.TeamStats(ctx, "team-x", 0, model.StatAll, 0); err == nil {
		t.Error("cold team stats read for unconfigured league should error")
	}
}

// TestFetchBoxScoreUncachedBeforeFinal covers the pre-final posture: live
// box scores are served but never cached.
func TestFetchBoxScoreUncachedBeforeFinal(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.boxScore = &model.BoxScore{
		HomeTeam: model.TeamBoxScore{ID: "team-lal", Score: 60},
		AwayTeam: model.TeamBoxScore{ID: "team-bos", Score: 58},
	}
	game := &model.Game{
		ID: "game-live", League: model.LeagueNBA, ExternalID: "ext-live", Status: model.GameInProgress,
		HomeTeam: model.TeamRef{ID: "team-lal"}, AwayTeam: model.TeamRef{ID: "team-bos"},
	}

	box, err := f.refresh.FetchBoxScore(ctx, game)
	if err != nil {
		t.Fatal(err)
	}
	if box.Status != "IN_PROGRESS" || box.GameID != "game-live" {
		t.Errorf("box score wrong: %+v", box)
	}
	if _, ok, _ := f.cache.GetBoxScore(ctx, "game-live"); ok {
		t.Error("pre-final box score must not be cached")
	}
}
