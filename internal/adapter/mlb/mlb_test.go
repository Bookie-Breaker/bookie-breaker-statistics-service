package mlb

// Golden-fixture tests over real MLB StatsAPI responses recorded 2026-07-05
// during the live 2026 season (testdata/*.json, trimmed). Every fixture is a
// real capture: the extra-innings finals (gamePks 824417 and 824621) and the
// truncated bottom-of-the-ninth game (823118) come from the July 3–4 slates,
// the postponed/makeup pair from April 2–3, and the probable pitchers from
// the July 6 schedule. The Wave 3 box-score and roster fixtures were
// recorded 2026-07-19: boxscore.json is gamePk 823372 (LAD 8 @ PIT 9,
// 2026-06-10 — a real Ohtani two-way game: HR at the plate, 6 2/3 IP on the
// mound) with Jake Mangum's "triples" key trimmed out to pin missing-stat
// handling; roster_119.json and the person_* season hydrates are the live
// Dodgers active roster trimmed to Betts, Ohtani, and Yamamoto.

import (
	"context"
	"encoding/json"
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

// statsAPIMux routes StatsAPI requests to the recorded fixtures.
func statsAPIMux(t *testing.T, personCalls *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/teams":
			serveFixture(t, w, "teams.json")
		case r.URL.Path == "/schedule" && r.URL.Query().Get("season") != "":
			serveFixture(t, w, "schedule.json")
		case strings.HasPrefix(r.URL.Path, "/people/"):
			if personCalls != nil {
				personCalls.Add(1)
			}
			switch strings.TrimPrefix(r.URL.Path, "/people/") {
			case "650911":
				serveFixture(t, w, "person_650911.json")
			case "702070":
				serveFixture(t, w, "person_702070.json")
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	})
}

func TestTeamsNormalization(t *testing.T) {
	p := testProvider(t, statsAPIMux(t, nil))

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

	var athletics model.TeamSummary
	for _, team := range teams {
		if team.Abbreviation == "ATH" {
			athletics = team
		}
	}
	if athletics.ID != ids.Team(leagueMLB, "133") {
		t.Errorf("id = %s, want UUIDv5 of statsapi team 133", athletics.ID)
	}
	if athletics.Name != "Athletics" || athletics.Location != "Sacramento" || !athletics.Active {
		t.Errorf("summary wrong: %+v", athletics)
	}
	if athletics.Conference != "American League" || athletics.Division != "American League West" {
		t.Errorf("league/division mapping wrong: %+v", athletics)
	}
	if athletics.ExternalIDs["mlb"] != "133" {
		t.Errorf("external ids wrong: %+v", athletics.ExternalIDs)
	}

	detail := details[athletics.ID]
	if detail.Venue == nil || detail.Venue.Name != "Sutter Health Park" || detail.Venue.ID != ids.Venue(leagueMLB, "2529") {
		t.Errorf("venue wrong: %+v", detail.Venue)
	}
	if detail.VenueID != detail.Venue.ID {
		t.Errorf("summary venue id = %s, want %s", detail.VenueID, detail.Venue.ID)
	}
}

