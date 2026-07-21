package espnbb

// Tests for the ESPN basketball client and normalizer, mirroring the espnfb
// football adapter's coverage but exercising basketball's two differences:
// two-half regulation (overtime is period 3+) and the client's day-at-a-time
// scoreboard walk (SeasonGames has no ranged query to fall back on). Fixtures
// are inlined here since this package carries no testdata/ directory.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

const teamsFixture = `{
  "sports": [
    {
      "leagues": [
        {
          "teams": [
            {"team": {"id": "150", "displayName": "Duke Blue Devils", "abbreviation": "DUKE", "location": "Duke", "isActive": true}},
            {"team": {"id": "2305", "displayName": "Kansas Jayhawks", "abbreviation": "KU", "location": "Kansas", "isActive": true}}
          ]
        }
      ]
    }
  ]
}`

// scoreboardFixture carries four events exercising every NormalizeEvent /
// ScoreboardUpdates branch: a completed one-OT final, an upcoming game with
// no venue and an omitted season year (falls back to the caller's year), an
// event missing its away competitor (malformed, skipped), and an event with
// an unparsable date (malformed, skipped by NormalizeEvent but not by
// ScoreboardUpdates, which never looks at the date).
const scoreboardFixture = `{
  "events": [
    {
      "id": "401700001",
      "date": "2026-01-24T17:00Z",
      "name": "Kansas at Duke",
      "season": {"year": 2026, "type": 2, "slug": "regular-season"},
      "status": {"period": 3, "type": {"name": "STATUS_FINAL", "state": "post", "completed": true}},
      "competitions": [
        {
          "venue": {"fullName": "Cameron Indoor Stadium"},
          "competitors": [
            {"homeAway": "home", "score": "78", "team": {"id": "150", "displayName": "Duke Blue Devils", "abbreviation": "DUKE"}, "linescores": [{"value": 35}, {"value": 33}, {"value": 10}]},
            {"homeAway": "away", "score": "75", "team": {"id": "2305", "displayName": "Kansas Jayhawks", "abbreviation": "KU"}, "linescores": [{"value": 30}, {"value": 38}, {"value": 7}]}
          ]
        }
      ]
    },
    {
      "id": "401700002",
      "date": "2026-01-25T00:00Z",
      "name": "Duke at Kansas",
      "season": {"year": 0, "type": 2, "slug": "regular-season"},
      "status": {"period": 0, "type": {"name": "STATUS_SCHEDULED", "state": "pre", "completed": false}},
      "competitions": [
        {
          "venue": null,
          "competitors": [
            {"homeAway": "home", "score": "0", "team": {"id": "2305", "displayName": "Kansas Jayhawks", "abbreviation": "KU"}},
            {"homeAway": "away", "score": "0", "team": {"id": "150", "displayName": "Duke Blue Devils", "abbreviation": "DUKE"}}
          ]
        }
      ]
    },
    {
      "id": "401700003",
      "date": "2026-01-26T00:00Z",
      "status": {"period": 1, "type": {"name": "STATUS_IN_PROGRESS", "state": "in", "completed": false}},
      "competitions": [
        {
          "competitors": [
            {"homeAway": "home", "score": "10", "team": {"id": "150"}}
          ]
        }
      ]
    },
    {
      "id": "401700004",
      "date": "not-a-date",
      "status": {"period": 1, "type": {"name": "STATUS_IN_PROGRESS", "state": "in", "completed": false}},
      "competitions": [
        {
          "competitors": [
            {"homeAway": "home", "score": "10", "team": {"id": "150"}},
            {"homeAway": "away", "score": "5", "team": {"id": "2305"}}
          ]
        }
      ]
    }
  ]
}`

func writeJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(server.URL, 5*time.Second)
}

func TestClientTeams(t *testing.T) {
	var gotPath string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Query().Get("limit") != "500" {
			t.Errorf("limit = %q, want 500", r.URL.Query().Get("limit"))
		}
		writeJSON(t, w, teamsFixture)
	}))

	resp, fetch, err := client.Teams(context.Background(), "basketball/mens-college-basketball")
	if err != nil {
		t.Fatalf("Teams failed: %v", err)
	}
	if gotPath != "/apis/site/v2/sports/basketball/mens-college-basketball/teams" {
		t.Errorf("path = %q", gotPath)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK || len(fetch.Body) == 0 {
		t.Fatalf("fetch = %+v", fetch)
	}
	if fetch.Endpoint != "/apis/site/v2/sports/basketball/mens-college-basketball/teams?limit=500" {
		t.Errorf("fetch endpoint = %q", fetch.Endpoint)
	}

	teams, details := NormalizeTeams(model.LeagueNCAABB, resp)
	if len(teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(teams))
	}
	var duke model.TeamSummary
	for _, tm := range teams {
		if tm.Abbreviation == "DUKE" {
			duke = tm
		}
	}
	if duke.ID != ids.Team("NCAA_BB", "150") {
		t.Errorf("id not minted with ids.Team: %s", duke.ID)
	}
	if duke.Name != "Duke Blue Devils" || duke.Location != "Duke" || !duke.Active {
		t.Errorf("summary wrong: %+v", duke)
	}
	if duke.ExternalIDs["espn"] != "150" {
		t.Errorf("external id wrong: %+v", duke.ExternalIDs)
	}
	if duke.Conference != "" || duke.Division != "" || duke.VenueID != "" {
		t.Errorf("basketball teams carry no venue/conference/division: %+v", duke)
	}
	if details[duke.ID].ID != duke.ID {
		t.Errorf("details missing entry for %s", duke.ID)
	}
}

func TestClientTeamsUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))

	_, fetch, err := client.Teams(context.Background(), "basketball/mens-college-basketball")
	if err == nil || !strings.Contains(err.Error(), "espn returned 500") {
		t.Fatalf("err = %v, want espn 500 error", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusInternalServerError || string(fetch.Body) != "boom" {
		t.Errorf("fetch not archived on error: %+v", fetch)
	}
}

func TestClientTeamsMalformedBody(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, "{not json")
	}))

	_, fetch, err := client.Teams(context.Background(), "basketball/mens-college-basketball")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want decode error", err)
	}
	if fetch == nil || len(fetch.Body) == 0 {
		t.Errorf("malformed body must still be archived: %+v", fetch)
	}
}

func TestClientCreateRequestError(t *testing.T) {
	// A NUL byte in the base URL makes http.NewRequestWithContext fail, so
	// no fetch is ever produced and the error is the request-creation one.
	client := NewClient("http://exa\x00mple.com", time.Second)
	_, fetch, err := client.Teams(context.Background(), "basketball/mens-college-basketball")
	if err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("err = %v, want create-request error", err)
	}
	if fetch != nil {
		t.Errorf("fetch = %+v, want nil (request never sent)", fetch)
	}
}

func TestClientExecuteRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, teamsFixture)
	}))
	server.Close() // refuse every connection

	client := NewClient(server.URL, time.Second)
	_, fetch, err := client.Teams(context.Background(), "basketball/mens-college-basketball")
	if err == nil || !strings.Contains(err.Error(), "execute request") {
		t.Fatalf("err = %v, want execute-request error", err)
	}
	if fetch != nil {
		t.Errorf("fetch = %+v, want nil (no response received)", fetch)
	}
}

func TestClientScoreboard(t *testing.T) {
	var gotQuery string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		writeJSON(t, w, scoreboardFixture)
	}))

	date := time.Date(2026, 1, 24, 0, 0, 0, 0, time.UTC)
	resp, fetch, err := client.Scoreboard(context.Background(), "basketball/mens-college-basketball", date)
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if !strings.Contains(gotQuery, "dates=20260124") {
		t.Errorf("query = %q, want dates=20260124", gotQuery)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK {
		t.Fatalf("fetch = %+v", fetch)
	}
	if len(resp.Events) != 4 {
		t.Fatalf("events = %d, want 4", len(resp.Events))
	}
}

func TestClientScoreboardUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	_, fetch, err := client.Scoreboard(context.Background(), "basketball/mens-college-basketball", time.Now())
	if err == nil || !strings.Contains(err.Error(), "espn returned 403") {
		t.Fatalf("err = %v, want 403 error", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusForbidden {
		t.Errorf("fetch not archived: %+v", fetch)
	}
}

func TestMapStatus(t *testing.T) {
	tests := []struct {
		name, state string
		completed   bool
		want        model.GameStatus
	}{
		{"STATUS_SCHEDULED", "pre", false, model.GameScheduled},
		{"STATUS_IN_PROGRESS", "in", false, model.GameInProgress},
		{"STATUS_FINAL", "post", true, model.GameFinal},
		{"STATUS_POSTPONED", "post", false, model.GamePostponed},
		{"STATUS_CANCELED", "post", false, model.GameCancelled},  //nolint:misspell // contract enum spelling
		{"STATUS_CANCELLED", "post", false, model.GameCancelled}, //nolint:misspell // contract enum spelling
		{"STATUS_SUSPENDED", "in", false, model.GameSuspended},
		{"STATUS_DELAYED", "in", false, model.GameSuspended},
		{"STATUS_UNKNOWN", "post", false, model.GamePostponed},
	}
	for _, tt := range tests {
		got := MapStatus(StatusType{Name: tt.name, State: tt.state, Completed: tt.completed})
		if got != tt.want {
			t.Errorf("MapStatus(%q,%q,%v) = %s, want %s", tt.name, tt.state, tt.completed, got, tt.want)
		}
	}
}

func TestMapSeasonType(t *testing.T) {
	for typ, want := range map[int]model.SeasonType{
		1: model.SeasonPreseason,
		2: model.SeasonRegular,
		3: model.SeasonPostseason,
		4: model.SeasonOffseason,
		0: model.SeasonRegular,
	} {
		if got := MapSeasonType(EventSeason{Type: typ}); got != want {
			t.Errorf("MapSeasonType(%d) = %s, want %s", typ, got, want)
		}
	}
}

func TestBuildResultOvertime(t *testing.T) {
	home := Competitor{Score: "78", Linescores: []Linescore{{35}, {33}, {10}}}
	away := Competitor{Score: "75", Linescores: []Linescore{{30}, {38}, {7}}}
	ev := Event{Status: EventStatus{Period: 3}}
	completedAt := time.Date(2026, 1, 24, 19, 30, 0, 0, time.UTC)

	r := BuildResult(ev, home, away, completedAt)
	if r.HomeScore != 78 || r.AwayScore != 75 || r.Margin != 3 || r.TotalScore != 153 {
		t.Errorf("score math wrong: %+v", r)
	}
	if !r.Overtime {
		t.Error("period 3 (past two halves) must be overtime")
	}
	if len(r.PeriodScores) != 3 || r.PeriodScores[2].Home != 10 || r.PeriodScores[2].Away != 7 {
		t.Errorf("period scores wrong: %+v", r.PeriodScores)
	}
	if r.RegulationHomeScore != nil || r.RegulationAwayScore != nil {
		t.Errorf("basketball carries no regulation fields: %+v", r)
	}
	if !r.CompletedAt.Equal(completedAt) {
		t.Errorf("completedAt = %s, want %s", r.CompletedAt, completedAt)
	}
}

func TestBuildResultRegulation(t *testing.T) {
	home := Competitor{Score: "70", Linescores: []Linescore{{35}, {35}}}
	away := Competitor{Score: "60", Linescores: []Linescore{{28}, {32}}}
	ev := Event{Status: EventStatus{Period: 2}}

	r := BuildResult(ev, home, away, time.Now())
	if r.Overtime {
		t.Error("period 2 is regulation, must not be overtime")
	}
	if len(r.PeriodScores) != 2 {
		t.Errorf("period scores = %d, want 2 halves", len(r.PeriodScores))
	}
}

func parseFixture(t *testing.T) *ScoreboardResponse {
	t.Helper()
	var resp ScoreboardResponse
	if err := json.Unmarshal([]byte(scoreboardFixture), &resp); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &resp
}

func TestNormalizeEventFinal(t *testing.T) {
	resp := parseFixture(t)
	game, ok := NormalizeEvent(model.LeagueNCAABB, resp.Events[0], 2025)
	if !ok {
		t.Fatal("final event should normalize")
	}
	if game.ID != ids.Game("NCAA_BB", "401700001") || game.ExternalID != "401700001" {
		t.Errorf("identity wrong: %+v", game)
	}
	if game.HomeTeam.Abbreviation != "DUKE" || game.AwayTeam.Abbreviation != "KU" {
		t.Errorf("team refs wrong: %+v / %+v", game.HomeTeam, game.AwayTeam)
	}
	if game.Status != model.GameFinal || game.Season != 2026 || game.SeasonType != model.SeasonRegular {
		t.Errorf("game meta wrong: %+v", game)
	}
	if game.Venue == nil || game.Venue.Name != "Cameron Indoor Stadium" {
		t.Errorf("venue wrong: %+v", game.Venue)
	}
	if game.HomeScore == nil || *game.HomeScore != 78 || game.AwayScore == nil || *game.AwayScore != 75 {
		t.Fatalf("scores wrong: %+v", game)
	}
	if game.Result == nil || game.Result.ID != game.ID || !game.Result.Overtime {
		t.Errorf("result wrong: %+v", game.Result)
	}
}

func TestNormalizeEventScheduledFallsBackToSeasonYear(t *testing.T) {
	resp := parseFixture(t)
	game, ok := NormalizeEvent(model.LeagueNCAABB, resp.Events[1], 2025)
	if !ok {
		t.Fatal("scheduled event should normalize")
	}
	if game.Season != 2025 {
		t.Errorf("season = %d, want fallback 2025 (event omitted year)", game.Season)
	}
	if game.Status != model.GameScheduled {
		t.Errorf("status = %s, want SCHEDULED", game.Status)
	}
	if game.HomeScore != nil || game.AwayScore != nil {
		t.Errorf("scheduled game must carry no scores: %+v", game)
	}
	if game.Venue != nil {
		t.Errorf("null venue must stay nil: %+v", game.Venue)
	}
}

func TestNormalizeEventMissingCompetitor(t *testing.T) {
	resp := parseFixture(t)
	if _, ok := NormalizeEvent(model.LeagueNCAABB, resp.Events[2], 2025); ok {
		t.Error("event missing its away competitor must not normalize")
	}
}

func TestNormalizeEventBadDate(t *testing.T) {
	resp := parseFixture(t)
	if _, ok := NormalizeEvent(model.LeagueNCAABB, resp.Events[3], 2025); ok {
		t.Error("event with an unparsable date must not normalize")
	}
}

func TestNormalizeEventNoCompetitions(t *testing.T) {
	if _, ok := NormalizeEvent(model.LeagueNCAABB, Event{ID: "no-comps"}, 2025); ok {
		t.Error("event with no competitions must not normalize")
	}
}

func TestScoreboardUpdates(t *testing.T) {
	resp := parseFixture(t)
	now := time.Date(2026, 1, 24, 20, 0, 0, 0, time.UTC)
	updates := ScoreboardUpdates(resp, now)

	// Event 3 (missing away competitor) is the only one dropped; the
	// bad-date event is still a valid scoreboard update (dates never enter
	// ScoreboardUpdates' logic).
	if len(updates) != 3 {
		t.Fatalf("updates = %d, want 3", len(updates))
	}

	final := updates["401700001"]
	if final.Status != model.GameFinal || final.HomeScore != 78 || final.AwayScore != 75 {
		t.Errorf("final update wrong: %+v", final)
	}
	if final.Result == nil || !final.Result.Overtime || !final.Result.CompletedAt.Equal(now) {
		t.Errorf("final result wrong: %+v", final.Result)
	}

	scheduled := updates["401700002"]
	if scheduled.Status != model.GameScheduled || scheduled.Result != nil {
		t.Errorf("scheduled update wrong: %+v", scheduled)
	}

	if _, ok := updates["401700003"]; ok {
		t.Error("event missing a competitor must be dropped")
	}
	if _, ok := updates["401700004"]; !ok {
		t.Error("bad-date event must still produce an update")
	}
}

// seasonGamesFixture serves two events with no malformed entries so
// SeasonGames' dedup and fetch-per-day walk can be asserted precisely.
const seasonGamesFixture = `{
  "events": [
    {
      "id": "401700001",
      "date": "2026-01-24T17:00Z",
      "season": {"year": 2026, "type": 2},
      "status": {"period": 2, "type": {"name": "STATUS_FINAL", "state": "post", "completed": true}},
      "competitions": [
        {
          "competitors": [
            {"homeAway": "home", "score": "70", "team": {"id": "150", "displayName": "Duke Blue Devils", "abbreviation": "DUKE"}, "linescores": [{"value": 35}, {"value": 35}]},
            {"homeAway": "away", "score": "60", "team": {"id": "2305", "displayName": "Kansas Jayhawks", "abbreviation": "KU"}, "linescores": [{"value": 28}, {"value": 32}]}
          ]
        }
      ]
    }
  ]
}`

func TestSeasonGamesWalksDaysAndDedupes(t *testing.T) {
	var calls int
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeJSON(t, w, seasonGamesFixture)
	}))

	start := time.Date(2026, 1, 24, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 25, 0, 0, 0, 0, time.UTC)
	games, fetches, err := SeasonGames(context.Background(), client, model.LeagueNCAABB, "basketball/mens-college-basketball", 2026, start, end)
	if err != nil {
		t.Fatalf("SeasonGames failed: %v", err)
	}
	if calls != 2 {
		t.Errorf("upstream calls = %d, want 2 (one per day)", calls)
	}
	if len(fetches) != 2 {
		t.Errorf("fetches = %d, want 2", len(fetches))
	}
	if len(games) != 1 {
		t.Fatalf("games = %d, want 1 (same event both days, deduped)", len(games))
	}
	if games[0].ExternalID != "401700001" {
		t.Errorf("game = %+v", games[0])
	}
}

func TestSeasonGamesPropagatesUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	start := time.Date(2026, 1, 24, 0, 0, 0, 0, time.UTC)
	games, fetches, err := SeasonGames(context.Background(), client, model.LeagueNCAABB, "basketball/mens-college-basketball", 2026, start, start)
	if err == nil || !strings.Contains(err.Error(), "fetch scoreboard") {
		t.Fatalf("err = %v, want wrapped fetch-scoreboard error", err)
	}
	if games != nil {
		t.Errorf("games = %+v, want nil on error", games)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (archived even on failure)", len(fetches))
	}
}

func TestNormalizeTeamsEmpty(t *testing.T) {
	teams, details := NormalizeTeams(model.LeagueNCAABB, &TeamsResponse{})
	if teams != nil || len(details) != 0 {
		t.Errorf("empty response must yield no teams: %+v / %+v", teams, details)
	}
}
