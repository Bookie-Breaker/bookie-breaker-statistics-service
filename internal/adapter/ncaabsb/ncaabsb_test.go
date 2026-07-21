package ncaabsb

// Golden-fixture tests over real ESPN college baseball responses recorded
// 2026-07-05. The league is in its off-season, so the fixtures mix eras of
// the real 2026 season: scoreboard_offseason.json is the day's live (empty)
// response, scoreboard_cws.json and scoreboard_regular.json come from ESPN's
// archive (the June 21 College World Series final and the May 21–22 regular
// season, including a real 10-inning game), and standings.json is the
// completed 2026 season standings, still served live. No derived fixtures.

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func testProvider(t *testing.T, handler http.Handler) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewProvider(NewClient(server.URL, 5*time.Second))
}

func serveFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

// espnMux routes ESPN requests to the recorded fixtures: the schedule week
// containing May 21–22 to the regular-season capture, the week containing
// the June 21 CWS final to its capture, and every other week to the real
// off-season (empty) response.
func espnMux(t *testing.T, scoreboardCalls *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case strings.HasSuffix(r.URL.Path, "/standings"):
			serveFixture(t, w, "standings.json")
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			if scoreboardCalls != nil {
				scoreboardCalls.Add(1)
			}
			switch dates := r.URL.Query().Get("dates"); {
			case strings.HasPrefix(dates, "20260517"):
				serveFixture(t, w, "scoreboard_regular.json")
			case strings.HasPrefix(dates, "20260621"):
				serveFixture(t, w, "scoreboard_cws.json")
			default:
				serveFixture(t, w, "scoreboard_offseason.json")
			}
		default:
			http.NotFound(w, r)
		}
	})
}

const leagueBSB = leagueNCAABSB