func TestScheduleNormalization(t *testing.T) {
	var personCalls atomic.Int32
	p := testProvider(t, statsAPIMux(t, &personCalls))

	games, _, err := p.Schedule(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	// Fixture: spring final + postponed/makeup pair (dedup to one) + two
	// regular finals + one scheduled game; the all-star game is skipped.
	if len(games) != 5 {
		t.Fatalf("games = %d, want 5", len(games))
	}
	// Only the scheduled game's two announced probables need stats calls;
	// past finals embed name and id without one.
	if personCalls.Load() != 2 {
		t.Errorf("person calls = %d, want 2 (upcoming probables only)", personCalls.Load())
	}

	byExternal := make(map[string]model.Game, len(games))
	for _, g := range games {
		byExternal[g.ExternalID] = g
	}
	if _, ok := byExternal["823443"]; ok {
		t.Error("all-star game must be skipped")
	}

	spring := byExternal["832034"]
	if spring.SeasonType != model.SeasonPreseason || spring.Status != model.GameFinal {
		t.Errorf("spring game wrong: %+v", spring)
	}

	// The April 2 postponement was made up on April 3 under the same gamePk:
	// exactly one canonical game, carrying the makeup's final result.
	makeup := byExternal["824621"]
	if makeup.Status != model.GameFinal || makeup.Result == nil {
		t.Fatalf("postponed/makeup dedup wrong: %+v", makeup)
	}
	if !makeup.ScheduledStart.Equal(time.Date(2026, 4, 3, 18, 10, 0, 0, time.UTC)) {
		t.Errorf("makeup must keep the rescheduled date, got %s", makeup.ScheduledStart)
	}

	// White Sox 3-4 Guardians in 10 innings.
	extras := byExternal["824417"]
	if extras.ID != ids.Game(leagueMLB, "824417") || extras.Season != 2026 || extras.SeasonType != model.SeasonRegular {
		t.Errorf("extra-innings game identity wrong: %+v", extras)
	}
	if extras.HomeTeam.ID != ids.Team(leagueMLB, "114") || extras.HomeTeam.Name != "Cleveland Guardians" || extras.HomeTeam.Abbreviation != "CLE" {
		t.Errorf("home team ref wrong: %+v", extras.HomeTeam)
	}
	if extras.Venue == nil || extras.Venue.Name != "Progressive Field" {
		t.Errorf("venue wrong: %+v", extras.Venue)
	}
	if extras.Result == nil || !extras.Result.Overtime || len(extras.Result.PeriodScores) != 10 {
		t.Fatalf("extra-innings result wrong: %+v", extras.Result)
	}
	if extras.Result.HomeScore != 4 || extras.Result.AwayScore != 3 || extras.Result.PeriodScores[9].Home != 1 {
		t.Errorf("extra-innings scores wrong: %+v", extras.Result)
	}
	if extras.Result.RegulationHomeScore != nil || extras.Result.RegulationAwayScore != nil {
		t.Errorf("baseball must carry no regulation fields: %+v", extras.Result)
	}
	if extras.Result.ID != extras.ID {
		t.Errorf("result id = %s, want game id %s", extras.Result.ID, extras.ID)
	}
	// Past final: probables carry name and external id only.
	if extras.AwayProbablePitcher == nil || extras.AwayProbablePitcher.Name != "Anthony Kay" || extras.AwayProbablePitcher.ERA != 0 {
		t.Errorf("past-final probable must be name-only: %+v", extras.AwayProbablePitcher)
	}

	// Mariners 11-0: the home ninth was not played (no runs key in the
	// linescore); the period-score sum still equals the final score.
	shutout := byExternal["823118"]
	if shutout.Result == nil || shutout.Result.Overtime || len(shutout.Result.PeriodScores) != 9 {
		t.Fatalf("nine-inning result wrong: %+v", shutout.Result)
	}
	if shutout.Result.HomeScore != 11 || shutout.Result.AwayScore != 0 {
		t.Errorf("shutout scores wrong: %+v", shutout.Result)
	}
	sum := 0
	for _, ps := range shutout.Result.PeriodScores {
		sum += ps.Home
	}
	if sum != 11 {
		t.Errorf("period-score sum = %d, want the final score 11", sum)
	}
}

func TestScheduleProbablePitchers(t *testing.T) {
	p := testProvider(t, statsAPIMux(t, nil))

	games, _, err := p.Schedule(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	var upcoming model.Game
	for _, g := range games {
		if g.ExternalID == "824089" {
			upcoming = g
		}
	}
	if upcoming.Status != model.GameScheduled {
		t.Fatalf("upcoming game wrong: %+v", upcoming)
	}

	away := upcoming.AwayProbablePitcher
	if away == nil {
		t.Fatal("away probable pitcher missing")
	}
	if away.Name != "Cristopher Sánchez" || away.ExternalID != "650911" || away.Throws != "L" {
		t.Errorf("away probable identity wrong: %+v", away)
	}
	if away.ERA != 2.00 || away.InningsPitched != 117.0 {
		t.Errorf("away probable rates wrong: %+v", away)
	}
	// FIP hand-computed from the real season line (HR 8, BB 23, HBP 2,
	// SO 136, IP 117.0): (13*8 + 3*(23+2) - 2*136)/117 + 3.10 = -93/117 + 3.10.
	wantFIP := -93.0/117.0 + 3.10
	if math.Abs(away.FIP-wantFIP) > 1e-9 {
		t.Errorf("away FIP = %v, want %v", away.FIP, wantFIP)
	}
	// K-BB% from batters faced: (136 - 23) / 475.
	if math.Abs(away.KBBPct-113.0/475.0) > 1e-9 {
		t.Errorf("away K-BB%% = %v, want 113/475", away.KBBPct)
	}

	home := upcoming.HomeProbablePitcher
	if home == nil || home.Name != "Noah Cameron" || home.Throws != "L" {
		t.Fatalf("home probable wrong: %+v", home)
	}
	// IP "83.2" is 83 and two thirds.
	if math.Abs(home.InningsPitched-(83.0+2.0/3.0)) > 1e-9 {
		t.Errorf("home innings pitched = %v, want 83 2/3", home.InningsPitched)
	}
	// (13*10 + 3*(25+2) - 2*75)/(83 2/3) + 3.10.
	wantFIP = 61.0/(83.0+2.0/3.0) + 3.10
	if math.Abs(home.FIP-wantFIP) > 1e-9 {
		t.Errorf("home FIP = %v, want %v", home.FIP, wantFIP)
	}
}

func TestScoreboardFinalsExtraInnings(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFixture(t, w, "scoreboard_final.json")
	}))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}

	// White Sox 3-4 Guardians, decided in the bottom of the 10th (real
	// extra-innings final from the July 3 slate).
	final := updates["824417"]
	if final.Status != model.GameFinal || final.Result == nil {
		t.Fatalf("final update wrong: %+v", final)
	}
	if final.HomeScore != 4 || final.AwayScore != 3 {
		t.Errorf("final scores wrong: %+v", final)
	}
	if !final.Result.Overtime || len(final.Result.PeriodScores) != 10 {
		t.Errorf("extra-innings result wrong: %+v", final.Result)
	}
	if final.Result.TotalScore != 7 || final.Result.Margin != 1 {
		t.Errorf("result math wrong: %+v", final.Result)
	}
	if final.Result.RegulationHomeScore != nil || final.Result.RegulationAwayScore != nil {
		t.Errorf("baseball must carry no regulation fields: %+v", final.Result)
	}
}

func TestScoreboardLiveAndFinal(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFixture(t, w, "scoreboard_live.json")
	}))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}

	live := updates["824010"]
	if live.Status != model.GameInProgress || live.HomeScore != 3 || live.AwayScore != 7 {
		t.Errorf("in-progress update wrong: %+v", live)
	}
	if live.Result != nil {
		t.Errorf("in-progress game must carry no result: %+v", live.Result)
	}

	nine := updates["824902"]
	if nine.Status != model.GameFinal || nine.Result == nil {
		t.Fatalf("nine-inning final wrong: %+v", nine)
	}
	if nine.Result.HomeScore != 9 || nine.Result.AwayScore != 10 || nine.Result.Overtime {
		t.Errorf("nine-inning result wrong: %+v", nine.Result)
	}
}

func TestScoreboardPreview(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFixture(t, w, "scoreboard_preview.json")
	}))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	preview := updates["824089"]
	if preview.Status != model.GameScheduled || preview.Result != nil {
		t.Errorf("preview update wrong: %+v", preview)
	}
}

