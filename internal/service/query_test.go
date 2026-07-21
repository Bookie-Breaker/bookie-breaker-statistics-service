package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espn"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func TestTeamsCacheAside(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()

	// Cold cache: the query falls back to a provider refresh.
	teams, hasMore, next, err := f.query.Teams(ctx, TeamFilters{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 3 || hasMore || next != "" {
		t.Fatalf("teams = %d hasMore=%v next=%q; want all 3 on one page", len(teams), hasMore, next)
	}
	// Sorted by abbreviation: BOS, DEF, LAL.
	if teams[0].Abbreviation != "BOS" || teams[2].Abbreviation != "LAL" {
		t.Errorf("teams not sorted by abbreviation: %+v", teams)
	}
	if got := f.provider.callCount("teams"); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}

	// Warm cache: no second provider call.
	if _, _, _, err := f.query.Teams(ctx, TeamFilters{Limit: 50}); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.callCount("teams"); got != 1 {
		t.Errorf("warm read hit the provider again (calls = %d)", got)
	}
}

func TestTeamsFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()

	active := false
	tests := []struct {
		name    string
		filters TeamFilters
		wantIDs []string
	}{
		{"league match is case-insensitive", TeamFilters{Leagues: []string{"nba"}}, []string{"team-bos", "team-old", "team-lal"}},
		{"other league returns empty", TeamFilters{Leagues: []string{"MLB"}}, []string{}},
		{"conference", TeamFilters{Conference: "western"}, []string{"team-lal"}},
		{"division", TeamFilters{Division: "Atlantic"}, []string{"team-bos", "team-old"}},
		{"inactive only", TeamFilters{Active: &active}, []string{"team-old"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filters := tc.filters
			filters.Limit = 50
			teams, _, _, err := f.query.Teams(ctx, filters)
			if err != nil {
				t.Fatal(err)
			}
			if len(teams) != len(tc.wantIDs) {
				t.Fatalf("got %d teams, want %d: %+v", len(teams), len(tc.wantIDs), teams)
			}
			for i, id := range tc.wantIDs {
				if teams[i].ID != id {
					t.Errorf("teams[%d] = %s, want %s", i, teams[i].ID, id)
				}
			}
		})
	}
}

