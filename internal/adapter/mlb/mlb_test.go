package mlb

// Golden-fixture tests over real MLB StatsAPI responses recorded 2026-07-05
// during the live 2026 season (testdata/*.json, trimmed). Every fixture is a
// real capture: the extra-innings finals (gamePks 824417 and 824621) and the
// truncated bottom-of-the-ninth game (823118) come from the July 3–4 slates,
// the postponed/makeup pair from April 2–3, and the probable pitchers from
// the July 6 schedule.

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
		time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC):   2026, // mid-season
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC):  2026, // pre spring training
		time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC): 2026, // post World Series
	} {
		if got := p.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}
