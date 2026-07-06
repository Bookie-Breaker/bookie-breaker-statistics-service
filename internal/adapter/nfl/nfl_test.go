package nfl

// Golden-fixture tests over real archived 2025-season data recorded
// 2026-07-06. testdata/scoreboard.json, teams.json, and standings.json are
// trimmed captures of ESPN's site API for the completed 2025 NFL season (the
// events include the real Green Bay–Dallas 40–40 overtime tie, gameId
// 401772921, a real overtime win, and a January 2026 playoff game).
// testdata/stats_team_week_2025.csv is the real nflverse stats_team week file
// trimmed to the four AFC East teams' intra-division regular-season rows (a
// self-consistent subset for which offensive and defensive EPA are exactly
// derivable) plus two New England postseason rows to prove the REG-only
// filter. No derived fixtures.

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnfb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func serveFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	_, _ = w.Write(data)
}

// nflMux routes both ESPN and nflverse requests to the recorded fixtures.
func nflMux(t *testing.T, nflverseStatus int) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".csv"):
			if nflverseStatus != 0 && nflverseStatus != http.StatusOK {
				http.Error(w, "boom", nflverseStatus)
				return
			}
			serveFixture(t, w, "stats_team_week_2025.csv")
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case strings.HasSuffix(r.URL.Path, "/standings"):
			serveFixture(t, w, "standings.json")
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			serveFixture(t, w, "scoreboard.json")
		default:
			http.NotFound(w, r)
		}
	})
}

func testProvider(t *testing.T, nflverseStatus int) *Provider {
	t.Helper()
	server := httptest.NewServer(nflMux(t, nflverseStatus))
	t.Cleanup(server.Close)
	return NewProvider(
		espnfb.NewClient(server.URL, 5*time.Second),
		NewNFLVerseClient(server.URL, 5*time.Second),
	)
}

func TestTeamsNormalization(t *testing.T) {
	p := testProvider(t, http.StatusOK)

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

	var ne model.TeamSummary
	for _, team := range teams {
		if team.Abbreviation == "NE" {
			ne = team
		}
	}
	if ne.ID != ids.Team(leagueNFL, "17") {
		t.Errorf("id = %s, want UUIDv5 of espn team 17", ne.ID)
	}
	if ne.Name != "New England Patriots" || ne.League != model.LeagueNFL || !ne.Active {
		t.Errorf("summary wrong: %+v", ne)
	}
	if ne.ExternalIDs["espn"] != "17" {
		t.Errorf("external ids wrong: %+v", ne.ExternalIDs)
	}
	if details[ne.ID].Venue != nil {
		t.Errorf("football teams carry no venue, got %+v", details[ne.ID].Venue)
	}
}