func TestTeamsNormalization(t *testing.T) {
	p := testProvider(t, espnMux(t, nil))

	teams, details, fetches, err := p.Teams(context.Background())
	if err != nil {
		t.Fatalf("Teams failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
	if len(teams) != 4 || len(details) != 4 {
		t.Fatalf("teams = %d, details = %d, want 4 each", len(teams), len(details))
	}

	var ucla model.TeamSummary
	for _, team := range teams {
		if team.Abbreviation == "UCLA" {
			ucla = team
		}
	}
	if ucla.ID != ids.Team(leagueBSB, "66") {
		t.Errorf("id = %s, want UUIDv5 of espn team 66", ucla.ID)
	}
	if ucla.Name != "UCLA Bruins" || ucla.League != model.LeagueNCAABSB || !ucla.Active {
		t.Errorf("summary wrong: %+v", ucla)
	}
	if ucla.ExternalIDs["espn"] != "66" {
		t.Errorf("external ids wrong: %+v", ucla.ExternalIDs)
	}
	if details[ucla.ID].Venue != nil {
		t.Errorf("college teams carry no venue, got %+v", details[ucla.ID].Venue)
	}
}

func TestScheduleSeasonWalkAndNormalization(t *testing.T) {
	var scoreboardCalls atomic.Int32
	p := testProvider(t, espnMux(t, &scoreboardCalls))

	games, _, err := p.Schedule(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	// February 1 – July 1 in week chunks: 22 ranged scoreboard calls (ESPN
	// truncates month-sized ranges at 1000 events, verified live).
	if scoreboardCalls.Load() != 22 {
		t.Errorf("scoreboard calls = %d, want 22 weekly chunks", scoreboardCalls.Load())
	}
	if len(games) != 3 {
		t.Fatalf("games = %d, want 3 (2 regular + 1 CWS)", len(games))
	}

	byExternal := make(map[string]model.Game, len(games))
	for _, g := range games {
		byExternal[g.ExternalID] = g
	}

	// College World Series final: North Carolina 6-2 at Oklahoma.
	cws := byExternal["401874452"]
	if cws.SeasonType != model.SeasonPostseason {
		t.Errorf("championship-series season type = %s, want POSTSEASON", cws.SeasonType)
	}
	if cws.ID != ids.Game(leagueBSB, "401874452") || cws.Season != 2026 {
		t.Errorf("CWS identity wrong: %+v", cws)
	}
	if cws.HomeTeam.ID != ids.Team(leagueBSB, "112") || cws.HomeTeam.Abbreviation != "OU" {
		t.Errorf("home team ref wrong: %+v", cws.HomeTeam)
	}
	if cws.Venue == nil || cws.Venue.Name != "Charles Schwab Field" {
		t.Errorf("venue wrong: %+v", cws.Venue)
	}
	if cws.Status != model.GameFinal || cws.Result == nil {
		t.Fatalf("CWS final wrong: %+v", cws)
	}
	if cws.Result.HomeScore != 2 || cws.Result.AwayScore != 6 || cws.Result.Overtime {
		t.Errorf("CWS result wrong: %+v", cws.Result)
	}
	if len(cws.Result.PeriodScores) != 9 || cws.Result.PeriodScores[2].Away != 3 {
		t.Errorf("CWS period scores wrong: %+v", cws.Result.PeriodScores)
	}
	if cws.Result.ID != cws.ID {
		t.Errorf("result id = %s, want game id %s", cws.Result.ID, cws.ID)
	}

	// Real 10-inning regular-season game (Eastern Illinois 3-2 at SIUE).
	extras := byExternal["401869640"]
	if extras.SeasonType != model.SeasonRegular || extras.Result == nil {
		t.Fatalf("regular extra-innings game wrong: %+v", extras)
	}
	if !extras.Result.Overtime || len(extras.Result.PeriodScores) != 10 {
		t.Errorf("extra-innings result wrong: %+v", extras.Result)
	}
	if extras.Result.HomeScore != 2 || extras.Result.AwayScore != 3 {
		t.Errorf("extra-innings scores wrong: %+v", extras.Result)
	}
	if extras.Result.RegulationHomeScore != nil || extras.Result.RegulationAwayScore != nil {
		t.Errorf("baseball must carry no regulation fields: %+v", extras.Result)
	}

	// High Point won 9-6 without batting in the bottom of the ninth: its
	// linescore list is one entry short of the away list (real 2026 data);
	// the ninth period still appears with home 0.
	truncated := byExternal["401869692"]
	if truncated.Result == nil || len(truncated.Result.PeriodScores) != 9 {
		t.Fatalf("truncated-ninth result wrong: %+v", truncated.Result)
	}
	ninth := truncated.Result.PeriodScores[8]
	if ninth.Home != 0 || ninth.Away != 0 {
		t.Errorf("unplayed bottom ninth wrong: %+v", ninth)
	}
	if truncated.Result.HomeScore != 9 || truncated.Result.AwayScore != 6 {
		t.Errorf("truncated-ninth scores wrong: %+v", truncated.Result)
	}
}

func TestScoreboardOffseasonEmpty(t *testing.T) {
	p := testProvider(t, espnMux(t, nil))

	// The real 2026-07-05 off-season response: no events, no error.
	updates, fetches, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if len(updates) != 0 {
		t.Errorf("off-season updates = %d, want 0", len(updates))
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
}

func TestScoreboardFinal(t *testing.T) {
	p := testProvider(t, espnMux(t, nil))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	final := updates["401874452"]
	if final.Status != model.GameFinal || final.Result == nil {
		t.Fatalf("final update wrong: %+v", final)
	}
	if final.HomeScore != 2 || final.AwayScore != 6 || final.Result.TotalScore != 8 || final.Result.Margin != -4 {
		t.Errorf("final scores wrong: %+v", final.Result)
	}
	if final.Result.Overtime {
		t.Errorf("nine-inning game must not be overtime: %+v", final.Result)
	}
}

func TestTeamStatsStandings(t *testing.T) {
	p := testProvider(t, espnMux(t, nil))

	stats, _, err := p.TeamStats(context.Background(), 2026, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("teams = %d, want 3", len(stats))
	}

	// UCLA's real completed 2026 season: 51-6 over 57 games, 463 runs
	// scored, 214 allowed.
	ucla := stats[ids.Team(leagueBSB, "66")]
	if ucla.GamesPlayed != 57 || ucla.Wins != 51 || ucla.Losses != 6 || ucla.Season != 2026 {
		t.Errorf("UCLA record wrong: %+v", ucla)
	}
	if ucla.TeamAbbreviation != "UCLA" {
		t.Errorf("abbreviation wrong: %+v", ucla)
	}
	if ucla.Stats.Offensive != nil || ucla.Stats.Defensive != nil || ucla.Stats.Advanced != nil || ucla.Stats.Soccer != nil {
		t.Error("non-baseball blocks must stay nil")
	}
	bb := ucla.Stats.Baseball
	if bb == nil {
		t.Fatal("baseball block missing")
	}
	if math.Abs(bb.RunsScoredPerGame-463.0/57.0) > 1e-9 {
		t.Errorf("runs scored per game = %v, want 463/57", bb.RunsScoredPerGame)
	}
	if math.Abs(bb.RunsAllowedPerGame-214.0/57.0) > 1e-9 {
		t.Errorf("runs allowed per game = %v, want 214/57", bb.RunsAllowedPerGame)
	}
	// ESPN carries no college wOBA/FIP inputs: the unsourceable fields stay
	// zero (documented descope; the league is dormant).
	if bb.TeamWOBA != 0 || bb.TeamFIP != 0 || bb.BullpenERA != 0 || bb.TeamERA != 0 {
		t.Errorf("unsourceable fields must stay zero: %+v", bb)
	}
}

func TestTeamStatsRollingWindowUnsupported(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2026, 10); err == nil ||
		!strings.Contains(err.Error(), "unsupported for college baseball") {
		t.Errorf("window > 0 must error, got %v", err)
	}
}

func TestStatusMapping(t *testing.T) {
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
	}
	for _, tt := range tests {
		if got := mapStatus(tt.name, tt.state, tt.completed); got != tt.want {
			t.Errorf("mapStatus(%q, %q, %v) = %s, want %s", tt.name, tt.state, tt.completed, got, tt.want)
		}
	}
}

func TestSeasonTypeMapping(t *testing.T) {
	for slug, want := range map[string]model.SeasonType{
		"regular-season":      model.SeasonRegular,
		"":                    model.SeasonRegular,
		"preseason":           model.SeasonPreseason,
		"championship-series": model.SeasonPostseason,
	} {
		if got := mapSeasonType(slug); got != want {
			t.Errorf("mapSeasonType(%q) = %s, want %s", slug, got, want)
		}
	}
}

func TestPlayersDescoped(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))

	summaries, details, fetches, err := p.Players(context.Background(), 2026)
	if err != nil || summaries == nil || details == nil || len(summaries) != 0 || len(details) != 0 || fetches != nil {
		t.Errorf("Players must return empty collections with nil error: %v %v %v %v", summaries, details, fetches, err)
	}

	log, fetches, err := p.PlayerGameLog(context.Background(), model.PlayerDetail{}, 2026)
	if err != nil || log == nil || len(log) != 0 || fetches != nil {
		t.Errorf("PlayerGameLog must return an empty log with nil error: %v %v %v", log, fetches, err)
	}
}