func TestTeamStats(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/teams/stats" && q.Get("stats") == "statSplits":
			if q.Get("sitCodes") != "rp" {
				t.Errorf("sitCodes = %s, want rp", q.Get("sitCodes"))
			}
			serveFixture(t, w, "bullpen_rp.json")
		case r.URL.Path == "/teams/stats" && q.Get("group") == "hitting":
			serveFixture(t, w, "teamstats_hitting.json")
		case r.URL.Path == "/teams/stats" && q.Get("group") == "pitching":
			serveFixture(t, w, "teamstats_pitching.json")
		case r.URL.Path == "/standings":
			serveFixture(t, w, "standings.json")
		case r.URL.Path == "/teams":
			serveFixture(t, w, "teams.json")
		default:
			http.NotFound(w, r)
		}
	}))

	stats, _, err := p.TeamStats(context.Background(), 2026, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("teams = %d, want 2 (Dodgers and Brewers)", len(stats))
	}

	lad := stats[ids.Team(leagueMLB, "119")]
	if lad.Season != 2026 || lad.GamesPlayed != 91 || lad.Wins != 59 || lad.Losses != 32 {
		t.Errorf("Dodgers record wrong: %+v", lad)
	}
	if lad.Stats.Offensive != nil || lad.Stats.Defensive != nil || lad.Stats.Advanced != nil || lad.Stats.Soccer != nil {
		t.Error("non-baseball blocks must stay nil for MLB")
	}
	bb := lad.Stats.Baseball
	if bb == nil {
		t.Fatal("baseball block missing")
	}

	// Hand checks from the real 2026-07-05 Dodgers season line.
	if math.Abs(bb.RunsScoredPerGame-483.0/91.0) > 1e-9 {
		t.Errorf("runs scored per game = %v, want 483/91", bb.RunsScoredPerGame)
	}
	if math.Abs(bb.RunsAllowedPerGame-320.0/91.0) > 1e-9 {
		t.Errorf("runs allowed per game = %v, want 320/91", bb.RunsAllowedPerGame)
	}
	// wOBA hand-computed with the 2026 weights: singles = 808-154-9-122 =
	// 523; numerator = 0.69*375 + 0.72*34 + 0.88*523 + 1.26*154 + 1.60*9 +
	// 2.07*122; denominator = 3061 + 375 - 14 + 34 + 34.
	wantWOBA := (0.69*375 + 0.72*34 + 0.88*523 + 1.26*154 + 1.60*9 + 2.07*122) / (3061 + 375 - 14 + 34 + 34)
	if math.Abs(bb.TeamWOBA-wantWOBA) > 1e-9 {
		t.Errorf("wOBA = %v, want %v", bb.TeamWOBA, wantWOBA)
	}
	if bb.TeamOBP != 0.347 || bb.TeamSLG != 0.440 {
		t.Errorf("OBP/SLG wrong: %+v", bb)
	}
	if math.Abs(bb.BattingStrikeoutPct-715.0/3517.0) > 1e-9 || math.Abs(bb.BattingWalkPct-375.0/3517.0) > 1e-9 {
		t.Errorf("K%%/BB%% wrong: %+v", bb)
	}
	if bb.TeamERA != 3.49 {
		t.Errorf("ERA = %v, want 3.49", bb.TeamERA)
	}
	// Team FIP hand-computed: (13*97 + 3*(266+34) - 2*809)/(802 1/3) + 3.10.
	wantFIP := 543.0/(802.0+1.0/3.0) + 3.10
	if math.Abs(bb.TeamFIP-wantFIP) > 1e-9 {
		t.Errorf("FIP = %v, want %v", bb.TeamFIP, wantFIP)
	}
	// Reliever-only split from the league-wide statSplits call.
	if bb.BullpenERA != 3.77 {
		t.Errorf("bullpen ERA = %v, want 3.77", bb.BullpenERA)
	}

	mil := stats[ids.Team(leagueMLB, "158")]
	if mil.Wins != 55 || mil.Losses != 33 || mil.GamesPlayed != 88 {
		t.Errorf("Brewers record wrong: %+v", mil)
	}
	if mil.Stats.Baseball == nil || mil.Stats.Baseball.BullpenERA != 3.48 {
		t.Errorf("Brewers bullpen ERA wrong: %+v", mil.Stats.Baseball)
	}
	if mil.Stats.Baseball.TeamERA != 3.35 {
		t.Errorf("Brewers team ERA wrong: %+v", mil.Stats.Baseball)
	}
}

func TestTeamStatsBullpenFallback(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/teams/stats" && q.Get("stats") == "statSplits":
			http.Error(w, "boom", http.StatusInternalServerError)
		case r.URL.Path == "/teams/stats" && q.Get("group") == "hitting":
			serveFixture(t, w, "teamstats_hitting.json")
		case r.URL.Path == "/teams/stats" && q.Get("group") == "pitching":
			serveFixture(t, w, "teamstats_pitching.json")
		case r.URL.Path == "/standings":
			serveFixture(t, w, "standings.json")
		case r.URL.Path == "/teams":
			serveFixture(t, w, "teams.json")
		default:
			http.NotFound(w, r)
		}
	}))

	stats, _, err := p.TeamStats(context.Background(), 2026, 0)
	if err != nil {
		t.Fatalf("TeamStats must survive a bullpen-split outage: %v", err)
	}
	lad := stats[ids.Team(leagueMLB, "119")]
	if lad.Stats.Baseball.BullpenERA != lad.Stats.Baseball.TeamERA {
		t.Errorf("bullpen ERA must fall back to team ERA, got %v vs %v",
			lad.Stats.Baseball.BullpenERA, lad.Stats.Baseball.TeamERA)
	}
}

