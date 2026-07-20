package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espn"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/mlb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/nba"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/cache"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/handler"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/repository/postgres"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/server"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/service"
)

const (
	lakersNBAID  = "1610612747"
	celticsNBAID = "1610612738"
	finalGameID  = "0022500001" // FINAL in the schedule fixture
	liveGameID   = "0022500456" // SCHEDULED in the schedule, FINAL on the scoreboard
	seasonYear   = 2025
)

func adapterFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapter", "nba", "testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

// nbaStub serves adapter fixtures, generating the schedule and scoreboard
// dynamically so game dates fall on "today" for the watcher test.
func nbaStub(t *testing.T, now time.Time) *httptest.Server {
	t.Helper()
	emptyRoster := []byte(`{"resultSets":[{"name":"CommonTeamRoster","headers":["PLAYER","NUM","POSITION","EXP","PLAYER_ID"],"rowSet":[]}]}`)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/stats/leaguedashteamstats":
			switch r.URL.Query().Get("MeasureType") {
			case "Advanced":
				_, _ = w.Write(adapterFixture(t, "leaguedashteamstats_advanced.json"))
			case "Opponent":
				_, _ = w.Write(adapterFixture(t, "leaguedashteamstats_opponent.json"))
			default:
				_, _ = w.Write(adapterFixture(t, "leaguedashteamstats_base.json"))
			}
		case "/stats/commonteamroster":
			if r.URL.Query().Get("TeamID") == lakersNBAID {
				_, _ = w.Write(adapterFixture(t, "commonteamroster.json"))
			} else {
				_, _ = w.Write(emptyRoster)
			}
		case "/stats/leaguedashplayerstats":
			_, _ = w.Write(adapterFixture(t, "leaguedashplayerstats.json"))
		case "/stats/playergamelog":
			_, _ = w.Write(adapterFixture(t, "playergamelog.json"))
		case "/stats/scheduleleaguev2":
			_, _ = w.Write(scheduleJSON(now))
		case "/stats/scoreboardv3":
			_, _ = w.Write(scoreboardJSON(now))
		case "/stats/boxscoretraditionalv2":
			_, _ = w.Write(adapterFixture(t, "boxscoretraditionalv2.json"))
		default:
			http.NotFound(w, r)
		}
	}))
}

