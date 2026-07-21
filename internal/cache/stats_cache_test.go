package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/tests/testutil"
)

func testTTLs() TTLs {
	return TTLs{
		Teams:     time.Hour,
		TeamStats: time.Hour,
		Players:   time.Hour,
		Games:     time.Hour,
		Injuries:  time.Hour,
		BoxScore:  time.Hour,
		Stale:     24 * time.Hour,
	}
}

func newTestCache(t *testing.T) (*StatsCache, *redis.Client) {
	t.Helper()
	rdb := testutil.RedisClient(t)
	return NewStatsCache(rdb, testTTLs()), rdb
}

func TestTeamsRoundTrip(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	// Cold cache: miss, no error.
	if _, ok, err := c.GetTeams(ctx, "NBA", false); ok || err != nil {
		t.Fatalf("cold get = %v, %v; want miss", ok, err)
	}

	teams := []model.TeamSummary{{ID: "team-1", Abbreviation: "LAL", League: model.LeagueNBA}}
	if err := c.SetTeams(ctx, "NBA", teams); err != nil {
		t.Fatal(err)
	}

	got, ok, err := c.GetTeams(ctx, "NBA", false)
	if err != nil || !ok {
		t.Fatalf("get after set = %v, %v", ok, err)
	}
	if len(got) != 1 || got[0].ID != "team-1" || got[0].Abbreviation != "LAL" {
		t.Errorf("teams round trip lost data: %+v", got)
	}

	// A different league key is independent.
	if _, ok, _ := c.GetTeams(ctx, "MLB", false); ok {
		t.Error("MLB key should be a miss")
	}
}

// TestStaleMirror covers the stale-serving contract: every set writes a
// ":stale" mirror that get falls back to only when allowStale is true.
func TestStaleMirror(t *testing.T) {
	c, rdb := newTestCache(t)
	ctx := context.Background()

	teams := []model.TeamSummary{{ID: "team-1", Abbreviation: "BOS"}}
	if err := c.SetTeams(ctx, "NBA", teams); err != nil {
		t.Fatal(err)
	}
	// Simulate the primary key expiring while the stale mirror survives.
	if err := rdb.Del(ctx, "stats:teams:NBA").Err(); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := c.GetTeams(ctx, "NBA", false); ok || err != nil {
		t.Fatalf("strict get = %v, %v; want miss without stale fallback", ok, err)
	}

	got, ok, err := c.GetTeams(ctx, "NBA", true)
	if err != nil || !ok {
		t.Fatalf("stale get = %v, %v; want hit from mirror", ok, err)
	}
	if got[0].Abbreviation != "BOS" {
		t.Errorf("stale mirror lost data: %+v", got)
	}
}

