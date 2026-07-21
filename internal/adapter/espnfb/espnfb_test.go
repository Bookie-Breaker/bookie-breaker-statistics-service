package espnfb

// Unit tests for the shared ESPN football helpers used by both the NFL and
// NCAA_FB adapters. Status/season mappings and the standings flattening are
// pure functions; the provider integration tests (nfl, cfbd) exercise them
// end-to-end over real archived 2025-season fixtures.

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
		{"STATUS_CANCELED", "post", false, model.GameCancelled}, //nolint:misspell // contract enum spelling
		{"STATUS_SUSPENDED", "in", false, model.GameSuspended},
		{"STATUS_DELAYED", "in", false, model.GameSuspended},
		// A "post" state that never completed is treated as postponed.
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
		0: model.SeasonRegular, // absent field falls back to regular season
	} {
		if got := MapSeasonType(EventSeason{Type: typ}); got != want {
			t.Errorf("MapSeasonType(%d) = %s, want %s", typ, got, want)
		}
	}
}

func TestParseRecordSummary(t *testing.T) {
	tests := []struct {
		in       string
		w, l, ti int
		ok       bool
	}{
		{"14-3", 14, 3, 0, true},
		{"9-7-1", 9, 7, 1, true},
		{" 12 - 2 ", 12, 2, 0, true},
		{"", 0, 0, 0, false},
		{"12", 0, 0, 0, false},
		{"a-b", 0, 0, 0, false},
	}
	for _, tt := range tests {
		w, l, ti, ok := parseRecordSummary(tt.in)
		if w != tt.w || l != tt.l || ti != tt.ti || ok != tt.ok {
			t.Errorf("parseRecordSummary(%q) = %d,%d,%d,%v, want %d,%d,%d,%v", tt.in, w, l, ti, ok, tt.w, tt.l, tt.ti, tt.ok)
		}
	}
}

func TestRecords(t *testing.T) {
	resp := &StandingsResponse{
		Children: []struct {
			Name      string `json:"name"`
			Standings *struct {
				Entries []StandingsEntry `json:"entries"`
			} `json:"standings"`
		}{
			{
				Name: "conference-with-explicit-stats",
				Standings: &struct {
					Entries []StandingsEntry `json:"entries"`
				}{
					Entries: []StandingsEntry{{
						Team: Team{ID: "17", Abbreviation: "NE"},
						Stats: []StandingStat{
							{Type: "wins", Value: 14},
							{Type: "losses", Value: 3},
							{Type: "ties", Value: 0},
							{Type: "pointsfor", Value: 490},
							{Type: "pointsagainst", Value: 320},
							{Type: "total", Summary: "14-3"},
						},
					}},
				},
			},
			{
				// College style: only the overall record summary is present.
				Name: "conference-summary-only",
				Standings: &struct {
					Entries []StandingsEntry `json:"entries"`
				}{
					Entries: []StandingsEntry{{
						Team:  Team{ID: "249", Abbreviation: "UNT"},
						Stats: []StandingStat{{Type: "total", Summary: "12-2"}},
					}},
				},
			},
			{Name: "conference-without-standings", Standings: nil}, // skipped
		},
	}

	records := Records(resp)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (nil-standings child skipped)", len(records))
	}
	ne := records[0]
	if ne.Wins != 14 || ne.Losses != 3 || ne.Ties != 0 || ne.Games() != 17 {
		t.Errorf("NE record wrong: %+v (games %d)", ne, ne.Games())
	}
	if ne.PointsFor != 490 || ne.PointsAgainst != 320 {
		t.Errorf("NE points wrong: %+v", ne)
	}
	unt := records[1]
	if unt.Wins != 12 || unt.Losses != 2 || unt.Games() != 14 {
		t.Errorf("UNT summary-only record wrong: %+v (games %d)", unt, unt.Games())
	}
}