func TestTeamStatsRollingWindowUnsupported(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2026, 10); err == nil ||
		!strings.Contains(err.Error(), "unsupported for MLB") {
		t.Errorf("window > 0 must error, got %v", err)
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		abstract, detailed string
		want               model.GameStatus
	}{
		{"Preview", "Scheduled", model.GameScheduled},
		{"Preview", "Pre-Game", model.GameScheduled},
		{"Live", "In Progress", model.GameInProgress},
		{"Final", "Final", model.GameFinal},
		{"Final", "Completed Early", model.GameFinal},
		// Verified live: postponed games report abstractGameState "Final".
		{"Final", "Postponed", model.GamePostponed},
		{"Final", "Cancelled", model.GameCancelled}, //nolint:misspell // contract enum spelling
		{"Live", "Suspended", model.GameSuspended},
	}
	for _, tt := range tests {
		if got := mapStatus(tt.abstract, tt.detailed); got != tt.want {
			t.Errorf("mapStatus(%q, %q) = %s, want %s", tt.abstract, tt.detailed, got, tt.want)
		}
	}
}

func TestSeasonTypeMapping(t *testing.T) {
	for gameType, want := range map[string]model.SeasonType{
		"S": model.SeasonPreseason,
		"R": model.SeasonRegular,
		"F": model.SeasonPostseason,
		"D": model.SeasonPostseason,
		"L": model.SeasonPostseason,
		"W": model.SeasonPostseason,
	} {
		got, ok := mapSeasonType(gameType)
		if !ok || got != want {
			t.Errorf("mapSeasonType(%q) = %s/%v, want %s", gameType, got, ok, want)
		}
	}
	for _, skipped := range []string{"A", "E"} {
		if _, ok := mapSeasonType(skipped); ok {
			t.Errorf("mapSeasonType(%q) must be skipped", skipped)
		}
	}
}

func TestParseInnings(t *testing.T) {
	tests := map[string]float64{
		"117.0": 117,
		"104.1": 104 + 1.0/3.0,
		"83.2":  83 + 2.0/3.0,
		"0.0":   0,
		"12":    12,
		"":      0,
		"x.y":   0,
		"10.5":  10, // out-of-range thirds fall back to the whole number
		"10.a":  10, // unparseable thirds fall back to the whole number
	}
	for in, want := range tests {
		if got := parseInnings(in); math.Abs(got-want) > 1e-9 {
			t.Errorf("parseInnings(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseRate(t *testing.T) {
	if got := parseRate(".347"); got != 0.347 {
		t.Errorf("parseRate(.347) = %v", got)
	}
	if got := parseRate("3.49"); got != 3.49 {
		t.Errorf("parseRate(3.49) = %v", got)
	}
	// StatsAPI's empty-season placeholder.
	if got := parseRate("-.--"); got != 0 {
		t.Errorf("parseRate(-.--) = %v, want 0", got)
	}
}

func TestPlayerGameLogDescoped(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))

	log, fetches, err := p.PlayerGameLog(context.Background(), model.PlayerDetail{}, 2026)
	if err != nil || log == nil || len(log) != 0 || fetches != nil {
		t.Errorf("PlayerGameLog must return an empty log with nil error: %v %v %v", log, fetches, err)
	}
}

// rosterMux routes the Players surface to the recorded fixtures: the team
// list, the real Dodgers roster (empty rosters elsewhere), and the three
// dual-group person hydrations.
func rosterMux(t *testing.T, peopleStatus int) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/teams":
			serveFixture(t, w, "teams.json")
		case strings.HasPrefix(r.URL.Path, "/teams/") && strings.HasSuffix(r.URL.Path, "/roster"):
			if r.URL.Query().Get("rosterType") != "active" {
				t.Errorf("rosterType = %s, want active", r.URL.Query().Get("rosterType"))
			}
			if r.URL.Path == "/teams/119/roster" {
				serveFixture(t, w, "roster_119.json")
			} else {
				_, _ = w.Write([]byte(`{"roster":[]}`))
			}
		case strings.HasPrefix(r.URL.Path, "/people/"):
			if peopleStatus != http.StatusOK {
				http.Error(w, "boom", peopleStatus)
				return
			}
			if h := r.URL.Query().Get("hydrate"); h != "stats(group=[hitting,pitching],type=[season],season=2026)" {
				t.Errorf("hydrate = %s, want dual-group season stats", h)
			}
			serveFixture(t, w, "person_"+strings.TrimPrefix(r.URL.Path, "/people/")+".json")
		default:
			http.NotFound(w, r)
		}
	})
}

