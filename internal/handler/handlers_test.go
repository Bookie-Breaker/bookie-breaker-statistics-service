package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/redis/go-redis/v9"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/tests/testutil"
)

const testSeason = 2025

// nopRawRepo satisfies the archival interface; handler tests never reach a
// provider so nothing is archived.
type nopRawRepo struct{}

func (nopRawRepo) Insert(context.Context, model.RawAPIResponse) error { return nil }

// newQueryFixture builds a QueryService over a real Redis with a
// pre-populated cache and no providers: warm reads serve from cache, cold
// reads surface internal errors (there is nothing to refresh from).
func newQueryFixture(t *testing.T) (*service.QueryService, *cache.StatsCache, *redis.Client) {
	t.Helper()

	rdb := testutil.RedisClient(t)
	statsCache := cache.NewStatsCache(rdb, cache.TTLs{
		Teams: time.Hour, TeamStats: time.Hour, Players: time.Hour,
		Games: time.Hour, Injuries: time.Hour, BoxScore: time.Hour, Stale: 24 * time.Hour,
	})
	refresh := service.NewRefreshService(
		map[model.League]sportsdata.StatsProvider{}, nil, statsCache, nopRawRepo{}, pubsub.NewPublisher(rdb),
	)
	query := service.NewQueryService(statsCache, refresh, []model.League{model.LeagueNBA}, func() int { return testSeason })
	return query, statsCache, rdb
}