// TestBuildResultTie covers the ADR-027 football rule: a FINAL with equal
// scores is a valid tie whose scores flow through as-is, and overtime play
// (period > 4) sets Overtime with no regulation fields.
func TestBuildResultTie(t *testing.T) {
	home := Competitor{Score: "40", Linescores: []Linescore{{0}, {16}, {7}, {14}, {3}}}
	away := Competitor{Score: "40", Linescores: []Linescore{{7}, {6}, {7}, {17}, {3}}}
	ev := Event{Status: EventStatus{Period: 5}}
	completedAt := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC)

	r := BuildResult(ev, home, away, completedAt)
	if r.HomeScore != 40 || r.AwayScore != 40 || r.Margin != 0 || r.TotalScore != 80 {
		t.Errorf("tie result math wrong: %+v", r)
	}
	if !r.Overtime {
		t.Error("period 5 must be overtime")
	}
	if len(r.PeriodScores) != 5 || r.PeriodScores[4].Home != 3 || r.PeriodScores[4].Away != 3 {
		t.Errorf("overtime period scores wrong: %+v", r.PeriodScores)
	}
	if r.RegulationHomeScore != nil || r.RegulationAwayScore != nil {
		t.Errorf("football carries no regulation fields: %+v", r)
	}
	if !r.CompletedAt.Equal(completedAt) {
		t.Errorf("completedAt = %s, want %s", r.CompletedAt, completedAt)
	}
}

// The remaining tests cover the Client (HTTP fetch/archival/error paths) and
// the normalizer functions that need a live response shape to exercise
// (NormalizeTeams, NormalizeEvent, SeasonGames, ScoreboardUpdates); fixtures
// are inlined since this package carries no testdata/ directory.

const fbTeamsFixture = `{
  "sports": [
    {
      "leagues": [
        {
          "teams": [
            {"team": {"id": "9", "displayName": "Green Bay Packers", "abbreviation": "GB", "location": "Green Bay", "isActive": true}},
            {"team": {"id": "6", "displayName": "Dallas Cowboys", "abbreviation": "DAL", "location": "Dallas", "isActive": true}}
          ]
        }
      ]
    }
  ]
}`

// fbScoreboardFixture carries a completed one-OT final, an upcoming game with
// no venue and an omitted season year (falls back to the caller's year), an
// event missing its away competitor (malformed, skipped by everything), and
// an event with an unparsable date (malformed for NormalizeEvent only;
// ScoreboardUpdates never looks at the date).
const fbScoreboardFixture = `{
  "events": [
    {
      "id": "401700101",
      "date": "2025-09-29T00:00Z",
      "season": {"year": 2025, "type": 2, "slug": "regular-season"},
      "status": {"period": 5, "type": {"name": "STATUS_FINAL", "state": "post", "completed": true}},
      "competitions": [
        {
          "venue": {"fullName": "Lambeau Field"},
          "competitors": [
            {"homeAway": "home", "score": "40", "team": {"id": "9", "displayName": "Green Bay Packers", "abbreviation": "GB"}, "linescores": [{"value": 0}, {"value": 16}, {"value": 7}, {"value": 14}, {"value": 3}]},
            {"homeAway": "away", "score": "40", "team": {"id": "6", "displayName": "Dallas Cowboys", "abbreviation": "DAL"}, "linescores": [{"value": 7}, {"value": 6}, {"value": 7}, {"value": 17}, {"value": 3}]}
          ]
        }
      ]
    },
    {
      "id": "401700102",
      "date": "2025-10-05T17:00Z",
      "season": {"year": 0, "type": 2},
      "status": {"period": 0, "type": {"name": "STATUS_SCHEDULED", "state": "pre", "completed": false}},
      "competitions": [
        {
          "venue": null,
          "competitors": [
            {"homeAway": "home", "score": "0", "team": {"id": "6", "displayName": "Dallas Cowboys", "abbreviation": "DAL"}},
            {"homeAway": "away", "score": "0", "team": {"id": "9", "displayName": "Green Bay Packers", "abbreviation": "GB"}}
          ]
        }
      ]
    },
    {
      "id": "401700103",
      "date": "2025-10-06T00:00Z",
      "status": {"period": 1, "type": {"name": "STATUS_IN_PROGRESS", "state": "in", "completed": false}},
      "competitions": [
        {
          "competitors": [
            {"homeAway": "home", "score": "10", "team": {"id": "9"}}
          ]
        }
      ]
    },
    {
      "id": "401700104",
      "date": "not-a-date",
      "status": {"period": 1, "type": {"name": "STATUS_IN_PROGRESS", "state": "in", "completed": false}},
      "competitions": [
        {
          "competitors": [
            {"homeAway": "home", "score": "10", "team": {"id": "9"}},
            {"homeAway": "away", "score": "7", "team": {"id": "6"}}
          ]
        }
      ]
    }
  ]
}`