func TestSeasonYear(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))
	for now, want := range map[time.Time]int{
		time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC):  2026, // in season
		time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC):   2026, // off-season, season just completed
		time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC): 2026,
		time.Date(2027, 2, 20, 0, 0, 0, 0, time.UTC):  2027, // next season underway
	} {
		if got := p.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}

func TestProviderIdentity(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))
	if p.League() != model.LeagueNCAABSB {
		t.Errorf("League() = %s, want NCAA_BSB", p.League())
	}
	if p.Source() != sourceESPN {
		t.Errorf("Source() = %s, want %s", p.Source(), sourceESPN)
	}
}

func TestBoxScoreNotSupported(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))
	if _, _, err := p.BoxScore(context.Background(), "1"); err == nil {
		t.Error("BoxScore must return an error (tracked Phase 7 deferral)")
	}
}

func upstream500(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
}

func TestTeamsUpstreamError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, _, err := p.Teams(context.Background()); err == nil {
		t.Error("upstream 500 on teams must error")
	}
}

func TestTeamsMalformedJSON(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid`))
	}))
	if _, _, _, err := p.Teams(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("malformed JSON must produce a decode error, got %v", err)
	}
}

func TestTeamStatsStandingsError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, err := p.TeamStats(context.Background(), 2026, 0); err == nil {
		t.Error("upstream 500 on standings must error")
	}
}

func TestScheduleUpstreamError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, err := p.Schedule(context.Background(), 2026); err == nil || !strings.Contains(err.Error(), "fetch scoreboard") {
		t.Errorf("upstream 500 on scoreboard must error, got %v", err)
	}
}

func TestScoreboardUpstreamError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("upstream 500 on scoreboard must error")
	}
}

func TestScoreboardMalformedEventSkipped(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"id":"malformed"}]}`))
	}))
	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if _, ok := updates["malformed"]; ok {
		t.Error("event with no competitors should be skipped, not surfaced")
	}
}