func TestPlayersRosterAndRates(t *testing.T) {
	p := testProvider(t, rosterMux(t, http.StatusOK))

	summaries, details, fetches, err := p.Players(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Players failed: %v", err)
	}
	// One team list + four rosters + three person hydrations.
	if len(fetches) != 8 {
		t.Errorf("fetches = %d, want 8", len(fetches))
	}
	if len(summaries) != 3 || len(details) != 3 {
		t.Fatalf("summaries = %d, details = %d, want 3 each", len(summaries), len(details))
	}

	byLast := make(map[string]model.PlayerSummary, len(summaries))
	for _, s := range summaries {
		byLast[s.LastName] = s
	}

	ohtani := byLast["Ohtani"]
	if ohtani.ID != ids.Player(leagueMLB, "660271") {
		t.Errorf("id = %s, want UUIDv5 of statsapi person 660271", ohtani.ID)
	}
	if ohtani.TeamID != ids.Team(leagueMLB, "119") || ohtani.TeamAbbreviation != "LAD" {
		t.Errorf("team ref wrong: %+v", ohtani)
	}
	if ohtani.FirstName != "Shohei" || ohtani.Position != "TWP" || ohtani.League != model.LeagueMLB {
		t.Errorf("summary wrong: %+v", ohtani)
	}
	if ohtani.Status != model.PlayerActive || ohtani.JerseyNumber == nil || *ohtani.JerseyNumber != 17 {
		t.Errorf("status/jersey wrong: %+v", ohtani)
	}
	if ohtani.ExternalIDs["mlb"] != "660271" {
		t.Errorf("external ids wrong: %+v", ohtani.ExternalIDs)
	}

	// Two-way player: the hitting and the pitching splits fill one block.
	st := details[ohtani.ID].BaseballSeasonStats
	if st == nil {
		t.Fatal("Ohtani season stats missing")
	}
	if st.Season != 2026 || st.Games != 95 || st.AtBats != 346 || st.Hits != 101 || st.HomeRuns != 22 {
		t.Errorf("Ohtani counting stats wrong: %+v", st)
	}
	// TB = H + 2B + 2*3B + 3*HR = 101 + 17 + 4 + 66 (the live capture's own
	// totalBases field agrees: 188).
	if st.TotalBases != 188 {
		t.Errorf("total bases = %d, want 188", st.TotalBases)
	}
	if st.BattingAvg != 0.292 || math.Abs(st.PlateAppearancesPerGame-417.0/95.0) > 1e-9 {
		t.Errorf("Ohtani batting rates wrong: %+v", st)
	}
	// IP "85.2" is 85 and two thirds; K/9 = 95*9/IP.
	if math.Abs(st.InningsPitched-(85.0+2.0/3.0)) > 1e-9 {
		t.Errorf("innings pitched = %v, want 85 2/3", st.InningsPitched)
	}
	if math.Abs(st.StrikeoutsPerNine-95.0*9.0/(85.0+2.0/3.0)) > 1e-9 {
		t.Errorf("K/9 = %v, want 95*9/85.2", st.StrikeoutsPerNine)
	}

	// Hitter only: pitching fields stay zero; structured names come from the
	// hydrated person (Betts's legal first name, not the roster fullName).
	betts := byLast["Betts"]
	if betts.FirstName != "Markus" || betts.Position != "SS" {
		t.Errorf("Betts summary wrong: %+v", betts)
	}
	st = details[betts.ID].BaseballSeasonStats
	if st == nil || st.Games != 63 || st.Hits != 56 || st.TotalBases != 99 || st.HomeRuns != 11 {
		t.Fatalf("Betts season stats wrong: %+v", st)
	}
	if st.BattingAvg != 0.228 || math.Abs(st.PlateAppearancesPerGame-270.0/63.0) > 1e-9 {
		t.Errorf("Betts batting rates wrong: %+v", st)
	}
	if st.InningsPitched != 0 || st.StrikeoutsPerNine != 0 {
		t.Errorf("Betts must carry no pitching rates: %+v", st)
	}

	// Pitcher only: games come from the pitching split, the batting block
	// stays zero (the pitching group's "avg" is opponents' average and must
	// not leak into BattingAvg).
	yamamoto := byLast["Yamamoto"]
	st = details[yamamoto.ID].BaseballSeasonStats
	if st == nil || st.Games != 18 || st.AtBats != 0 || st.BattingAvg != 0 {
		t.Fatalf("Yamamoto season stats wrong: %+v", st)
	}
	if math.Abs(st.InningsPitched-(119.0+2.0/3.0)) > 1e-9 {
		t.Errorf("Yamamoto innings pitched = %v, want 119 2/3", st.InningsPitched)
	}
	if math.Abs(st.StrikeoutsPerNine-113.0*9.0/(119.0+2.0/3.0)) > 1e-9 {
		t.Errorf("Yamamoto K/9 = %v", st.StrikeoutsPerNine)
	}
}

func TestPlayersHydrationFailureDegrades(t *testing.T) {
	p := testProvider(t, rosterMux(t, http.StatusInternalServerError))

	summaries, details, _, err := p.Players(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Players must survive per-player hydration failures: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("summaries = %d, want 3", len(summaries))
	}
	for _, s := range summaries {
		if details[s.ID].BaseballSeasonStats != nil {
			t.Errorf("season stats must be nil without hydration: %+v", details[s.ID])
		}
	}
	// Names degrade to splitting the roster fullName.
	byLast := make(map[string]model.PlayerSummary, len(summaries))
	for _, s := range summaries {
		byLast[s.LastName] = s
	}
	if betts := byLast["Betts"]; betts.FirstName != "Mookie" {
		t.Errorf("fallback name wrong: %+v", betts)
	}
}

func TestPlayersRosterFailureFatal(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/teams" {
			serveFixture(t, w, "teams.json")
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	if _, _, _, err := p.Players(context.Background(), 2026); err == nil {
		t.Error("a failed roster call must fail the refresh")
	}
}