func TestScheduleNormalization(t *testing.T) {
	p := testProvider(t, http.StatusOK)

	games, _, err := p.Schedule(context.Background(), 2025)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	// The ranged walk returns the same fixture per chunk; dedup by event id
	// yields the four unique games.
	if len(games) != 4 {
		t.Fatalf("games = %d, want 4 unique", len(games))
	}

	byExternal := make(map[string]model.Game, len(games))
	for _, g := range games {
		byExternal[g.ExternalID] = g
	}

	// The real Green Bay–Dallas 40–40 overtime tie: equal scores flow through
	// as-is, overtime is set, and no regulation fields are written (ADR-027).
	tie := byExternal["401772921"]
	if tie.ID != ids.Game(leagueNFL, "401772921") || tie.Season != 2025 || tie.SeasonType != model.SeasonRegular {
		t.Errorf("tie identity wrong: %+v", tie)
	}
	if tie.HomeTeam.Abbreviation != "DAL" || tie.AwayTeam.Abbreviation != "GB" {
		t.Errorf("tie team refs wrong: %+v / %+v", tie.HomeTeam, tie.AwayTeam)
	}
	if tie.Status != model.GameFinal || tie.Result == nil {
		t.Fatalf("tie final wrong: %+v", tie)
	}
	if tie.Result.HomeScore != 40 || tie.Result.AwayScore != 40 || tie.Result.Margin != 0 {
		t.Errorf("tie scores wrong: %+v", tie.Result)
	}
	if !tie.Result.Overtime || len(tie.Result.PeriodScores) != 5 {
		t.Errorf("tie overtime/period scores wrong: %+v", tie.Result)
	}
	if tie.Result.RegulationHomeScore != nil || tie.Result.RegulationAwayScore != nil {
		t.Errorf("football must carry no regulation fields: %+v", tie.Result)
	}

	// Real overtime win (SF 26, LAR 23 in OT): overtime with a decided margin.
	otWin := byExternal["401772939"]
	if otWin.Result == nil || !otWin.Result.Overtime || len(otWin.Result.PeriodScores) != 5 {
		t.Fatalf("overtime win result wrong: %+v", otWin.Result)
	}
	if otWin.Result.HomeScore != 23 || otWin.Result.AwayScore != 26 || otWin.Result.Margin != -3 {
		t.Errorf("overtime win scores wrong: %+v", otWin.Result)
	}

	// Regular-time final, four quarters, not overtime.
	reg := byExternal["401772510"]
	if reg.Result == nil || reg.Result.Overtime || len(reg.Result.PeriodScores) != 4 {
		t.Errorf("regular final result wrong: %+v", reg.Result)
	}
	if reg.Venue == nil || reg.Venue.Name != "Lincoln Financial Field" {
		t.Errorf("venue wrong: %+v", reg.Venue)
	}

	// January playoff game maps to the postseason.
	playoff := byExternal["401772979"]
	if playoff.SeasonType != model.SeasonPostseason || playoff.Season != 2025 {
		t.Errorf("playoff season type/year wrong: %+v", playoff)
	}
}

func TestScoreboardFinals(t *testing.T) {
	p := testProvider(t, http.StatusOK)

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2025, 9, 28, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}

	tie := updates["401772921"]
	if tie.Status != model.GameFinal || tie.Result == nil {
		t.Fatalf("tie update wrong: %+v", tie)
	}
	if tie.HomeScore != 40 || tie.AwayScore != 40 || tie.Result.Margin != 0 || !tie.Result.Overtime {
		t.Errorf("tie update result wrong: %+v", tie.Result)
	}
	if tie.Result.RegulationHomeScore != nil || tie.Result.RegulationAwayScore != nil {
		t.Errorf("football must carry no regulation fields: %+v", tie.Result)
	}

	otWin := updates["401772939"]
	if otWin.HomeScore != 23 || otWin.AwayScore != 26 || !otWin.Result.Overtime {
		t.Errorf("overtime update wrong: %+v", otWin)
	}
}