func TestScheduleDedupesRepeatedEvents(t *testing.T) {
	const eventJSON = `{"events":[{"id":"999","date":"2026-02-15T18:00Z","season":{"slug":"regular-season"},
		"status":{"period":9,"type":{"name":"STATUS_FINAL","state":"post","completed":true}},
		"competitions":[{"competitors":[
			{"homeAway":"home","score":"1","team":{"id":"1","displayName":"Home U","abbreviation":"HOM"}},
			{"homeAway":"away","score":"0","team":{"id":"2","displayName":"Away U","abbreviation":"AWY"}}]}]}]}`

	var scoreboardCalls atomic.Int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scoreboardCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(eventJSON))
	}))

	games, _, err := p.Schedule(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	if scoreboardCalls.Load() < 2 {
		t.Fatalf("scoreboard calls = %d, want at least 2 weekly chunks", scoreboardCalls.Load())
	}
	if len(games) != 1 {
		t.Errorf("games = %d, want 1 (the repeated event across chunks must be deduplicated)", len(games))
	}
}

func TestMapStatusPostNotCompleted(t *testing.T) {
	if got := mapStatus("STATUS_HALFTIME", "post", false); got != model.GamePostponed {
		t.Errorf("mapStatus post-but-not-completed = %s, want POSTPONED", got)
	}
}

func TestHomeAwayCompetitorsEmpty(t *testing.T) {
	if _, _, ok := homeAwayCompetitors(espnEvent{}); ok {
		t.Error("empty event should not resolve home/away competitors")
	}
}

func TestNormalizeEventMalformed(t *testing.T) {
	if _, ok := normalizeEvent(espnEvent{ID: "1", Date: "2026-02-15T18:00Z"}, 2026); ok {
		t.Error("event with no competitors should not normalize")
	}
	ev := espnEvent{
		ID:   "2",
		Date: "not-a-date",
		Competitions: []espnCompetition{{Competitors: []espnCompetitor{
			{HomeAway: "home", Team: espnTeam{ID: "1"}},
			{HomeAway: "away", Team: espnTeam{ID: "2"}},
		}}},
	}
	if _, ok := normalizeEvent(ev, 2026); ok {
		t.Error("event with unparseable date should not normalize")
	}
}

func TestStatValueMissing(t *testing.T) {
	e := standingsEntry{Stats: []standingStat{{Name: "wins", Value: 5}}}
	if got := statValue(e, "missing"); got != 0 {
		t.Errorf("statValue for missing name = %v, want 0", got)
	}
}