func seedCache(t *testing.T, statsCache *cache.StatsCache) {
	t.Helper()
	ctx := context.Background()

	teams := []model.TeamSummary{
		{ID: "team-lal", League: model.LeagueNBA, Name: "Los Angeles Lakers", Abbreviation: "LAL", Conference: "Western", Division: "Pacific", Active: true},
		{ID: "team-bos", League: model.LeagueNBA, Name: "Boston Celtics", Abbreviation: "BOS", Conference: "Eastern", Division: "Atlantic", Active: true},
	}
	if err := statsCache.SetTeams(ctx, "NBA", teams); err != nil {
		t.Fatal(err)
	}
	details := map[string]model.TeamDetail{
		"team-lal": {TeamSummary: teams[0]},
		"team-bos": {TeamSummary: teams[1]},
	}
	if err := statsCache.SetTeamDetails(ctx, "NBA", details); err != nil {
		t.Fatal(err)
	}
	if err := statsCache.SetTeamStats(ctx, "NBA", testSeason, 0, map[string]model.TeamStats{
		"team-lal": {
			TeamID: "team-lal", Wins: 30, Losses: 10,
			Stats: model.StatBlocks{
				Offensive: &model.OffensiveStats{PointsPerGame: 115},
				Defensive: &model.DefensiveStats{PointsAllowedPerGame: 108},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := statsCache.SetPlayers(ctx, "NBA", []model.PlayerSummary{
		{ID: "player-lbj", TeamID: "team-lal", FirstName: "LeBron", LastName: "James", Position: "F", Status: model.PlayerActive, League: model.LeagueNBA},
	}); err != nil {
		t.Fatal(err)
	}
	if err := statsCache.SetPlayerDetails(ctx, "NBA", map[string]model.PlayerDetail{
		"player-lbj": {PlayerSummary: model.PlayerSummary{ID: "player-lbj", FirstName: "LeBron", LastName: "James"}},
	}); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	games := []model.Game{
		{
			ID: "game-1", League: model.LeagueNBA, ExternalID: "ext-1",
			HomeTeam:       model.TeamRef{ID: "team-lal", Abbreviation: "LAL"},
			AwayTeam:       model.TeamRef{ID: "team-bos", Abbreviation: "BOS"},
			ScheduledStart: now.Add(-2 * time.Hour), Status: model.GameFinal, Season: testSeason,
			Result: &model.GameResult{ID: "game-1", HomeScore: 112, AwayScore: 104, TotalScore: 216, Margin: 8},
		},
		{
			ID: "game-2", League: model.LeagueNBA, ExternalID: "ext-2",
			HomeTeam:       model.TeamRef{ID: "team-bos", Abbreviation: "BOS"},
			AwayTeam:       model.TeamRef{ID: "team-lal", Abbreviation: "LAL"},
			ScheduledStart: now.Add(24 * time.Hour), Status: model.GameScheduled, Season: testSeason,
		},
	}
	if err := statsCache.SetGames(ctx, "NBA", testSeason, games); err != nil {
		t.Fatal(err)
	}
	if err := statsCache.SetBoxScore(ctx, "game-1", &model.BoxScore{
		GameID: "game-1", Sport: "BASKETBALL", Status: "FINAL",
		HomeTeam: model.TeamBoxScore{ID: "team-lal", Score: 112},
	}); err != nil {
		t.Fatal(err)
	}
	if err := statsCache.SetInjuries(ctx, "NBA", []model.InjuryReport{
		{PlayerID: "player-lbj", TeamID: "team-lal", League: model.LeagueNBA, Status: "OUT"},
	}); err != nil {
		t.Fatal(err)
	}
}

// doRequest invokes an echo handler directly and decodes the JSON envelope.
func doRequest(t *testing.T, h echo.HandlerFunc, target string, params map[string]string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	for k, v := range params {
		c.SetParamNames(k)
		c.SetParamValues(v)
	}
	if err := h(c); err != nil {
		t.Fatalf("handler returned unhandled error: %v", err)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not a JSON envelope: %v\n%s", err, rec.Body.String())
	}
	return rec, envelope
}

type meta struct {
	Timestamp  string `json:"timestamp"`
	RequestID  string `json:"request_id"`
	Pagination *struct {
		Limit      int    `json:"limit"`
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} `json:"pagination"`
}

func decodeMeta(t *testing.T, envelope map[string]json.RawMessage) meta {
	t.Helper()
	var m meta
	if err := json.Unmarshal(envelope["meta"], &m); err != nil {
		t.Fatalf("meta malformed: %v", err)
	}
	if m.Timestamp == "" || m.RequestID == "" {
		t.Errorf("meta missing timestamp/request_id: %+v", m)
	}
	return m
}

func decodeError(t *testing.T, envelope map[string]json.RawMessage) ErrorDetail {
	t.Helper()
	var detail ErrorDetail
	if err := json.Unmarshal(envelope["error"], &detail); err != nil {
		t.Fatalf("error detail malformed: %v", err)
	}
	return detail
}

func TestGetTeams(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewTeamsHandler(query)

	rec, envelope := doRequest(t, h.GetTeams, "/?league=NBA&conference=Western", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body.String())
	}
	var teams []model.TeamSummary
	if err := json.Unmarshal(envelope["data"], &teams); err != nil {
		t.Fatal(err)
	}
	if len(teams) != 1 || teams[0].ID != "team-lal" {
		t.Errorf("filtered teams wrong: %+v", teams)
	}
	m := decodeMeta(t, envelope)
	if m.Pagination == nil || m.Pagination.Limit != defaultLimit || m.Pagination.HasMore {
		t.Errorf("pagination meta wrong: %+v", m.Pagination)
	}
}

// TestGetTeamsLimitClamping covers parseLimit through the endpoint: bad and
// oversized limits fall back to the default and max.
func TestGetTeamsLimitClamping(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewTeamsHandler(query)

	tests := []struct {
		raw  string
		want int
	}{
		{"", defaultLimit},
		{"abc", defaultLimit},
		{"-3", defaultLimit},
		{"999", maxLimit},
		{"1", 1},
	}
	for _, tc := range tests {
		rec, envelope := doRequest(t, h.GetTeams, "/?limit="+tc.raw, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("limit=%q status = %d", tc.raw, rec.Code)
		}
		if m := decodeMeta(t, envelope); m.Pagination.Limit != tc.want {
			t.Errorf("limit=%q parsed to %d, want %d", tc.raw, m.Pagination.Limit, tc.want)
		}
	}
}

// TestGetTeamsActiveFilter covers the active query-param parse branch.
func TestGetTeamsActiveFilter(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewTeamsHandler(query)

	rec, envelope := doRequest(t, h.GetTeams, "/?active=false", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var teams []model.TeamSummary
	if err := json.Unmarshal(envelope["data"], &teams); err != nil {
		t.Fatal(err)
	}
	// Both seeded teams are active; data must be [] rather than null.
	if teams == nil || len(teams) != 0 {
		t.Errorf("inactive filter should serialize as [], got %s", envelope["data"])
	}
}

func TestGetTeamsBadCursor(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewTeamsHandler(query)

	rec, envelope := doRequest(t, h.GetTeams, "/?cursor=garbage", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if detail := decodeError(t, envelope); detail.Code != "INVALID_PARAMETER" {
		t.Errorf("error code = %q", detail.Code)
	}
}

// TestInternalErrorMapping covers the 500 mapping across the read
// endpoints when the store is down.
func TestInternalErrorMapping(t *testing.T) {
	query, _, rdb := newQueryFixture(t)
	if err := rdb.Close(); err != nil {
		t.Fatal(err)
	}

	teams := NewTeamsHandler(query)
	players := NewPlayersHandler(query)
	games := NewGamesHandler(query)
	injuries := NewInjuriesHandler(query)

	endpoints := map[string]echo.HandlerFunc{
		"teams":    teams.GetTeams,
		"players":  players.GetPlayers,
		"games":    games.GetGames,
		"schedule": games.GetSchedule,
		"injuries": injuries.GetInjuries,
	}
	for name, h := range endpoints {
		t.Run(name, func(t *testing.T) {
			rec, envelope := doRequest(t, h, "/", nil)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			if detail := decodeError(t, envelope); detail.Code != "INTERNAL_ERROR" {
				t.Errorf("error code = %q", detail.Code)
			}
		})
	}
}

func TestGetTeamByID(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewTeamsHandler(query)

	rec, envelope := doRequest(t, h.GetTeamByID, "/", map[string]string{"team_id": "team-lal"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var team model.TeamDetail
	if err := json.Unmarshal(envelope["data"], &team); err != nil {
		t.Fatal(err)
	}
	if team.Name != "Los Angeles Lakers" || team.SeasonSummary == nil || team.SeasonSummary.Wins != 30 {
		t.Errorf("team detail wrong: %+v", team)
	}

	rec, envelope = doRequest(t, h.GetTeamByID, "/", map[string]string{"team_id": "nope"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if detail := decodeError(t, envelope); detail.Code != "NOT_FOUND" {
		t.Errorf("error code = %q", detail.Code)
	}
}

func TestGetTeamStats(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewTeamsHandler(query)

	// Default stat_type is all.
	rec, envelope := doRequest(t, h.GetTeamStats, "/", map[string]string{"team_id": "team-lal"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var stats model.TeamStats
	if err := json.Unmarshal(envelope["data"], &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Stats.Offensive == nil || stats.Stats.Defensive == nil {
		t.Errorf("stat_type=all should keep both blocks: %+v", stats.Stats)
	}

	// Shaped request drops the other block.
	rec, envelope = doRequest(t, h.GetTeamStats, "/?stat_type=offensive", map[string]string{"team_id": "team-lal"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var shaped model.TeamStats
	if err := json.Unmarshal(envelope["data"], &shaped); err != nil {
		t.Fatal(err)
	}
	if shaped.Stats.Offensive == nil || shaped.Stats.Defensive != nil {
		t.Errorf("stat_type=offensive shaping wrong: %+v", shaped.Stats)
	}

	// Historical seasons are a client error.
	rec, envelope = doRequest(t, h.GetTeamStats, "/?season=2020", map[string]string{"team_id": "team-lal"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if detail := decodeError(t, envelope); detail.Code != "INVALID_PARAMETER" {
		t.Errorf("error code = %q", detail.Code)
	}
}

func TestGetPlayers(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewPlayersHandler(query)

	rec, envelope := doRequest(t, h.GetPlayers, "/?team_id=team-lal&position=F", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var players []model.PlayerSummary
	if err := json.Unmarshal(envelope["data"], &players); err != nil {
		t.Fatal(err)
	}
	if len(players) != 1 || players[0].ID != "player-lbj" {
		t.Errorf("filtered players wrong: %+v", players)
	}
	decodeMeta(t, envelope)
}

func TestGetPlayerByID(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewPlayersHandler(query)

	rec, envelope := doRequest(t, h.GetPlayerByID, "/", map[string]string{"player_id": "player-lbj"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var player model.PlayerDetail
	if err := json.Unmarshal(envelope["data"], &player); err != nil {
		t.Fatal(err)
	}
	if player.FirstName != "LeBron" {
		t.Errorf("player detail wrong: %+v", player)
	}

	rec, _ = doRequest(t, h.GetPlayerByID, "/", map[string]string{"player_id": "nope"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestGetGames(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewGamesHandler(query)

	// The status CSV filter narrows to the final game.
	rec, envelope := doRequest(t, h.GetGames, "/?status=FINAL,%20CANCELLED", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var games []model.Game
	if err := json.Unmarshal(envelope["data"], &games); err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 || games[0].ID != "game-1" {
		t.Errorf("status filter wrong: %+v", games)
	}

	rec, envelope = doRequest(t, h.GetGames, "/?date_from=garbage", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if detail := decodeError(t, envelope); detail.Code != "INVALID_PARAMETER" {
		t.Errorf("error code = %q", detail.Code)
	}
}

func TestGetGameByIDAndResult(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewGamesHandler(query)

	rec, envelope := doRequest(t, h.GetGameByID, "/", map[string]string{"game_id": "game-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var game model.Game
	if err := json.Unmarshal(envelope["data"], &game); err != nil {
		t.Fatal(err)
	}
	if game.Status != model.GameFinal {
		t.Errorf("game wrong: %+v", game)
	}

	rec, envelope = doRequest(t, h.GetGameResult, "/", map[string]string{"game_id": "game-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var result model.GameResult
	if err := json.Unmarshal(envelope["data"], &result); err != nil {
		t.Fatal(err)
	}
	if result.TotalScore != 216 {
		t.Errorf("result wrong: %+v", result)
	}

	// A pending game has no result; an unknown id has no game.
	rec, _ = doRequest(t, h.GetGameResult, "/", map[string]string{"game_id": "game-2"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("pending result status = %d, want 404", rec.Code)
	}
	rec, _ = doRequest(t, h.GetGameByID, "/", map[string]string{"game_id": "nope"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown game status = %d, want 404", rec.Code)
	}
}

func TestGetBoxScore(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewGamesHandler(query)

	rec, envelope := doRequest(t, h.GetBoxScore, "/", map[string]string{"game_id": "game-1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var box model.BoxScore
	if err := json.Unmarshal(envelope["data"], &box); err != nil {
		t.Fatal(err)
	}
	if box.GameID != "game-1" || box.HomeTeam.Score != 112 {
		t.Errorf("box score wrong: %+v", box)
	}

	rec, _ = doRequest(t, h.GetBoxScore, "/", map[string]string{"game_id": "nope"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown game status = %d, want 404", rec.Code)
	}
}

func TestGetSchedule(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewGamesHandler(query)

	rec, envelope := doRequest(t, h.GetSchedule, "/?team_id=LAL", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var games []model.Game
	if err := json.Unmarshal(envelope["data"], &games); err != nil {
		t.Fatal(err)
	}
	if len(games) != 2 {
		t.Errorf("schedule = %d games, want 2", len(games))
	}
	decodeMeta(t, envelope)
}

func TestGetInjuries(t *testing.T) {
	query, statsCache, _ := newQueryFixture(t)
	seedCache(t, statsCache)
	h := NewInjuriesHandler(query)

	rec, envelope := doRequest(t, h.GetInjuries, "/?team_id=team-lal&status=OUT", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var injuries []model.InjuryReport
	if err := json.Unmarshal(envelope["data"], &injuries); err != nil {
		t.Fatal(err)
	}
	if len(injuries) != 1 || injuries[0].PlayerID != "player-lbj" {
		t.Errorf("injuries wrong: %+v", injuries)
	}

	// No matches serialize as [], not null.
	rec, envelope = doRequest(t, h.GetInjuries, "/?team_id=nope", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if string(envelope["data"]) != "[]" {
		t.Errorf("empty injuries should serialize as [], got %s", envelope["data"])
	}
}

func TestResponseHelpers(t *testing.T) {
	e := echo.New()

	newCtx := func() (echo.Context, *httptest.ResponseRecorder) {
		rec := httptest.NewRecorder()
		return e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec), rec
	}

	c, rec := newCtx()
	if err := CreatedResponse(c, map[string]string{"id": "x"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("CreatedResponse status = %d", rec.Code)
	}

	c, rec = newCtx()
	if err := AcceptedResponse(c, nil); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusAccepted {
		t.Errorf("AcceptedResponse status = %d", rec.Code)
	}

	// newMeta reuses an upstream request id when the middleware set one.
	c, rec = newCtx()
	c.Response().Header().Set(echo.HeaderXRequestID, "req-123")
	if err := SuccessResponse(c, nil); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Meta meta `json:"meta"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Meta.RequestID != "req-123" {
		t.Errorf("request id not propagated: %+v", envelope.Meta)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	e := echo.New()
	mw := RequestIDMiddleware()
	next := func(c echo.Context) error { return c.NoContent(http.StatusOK) }

	// Absent header: a fresh id is generated.
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodGet, "/", nil), rec)
	if err := mw(next)(c); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get(echo.HeaderXRequestID) == "" {
		t.Error("request id not generated")
	}

	// Present header: the caller's id is preserved.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(echo.HeaderXRequestID, "caller-id")
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	if err := mw(next)(c); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get(echo.HeaderXRequestID); got != "caller-id" {
		t.Errorf("request id = %q, want caller-id", got)
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a,b", []string{"a", "b"}},
		{" a , ,b, ", []string{"a", "b"}},
	}
	for _, tc := range tests {
		got := splitCSV(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tc.in, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tc.in, i, got[i], tc.want[i])
			}
		}
	}
}