func fbWriteJSON(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func fbTestClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(server.URL, 5*time.Second)
}

func TestClientTeams(t *testing.T) {
	var gotPath string
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Query().Get("limit") != "900" {
			t.Errorf("limit = %q, want 900", r.URL.Query().Get("limit"))
		}
		fbWriteJSON(t, w, fbTeamsFixture)
	}))

	resp, fetch, err := client.Teams(context.Background(), "football/nfl")
	if err != nil {
		t.Fatalf("Teams failed: %v", err)
	}
	if gotPath != "/apis/site/v2/sports/football/nfl/teams" {
		t.Errorf("path = %q", gotPath)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK || len(fetch.Body) == 0 {
		t.Fatalf("fetch = %+v", fetch)
	}

	teams, details := NormalizeTeams(model.LeagueNFL, resp)
	if len(teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(teams))
	}
	var gb model.TeamSummary
	for _, tm := range teams {
		if tm.Abbreviation == "GB" {
			gb = tm
		}
	}
	if gb.ID != ids.Team("NFL", "9") {
		t.Errorf("id not minted with ids.Team: %s", gb.ID)
	}
	if gb.Name != "Green Bay Packers" || gb.Location != "Green Bay" || !gb.Active {
		t.Errorf("summary wrong: %+v", gb)
	}
	if gb.ExternalIDs["espn"] != "9" {
		t.Errorf("external id wrong: %+v", gb.ExternalIDs)
	}
	if details[gb.ID].ID != gb.ID {
		t.Errorf("details missing entry for %s", gb.ID)
	}
}

func TestClientTeamsUpstreamError(t *testing.T) {
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))

	_, fetch, err := client.Teams(context.Background(), "football/nfl")
	if err == nil || !strings.Contains(err.Error(), "espn returned 500") {
		t.Fatalf("err = %v, want espn 500 error", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusInternalServerError || string(fetch.Body) != "boom" {
		t.Errorf("fetch not archived on error: %+v", fetch)
	}
}

func TestClientTeamsMalformedBody(t *testing.T) {
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fbWriteJSON(t, w, "{not json")
	}))

	_, fetch, err := client.Teams(context.Background(), "football/nfl")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want decode error", err)
	}
	if fetch == nil || len(fetch.Body) == 0 {
		t.Errorf("malformed body must still be archived: %+v", fetch)
	}
}

func TestClientCreateRequestError(t *testing.T) {
	client := NewClient("http://exa\x00mple.com", time.Second)
	_, fetch, err := client.Teams(context.Background(), "football/nfl")
	if err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("err = %v, want create-request error", err)
	}
	if fetch != nil {
		t.Errorf("fetch = %+v, want nil (request never sent)", fetch)
	}
}

func TestClientExecuteRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fbWriteJSON(t, w, fbTeamsFixture)
	}))
	server.Close() // refuse every connection

	client := NewClient(server.URL, time.Second)
	_, fetch, err := client.Teams(context.Background(), "football/nfl")
	if err == nil || !strings.Contains(err.Error(), "execute request") {
		t.Fatalf("err = %v, want execute-request error", err)
	}
	if fetch != nil {
		t.Errorf("fetch = %+v, want nil (no response received)", fetch)
	}
}

// TestClientScoreboardSameDay covers the from==to case (no "-to" suffix in
// the dates query); TestClientScoreboardRange covers a ranged window.
func TestClientScoreboardSameDay(t *testing.T) {
	var gotQuery string
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fbWriteJSON(t, w, fbScoreboardFixture)
	}))

	day := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC)
	resp, fetch, err := client.Scoreboard(context.Background(), "football/nfl", day, day)
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if !strings.Contains(gotQuery, "dates=20250929") || strings.Contains(gotQuery, "-") {
		t.Errorf("query = %q, want a single date with no range suffix", gotQuery)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK {
		t.Fatalf("fetch = %+v", fetch)
	}
	if len(resp.Events) != 4 {
		t.Fatalf("events = %d, want 4", len(resp.Events))
	}
}

