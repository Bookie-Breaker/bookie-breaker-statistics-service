package nhl

// Tests for the NHL adapter. Every fixture in testdata/ is a real trimmed
// capture of the official NHL API for the completed 2025-26 season, recorded
// 2026-07-06: standings.json and team_summary.json/team_ref.json drive teams
// and season stats; score.json (api-web /v1/score/2025-11-01) carries a
// regulation final, a real overtime game, and a real shootout game whose
// final reflects the NHL +1-goal convention; schedule.json is the matching
// /v1/schedule week.

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

func mux(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/standings/now":
			serveFixture(t, w, "standings.json")
		case strings.HasPrefix(r.URL.Path, "/v1/schedule/"):
			serveFixture(t, w, "schedule.json")
		case strings.HasPrefix(r.URL.Path, "/v1/score/"):
			serveFixture(t, w, "score.json")
		case strings.HasPrefix(r.URL.Path, "/team/summary"):
			serveFixture(t, w, "team_summary.json")
		case r.URL.Path == "/team":
			serveFixture(t, w, "team_ref.json")
		default:
			http.NotFound(w, r)
		}
	})
}

func testProvider(t *testing.T) *Provider {
	t.Helper()
	server := httptest.NewServer(mux(t))
	t.Cleanup(server.Close)
	return NewProvider(NewClient(server.URL, server.URL, 5*time.Second))
}

func TestTeamsNormalization(t *testing.T) {
	p := testProvider(t)
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

	var col model.TeamSummary
	for _, team := range teams {
		if team.Abbreviation == "COL" {
			col = team
		}
	}
	if col.ID != ids.Team(leagueNHL, "COL") {
		t.Errorf("id = %s, want UUIDv5 of tricode COL", col.ID)
	}
	if col.Name != "Colorado Avalanche" || col.League != model.LeagueNHL {
		t.Errorf("summary wrong: %+v", col)
	}
	if col.Conference != "Western" || col.Division != "Central" {
		t.Errorf("conference/division wrong: %+v", col)
	}
	if col.Mascot != "Avalanche" || col.Location != "Colorado" {
		t.Errorf("mascot/location wrong: %+v", col)
	}
	if col.ExternalIDs["nhl"] != "COL" {
		t.Errorf("external id wrong: %+v", col.ExternalIDs)
	}
	if details[col.ID].Venue != nil {
		t.Errorf("NHL teams carry no venue, got %+v", details[col.ID].Venue)
	}
}

func TestScheduleNormalization(t *testing.T) {
	p := testProvider(t)
	games, fetches, err := p.Schedule(context.Background(), 2025)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	// The season window is walked a week at a time, so many empty-week fetches
	// are expected; at least one must have happened.
	if len(fetches) == 0 {
		t.Error("expected schedule fetches")
	}
	// The fixture serves the same three games for every week; dedup by id
	// leaves exactly three.
	if len(games) != 3 {
		t.Fatalf("games = %d, want 3 unique", len(games))
	}

	byExternal := make(map[string]model.Game, len(games))
	for _, g := range games {
		byExternal[g.ExternalID] = g
	}

	// Overtime game (COL 2 at SJS 3): FINAL, past regulation, season 2025 from
	// the concatenated id 20252026. The schedule endpoint carries no goal
	// detail, so no period scores.
	ot := byExternal["2025020184"]
	if ot.ID != ids.Game(leagueNHL, "2025020184") || ot.Season != 2025 {
		t.Errorf("OT identity wrong: %+v", ot)
	}
	if ot.Status != model.GameFinal || ot.Result == nil || !ot.Result.Overtime {
		t.Fatalf("OT result wrong: %+v", ot.Result)
	}
	if ot.Result.HomeScore != 3 || ot.Result.AwayScore != 2 || ot.Result.Margin != 1 {
		t.Errorf("OT scores wrong: %+v", ot.Result)
	}
	if len(ot.Result.PeriodScores) != 0 {
		t.Errorf("schedule endpoint has no goal detail; want no period scores, got %+v", ot.Result.PeriodScores)
	}
	if ot.Result.RegulationHomeScore != nil || ot.Result.RegulationAwayScore != nil {
		t.Errorf("hockey must carry no regulation fields: %+v", ot.Result)
	}
	if ot.HomeTeam.Abbreviation != "SJS" || ot.AwayTeam.Abbreviation != "COL" {
		t.Errorf("OT team refs wrong: %+v / %+v", ot.HomeTeam, ot.AwayTeam)
	}
	if ot.Venue == nil || ot.Venue.Name == "" {
		t.Errorf("venue missing: %+v", ot.Venue)
	}

	// Regulation final (CAR 1 at BOS 2): not overtime.
	reg := byExternal["2025020181"]
	if reg.Result == nil || reg.Result.Overtime {
		t.Errorf("regulation final must not be overtime: %+v", reg.Result)
	}
	if reg.Result.HomeScore != 2 || reg.Result.AwayScore != 1 {
		t.Errorf("regulation scores wrong: %+v", reg.Result)
	}
}