func TestTeamsPagination(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()

	page1, hasMore, next, err := f.query.Teams(ctx, TeamFilters{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 2 || !hasMore || next == "" {
		t.Fatalf("page 1 = %d items hasMore=%v next=%q", len(page1), hasMore, next)
	}

	page2, hasMore, _, err := f.query.Teams(ctx, TeamFilters{Limit: 2, Cursor: next})
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 1 || hasMore {
		t.Fatalf("page 2 = %d items hasMore=%v; want the final team", len(page2), hasMore)
	}
	if page2[0].ID == page1[0].ID || page2[0].ID == page1[1].ID {
		t.Error("page 2 repeated a page 1 team")
	}

	// A malformed cursor is a client error.
	if _, _, _, err := f.query.Teams(ctx, TeamFilters{Cursor: "garbage"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("bad cursor error = %v, want ErrBadRequest", err)
	}
}

func TestTeamsRefreshFailure(t *testing.T) {
	f := newFixture(t)
	f.provider.fail("teams", errors.New("upstream down"))

	if _, _, _, err := f.query.Teams(context.Background(), TeamFilters{Limit: 50}); err == nil {
		t.Error("cold read with failing provider should error")
	}
}

func TestTeamByID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()
	if err := f.cache.SetTeamStats(ctx, "NBA", testSeason, 0, sampleTeamStats()); err != nil {
		t.Fatal(err)
	}

	detail, err := f.query.TeamByID(ctx, "team-lal")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Name != "Los Angeles Lakers" {
		t.Errorf("detail name = %q", detail.Name)
	}
	// The current season summary is derived from cached team stats.
	if detail.SeasonSummary == nil {
		t.Fatal("season summary missing")
	}
	s := detail.SeasonSummary
	if s.Season != testSeason || s.Wins != 30 || s.Losses != 10 {
		t.Errorf("season summary record wrong: %+v", s)
	}
	if s.WinPct != 0.75 {
		t.Errorf("win pct = %v, want 0.75", s.WinPct)
	}
	if s.PointsPerGame != 115 || s.OffensiveRating != 118 || s.PointsAllowedPerGame != 108 || s.DefensiveRating != 110 {
		t.Errorf("season summary ratings wrong: %+v", s)
	}

	// A team without stats still resolves, just without a summary.
	bos, err := f.query.TeamByID(ctx, "team-bos")
	if err != nil {
		t.Fatal(err)
	}
	if bos.SeasonSummary != nil {
		t.Errorf("team without stats should have no summary: %+v", bos.SeasonSummary)
	}

	if _, err := f.query.TeamByID(ctx, "no-such-team"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id error = %v, want ErrNotFound", err)
	}
}

func TestTeamByIDRefreshFailure(t *testing.T) {
	f := newFixture(t)
	f.provider.fail("teams", errors.New("upstream down"))
	if _, err := f.query.TeamByID(context.Background(), "team-lal"); err == nil {
		t.Error("cold read with failing provider should error")
	}
}

func TestTeamStatsFullSeason(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.teamStats = sampleTeamStats()

	// Historical seasons are rejected up front.
	if _, err := f.query.TeamStats(ctx, "team-lal", testSeason-1, model.StatAll, 0); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("historical season error = %v, want ErrBadRequest", err)
	}

	// Cold cache: refresh then serve, shaped by stat_type.
	stats, err := f.query.TeamStats(ctx, "team-lal", 0, model.StatOffensive, 0)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Stats.Offensive == nil || stats.Stats.Offensive.PointsPerGame != 115 {
		t.Errorf("offensive block wrong: %+v", stats.Stats)
	}
	if stats.Stats.Defensive != nil || stats.Stats.Advanced != nil || stats.HomeAwaySplits != nil {
		t.Errorf("stat_type=offensive must drop other blocks: %+v", stats.Stats)
	}
	if got := f.provider.callCount("teamstats"); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}

	// Warm cache, passing the current season explicitly is allowed.
	if _, err := f.query.TeamStats(ctx, "team-lal", testSeason, model.StatAll, 0); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.callCount("teamstats"); got != 1 {
		t.Errorf("warm read hit the provider again (calls = %d)", got)
	}

	if _, err := f.query.TeamStats(ctx, "no-such-team", 0, model.StatAll, 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown team error = %v, want ErrNotFound", err)
	}
}

func TestTeamStatsRollingWindow(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.windowStats = map[string]model.TeamStats{
		"team-lal": {TeamID: "team-lal", Wins: 8, Losses: 2},
	}

	stats, err := f.query.TeamStats(ctx, "team-lal", 0, model.StatAll, 10)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Wins != 8 {
		t.Errorf("window stats wrong: %+v", stats)
	}
	if got := f.provider.callCount("teamstats_window"); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}

	// Second read is served from the window cache.
	if _, err := f.query.TeamStats(ctx, "team-lal", 0, model.StatAll, 10); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.callCount("teamstats_window"); got != 1 {
		t.Errorf("cached window read hit the provider again (calls = %d)", got)
	}

	// Out-of-range windows are rejected before any fetch.
	if _, err := f.query.TeamStats(ctx, "team-lal", 0, model.StatAll, windowGamesMax+1); err == nil {
		t.Error("oversized window should error")
	}

	f.provider.fail("teamstats_window", errors.New("upstream down"))
	if _, err := f.query.TeamStats(ctx, "team-lal", 0, model.StatAll, 5); err == nil {
		t.Error("window fetch failure should propagate")
	}
}

func TestPlayersCacheAsideAndFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.players = samplePlayers()
	f.provider.playerDetails = samplePlayerDetails()

	players, hasMore, _, err := f.query.Players(ctx, PlayerFilters{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(players) != 2 || hasMore {
		t.Fatalf("players = %d hasMore=%v", len(players), hasMore)
	}
	// refreshPlayersFor sorts by last name: James before Tatum.
	if players[0].LastName != "James" || players[1].LastName != "Tatum" {
		t.Errorf("players not sorted by last name: %+v", players)
	}
	if got := f.provider.callCount("players"); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}

	tests := []struct {
		name    string
		filters PlayerFilters
		wantIDs []string
	}{
		{"team", PlayerFilters{TeamID: "team-bos"}, []string{"player-jt"}},
		{"league case-insensitive", PlayerFilters{League: "nba"}, []string{"player-lbj", "player-jt"}},
		{"other league empty", PlayerFilters{League: "MLB"}, []string{}},
		{"position", PlayerFilters{Position: "f"}, []string{"player-lbj", "player-jt"}},
		{"status", PlayerFilters{Status: "active"}, []string{"player-lbj", "player-jt"}},
		{"status no match", PlayerFilters{Status: "OUT"}, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filters := tc.filters
			filters.Limit = 50
			players, _, _, err := f.query.Players(ctx, filters)
			if err != nil {
				t.Fatal(err)
			}
			if len(players) != len(tc.wantIDs) {
				t.Fatalf("got %d players, want %d: %+v", len(players), len(tc.wantIDs), players)
			}
			for i, id := range tc.wantIDs {
				if players[i].ID != id {
					t.Errorf("players[%d] = %s, want %s", i, players[i].ID, id)
				}
			}
		})
	}

	if _, _, _, err := f.query.Players(ctx, PlayerFilters{Cursor: "garbage"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("bad cursor error = %v, want ErrBadRequest", err)
	}
}

func TestPlayersRefreshFailure(t *testing.T) {
	f := newFixture(t)
	f.provider.fail("players", errors.New("upstream down"))
	if _, _, _, err := f.query.Players(context.Background(), PlayerFilters{Limit: 50}); err == nil {
		t.Error("cold read with failing provider should error")
	}
}

func TestPlayerByID(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.provider.players = samplePlayers()
	f.provider.playerDetails = samplePlayerDetails()
	f.provider.gameLog = []model.PlayerGameLine{{GameID: "game-1", Points: 32, Rebounds: 8}}

	detail, err := f.query.PlayerByID(ctx, "player-lbj", false)
	if err != nil {
		t.Fatal(err)
	}
	if detail.FirstName != "LeBron" || len(detail.GameLog) != 0 {
		t.Errorf("detail wrong without game log: %+v", detail)
	}

	// game_log=true fetches through the provider, then serves from cache.
	detail, err = f.query.PlayerByID(ctx, "player-lbj", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.GameLog) != 1 || detail.GameLog[0].Points != 32 {
		t.Errorf("game log wrong: %+v", detail.GameLog)
	}
	if got := f.provider.callCount("gamelog"); got != 1 {
		t.Fatalf("gamelog calls = %d, want 1", got)
	}
	if _, err := f.query.PlayerByID(ctx, "player-lbj", true); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.callCount("gamelog"); got != 1 {
		t.Errorf("cached game log hit the provider again (calls = %d)", got)
	}

	if _, err := f.query.PlayerByID(ctx, "no-such-player", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown id error = %v, want ErrNotFound", err)
	}
}

func TestPlayerByIDGameLogFailure(t *testing.T) {
	f := newFixture(t)
	f.provider.players = samplePlayers()
	f.provider.playerDetails = samplePlayerDetails()
	f.provider.fail("gamelog", errors.New("upstream down"))

	if _, err := f.query.PlayerByID(context.Background(), "player-lbj", true); err == nil {
		t.Error("game log fetch failure should propagate")
	}
}

func TestGamesCacheAsideAndFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f.provider.schedule = sampleGames(now)

	games, _, _, err := f.query.Games(ctx, GameFilters{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Fatalf("games = %d, want 2", len(games))
	}
	if got := f.provider.callCount("schedule"); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}

	day := now.Format("2006-01-02")
	tests := []struct {
		name    string
		filters GameFilters
		wantIDs []string
	}{
		{"league", GameFilters{League: "nba"}, []string{"game-1", "game-2"}},
		{"other league empty", GameFilters{League: "MLB"}, []string{}},
		{"date range keeps today only", GameFilters{DateFrom: day, DateTo: day}, []string{"game-1"}},
		{"team by uuid", GameFilters{Team: "team-lal"}, []string{"game-1", "game-2"}},
		{"team by abbreviation", GameFilters{Team: "bos"}, []string{"game-1", "game-2"}},
		{"status", GameFilters{Statuses: []string{"scheduled"}}, []string{"game-1", "game-2"}},
		{"status no match", GameFilters{Statuses: []string{"FINAL"}}, []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			filters := tc.filters
			filters.Limit = 50
			games, _, _, err := f.query.Games(ctx, filters)
			if err != nil {
				t.Fatal(err)
			}
			if len(games) != len(tc.wantIDs) {
				t.Fatalf("got %d games, want %d: %+v", len(games), len(tc.wantIDs), games)
			}
			for i, id := range tc.wantIDs {
				if games[i].ID != id {
					t.Errorf("games[%d] = %s, want %s", i, games[i].ID, id)
				}
			}
		})
	}

	if _, _, _, err := f.query.Games(ctx, GameFilters{DateFrom: "garbage"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("bad date error = %v, want ErrBadRequest", err)
	}
	if _, _, _, err := f.query.Games(ctx, GameFilters{Cursor: "garbage"}); !errors.Is(err, ErrBadRequest) {
		t.Errorf("bad cursor error = %v, want ErrBadRequest", err)
	}
}