func TestClientScoreboardRange(t *testing.T) {
	var gotQuery string
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		fbWriteJSON(t, w, fbScoreboardFixture)
	}))

	from := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC)
	to := time.Date(2025, 10, 5, 0, 0, 0, 0, time.UTC)
	if _, _, err := client.Scoreboard(context.Background(), "football/college-football", from, to); err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if !strings.Contains(gotQuery, "dates=20250929-20251005") {
		t.Errorf("query = %q, want a ranged dates param", gotQuery)
	}
}

func TestClientScoreboardUpstreamError(t *testing.T) {
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	day := time.Now()
	_, fetch, err := client.Scoreboard(context.Background(), "football/nfl", day, day)
	if err == nil || !strings.Contains(err.Error(), "espn returned 403") {
		t.Fatalf("err = %v, want 403 error", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusForbidden {
		t.Errorf("fetch not archived: %+v", fetch)
	}
}

const fbStandingsFixture = `{
  "children": [
    {
      "name": "AFC",
      "standings": {
        "entries": [
          {"team": {"id": "9", "abbreviation": "GB"}, "stats": [{"type": "total", "summary": "14-3"}]}
        ]
      }
    }
  ]
}`

func TestClientStandings(t *testing.T) {
	var gotPath string
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Query().Get("season") != "2025" {
			t.Errorf("season = %q, want 2025", r.URL.Query().Get("season"))
		}
		fbWriteJSON(t, w, fbStandingsFixture)
	}))

	resp, fetch, err := client.Standings(context.Background(), "football/nfl", 2025)
	if err != nil {
		t.Fatalf("Standings failed: %v", err)
	}
	if gotPath != "/apis/v2/sports/football/nfl/standings" {
		t.Errorf("path = %q, want /apis/v2 prefix", gotPath)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK {
		t.Fatalf("fetch = %+v", fetch)
	}
	records := Records(resp)
	if len(records) != 1 || records[0].Wins != 14 {
		t.Errorf("records = %+v", records)
	}
}