func TestGetCorruptPayloadReturnsError(t *testing.T) {
	c, rdb := newTestCache(t)
	ctx := context.Background()

	if err := rdb.Set(ctx, "stats:teams:NBA", "{not json", time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.GetTeams(ctx, "NBA", false); err == nil || ok {
		t.Errorf("corrupt payload should error, got ok=%v err=%v", ok, err)
	}
}

func TestRedisDownReturnsErrors(t *testing.T) {
	rdb := testutil.RedisClient(t)
	c := NewStatsCache(rdb, testTTLs())
	ctx := context.Background()

	// Closing the client makes every command fail: set and get must surface
	// the error rather than reporting a miss.
	if err := rdb.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTeams(ctx, "NBA", nil); err == nil {
		t.Error("set on closed client should error")
	}
	if _, ok, err := c.GetTeams(ctx, "NBA", false); err == nil || ok {
		t.Errorf("get on closed client should error, got ok=%v err=%v", ok, err)
	}
	if err := c.SetLastSuccess(ctx, "nba_com"); err == nil {
		t.Error("SetLastSuccess on closed client should error")
	}
	if _, _, err := c.GetLastSuccess(ctx, "nba_com"); err == nil {
		t.Error("GetLastSuccess on closed client should error")
	}
}

func TestTeamDetailsRoundTrip(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	details := map[string]model.TeamDetail{
		"team-1": {TeamSummary: model.TeamSummary{ID: "team-1", Name: "Lakers"}},
	}
	if err := c.SetTeamDetails(ctx, "NBA", details); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetTeamDetails(ctx, "NBA", false)
	if err != nil || !ok || got["team-1"].Name != "Lakers" {
		t.Errorf("team details round trip wrong: ok=%v err=%v got=%+v", ok, err, got)
	}
}

// TestTeamStatsWindowKeys asserts full-season and rolling-window stats live
// under distinct keys and do not shadow each other.
func TestTeamStatsWindowKeys(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	full := map[string]model.TeamStats{"team-1": {Wins: 50}}
	windowed := map[string]model.TeamStats{"team-1": {Wins: 8}}
	if err := c.SetTeamStats(ctx, "NBA", 2025, 0, full); err != nil {
		t.Fatal(err)
	}
	if err := c.SetTeamStats(ctx, "NBA", 2025, 10, windowed); err != nil {
		t.Fatal(err)
	}

	gotFull, ok, err := c.GetTeamStats(ctx, "NBA", 2025, 0, false)
	if err != nil || !ok || gotFull["team-1"].Wins != 50 {
		t.Errorf("full-season stats wrong: ok=%v err=%v got=%+v", ok, err, gotFull)
	}
	gotWin, ok, err := c.GetTeamStats(ctx, "NBA", 2025, 10, false)
	if err != nil || !ok || gotWin["team-1"].Wins != 8 {
		t.Errorf("windowed stats wrong: ok=%v err=%v got=%+v", ok, err, gotWin)
	}
	if _, ok, _ := c.GetTeamStats(ctx, "NBA", 2024, 0, false); ok {
		t.Error("other season should be a miss")
	}
}

func TestPlayersAndDetailsRoundTrip(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	players := []model.PlayerSummary{{ID: "p-1", LastName: "James"}}
	if err := c.SetPlayers(ctx, "NBA", players); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetPlayers(ctx, "NBA", false)
	if err != nil || !ok || got[0].LastName != "James" {
		t.Errorf("players round trip wrong: ok=%v err=%v got=%+v", ok, err, got)
	}

	details := map[string]model.PlayerDetail{"p-1": {PlayerSummary: model.PlayerSummary{ID: "p-1"}}}
	if err := c.SetPlayerDetails(ctx, "NBA", details); err != nil {
		t.Fatal(err)
	}
	gotD, ok, err := c.GetPlayerDetails(ctx, "NBA", false)
	if err != nil || !ok || gotD["p-1"].ID != "p-1" {
		t.Errorf("player details round trip wrong: ok=%v err=%v got=%+v", ok, err, gotD)
	}
}

func TestGamesRoundTrip(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	games := []model.Game{{ID: "g-1", League: model.LeagueNBA, Status: model.GameScheduled}}
	if err := c.SetGames(ctx, "NBA", 2025, games); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetGames(ctx, "NBA", 2025, false)
	if err != nil || !ok || got[0].ID != "g-1" {
		t.Errorf("games round trip wrong: ok=%v err=%v got=%+v", ok, err, got)
	}
	if _, ok, _ := c.GetGames(ctx, "NBA", 2024, false); ok {
		t.Error("other season should be a miss")
	}
}

func TestInjuriesRoundTrip(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	injuries := []model.InjuryReport{{PlayerID: "p-1", Status: "OUT"}}
	if err := c.SetInjuries(ctx, "NBA", injuries); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetInjuries(ctx, "NBA", false)
	if err != nil || !ok || got[0].Status != "OUT" {
		t.Errorf("injuries round trip wrong: ok=%v err=%v got=%+v", ok, err, got)
	}
}

// TestGameLogNoStaleFallback covers the per-entity contract: game logs are
// fetch-on-demand and never served from the stale mirror.
func TestGameLogNoStaleFallback(t *testing.T) {
	c, rdb := newTestCache(t)
	ctx := context.Background()

	log := []model.PlayerGameLine{{GameID: "g-1", Points: 30}}
	if err := c.SetGameLog(ctx, "p-1", 2025, log); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetGameLog(ctx, "p-1", 2025)
	if err != nil || !ok || got[0].Points != 30 {
		t.Errorf("game log round trip wrong: ok=%v err=%v got=%+v", ok, err, got)
	}

	// Even with the stale mirror present, deleting the primary key is a miss.
	if err := rdb.Del(ctx, "stats:gamelog:p-1:2025").Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.GetGameLog(ctx, "p-1", 2025); ok {
		t.Error("game logs must not fall back to the stale mirror")
	}
}

func TestBoxScoreRoundTrip(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	// Miss returns a nil pointer, not a zero-value struct.
	box, ok, err := c.GetBoxScore(ctx, "g-1")
	if box != nil || ok || err != nil {
		t.Fatalf("cold box score = %v, %v, %v; want nil miss", box, ok, err)
	}

	if err := c.SetBoxScore(ctx, "g-1", &model.BoxScore{GameID: "g-1", Status: "FINAL"}); err != nil {
		t.Fatal(err)
	}
	box, ok, err = c.GetBoxScore(ctx, "g-1")
	if err != nil || !ok || box == nil || box.GameID != "g-1" || box.Status != "FINAL" {
		t.Errorf("box score round trip wrong: ok=%v err=%v got=%+v", ok, err, box)
	}
}

// TestMarkGameCompleted covers the publish dedup marker: only the first call
// per game reports true.
func TestMarkGameCompleted(t *testing.T) {
	c, _ := newTestCache(t)
	ctx := context.Background()

	first, err := c.MarkGameCompleted(ctx, "g-1")
	if err != nil || !first {
		t.Fatalf("first mark = %v, %v; want true", first, err)
	}
	again, err := c.MarkGameCompleted(ctx, "g-1")
	if err != nil || again {
		t.Errorf("second mark = %v, %v; want false (already published)", again, err)
	}
	other, err := c.MarkGameCompleted(ctx, "g-2")
	if err != nil || !other {
		t.Errorf("different game mark = %v, %v; want true", other, err)
	}
}

func TestLastSuccess(t *testing.T) {
	c, rdb := newTestCache(t)
	ctx := context.Background()

	// Missing marker: no success recorded, no error.
	if _, ok, err := c.GetLastSuccess(ctx, "nba_com"); ok || err != nil {
		t.Fatalf("missing marker = %v, %v; want miss", ok, err)
	}

	before := time.Now().UTC().Add(-time.Second)
	if err := c.SetLastSuccess(ctx, "nba_com"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.GetLastSuccess(ctx, "nba_com")
	if err != nil || !ok {
		t.Fatalf("get after set = %v, %v", ok, err)
	}
	if got.Before(before) || got.After(time.Now().UTC().Add(time.Second)) {
		t.Errorf("last success timestamp implausible: %v", got)
	}

	// An unparseable marker means no recorded success, not an error.
	if err := rdb.Set(ctx, "stats:health:nba_com:last_success", "not-a-time", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := c.GetLastSuccess(ctx, "nba_com"); ok || err != nil {
		t.Errorf("unparseable marker = %v, %v; want miss without error", ok, err)
	}
}