// scheduleJSON produces two games: one FINAL yesterday, one starting two
// hours ago today (still SCHEDULED per the schedule feed).
func scheduleJSON(now time.Time) []byte {
	payload := map[string]any{
		"leagueSchedule": map[string]any{
			"seasonYear": "2025-26",
			"leagueId":   "00",
			"gameDates": []map[string]any{
				{
					"gameDate": now.Add(-24 * time.Hour).Format("01/02/2006 15:04:05"),
					"games": []map[string]any{{
						"gameId": finalGameID, "gameStatus": 3, "gameStatusText": "Final",
						"gameDateTimeUTC": now.Add(-26 * time.Hour).Format(time.RFC3339),
						"gameLabel":       "", "arenaName": "TD Garden", "arenaCity": "Boston", "arenaState": "MA",
						"homeTeam": map[string]any{"teamId": 1610612738, "teamName": "Celtics", "teamCity": "Boston", "teamTricode": "BOS", "score": 118},
						"awayTeam": map[string]any{"teamId": 1610612747, "teamName": "Lakers", "teamCity": "Los Angeles", "teamTricode": "LAL", "score": 110},
					}},
				},
				{
					"gameDate": now.Format("01/02/2006 15:04:05"),
					"games": []map[string]any{{
						"gameId": liveGameID, "gameStatus": 1, "gameStatusText": "7:30 pm ET",
						"gameDateTimeUTC": now.Add(-2 * time.Hour).Format(time.RFC3339),
						"gameLabel":       "", "arenaName": "Crypto.com Arena", "arenaCity": "Los Angeles", "arenaState": "CA",
						"homeTeam": map[string]any{"teamId": 1610612747, "teamName": "Lakers", "teamCity": "Los Angeles", "teamTricode": "LAL", "score": 0},
						"awayTeam": map[string]any{"teamId": 1610612738, "teamName": "Celtics", "teamCity": "Boston", "teamTricode": "BOS", "score": 0},
					}},
				},
			},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

// scoreboardJSON reports the live game as FINAL with period scores.
func scoreboardJSON(now time.Time) []byte {
	periods := func(scores ...int) []map[string]any {
		out := make([]map[string]any, 0, len(scores))
		for i, s := range scores {
			out = append(out, map[string]any{"period": i + 1, "score": s})
		}
		return out
	}
	payload := map[string]any{
		"scoreboard": map[string]any{
			"gameDate": now.Format("2006-01-02"),
			"games": []map[string]any{{
				"gameId": liveGameID, "gameStatus": 3, "gameStatusText": "Final", "period": 4,
				"gameTimeUTC": now.Add(-2 * time.Hour).Format(time.RFC3339),
				"homeTeam":    map[string]any{"teamId": 1610612747, "teamTricode": "LAL", "score": 112, "periods": periods(28, 30, 26, 28)},
				"awayTeam":    map[string]any{"teamId": 1610612738, "teamTricode": "BOS", "score": 104, "periods": periods(25, 27, 26, 26)},
			}},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

func espnStub(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapter", "espn", "testdata", "injuries.json"))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
}

type envelope struct {
	Data json.RawMessage `json:"data"`
	Meta struct {
		Pagination *struct {
			Limit      int    `json:"limit"`
			HasMore    bool   `json:"has_more"`
			NextCursor string `json:"next_cursor"`
		} `json:"pagination"`
	} `json:"meta"`
	Error *struct {
		Code string `json:"code"`
	} `json:"error"`
}

func doRequest(t *testing.T, e http.Handler, target string) (int, envelope) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		// The health endpoint is envelope-free per the contract.
		return rec.Code, envelope{Data: rec.Body.Bytes()}
	}
	return rec.Code, env
}

func TestStatisticsService(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()

	nbaServer := nbaStub(t, now)
	defer nbaServer.Close()
	espnServer := espnStub(t)
	defer espnServer.Close()

	statsCache := cache.NewStatsCache(testRedis, cache.TTLs{
		Teams: time.Hour, TeamStats: time.Hour, Players: time.Hour,
		Games: time.Hour, Injuries: time.Hour, Stale: 24 * time.Hour,
	})
	nbaClient := nba.NewClient(nba.Options{
		BaseURL:            nbaServer.URL,
		UserAgent:          "integration-test",
		MinRequestInterval: time.Millisecond,
		Timeout:            10 * time.Second,
		FailureThreshold:   5,
		OpenDuration:       time.Minute,
	})
	espnClient := espn.NewClient(espnServer.URL, "basketball/nba", 10*time.Second)

	seasonFn := func() int { return seasonYear }
	providers := map[model.League]sportsdata.StatsProvider{
		model.LeagueNBA: nba.NewProvider(nbaClient, func(time.Time) int { return seasonYear }),
	}
	rawRepo := postgres.NewRawResponseRepo(testPool)
	publisher := pubsub.NewPublisher(testRedis)
	refresh := service.NewRefreshService(providers, map[model.League]*espn.Client{model.LeagueNBA: espnClient}, statsCache, rawRepo, publisher)
	watcher := service.NewGameWatcher(refresh, statsCache, publisher)
	query := service.NewQueryService(statsCache, refresh, []model.League{model.LeagueNBA}, seasonFn)

	e := server.New(server.Deps{
		DB:    testPool,
		Redis: testRedis,
		Cache: statsCache,
		Query: query,
		Upstreams: []handler.UpstreamCheck{
			{Source: "nba_com", Freshness: time.Hour},
			{Source: "espn", Freshness: time.Hour},
		},
	})

	// Full refresh pass, in scheduler order.
	if err := refresh.RefreshTeams(ctx); err != nil {
		t.Fatalf("RefreshTeams: %v", err)
	}
	if err := refresh.RefreshSchedule(ctx); err != nil {
		t.Fatalf("RefreshSchedule: %v", err)
	}
	if err := refresh.RefreshTeamStats(ctx); err != nil {
		t.Fatalf("RefreshTeamStats: %v", err)
	}
	if err := refresh.RefreshPlayers(ctx); err != nil {
		t.Fatalf("RefreshPlayers: %v", err)
	}
	if err := refresh.RefreshInjuries(ctx); err != nil {
		t.Fatalf("RefreshInjuries: %v", err)
	}

	lakersID := ids.Team("NBA", lakersNBAID)
	lebronID := ids.Player("NBA", "2544")

	t.Run("health reports healthy with upstream statuses", func(t *testing.T) {
		// The health payload is envelope-free per the contract.
		req := httptest.NewRequest(http.MethodGet, "/api/v1/stats/health", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
		}
		var health map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
			t.Fatal(err)
		}
		deps := health["dependencies"].(map[string]any)
		for _, dep := range []string{"redis", "postgres", "nba_com", "espn"} {
			status := deps[dep].(map[string]any)["status"]
			if status != "healthy" {
				t.Errorf("dependency %s = %v, want healthy", dep, status)
			}
		}
	})

	t.Run("teams list paginates through 30 teams", func(t *testing.T) {
		seen := 0
		cursor := ""
		for pages := 0; pages < 5; pages++ {
			target := "/api/v1/stats/teams?limit=12"
			if cursor != "" {
				target += "&cursor=" + cursor
			}
			code, env := doRequest(t, e, target)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			var teams []map[string]any
			if err := json.Unmarshal(env.Data, &teams); err != nil {
				t.Fatal(err)
			}
			seen += len(teams)
			if env.Meta.Pagination == nil || !env.Meta.Pagination.HasMore {
				break
			}
			cursor = env.Meta.Pagination.NextCursor
		}
		if seen != 30 {
			t.Errorf("teams = %d, want 30", seen)
		}
	})

	t.Run("team detail carries venue and season summary", func(t *testing.T) {
		code, env := doRequest(t, e, "/api/v1/stats/teams/"+lakersID)
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var team map[string]any
		if err := json.Unmarshal(env.Data, &team); err != nil {
			t.Fatal(err)
		}
		if team["abbreviation"] != "LAL" {
			t.Errorf("abbreviation = %v", team["abbreviation"])
		}
		venue := team["venue"].(map[string]any)
		if venue["name"] != "Crypto.com Arena" {
			t.Errorf("venue = %v", venue)
		}
		summary := team["season_summary"].(map[string]any)
		if summary["wins"].(float64) != 47 {
			t.Errorf("season summary = %v", summary)
		}
	})

	t.Run("team stats shape by stat_type", func(t *testing.T) {
		code, env := doRequest(t, e, "/api/v1/stats/teams/"+lakersID+"/stats?stat_type=offensive")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var stats map[string]any
		if err := json.Unmarshal(env.Data, &stats); err != nil {
			t.Fatal(err)
		}
		blocks := stats["stats"].(map[string]any)
		if blocks["offensive"] == nil {
			t.Error("offensive block missing")
		}
		if _, ok := blocks["defensive"]; ok {
			t.Error("defensive block should be omitted for stat_type=offensive")
		}

		code, env = doRequest(t, e, "/api/v1/stats/teams/"+lakersID+"/stats")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		if err := json.Unmarshal(env.Data, &stats); err != nil {
			t.Fatal(err)
		}
		splits := stats["home_away_splits"].(map[string]any)
		if splits["home"] == nil || splits["away"] == nil {
			t.Errorf("home/away splits missing: %v", splits)
		}
	})

	t.Run("players list and detail with game log", func(t *testing.T) {
		code, env := doRequest(t, e, "/api/v1/stats/players?team_id="+lakersID)
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var players []map[string]any
		if err := json.Unmarshal(env.Data, &players); err != nil {
			t.Fatal(err)
		}
		if len(players) != 2 {
			t.Fatalf("players = %d, want 2", len(players))
		}

		code, env = doRequest(t, e, "/api/v1/stats/players/"+lebronID+"?game_log=true")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var player map[string]any
		if err := json.Unmarshal(env.Data, &player); err != nil {
			t.Fatal(err)
		}
		if player["last_name"] != "James" {
			t.Errorf("player = %v", player["last_name"])
		}
		// ESPN reported LeBron OUT; the players refresh folds that in.
		if player["status"] != "OUT" {
			t.Errorf("status = %v, want OUT from injury report", player["status"])
		}
		log := player["game_log"].([]any)
		if len(log) != 2 {
			t.Errorf("game log = %d entries, want 2", len(log))
		}
	})

	t.Run("games and result endpoints", func(t *testing.T) {
		code, env := doRequest(t, e, "/api/v1/stats/games?status=FINAL")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var games []map[string]any
		if err := json.Unmarshal(env.Data, &games); err != nil {
			t.Fatal(err)
		}
		if len(games) != 1 {
			t.Fatalf("FINAL games = %d, want 1", len(games))
		}

		finalUUID := ids.Game("NBA", finalGameID)
		code, env = doRequest(t, e, "/api/v1/stats/games/"+finalUUID+"/result")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var result map[string]any
		if err := json.Unmarshal(env.Data, &result); err != nil {
			t.Fatal(err)
		}
		if result["home_score"].(float64) != 118 || result["margin"].(float64) != 8 {
			t.Errorf("result = %v", result)
		}

		// A scheduled game has no result yet.
		liveUUID := ids.Game("NBA", liveGameID)
		code, _ = doRequest(t, e, "/api/v1/stats/games/"+liveUUID+"/result")
		if code != http.StatusNotFound {
			t.Errorf("pre-final result status = %d, want 404", code)
		}
	})

	t.Run("box score endpoint serves oriented player lines", func(t *testing.T) {
		finalUUID := ids.Game("NBA", finalGameID)
		code, env := doRequest(t, e, "/api/v1/stats/games/"+finalUUID+"/box-score")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var box map[string]any
		if err := json.Unmarshal(env.Data, &box); err != nil {
			t.Fatal(err)
		}
		if box["game_id"] != finalUUID || box["sport"] != "BASKETBALL" || box["status"] != "FINAL" {
			t.Errorf("box score envelope wrong: game_id=%v sport=%v status=%v", box["game_id"], box["sport"], box["status"])
		}

		// The fixture emits LAL first, but the canonical game's home team
		// is BOS: the service must orient home/away against the game.
		home := box["home_team"].(map[string]any)
		away := box["away_team"].(map[string]any)
		if home["abbreviation"] != "BOS" || away["abbreviation"] != "LAL" {
			t.Fatalf("orientation wrong: home=%v away=%v", home["abbreviation"], away["abbreviation"])
		}
		if home["id"] != ids.Team("NBA", celticsNBAID) {
			t.Errorf("home team id = %v, want canonical Celtics id", home["id"])
		}
		if home["score"].(float64) != 104 || away["score"].(float64) != 112 {
			t.Errorf("scores = %v-%v, want 104-112", home["score"], away["score"])
		}

		players := away["players"].([]any)
		if len(players) != 3 {
			t.Fatalf("away players = %d, want 3", len(players))
		}
		lebron := players[0].(map[string]any)
		if lebron["player_id"] != lebronID || lebron["points"].(float64) != 28 {
			t.Errorf("player line wrong: %v", lebron)
		}
		teamStats := away["team_stats"].(map[string]any)
		if teamStats["field_goals_made"].(float64) != 42 {
			t.Errorf("team stats wrong: %v", teamStats)
		}

		// The FINAL game's box score is now cached under its Redis key.
		if n, err := testRedis.Exists(ctx, "stats:boxscore:"+finalUUID).Result(); err != nil || n != 1 {
			t.Errorf("box score not cached: exists=%d err=%v", n, err)
		}

		// Unknown game ids map to 404.
		code, env = doRequest(t, e, "/api/v1/stats/games/00000000-0000-0000-0000-000000000000/box-score")
		if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "NOT_FOUND" {
			t.Errorf("unknown game: status=%d error=%v", code, env.Error)
		}
	})

	t.Run("injuries endpoint serves normalized reports", func(t *testing.T) {
		code, env := doRequest(t, e, "/api/v1/stats/injuries?league=NBA")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var injuries []map[string]any
		if err := json.Unmarshal(env.Data, &injuries); err != nil {
			t.Fatal(err)
		}
		if len(injuries) != 2 {
			t.Fatalf("injuries = %d, want 2", len(injuries))
		}
		if injuries[0]["player_name"] != "LeBron James" || injuries[0]["status"] != "OUT" {
			t.Errorf("injury = %v", injuries[0])
		}
	})

	t.Run("unknown ids return 404 and bad cursors 400", func(t *testing.T) {
		code, env := doRequest(t, e, "/api/v1/stats/teams/00000000-0000-0000-0000-000000000000")
		if code != http.StatusNotFound || env.Error == nil || env.Error.Code != "NOT_FOUND" {
			t.Errorf("unknown team: status=%d error=%v", code, env.Error)
		}

		code, env = doRequest(t, e, "/api/v1/stats/teams?cursor=!!!")
		if code != http.StatusBadRequest || env.Error == nil {
			t.Errorf("bad cursor: status=%d error=%v", code, env.Error)
		}
	})

	t.Run("game watcher publishes game.completed exactly once", func(t *testing.T) {
		sub := testRedis.Subscribe(ctx, "events:game.completed")
		defer func() { _ = sub.Close() }()
		if _, err := sub.Receive(ctx); err != nil {
			t.Fatal(err)
		}

		if err := watcher.Tick(ctx); err != nil {
			t.Fatalf("watcher tick: %v", err)
		}

		select {
		case msg := <-sub.Channel():
			var event map[string]any
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				t.Fatal(err)
			}
			if event["game_external_id"] != liveGameID || event["home_score"].(float64) != 112 {
				t.Errorf("event = %v", event)
			}
			if event["total"].(float64) != 216 || event["margin"].(float64) != 8 {
				t.Errorf("event math = %v", event)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no game.completed event received")
		}

		// Second tick re-observes FINAL but must not re-publish.
		if err := watcher.Tick(ctx); err != nil {
			t.Fatalf("second tick: %v", err)
		}
		select {
		case msg := <-sub.Channel():
			t.Fatalf("unexpected duplicate event: %s", msg.Payload)
		case <-time.After(500 * time.Millisecond):
		}

		// The game endpoint now reflects the final score and periods.
		liveUUID := ids.Game("NBA", liveGameID)
		code, env := doRequest(t, e, "/api/v1/stats/games/"+liveUUID+"/result")
		if code != http.StatusOK {
			t.Fatalf("status = %d", code)
		}
		var result map[string]any
		if err := json.Unmarshal(env.Data, &result); err != nil {
			t.Fatal(err)
		}
		periods := result["period_scores"].([]any)
		if len(periods) != 4 {
			t.Errorf("period scores = %d, want 4", len(periods))
		}

		// The FINAL transition also prefetched the box score (best-effort
		// GameWatcher hook).
		if n, err := testRedis.Exists(ctx, "stats:boxscore:"+liveUUID).Result(); err != nil || n != 1 {
			t.Errorf("box score not prefetched on FINAL transition: exists=%d err=%v", n, err)
		}
	})

	t.Run("raw API responses are archived", func(t *testing.T) {
		var nbaCount, espnCount int
		if err := testPool.QueryRow(ctx,
			`SELECT COUNT(*) FROM public.raw_api_responses WHERE source = 'nba_com'`).Scan(&nbaCount); err != nil {
			t.Fatal(err)
		}
		if err := testPool.QueryRow(ctx,
			`SELECT COUNT(*) FROM public.raw_api_responses WHERE source = 'espn'`).Scan(&espnCount); err != nil {
			t.Fatal(err)
		}
		if nbaCount == 0 || espnCount == 0 {
			t.Errorf("archived responses: nba=%d espn=%d, want both > 0", nbaCount, espnCount)
		}
	})

}

// TestMLBInjuriesRefresh exercises the per-league injuries generalization:
// an enabled MLB league with an ESPN baseball/mlb client refreshes the MLB
// injury report (real fixture recorded 2026-07-05) into the cache, mapping
// injured-list stints to OUT.
func TestMLBInjuriesRefresh(t *testing.T) {
	ctx := context.Background()

	espnMLB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/site/v2/sports/baseball/mlb/injuries" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapter", "espn", "testdata", "injuries_mlb.json"))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer espnMLB.Close()

	statsCache := cache.NewStatsCache(testRedis, cache.TTLs{
		Teams: time.Hour, TeamStats: time.Hour, Players: time.Hour,
		Games: time.Hour, Injuries: time.Hour, Stale: 24 * time.Hour,
	})
	// The injuries refresh never calls the stats provider; MLB only needs
	// to be enabled.
	providers := map[model.League]sportsdata.StatsProvider{
		model.LeagueMLB: mlb.NewProvider(mlb.NewClient("http://unused", time.Second)),
	}
	espnClients := map[model.League]*espn.Client{
		model.LeagueMLB: espn.NewClient(espnMLB.URL, "baseball/mlb", 10*time.Second),
	}
	refresh := service.NewRefreshService(providers, espnClients, statsCache,
		postgres.NewRawResponseRepo(testPool), pubsub.NewPublisher(testRedis))

	if err := refresh.RefreshInjuries(ctx); err != nil {
		t.Fatalf("RefreshInjuries: %v", err)
	}

	reports, ok, err := statsCache.GetInjuries(ctx, string(model.LeagueMLB), false)
	if err != nil || !ok {
		t.Fatalf("GetInjuries: ok=%v err=%v", ok, err)
	}
	if len(reports) != 4 {
		t.Fatalf("reports = %d, want 4", len(reports))
	}
	byName := make(map[string]model.InjuryReport, len(reports))
	for _, r := range reports {
		byName[r.PlayerName] = r
	}
	if byName["Landon Knack"].Status != string(model.PlayerOut) {
		t.Errorf("60-day IL must map to OUT: %+v", byName["Landon Knack"])
	}
	if byName["Jazz Chisholm Jr."].Status != string(model.PlayerInjured) {
		t.Errorf("day-to-day must map to INJURED: %+v", byName["Jazz Chisholm Jr."])
	}
}

// TestMLBBoxScoreFetch mirrors the Wave 0 box-score integration pattern for
// MLB (Phase 7 Wave 3): FetchBoxScore against a StatsAPI stub serving the
// real gamePk 823372 capture passes the already-labeled home/away blocks
// through orientation, caches the FINAL box score under its Redis key, and
// archives the raw body.
func TestMLBBoxScoreFetch(t *testing.T) {
	ctx := context.Background()

	statsAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/game/823372/boxscore" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("..", "..", "internal", "adapter", "mlb", "testdata", "boxscore.json"))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer statsAPI.Close()

	statsCache := cache.NewStatsCache(testRedis, cache.TTLs{
		Teams: time.Hour, TeamStats: time.Hour, Players: time.Hour,
		Games: time.Hour, Injuries: time.Hour, BoxScore: time.Hour, Stale: 24 * time.Hour,
	})
	providers := map[model.League]sportsdata.StatsProvider{
		model.LeagueMLB: mlb.NewProvider(mlb.NewClient(statsAPI.URL, 10*time.Second)),
	}
	refresh := service.NewRefreshService(providers, nil, statsCache,
		postgres.NewRawResponseRepo(testPool), pubsub.NewPublisher(testRedis))

	gameUUID := ids.Game("MLB", "823372")
	game := &model.Game{
		ID:         gameUUID,
		ExternalID: "823372",
		League:     model.LeagueMLB,
		HomeTeam:   model.TeamRef{ID: ids.Team("MLB", "134"), Abbreviation: "PIT"},
		AwayTeam:   model.TeamRef{ID: ids.Team("MLB", "119"), Abbreviation: "LAD"},
		Status:     model.GameFinal,
	}

	box, err := refresh.FetchBoxScore(ctx, game)
	if err != nil {
		t.Fatalf("FetchBoxScore: %v", err)
	}
	if box.GameID != gameUUID || box.Sport != "BASEBALL" || box.Status != "FINAL" {
		t.Errorf("envelope wrong: game_id=%s sport=%s status=%s", box.GameID, box.Sport, box.Status)
	}
	// The StatsAPI labels home/away itself; orientation must be a no-op.
	if box.HomeTeam.Abbreviation != "PIT" || box.HomeTeam.Score != 9 ||
		box.AwayTeam.Abbreviation != "LAD" || box.AwayTeam.Score != 8 {
		t.Errorf("orientation wrong: home=%s %d away=%s %d",
			box.HomeTeam.Abbreviation, box.HomeTeam.Score, box.AwayTeam.Abbreviation, box.AwayTeam.Score)
	}

	// Ohtani's real two-way line: batting and pitching on one row, with the
	// pinned grading keys populated (batter_hits/batter_total_bases/
	// batter_home_runs → Hits/TotalBases/HomeRuns, pitcher_strikeouts →
	// StrikeoutsPitching).
	var found bool
	for _, line := range box.AwayTeam.BaseballPlayers {
		if line.PlayerID != ids.Player("MLB", "660271") {
			continue
		}
		found = true
		if line.Hits != 1 || line.TotalBases != 4 || line.HomeRuns != 1 || line.StrikeoutsPitching != 6 {
			t.Errorf("Ohtani grading keys wrong: %+v", line)
		}
	}
	if !found {
		t.Error("Ohtani line missing from the away block")
	}

	// The FINAL game's box score is cached under its Redis key.
	if n, err := testRedis.Exists(ctx, "stats:boxscore:"+gameUUID).Result(); err != nil || n != 1 {
		t.Errorf("box score not cached: exists=%d err=%v", n, err)
	}

	// The raw body is archived like every provider fetch.
	var count int
	if err := testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM public.raw_api_responses WHERE source = 'mlb_statsapi' AND endpoint LIKE '%boxscore%'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Error("box score body not archived")
	}
}