func TestTeamStats(t *testing.T) {
	p := testProvider(t, http.StatusOK)

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(stats) != 4 {
		t.Fatalf("teams = %d, want 4", len(stats))
	}

	ne := stats[ids.Team(leagueNFL, "17")]
	if ne.Season != 2025 || ne.GamesPlayed != 17 || ne.Wins != 14 || ne.Losses != 3 {
		t.Errorf("NE record wrong: %+v", ne)
	}
	if ne.TeamAbbreviation != "NE" {
		t.Errorf("abbreviation wrong: %+v", ne)
	}
	if ne.Stats.Offensive != nil || ne.Stats.Defensive != nil || ne.Stats.Advanced != nil ||
		ne.Stats.Soccer != nil || ne.Stats.Baseball != nil {
		t.Error("non-football blocks must stay nil for the NFL")
	}
	fb := ne.Stats.Football
	if fb == nil {
		t.Fatal("football block missing")
	}

	// Points come from the real ESPN standings (490 for, 320 against over 17
	// games); drive metrics are the documented ÷10.9 approximation.
	if math.Abs(fb.PointsPerGame-490.0/17.0) > 1e-9 {
		t.Errorf("points/game = %v, want 490/17", fb.PointsPerGame)
	}
	if math.Abs(fb.PointsAllowedPerGame-320.0/17.0) > 1e-9 {
		t.Errorf("points allowed/game = %v, want 320/17", fb.PointsAllowedPerGame)
	}
	if fb.DrivesPerGame != leagueAvgDrivesPerGame {
		t.Errorf("drives/game = %v, want %v", fb.DrivesPerGame, leagueAvgDrivesPerGame)
	}
	if math.Abs(fb.PointsPerDriveOff-(490.0/17.0)/leagueAvgDrivesPerGame) > 1e-9 {
		t.Errorf("points/drive off wrong: %v", fb.PointsPerDriveOff)
	}
	if math.Abs(fb.PointsPerDriveDef-(320.0/17.0)/leagueAvgDrivesPerGame) > 1e-9 {
		t.Errorf("points/drive def wrong: %v", fb.PointsPerDriveDef)
	}

	// EPA and turnover margin come from the real nflverse rows (AFC East
	// intra-division subset): New England's offensive EPA/play, defensive
	// EPA/play, and turnover margin/game hand-computed from the trimmed file.
	if math.Abs(fb.EPAPerPlayOff-0.2759032319134249) > 1e-9 {
		t.Errorf("EPA/play off = %v, want 0.2759032319134249", fb.EPAPerPlayOff)
	}
	if math.Abs(fb.EPAPerPlayDef-(-0.02124436431625811)) > 1e-9 {
		t.Errorf("EPA/play def = %v, want -0.02124436431625811", fb.EPAPerPlayDef)
	}
	if math.Abs(fb.TurnoverMarginPerGame-1.0) > 1e-9 {
		t.Errorf("turnover margin/game = %v, want 1.0", fb.TurnoverMarginPerGame)
	}
	// SP+ is a college rating; it stays zero for the NFL.
	if fb.SPPlusRating != 0 {
		t.Errorf("sp_plus_rating = %v, want 0 for the NFL", fb.SPPlusRating)
	}

	// A team that made the playoffs still aggregates regular-season EPA only:
	// New York's turnover margin over its six intra-division games.
	nyj := stats[ids.Team(leagueNFL, "20")]
	if math.Abs(nyj.Stats.Football.TurnoverMarginPerGame-(-4.0/3.0)) > 1e-9 {
		t.Errorf("NYJ turnover margin/game = %v, want -4/3", nyj.Stats.Football.TurnoverMarginPerGame)
	}
}

func TestTeamStatsNFLVerseDegradation(t *testing.T) {
	p := testProvider(t, http.StatusInternalServerError)

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats must survive an nflverse outage: %v", err)
	}
	ne := stats[ids.Team(leagueNFL, "17")]
	fb := ne.Stats.Football
	if fb == nil {
		t.Fatal("football block missing")
	}
	// Points-based fields still populate from the standings.
	if math.Abs(fb.PointsPerGame-490.0/17.0) > 1e-9 {
		t.Errorf("points/game must survive nflverse outage: %v", fb.PointsPerGame)
	}
	// EPA and turnover margin degrade to zero.
	if fb.EPAPerPlayOff != 0 || fb.EPAPerPlayDef != 0 || fb.TurnoverMarginPerGame != 0 {
		t.Errorf("EPA/turnover must be zero without nflverse: %+v", fb)
	}
}

func TestTeamStatsRollingWindowUnsupported(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewNFLVerseClient("http://unused", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2025, 10); err == nil ||
		!strings.Contains(err.Error(), "unsupported for the NFL") {
		t.Errorf("window > 0 must error, got %v", err)
	}
}

func TestPlayersDescoped(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewNFLVerseClient("http://unused", time.Second))

	summaries, details, fetches, err := p.Players(context.Background(), 2025)
	if err != nil || summaries == nil || details == nil || len(summaries) != 0 || len(details) != 0 || fetches != nil {
		t.Errorf("Players must return empty collections with nil error: %v %v %v %v", summaries, details, fetches, err)
	}

	log, fetches, err := p.PlayerGameLog(context.Background(), model.PlayerDetail{}, 2025)
	if err != nil || log == nil || len(log) != 0 || fetches != nil {
		t.Errorf("PlayerGameLog must return an empty log with nil error: %v %v %v", log, fetches, err)
	}
}

func TestSeasonYear(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewNFLVerseClient("http://unused", time.Second))
	for now, want := range map[time.Time]int{
		time.Date(2025, 9, 14, 0, 0, 0, 0, time.UTC): 2025, // mid-season
		time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC): 2025, // playoffs belong to the 2025 season
		time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC):  2025, // off-season, season just completed
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC):  2026, // new season opens in August
	} {
		if got := p.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}