func TestScoreboard(t *testing.T) {
	p := testProvider(t)
	updates, fetches, err := p.Scoreboard(context.Background(), time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
	if len(updates) != 3 {
		t.Fatalf("updates = %d, want 3", len(updates))
	}

	// Shootout game (DAL 3 at FLA 4): the final includes the +1 shootout goal
	// (never stripped), Overtime is set, and the per-goal detail yields five
	// periods whose totals sum to the final — the shootout winner's decisive
	// goal is the single period-5 goal.
	so := updates["2025020185"]
	if so.Status != model.GameFinal || so.Result == nil {
		t.Fatalf("SO update wrong: %+v", so)
	}
	if so.HomeScore != 4 || so.AwayScore != 3 {
		t.Errorf("SO top-line score wrong (must include shootout +1): %+v", so)
	}
	if !so.Result.Overtime || so.Result.HomeScore != 4 || so.Result.AwayScore != 3 || so.Result.Margin != 1 {
		t.Errorf("SO result wrong: %+v", so.Result)
	}
	if len(so.Result.PeriodScores) != 5 {
		t.Fatalf("SO period scores = %d, want 5 (incl shootout period)", len(so.Result.PeriodScores))
	}
	shootout := so.Result.PeriodScores[4]
	if shootout.Period != 5 || shootout.Home != 1 || shootout.Away != 0 {
		t.Errorf("shootout period must record the +1 winning goal: %+v", shootout)
	}
	var sumHome, sumAway int
	for _, ps := range so.Result.PeriodScores {
		sumHome += ps.Home
		sumAway += ps.Away
	}
	if sumHome != so.Result.HomeScore || sumAway != so.Result.AwayScore {
		t.Errorf("period scores %d-%d must sum to final %d-%d", sumHome, sumAway, so.Result.HomeScore, so.Result.AwayScore)
	}
	if so.Result.RegulationHomeScore != nil || so.Result.RegulationAwayScore != nil {
		t.Errorf("hockey must carry no regulation fields: %+v", so.Result)
	}

	// Overtime game (COL 2 at SJS 3): four periods, past regulation.
	ot := updates["2025020184"]
	if !ot.Result.Overtime || len(ot.Result.PeriodScores) != 4 {
		t.Errorf("OT update wrong: %+v", ot.Result)
	}

	// Regulation final: three periods, not overtime.
	reg := updates["2025020181"]
	if reg.Result.Overtime || len(reg.Result.PeriodScores) != 3 {
		t.Errorf("regulation update wrong: %+v", reg.Result)
	}
}

func TestTeamStats(t *testing.T) {
	p := testProvider(t)
	stats, fetches, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(fetches) != 2 {
		t.Errorf("fetches = %d, want 2 (summary + team ref)", len(fetches))
	}
	if len(stats) != 4 {
		t.Fatalf("teams = %d, want 4", len(stats))
	}

	wsh := stats[ids.Team(leagueNHL, "WSH")]
	if wsh.Season != 2025 || wsh.GamesPlayed != 82 || wsh.TeamAbbreviation != "WSH" {
		t.Errorf("WSH identity wrong: %+v", wsh)
	}
	if wsh.Wins != 43 || wsh.Losses != 30 {
		t.Errorf("WSH record wrong: %d-%d", wsh.Wins, wsh.Losses)
	}
	if wsh.Stats.Offensive != nil || wsh.Stats.Defensive != nil || wsh.Stats.Advanced != nil ||
		wsh.Stats.Baseball != nil || wsh.Stats.Football != nil || wsh.Stats.Soccer != nil {
		t.Error("non-hockey blocks must stay nil for NHL")
	}
	h := wsh.Stats.Hockey
	if h == nil {
		t.Fatal("hockey block missing")
	}
	if math.Abs(h.GoalsForPerGame-3.18292) > 1e-6 || math.Abs(h.GoalsAgainstPerGame-2.90243) > 1e-6 {
		t.Errorf("WSH goals per game wrong: %+v", h)
	}
	if math.Abs(h.ShotsForPerGame-28.04878) > 1e-6 || math.Abs(h.ShotsAgainstPerGame-28.13414) > 1e-6 {
		t.Errorf("WSH shots per game wrong: %+v", h)
	}
	if math.Abs(h.PowerPlayPct-0.178423) > 1e-6 || math.Abs(h.PenaltyKillPct-0.800797) > 1e-6 {
		t.Errorf("WSH special teams wrong: %+v", h)
	}
	// team_save_pct = 1 - goalsAgainst / (shotsAgainstPerGame * gamesPlayed)
	//              = 1 - 238 / (28.13414 * 82) = 0.896836 (hand-checked).
	wantSave := 1 - 238.0/(28.13414*82)
	if math.Abs(h.TeamSavePct-wantSave) > 1e-6 {
		t.Errorf("WSH save pct = %v, want %v", h.TeamSavePct, wantSave)
	}
}

func TestTeamStatsRollingWindowUnsupported(t *testing.T) {
	p := NewProvider(NewClient("http://unused", "http://unused", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2025, 10); err == nil ||
		!strings.Contains(err.Error(), "unsupported for NHL") {
		t.Errorf("window > 0 must error, got %v", err)
	}
}

func TestStatusMapping(t *testing.T) {
	cases := []struct {
		state, schedule string
		want            model.GameStatus
	}{
		{"FUT", "OK", model.GameScheduled},
		{"PRE", "OK", model.GameScheduled},
		{"LIVE", "OK", model.GameInProgress},
		{"CRIT", "OK", model.GameInProgress},
		{"OVER", "OK", model.GameFinal},
		{"FINAL", "OK", model.GameFinal},
		{"OFF", "OK", model.GameFinal},
		{"FUT", "PPD", model.GamePostponed},
		{"FUT", "CNCL", model.GameCancelled},
		{"LIVE", "SUSP", model.GameSuspended},
	}
	for _, c := range cases {
		if got := mapStatus(c.state, c.schedule); got != c.want {
			t.Errorf("mapStatus(%q,%q) = %s, want %s", c.state, c.schedule, got, c.want)
		}
	}
}

func TestSeasonYear(t *testing.T) {
	p := NewProvider(NewClient("http://unused", "http://unused", time.Second))
	for now, want := range map[time.Time]int{
		time.Date(2025, 11, 1, 0, 0, 0, 0, time.UTC): 2025, // mid-season
		time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC): 2025, // spring, same season
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC):  2025, // Stanley Cup Final
		time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC):  2026, // off-season rolls forward
		time.Date(2025, 10, 8, 0, 0, 0, 0, time.UTC): 2025, // season opener
	} {
		if got := p.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}

func TestSeasonID(t *testing.T) {
	if got := seasonID(2025); got != 20252026 {
		t.Errorf("seasonID(2025) = %d, want 20252026", got)
	}
	if got := startingYear(20252026); got != 2025 {
		t.Errorf("startingYear(20252026) = %d, want 2025", got)
	}
}

func TestPlayersDescoped(t *testing.T) {
	p := NewProvider(NewClient("http://unused", "http://unused", time.Second))
	summaries, details, fetches, err := p.Players(context.Background(), 2025)
	if err != nil || len(summaries) != 0 || len(details) != 0 || fetches != nil {
		t.Errorf("Players must return empty collections: %v %v %v %v", summaries, details, fetches, err)
	}
	log, fetches, err := p.PlayerGameLog(context.Background(), model.PlayerDetail{}, 2025)
	if err != nil || len(log) != 0 || fetches != nil {
		t.Errorf("PlayerGameLog must return an empty log: %v %v %v", log, fetches, err)
	}
}