func TestBoxScoreNormalization(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/game/823372/boxscore" {
			http.NotFound(w, r)
			return
		}
		serveFixture(t, w, "boxscore.json")
	}))

	box, fetches, err := p.BoxScore(context.Background(), "823372")
	if err != nil {
		t.Fatalf("BoxScore failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
	if box.GameID != ids.Game(leagueMLB, "823372") || box.Sport != string(model.SportBaseball) {
		t.Errorf("envelope wrong: %+v", box)
	}

	// The StatsAPI labels home/away directly: LAD 8 @ PIT 9 arrives already
	// oriented, with scores from the box score's own team batting totals.
	if box.AwayTeam.ID != ids.Team(leagueMLB, "119") || box.AwayTeam.Abbreviation != "LAD" || box.AwayTeam.Score != 8 {
		t.Errorf("away team wrong: %+v", box.AwayTeam)
	}
	if box.HomeTeam.ID != ids.Team(leagueMLB, "134") || box.HomeTeam.Abbreviation != "PIT" || box.HomeTeam.Score != 9 {
		t.Errorf("home team wrong: %+v", box.HomeTeam)
	}
	if box.HomeTeam.Players != nil || box.HomeTeam.SoccerPlayers != nil || box.HomeTeam.TeamStats != nil {
		t.Error("non-baseball blocks must stay empty for MLB")
	}

	// Away: Espinal's bench block (empty batting and pitching) is skipped;
	// lines are sorted by name.
	away := box.AwayTeam.BaseballPlayers
	if len(away) != 2 || away[0].PlayerName != "Kyle Tucker" || away[1].PlayerName != "Shohei Ohtani" {
		t.Fatalf("away lines wrong: %+v", away)
	}

	// Ohtani's real two-way game: both blocks on one line.
	ohtani := away[1]
	if ohtani.PlayerID != ids.Player(leagueMLB, "660271") || ohtani.Position != "DH" {
		t.Errorf("Ohtani identity wrong: %+v", ohtani)
	}
	if ohtani.AtBats != 5 || ohtani.Hits != 1 || ohtani.HomeRuns != 1 || ohtani.RunsBattedIn != 2 ||
		ohtani.Runs != 1 || ohtani.Walks != 0 || ohtani.StrikeoutsBatting != 2 {
		t.Errorf("Ohtani batting line wrong: %+v", ohtani)
	}
	// TB = H + 2B + 2*3B + 3*HR = 1 + 0 + 0 + 3.
	if ohtani.TotalBases != 4 {
		t.Errorf("Ohtani total bases = %d, want 4", ohtani.TotalBases)
	}
	// IP "6.2" is 6 and two thirds; the pinned pitcher_strikeouts key grades
	// against StrikeoutsPitching, not the batting K's.
	if math.Abs(ohtani.InningsPitched-(6.0+2.0/3.0)) > 1e-9 || ohtani.StrikeoutsPitching != 6 || ohtani.EarnedRuns != 3 {
		t.Errorf("Ohtani pitching line wrong: %+v", ohtani)
	}

	// Tucker: double only. TB = 2 + 1 = 3 (h + 2b; not 2*2B).
	tucker := away[0]
	if tucker.AtBats != 4 || tucker.Hits != 2 || tucker.TotalBases != 3 || tucker.RunsBattedIn != 1 {
		t.Errorf("Tucker line wrong: %+v", tucker)
	}
	if tucker.InningsPitched != 0 || tucker.StrikeoutsPitching != 0 {
		t.Errorf("Tucker must carry no pitching stats: %+v", tucker)
	}

	// Home: Mangum's "triples" key is trimmed from the fixture (missing
	// stats decode as zero); Jones is a pure pitcher with a zero batting
	// block.
	home := box.HomeTeam.BaseballPlayers
	if len(home) != 2 || home[0].PlayerName != "Jake Mangum" || home[1].PlayerName != "Jared Jones" {
		t.Fatalf("home lines wrong: %+v", home)
	}
	if home[0].TotalBases != 4 || home[0].Hits != 3 {
		t.Errorf("Mangum line wrong: %+v", home[0])
	}
	jones := home[1]
	if jones.AtBats != 0 || jones.Hits != 0 || jones.TotalBases != 0 {
		t.Errorf("Jones must carry a zero batting block: %+v", jones)
	}
	if jones.InningsPitched != 4 || jones.StrikeoutsPitching != 4 || jones.EarnedRuns != 2 {
		t.Errorf("Jones pitching line wrong: %+v", jones)
	}
}

func TestBoxScoreEmptyResponse(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	if _, _, err := p.BoxScore(context.Background(), "823372"); err == nil ||
		!strings.Contains(err.Error(), "no box score data") {
		t.Errorf("empty response must error, got %v", err)
	}
}

func TestTotalBases(t *testing.T) {
	// TB = 1B + 2*2B + 3*3B + 4*HR with 1B = H - 2B - 3B - HR, i.e.
	// H + 2B + 2*3B + 3*HR. Verified against the live capture's own
	// totalBases field for every batter of gamePk 823372.
	tests := []struct {
		hits, doubles, triples, homeRuns, want int
	}{
		{0, 0, 0, 0, 0},
		{3, 0, 0, 0, 3},  // three singles
		{2, 1, 0, 0, 3},  // single + double
		{3, 1, 1, 0, 6},  // single + double + triple
		{1, 0, 0, 1, 4},  // solo shot
		{4, 1, 1, 1, 10}, // cycle plus a single: 1+2+3+4
	}
	for _, tt := range tests {
		st := statsapiStatLine{Hits: tt.hits, Doubles: tt.doubles, Triples: tt.triples, HomeRuns: tt.homeRuns}
		if got := totalBases(st); got != tt.want {
			t.Errorf("totalBases(h=%d 2b=%d 3b=%d hr=%d) = %d, want %d",
				tt.hits, tt.doubles, tt.triples, tt.homeRuns, got, tt.want)
		}
	}
}

func TestSeasonYear(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))
	for now, want := range map[time.Time]int{
		time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC):   2026, // mid-season
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC):  2026, // pre spring training
		time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC): 2026, // post World Series
	} {
		if got := p.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}

func TestProviderIdentity(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second))
	if p.League() != model.LeagueMLB {
		t.Errorf("League() = %s, want MLB", p.League())
	}
	if p.Source() != sourceMLB {
		t.Errorf("Source() = %s, want %s", p.Source(), sourceMLB)
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

func TestTeamStatsHittingError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, err := p.TeamStats(context.Background(), 2026, 0); err == nil || !strings.Contains(err.Error(), "fetch hitting stats") {
		t.Errorf("upstream 500 on hitting stats must error, got %v", err)
	}
}

func TestTeamStatsPitchingError(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/teams/stats" && q.Get("group") == "hitting":
			serveFixture(t, w, "teamstats_hitting.json")
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	if _, _, err := p.TeamStats(context.Background(), 2026, 0); err == nil || !strings.Contains(err.Error(), "fetch pitching stats") {
		t.Errorf("upstream 500 on pitching stats must error, got %v", err)
	}
}

func TestTeamStatsStandingsError(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/teams/stats" && q.Get("group") == "hitting":
			serveFixture(t, w, "teamstats_hitting.json")
		case r.URL.Path == "/teams/stats" && q.Get("group") == "pitching":
			serveFixture(t, w, "teamstats_pitching.json")
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	if _, _, err := p.TeamStats(context.Background(), 2026, 0); err == nil || !strings.Contains(err.Error(), "fetch standings") {
		t.Errorf("upstream 500 on standings must error, got %v", err)
	}
}