func TestClientStandingsUpstreamError(t *testing.T) {
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	_, fetch, err := client.Standings(context.Background(), "football/nfl", 2025)
	if err == nil || !strings.Contains(err.Error(), "espn returned 502") {
		t.Fatalf("err = %v, want 502 error", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusBadGateway {
		t.Errorf("fetch not archived: %+v", fetch)
	}
}

func fbParseFixture(t *testing.T) *ScoreboardResponse {
	t.Helper()
	var resp ScoreboardResponse
	if err := json.Unmarshal([]byte(fbScoreboardFixture), &resp); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	return &resp
}

func TestNormalizeEventFinal(t *testing.T) {
	resp := fbParseFixture(t)
	game, ok := NormalizeEvent(model.LeagueNFL, resp.Events[0], 2024)
	if !ok {
		t.Fatal("final event should normalize")
	}
	if game.ID != ids.Game("NFL", "401700101") || game.ExternalID != "401700101" {
		t.Errorf("identity wrong: %+v", game)
	}
	if game.HomeTeam.Abbreviation != "GB" || game.AwayTeam.Abbreviation != "DAL" {
		t.Errorf("team refs wrong: %+v / %+v", game.HomeTeam, game.AwayTeam)
	}
	if game.Status != model.GameFinal || game.Season != 2025 || game.SeasonType != model.SeasonRegular {
		t.Errorf("game meta wrong: %+v", game)
	}
	if game.Venue == nil || game.Venue.Name != "Lambeau Field" {
		t.Errorf("venue wrong: %+v", game.Venue)
	}
	if game.HomeScore == nil || *game.HomeScore != 40 || game.AwayScore == nil || *game.AwayScore != 40 {
		t.Fatalf("scores wrong: %+v", game)
	}
	if game.Result == nil || game.Result.ID != game.ID || !game.Result.Overtime {
		t.Errorf("result wrong: %+v", game.Result)
	}
}

func TestNormalizeEventScheduledFallsBackToSeasonYear(t *testing.T) {
	resp := fbParseFixture(t)
	game, ok := NormalizeEvent(model.LeagueNFL, resp.Events[1], 2024)
	if !ok {
		t.Fatal("scheduled event should normalize")
	}
	if game.Season != 2024 {
		t.Errorf("season = %d, want fallback 2024 (event omitted year)", game.Season)
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
	resp := fbParseFixture(t)
	if _, ok := NormalizeEvent(model.LeagueNFL, resp.Events[2], 2024); ok {
		t.Error("event missing its away competitor must not normalize")
	}
}

func TestNormalizeEventBadDate(t *testing.T) {
	resp := fbParseFixture(t)
	if _, ok := NormalizeEvent(model.LeagueNFL, resp.Events[3], 2024); ok {
		t.Error("event with an unparsable date must not normalize")
	}
}

func TestNormalizeEventNoCompetitions(t *testing.T) {
	if _, ok := NormalizeEvent(model.LeagueNFL, Event{ID: "no-comps"}, 2024); ok {
		t.Error("event with no competitions must not normalize")
	}
}

func TestScoreboardUpdates(t *testing.T) {
	resp := fbParseFixture(t)
	now := time.Date(2025, 9, 29, 3, 0, 0, 0, time.UTC)
	updates := ScoreboardUpdates(resp, now)

	if len(updates) != 3 {
		t.Fatalf("updates = %d, want 3 (the missing-competitor event is dropped)", len(updates))
	}

	final := updates["401700101"]
	if final.Status != model.GameFinal || final.HomeScore != 40 || final.AwayScore != 40 {
		t.Errorf("final update wrong: %+v", final)
	}
	if final.Result == nil || !final.Result.Overtime || !final.Result.CompletedAt.Equal(now) {
		t.Errorf("final result wrong: %+v", final.Result)
	}

	scheduled := updates["401700102"]
	if scheduled.Status != model.GameScheduled || scheduled.Result != nil {
		t.Errorf("scheduled update wrong: %+v", scheduled)
	}

	if _, ok := updates["401700103"]; ok {
		t.Error("event missing a competitor must be dropped")
	}
	if _, ok := updates["401700104"]; !ok {
		t.Error("bad-date event must still produce an update (dates are irrelevant here)")
	}
}

// seasonGamesFbFixture serves one clean event with no malformed entries so
// SeasonGames' chunked-range walk and dedup can be asserted precisely.
const seasonGamesFbFixture = `{
  "events": [
    {
      "id": "401700101",
      "date": "2025-09-29T00:00Z",
      "season": {"year": 2025, "type": 2},
      "status": {"period": 4, "type": {"name": "STATUS_FINAL", "state": "post", "completed": true}},
      "competitions": [
        {
          "competitors": [
            {"homeAway": "home", "score": "24", "team": {"id": "9", "displayName": "Green Bay Packers", "abbreviation": "GB"}, "linescores": [{"value": 7}, {"value": 7}, {"value": 7}, {"value": 3}]},
            {"homeAway": "away", "score": "17", "team": {"id": "6", "displayName": "Dallas Cowboys", "abbreviation": "DAL"}, "linescores": [{"value": 0}, {"value": 10}, {"value": 0}, {"value": 7}]}
          ]
        }
      ]
    }
  ]
}`

func TestSeasonGamesWalksChunksAndDedupes(t *testing.T) {
	var calls int
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fbWriteJSON(t, w, seasonGamesFbFixture)
	}))

	start := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 10, 12, 0, 0, 0, 0, time.UTC) // 14 days, chunkDays=7 -> 2 calls
	games, fetches, err := SeasonGames(context.Background(), client, model.LeagueNFL, "football/nfl", 2025, start, end, 7)
	if err != nil {
		t.Fatalf("SeasonGames failed: %v", err)
	}
	if calls != 2 {
		t.Errorf("upstream calls = %d, want 2 (two 7-day chunks)", calls)
	}
	if len(fetches) != 2 {
		t.Errorf("fetches = %d, want 2", len(fetches))
	}
	if len(games) != 1 {
		t.Fatalf("games = %d, want 1 (same event both chunks, deduped)", len(games))
	}
	if games[0].ExternalID != "401700101" {
		t.Errorf("game = %+v", games[0])
	}
}

func TestSeasonGamesPropagatesUpstreamError(t *testing.T) {
	client := fbTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	start := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC)
	games, fetches, err := SeasonGames(context.Background(), client, model.LeagueNFL, "football/nfl", 2025, start, start, 7)
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
	teams, details := NormalizeTeams(model.LeagueNFL, &TeamsResponse{})
	if teams != nil || len(details) != 0 {
		t.Errorf("empty response must yield no teams: %+v / %+v", teams, details)
	}
}