// TestGamesHistoricalSeasonNotRefreshed covers the on-demand refresh guard:
// only the current season is fetched when cold, so a cold historical season
// yields an empty page rather than a provider call.
func TestGamesHistoricalSeasonNotRefreshed(t *testing.T) {
	f := newFixture(t)
	f.provider.schedule = sampleGames(time.Now().UTC())

	games, _, _, err := f.query.Games(context.Background(), GameFilters{Season: testSeason - 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 0 {
		t.Errorf("historical season should be empty, got %+v", games)
	}
	if got := f.provider.callCount("schedule"); got != 0 {
		t.Errorf("historical season must not trigger a refresh (calls = %d)", got)
	}
}

func TestGameByIDAndResult(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	games := sampleGames(now)
	games[0].Status = model.GameFinal
	games[0].Result = &model.GameResult{ID: "game-1", HomeScore: 112, AwayScore: 104, TotalScore: 216, Margin: 8}
	if err := f.cache.SetGames(ctx, "NBA", testSeason, games); err != nil {
		t.Fatal(err)
	}

	game, err := f.query.GameByID(ctx, "game-1")
	if err != nil {
		t.Fatal(err)
	}
	if game.Status != model.GameFinal {
		t.Errorf("game status = %s", game.Status)
	}

	result, err := f.query.GameResult(ctx, "game-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalScore != 216 || result.Margin != 8 {
		t.Errorf("result wrong: %+v", result)
	}

	// A scheduled game has no result yet.
	if _, err := f.query.GameResult(ctx, "game-2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("pending game result error = %v, want ErrNotFound", err)
	}
	if _, err := f.query.GameByID(ctx, "no-such-game"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown game error = %v, want ErrNotFound", err)
	}
}

func TestBoxScore(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()

	games := sampleGames(now)
	games[0].Status = model.GameFinal
	if err := f.cache.SetGames(ctx, "NBA", testSeason, games); err != nil {
		t.Fatal(err)
	}
	// Provider emits the teams in away-first order to exercise orientation.
	f.provider.boxScore = &model.BoxScore{
		HomeTeam: model.TeamBoxScore{ID: "team-bos", Abbreviation: "BOS", Score: 104},
		AwayTeam: model.TeamBoxScore{ID: "team-lal", Abbreviation: "LAL", Score: 112},
	}

	box, err := f.query.BoxScore(ctx, "game-1")
	if err != nil {
		t.Fatal(err)
	}
	if box.GameID != "game-1" || box.Sport != "BASKETBALL" || box.Status != "FINAL" {
		t.Errorf("canonical fields wrong: %+v", box)
	}
	if box.HomeTeam.ID != "team-lal" || box.HomeTeam.Score != 112 {
		t.Errorf("box score not oriented to the canonical game: %+v", box.HomeTeam)
	}

	// The FINAL box score was cached: the second read skips the provider.
	if _, err := f.query.BoxScore(ctx, "game-1"); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.callCount("boxscore"); got != 1 {
		t.Errorf("cached box score hit the provider again (calls = %d)", got)
	}

	if _, err := f.query.BoxScore(ctx, "no-such-game"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown game error = %v, want ErrNotFound", err)
	}
}

func TestBoxScoreNotSupportedMapsToNotFound(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.cache.SetGames(ctx, "NBA", testSeason, sampleGames(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	f.provider.fail("boxscore", sportsdata.ErrNotSupported)

	if _, err := f.query.BoxScore(ctx, "game-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unsupported league error = %v, want ErrNotFound", err)
	}

	f.provider.fail("boxscore", errors.New("upstream down"))
	if err := func() error { _, err := f.query.BoxScore(ctx, "game-2"); return err }(); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("provider failure = %v, want a non-404 error", err)
	}
}

func TestInjuriesFromCache(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	reports := []model.InjuryReport{
		{PlayerID: "player-lbj", TeamID: "team-lal", League: model.LeagueNBA, Status: "OUT"},
		{PlayerID: "player-jt", TeamID: "team-bos", League: model.LeagueNBA, Status: "INJURED"},
	}
	if err := f.cache.SetInjuries(ctx, "NBA", reports); err != nil {
		t.Fatal(err)
	}

	all, err := f.query.Injuries(ctx, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("injuries = %d, want 2", len(all))
	}

	byTeam, err := f.query.Injuries(ctx, "NBA", "team-lal", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(byTeam) != 1 || byTeam[0].PlayerID != "player-lbj" {
		t.Errorf("team filter wrong: %+v", byTeam)
	}

	byStatus, err := f.query.Injuries(ctx, "", "", "injured")
	if err != nil {
		t.Fatal(err)
	}
	if len(byStatus) != 1 || byStatus[0].PlayerID != "player-jt" {
		t.Errorf("status filter wrong: %+v", byStatus)
	}

	// A league outside the enabled set returns empty, not an error.
	outside, err := f.query.Injuries(ctx, "NHL", "", "")
	if err != nil || len(outside) != 0 {
		t.Errorf("outside league = %v, %v; want empty", outside, err)
	}
}

// TestInjuriesColdCacheDegradesToEmpty covers the documented best-effort
// posture: when the cache is cold and the ESPN refresh fails, the endpoint
// serves an empty report rather than an error.
func TestInjuriesColdCacheDegradesToEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := newFixtureWithInjuries(t, map[model.League]*espn.Client{
		model.LeagueNBA: espn.NewClient(srv.URL, "basketball/nba", time.Second),
	})

	injuries, err := f.query.Injuries(context.Background(), "", "", "")
	if err != nil {
		t.Fatalf("degraded read must not error: %v", err)
	}
	if len(injuries) != 0 {
		t.Errorf("degraded read should be empty, got %+v", injuries)
	}
}