func TestTeamStatsAbbreviationsDegrade(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/teams/stats" && q.Get("stats") == "statSplits":
			serveFixture(t, w, "bullpen_rp.json")
		case r.URL.Path == "/teams/stats" && q.Get("group") == "hitting":
			serveFixture(t, w, "teamstats_hitting.json")
		case r.URL.Path == "/teams/stats" && q.Get("group") == "pitching":
			serveFixture(t, w, "teamstats_pitching.json")
		case r.URL.Path == "/standings":
			serveFixture(t, w, "standings.json")
		case r.URL.Path == "/teams":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))

	stats, _, err := p.TeamStats(context.Background(), 2026, 0)
	if err != nil {
		t.Fatalf("TeamStats must survive a team-list outage: %v", err)
	}
	lad := stats[ids.Team(leagueMLB, "119")]
	if lad.TeamAbbreviation != "" {
		t.Errorf("abbreviation should be empty without the team list, got %q", lad.TeamAbbreviation)
	}
	if lad.Stats.Baseball == nil {
		t.Error("stats should still be present despite the abbreviation outage")
	}
}

func TestScheduleUpstreamError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, err := p.Schedule(context.Background(), 2026); err == nil {
		t.Error("upstream 500 on schedule must error")
	}
}

func TestScheduleResolvePitchersDedupAndDegrade(t *testing.T) {
	const sched = `{"dates":[{"date":"2026-08-01","games":[
		{"gamePk":9001,"gameType":"R","gameDate":"2026-08-01T18:00:00Z","status":{"abstractGameState":"Preview","detailedState":"Scheduled"},
		 "teams":{"home":{"team":{"id":10,"name":"Home A"},"probablePitcher":{"id":1001,"fullName":"Ace One"}},
		          "away":{"team":{"id":20,"name":"Away A"}}}},
		{"gamePk":9002,"gameType":"R","gameDate":"2026-08-01T21:00:00Z","status":{"abstractGameState":"Preview","detailedState":"Scheduled"},
		 "teams":{"home":{"team":{"id":10,"name":"Home A"},"probablePitcher":{"id":1001,"fullName":"Ace One"}},
		          "away":{"team":{"id":30,"name":"Away B"},"probablePitcher":{"id":1002,"fullName":"Bad Arm"}}}}
	]}]}`

	var personCalls atomic.Int32
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/teams":
			serveFixture(t, w, "teams.json")
		case "/schedule":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(sched))
		case "/people/1001":
			personCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"people":[{"id":1001,"fullName":"Ace One","pitchHand":{"code":"R"}}]}`))
		case "/people/1002":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))

	games, _, err := p.Schedule(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	if personCalls.Load() != 1 {
		t.Errorf("person calls = %d, want 1 (pitcher 1001 cached after the first game)", personCalls.Load())
	}
	byExternal := make(map[string]model.Game, len(games))
	for _, g := range games {
		byExternal[g.ExternalID] = g
	}
	g1 := byExternal["9001"]
	if g1.AwayProbablePitcher != nil {
		t.Errorf("away probable must be nil with no announced pitcher: %+v", g1.AwayProbablePitcher)
	}
	if g1.HomeProbablePitcher == nil || g1.HomeProbablePitcher.Name != "Ace One" || g1.HomeProbablePitcher.Throws != "R" {
		t.Errorf("home probable wrong: %+v", g1.HomeProbablePitcher)
	}
	g2 := byExternal["9002"]
	if g2.AwayProbablePitcher == nil || g2.AwayProbablePitcher.Name != "Bad Arm" || g2.AwayProbablePitcher.ExternalID != "1002" {
		t.Errorf("degraded probable must be name/id only: %+v", g2.AwayProbablePitcher)
	}
	if g2.AwayProbablePitcher.ERA != 0 || g2.AwayProbablePitcher.Throws != "" {
		t.Errorf("degraded probable must carry no stats: %+v", g2.AwayProbablePitcher)
	}
}

func TestScoreboardUpstreamError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("upstream 500 on scoreboard must error")
	}
}

func TestScoreboardFallbackScoreWithoutLinescore(t *testing.T) {
	const body = `{"dates":[{"date":"2026-07-05","games":[
		{"gamePk":5001,"gameType":"R","gameDate":"2026-07-05T18:00:00Z","status":{"abstractGameState":"Final","detailedState":"Final"},
		 "teams":{"home":{"team":{"id":10,"name":"Home A"},"score":5},
		          "away":{"team":{"id":20,"name":"Away A"},"score":2}}}
	]}]}`
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	final := updates["5001"]
	if final.HomeScore != 5 || final.AwayScore != 2 {
		t.Errorf("fallback score fields wrong: %+v", final)
	}
}

func TestBoxScoreUpstreamError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, err := p.BoxScore(context.Background(), "1"); err == nil {
		t.Error("upstream 500 on box score must error")
	}
}

func TestPlayersTeamsError(t *testing.T) {
	p := testProvider(t, upstream500(t))
	if _, _, _, err := p.Players(context.Background(), 2026); err == nil {
		t.Error("upstream 500 on teams must error")
	}
}

func TestPlayersSkipsZeroIDRosterEntries(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/teams":
			serveFixture(t, w, "teams.json")
		case strings.HasPrefix(r.URL.Path, "/teams/") && strings.HasSuffix(r.URL.Path, "/roster"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"roster":[{"person":{"id":0,"fullName":"Ghost"},"jerseyNumber":"0","position":{"abbreviation":"P"}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	summaries, details, _, err := p.Players(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Players failed: %v", err)
	}
	for _, s := range summaries {
		if s.LastName == "Ghost" {
			t.Errorf("roster entry with person id 0 must be skipped: %+v", s)
		}
	}
	if len(summaries) != 0 || len(details) != 0 {
		t.Errorf("all-zero-id rosters should yield no players: %d summaries, %d details", len(summaries), len(details))
	}
}

func TestNormalizeGameSkipsIrrelevantType(t *testing.T) {
	g := statsapiGame{GamePk: 1, GameType: "A", GameDate: "2026-07-05T18:00:00Z"}
	if _, ok := normalizeGame(g, 2026, nil, nil); ok {
		t.Error("all-star game type must not normalize")
	}
}

func TestNormalizeGameMalformedDate(t *testing.T) {
	g := statsapiGame{GamePk: 1, GameType: "R", GameDate: "not-a-date"}
	if _, ok := normalizeGame(g, 2026, nil, nil); ok {
		t.Error("unparseable game date must not normalize")
	}
}

func TestNormalizeGameScoreFallbackWithoutLinescore(t *testing.T) {
	var g statsapiGame
	g.GamePk = 42
	g.GameType = "R"
	g.GameDate = "2026-07-05T18:00:00Z"
	g.Status.AbstractGameState = "Final"
	g.Status.DetailedState = "Final"
	g.Teams.Home.Team.ID = 10
	g.Teams.Away.Team.ID = 20
	home, away := 5, 2
	g.Teams.Home.Score = &home
	g.Teams.Away.Score = &away

	game, ok := normalizeGame(g, 2026, nil, map[int]string{10: "HOM", 20: "AWY"})
	if !ok {
		t.Fatal("normalizeGame failed")
	}
	if game.HomeScore == nil || *game.HomeScore != 5 || game.AwayScore == nil || *game.AwayScore != 2 {
		t.Errorf("fallback score without a linescore wrong: %+v", game)
	}
}

func TestNormalizePitcherNoData(t *testing.T) {
	if got := normalizePitcher(&peopleResponse{}); got != nil {
		t.Errorf("empty people response should yield nil, got %+v", got)
	}
}

func TestFIPZeroInnings(t *testing.T) {
	if got := fip(5, 3, 0, 10, 0); got != 0 {
		t.Errorf("fip with zero innings pitched = %v, want 0", got)
	}
}

func TestWobaZeroDenominator(t *testing.T) {
	if got := woba(statsapiStatLine{}); got != 0 {
		t.Errorf("woba with an all-zero line = %v, want 0", got)
	}
}

func TestNormalizeTeamStatsPitchingOnlyGamesPlayed(t *testing.T) {
	var pitching teamStatsResponse
	if err := json.Unmarshal([]byte(`{"stats":[{"splits":[{"team":{"id":42},"stat":{"gamesPlayed":10,"era":"3.00"}}]}]}`), &pitching); err != nil {
		t.Fatal(err)
	}
	var standings standingsResponse

	out := normalizeTeamStats(2026, nil, &pitching, nil, &standings, map[int]string{42: "XYZ"})
	ts := out[ids.Team(leagueMLB, "42")]
	if ts.GamesPlayed != 10 {
		t.Errorf("games played should come from the pitching split when hitting is absent, got %d", ts.GamesPlayed)
	}
	if ts.Stats.Baseball.TeamERA != 3.00 {
		t.Errorf("team ERA wrong: %+v", ts.Stats.Baseball)
	}
	if ts.Stats.Baseball.BullpenERA != ts.Stats.Baseball.TeamERA {
		t.Errorf("bullpen ERA must fall back to team ERA without a bullpen split: %+v", ts.Stats.Baseball)
	}
}

func TestBaseballSeasonStatsNoMatchingGroup(t *testing.T) {
	var resp peopleResponse
	if err := json.Unmarshal([]byte(`{"people":[{"id":1,"fullName":"No Stats"}]}`), &resp); err != nil {
		t.Fatal(err)
	}
	if got := baseballSeasonStats(&resp, 2026); got != nil {
		t.Errorf("no stat groups should yield nil, got %+v", got)
	}
}

func TestPersonNamesSingleWordFallback(t *testing.T) {
	first, last := personNames(nil, "Ichiro")
	if first != "" || last != "Ichiro" {
		t.Errorf("single-word name: got (%q, %q), want (\"\", \"Ichiro\")", first, last)
	}
}

func TestBoxScoreSameNamePlayerIDTiebreak(t *testing.T) {
	body := `{"teams":{
		"home":{"team":{"id":11,"abbreviation":"HOM"},"teamStats":{"batting":{"runs":0}},"players":{
			"ID100":{"person":{"id":100,"fullName":"Same Name"},"position":{"abbreviation":"P"},"stats":{"batting":{"gamesPlayed":1,"hits":1},"pitching":{}}},
			"ID200":{"person":{"id":200,"fullName":"Same Name"},"position":{"abbreviation":"P"},"stats":{"batting":{"gamesPlayed":1,"hits":2},"pitching":{}}}
		}},
		"away":{"team":{"id":22,"abbreviation":"AWY"},"teamStats":{"batting":{"runs":0}},"players":{}}
	}}`
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	box, _, err := p.BoxScore(context.Background(), "1")
	if err != nil {
		t.Fatalf("BoxScore failed: %v", err)
	}
	lines := box.HomeTeam.BaseballPlayers
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	if lines[0].PlayerName != "Same Name" || lines[1].PlayerName != "Same Name" {
		t.Fatalf("both lines should share the name: %+v", lines)
	}
	id100, id200 := ids.Player(leagueMLB, "100"), ids.Player(leagueMLB, "200")
	present := map[string]bool{lines[0].PlayerID: true, lines[1].PlayerID: true}
	if !present[id100] || !present[id200] {
		t.Errorf("expected both player ids present: %+v", lines)
	}
	if lines[0].PlayerID == lines[1].PlayerID {
		t.Errorf("tiebreak must not collapse distinct players: %+v", lines)
	}
}
