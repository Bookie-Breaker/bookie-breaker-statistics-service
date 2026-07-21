package soccer

// Golden-fixture tests over real ESPN responses recorded 2026-07-05 during
// the live 2026 World Cup (testdata/*.json, trimmed). The extra-time and
// penalty-shootout cases are real matches (Belgium 3-2 Senegal AET;
// Germany 1-1 Paraguay, 3-4 on penalties). The eng.1 fixture is a
// standings snapshot of the completed 2025-26 season — the EPL scoreboard
// was empty in the July off-season, so no EPL scoreboard fixture exists.

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

const leagueWC = string(model.LeagueFIFAWC)

func testProvider(t *testing.T, comp Competition, handler http.Handler) *Provider {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewProvider(NewClient(server.URL, 5*time.Second), comp)
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

// worldCupMux routes World Cup requests to the recorded fixtures: June
// scoreboards to the group stage, July to the knockout rounds, and the two
// extra-time events to their summaries.
func worldCupMux(t *testing.T, summaryCalls *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams_fifa_world.json")
		case strings.HasSuffix(r.URL.Path, "/standings"):
			serveFixture(t, w, "standings_fifa_world.json")
		case strings.HasSuffix(r.URL.Path, "/summary"):
			if summaryCalls != nil {
				summaryCalls.Add(1)
			}
			switch r.URL.Query().Get("event") {
			case "760493":
				serveFixture(t, w, "summary_aet.json")
			case "760489":
				serveFixture(t, w, "summary_pen.json")
			default:
				http.NotFound(w, r)
			}
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			switch dates := r.URL.Query().Get("dates"); {
			case strings.HasPrefix(dates, "202606"):
				serveFixture(t, w, "scoreboard_group.json")
			case strings.HasPrefix(dates, "202607"):
				serveFixture(t, w, "scoreboard_knockout.json")
			default:
				_, _ = w.Write([]byte(`{"events":[]}`))
			}
		default:
			http.NotFound(w, r)
		}
	})
}

func TestTeamsNormalization(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), worldCupMux(t, nil))

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

	var england model.TeamSummary
	for _, team := range teams {
		if team.Abbreviation == "ENG" {
			england = team
		}
	}
	if england.ID != ids.Team(leagueWC, "448") {
		t.Errorf("id = %s, want UUIDv5 of espn team 448", england.ID)
	}
	if england.Name != "England" || england.League != model.LeagueFIFAWC || !england.Active {
		t.Errorf("summary wrong: %+v", england)
	}
	if england.ExternalIDs["espn"] != "448" {
		t.Errorf("external ids wrong: %+v", england.ExternalIDs)
	}
	if details[england.ID].Venue != nil {
		t.Errorf("soccer teams should carry no venue, got %+v", details[england.ID].Venue)
	}
}

func TestScoreboardLiveStatuses(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFixture(t, w, "scoreboard_live.json")
	}))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}

	live := updates["760505"] // STATUS_SECOND_HALF
	if live.Status != model.GameInProgress || live.HomeScore != 2 || live.AwayScore != 3 {
		t.Errorf("in-progress update wrong: %+v", live)
	}
	if live.Result != nil {
		t.Errorf("in-progress game must carry no result: %+v", live.Result)
	}
}

func TestScoreboardScheduledStatus(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFixture(t, w, "scoreboard_scheduled.json")
	}))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	for id, u := range updates {
		if u.Status != model.GameScheduled || u.Result != nil {
			t.Errorf("event %s: scheduled update wrong: %+v", id, u)
		}
	}
}

func TestScoreboardFinals(t *testing.T) {
	var summaryCalls atomic.Int32
	p := testProvider(t, FIFAWorldCup(), worldCupMux(t, &summaryCalls))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if len(updates) != 3 {
		t.Fatalf("updates = %d, want 3", len(updates))
	}
	// Only the two extra-time matches need a summary fetch.
	if summaryCalls.Load() != 2 {
		t.Errorf("summary calls = %d, want 2 (extra-time finals only)", summaryCalls.Load())
	}

	// England 2-1 Congo DR, decided in regulation: no regulation fields.
	ft := updates["760495"]
	if ft.Status != model.GameFinal || ft.Result == nil {
		t.Fatalf("full-time update wrong: %+v", ft)
	}
	if ft.Result.HomeScore != 2 || ft.Result.AwayScore != 1 || ft.Result.Overtime {
		t.Errorf("full-time result wrong: %+v", ft.Result)
	}
	if ft.Result.RegulationHomeScore != nil || ft.Result.RegulationAwayScore != nil {
		t.Errorf("regulation-time final must carry no regulation fields: %+v", ft.Result)
	}

	// Belgium 3-2 Senegal after extra time; linescores 0+2|0+1 vs 1+1|0+0,
	// so the ADR-027 regulation score is the first-two-period sum: 2-2.
	aet := updates["760493"]
	if aet.Result == nil || !aet.Result.Overtime {
		t.Fatalf("AET result wrong: %+v", aet.Result)
	}
	if aet.Result.HomeScore != 3 || aet.Result.AwayScore != 2 || aet.Result.TotalScore != 5 {
		t.Errorf("AET full-time score wrong: %+v", aet.Result)
	}
	if aet.Result.RegulationHomeScore == nil || *aet.Result.RegulationHomeScore != 2 ||
		aet.Result.RegulationAwayScore == nil || *aet.Result.RegulationAwayScore != 2 {
		t.Errorf("AET regulation score wrong: %+v", aet.Result)
	}
	if len(aet.Result.PeriodScores) != 4 || aet.Result.PeriodScores[1].Home != 2 {
		t.Errorf("AET period scores wrong: %+v", aet.Result.PeriodScores)
	}

	// Germany 1-1 Paraguay, 3-4 on penalties: the full-time score excludes
	// the shootout, and ESPN's trailing shootout linescore is dropped.
	pen := updates["760489"]
	if pen.Result == nil || !pen.Result.Overtime {
		t.Fatalf("shootout result wrong: %+v", pen.Result)
	}
	if pen.Result.HomeScore != 1 || pen.Result.AwayScore != 1 || pen.Result.TotalScore != 2 || pen.Result.Margin != 0 {
		t.Errorf("shootout full-time score must exclude shootout goals: %+v", pen.Result)
	}
	if pen.Result.RegulationHomeScore == nil || *pen.Result.RegulationHomeScore != 1 ||
		pen.Result.RegulationAwayScore == nil || *pen.Result.RegulationAwayScore != 1 {
		t.Errorf("shootout regulation score wrong: %+v", pen.Result)
	}
	if len(pen.Result.PeriodScores) != 4 {
		t.Errorf("shootout linescore must be dropped from period scores: %+v", pen.Result.PeriodScores)
	}
}

func TestScheduleSeasonTypesAndIDs(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), worldCupMux(t, nil))

	games, _, err := p.Schedule(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	if len(games) != 5 {
		t.Fatalf("games = %d, want 5 (2 group + 3 knockout)", len(games))
	}

	byExternal := make(map[string]model.Game, len(games))
	for _, g := range games {
		byExternal[g.ExternalID] = g
	}

	group := byExternal["760428"] // Cape Verde at Spain, group stage
	if group.SeasonType != model.SeasonRegular {
		t.Errorf("group-stage season type = %s, want REGULAR", group.SeasonType)
	}
	if group.ID != ids.Game(leagueWC, "760428") || group.Season != 2026 {
		t.Errorf("group game identity wrong: %+v", group)
	}
	if group.HomeTeam.ID != ids.Team(leagueWC, "164") || group.HomeTeam.Abbreviation != "ESP" {
		t.Errorf("home team ref wrong: %+v", group.HomeTeam)
	}
	if group.Venue == nil || group.Venue.Name != "Mercedes-Benz Stadium" {
		t.Errorf("venue wrong: %+v", group.Venue)
	}

	knockout := byExternal["760493"] // Belgium vs Senegal, round of 32
	if knockout.SeasonType != model.SeasonPostseason {
		t.Errorf("knockout season type = %s, want POSTSEASON", knockout.SeasonType)
	}
	// Schedule finals carry full results including regulation fields.
	if knockout.Status != model.GameFinal || knockout.Result == nil {
		t.Fatalf("knockout final wrong: %+v", knockout)
	}
	if knockout.Result.ID != knockout.ID {
		t.Errorf("result id = %s, want game id %s", knockout.Result.ID, knockout.ID)
	}
	if knockout.Result.RegulationHomeScore == nil || *knockout.Result.RegulationHomeScore != 2 {
		t.Errorf("schedule AET regulation score wrong: %+v", knockout.Result)
	}
}

func TestTeamStatsStandingsAndForm(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/standings"):
			if r.URL.Query().Get("season") != "2026" {
				t.Errorf("standings season = %s, want 2026", r.URL.Query().Get("season"))
			}
			serveFixture(t, w, "standings_fifa_world.json")
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			if strings.HasPrefix(r.URL.Query().Get("dates"), "202606") {
				serveFixture(t, w, "scoreboard_form.json") // Mexico's 3 real group games
			} else {
				_, _ = w.Write([]byte(`{"events":[]}`))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	p.now = func() time.Time { return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) }

	stats, _, err := p.TeamStats(context.Background(), 2026, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(stats) != 4 {
		t.Fatalf("teams = %d, want 4 (groups A and B, two entries each)", len(stats))
	}

	// Fixture totals: GF 6+2+8+5 = 21 over 4x3 = 12 team-games, so the
	// competition average is 21/12 = 1.75 goals per team per match.
	mexico := stats[ids.Team(leagueWC, "203")]
	if mexico.GamesPlayed != 3 || mexico.Wins != 3 || mexico.Losses != 0 || mexico.Draws != 0 {
		t.Errorf("Mexico record wrong: %+v", mexico)
	}
	if mexico.Stats.Offensive != nil || mexico.Stats.Defensive != nil || mexico.Stats.Advanced != nil {
		t.Error("basketball blocks must stay nil for soccer")
	}
	soc := mexico.Stats.Soccer
	if soc == nil {
		t.Fatal("soccer block missing")
	}
	// Mexico: 6 GF / 3 GP = 2.0 per match; raw attack = 2.0/1.75 = 8/7;
	// shrunk (K=5) = (3*(8/7) + 5) / (3+5) = 59/56.
	if soc.GoalsForPerMatch != 2.0 || soc.GoalsAgainstPerMatch != 0.0 {
		t.Errorf("Mexico per-match goals wrong: %+v", soc)
	}
	if math.Abs(soc.AttackStrength-59.0/56.0) > 1e-9 {
		t.Errorf("attack strength = %v, want 59/56", soc.AttackStrength)
	}
	// Mexico defense: 0 GA, raw 0; shrunk = (3*0 + 5) / 8 = 0.625.
	if math.Abs(soc.DefenseStrength-0.625) > 1e-9 {
		t.Errorf("defense strength = %v, want 0.625", soc.DefenseStrength)
	}
	// Form from Mexico's three real group games (2-0, 1-0, 3-0 wins).
	if soc.FormGoalsForLast5 != 2.0 || soc.FormGoalsAgainstLast5 != 0.0 || soc.FormPointsLast5 != 9 {
		t.Errorf("Mexico form wrong: %+v", soc)
	}

	// Canada drew once (1W-1D-1L); draws surface both top-level and in the
	// soccer block.
	var canada model.TeamStats
	for _, ts := range stats {
		if ts.TeamAbbreviation == "CAN" {
			canada = ts
		}
	}
	if canada.Draws != 1 || canada.Stats.Soccer == nil || canada.Stats.Soccer.Draws != 1 {
		t.Errorf("Canada draws wrong: %+v", canada)
	}
}

func TestTeamStatsEPLStandings(t *testing.T) {
	p := testProvider(t, PremierLeague(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/standings") {
			serveFixture(t, w, "standings_eng1.json")
			return
		}
		_, _ = w.Write([]byte(`{"events":[]}`)) // off-season: no form games
	}))
	p.now = func() time.Time { return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) }

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(stats) != 3 {
		t.Fatalf("teams = %d, want 3", len(stats))
	}

	var arsenal model.TeamStats
	for _, ts := range stats {
		if ts.TeamAbbreviation == "ARS" {
			arsenal = ts
		}
	}
	if arsenal.GamesPlayed != 38 || arsenal.Wins != 26 || arsenal.Draws != 7 || arsenal.Losses != 5 {
		t.Errorf("Arsenal 2025-26 record wrong: %+v", arsenal)
	}
	// Fixture totals: GF 71+77+69 = 217 over 3x38 = 114 team-games.
	avg := 217.0 / 114.0
	wantAttack := (38.0*((71.0/38.0)/avg) + 5) / 43.0
	if math.Abs(arsenal.Stats.Soccer.AttackStrength-wantAttack) > 1e-9 {
		t.Errorf("attack strength = %v, want %v", arsenal.Stats.Soccer.AttackStrength, wantAttack)
	}
	if arsenal.Stats.Soccer.FormPointsLast5 != 0 {
		t.Errorf("off-season form must be zero: %+v", arsenal.Stats.Soccer)
	}
}

func TestTeamStatsRollingWindowUnsupported(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second), FIFAWorldCup())
	if _, _, err := p.TeamStats(context.Background(), 2026, 10); err == nil ||
		!strings.Contains(err.Error(), "unsupported for soccer") {
		t.Errorf("window > 0 must error, got %v", err)
	}
}

func TestPlayerGameLogDescoped(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second), PremierLeague())

	log, fetches, err := p.PlayerGameLog(context.Background(), model.PlayerDetail{}, 2025)
	if err != nil || log == nil || len(log) != 0 || fetches != nil {
		t.Errorf("PlayerGameLog must return an empty log with nil error: %v %v %v", log, fetches, err)
	}
}

func TestWorldCupSeasonYear(t *testing.T) {
	wc := FIFAWorldCup()
	for now, want := range map[time.Time]int{
		time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC):   2026, // during the tournament
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC):   2026, // tournament year, pre-tournament
		time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC):   2026, // between tournaments
		time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC): 2026,
		time.Date(2030, 6, 1, 0, 0, 0, 0, time.UTC):   2030,
	} {
		if got := wc.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}

func TestEPLSeasonRollover(t *testing.T) {
	epl := PremierLeague()
	for now, want := range map[time.Time]int{
		time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC):  2026, // just after kickoff
		time.Date(2026, 12, 26, 0, 0, 0, 0, time.UTC): 2026, // boxing day
		time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC):   2026, // january transfer window
		time.Date(2027, 5, 20, 0, 0, 0, 0, time.UTC):  2026, // final matchday
		time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC):  2025, // day before rollover
		time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC):   2026, // rollover day
	} {
		if got := epl.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}

	start, end := epl.SeasonWindow(2026)
	if start != time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC) || end != time.Date(2027, 6, 15, 0, 0, 0, 0, time.UTC) {
		t.Errorf("season window wrong: %s – %s", start, end)
	}
}

func TestBoxScoreNormalization(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/summary") || r.URL.Query().Get("event") != "760501" {
			http.NotFound(w, r)
			return
		}
		serveFixture(t, w, "summary_boxscore.json")
	}))

	box, fetches, err := p.BoxScore(context.Background(), "760501")
	if err != nil {
		t.Fatalf("BoxScore failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}

	if box.Sport != "SOCCER" {
		t.Errorf("sport = %q, want SOCCER", box.Sport)
	}
	if box.GameID != ids.Game(leagueWC, "760501") {
		t.Errorf("game id not minted with ids.Game: %s", box.GameID)
	}
	eng, bel := box.HomeTeam, box.AwayTeam
	if eng.Abbreviation != "ENG" || bel.Abbreviation != "BEL" {
		t.Fatalf("home/away = %s/%s, want ENG/BEL (from header homeAway)", eng.Abbreviation, bel.Abbreviation)
	}
	if eng.ID != ids.Team(leagueWC, "448") || bel.ID != ids.Team(leagueWC, "459") {
		t.Errorf("team ids not minted with ids.Team: %s / %s", eng.ID, bel.ID)
	}
	if eng.Score != 2 || bel.Score != 1 {
		t.Errorf("scores = %d-%d, want 2-1", eng.Score, bel.Score)
	}
	if eng.TeamStats != nil || len(eng.Players) != 0 {
		t.Error("basketball blocks must be empty for soccer")
	}

	// Outfield block plus the goalkeeper block, deduplicated by athlete.
	if len(eng.SoccerPlayers) != 3 || len(bel.SoccerPlayers) != 2 {
		t.Fatalf("players = %d/%d, want 3/2", len(eng.SoccerPlayers), len(bel.SoccerPlayers))
	}
	byName := make(map[string]model.SoccerPlayerBoxScore)
	for _, pl := range append(eng.SoccerPlayers, bel.SoccerPlayers...) {
		byName[pl.PlayerName] = pl
	}

	kane := byName["Harry Kane"]
	if kane.PlayerID != ids.Player(leagueWC, "310580") {
		t.Errorf("player id not minted with ids.Player: %s", kane.PlayerID)
	}
	if kane.Position != "F" || kane.Minutes != 90 || kane.Goals != 2 || kane.Assists != 0 ||
		kane.Shots != 5 || kane.ShotsOnTarget != 3 || kane.YellowCards != 0 || kane.RedCards != 0 {
		t.Errorf("line wrong: %+v", kane)
	}
	if bellingham := byName["Jude Bellingham"]; bellingham.Assists != 1 || bellingham.YellowCards != 1 || bellingham.Minutes != 84 {
		t.Errorf("line wrong: %+v", bellingham)
	}
	// The goalkeeper block has different keys: card and minute synonyms
	// resolve, everything absent defaults to zero.
	if gk := byName["Jordan Pickford"]; gk.Minutes != 90 || gk.Goals != 0 || gk.Shots != 0 {
		t.Errorf("goalkeeper line wrong: %+v", gk)
	}
	if kdb := byName["Kevin De Bruyne"]; kdb.Goals != 1 || kdb.RedCards != 1 {
		t.Errorf("line wrong: %+v", kdb)
	}
	// Unparseable stat values ("-") default to zero, never fail.
	if sub := byName["Late Sub"]; sub.Minutes != 0 {
		t.Errorf("unparseable minutes should default to 0: %+v", sub)
	}
}

func TestPlayersFromRosters(t *testing.T) {
	emptyRoster := []byte(`{"team":{"id":"0"},"athletes":[]}`)
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams/448/roster"):
			serveFixture(t, w, "roster_eng.json")
		case strings.Contains(r.URL.Path, "/teams/") && strings.HasSuffix(r.URL.Path, "/roster"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(emptyRoster)
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams_fifa_world.json")
		default:
			http.NotFound(w, r)
		}
	}))

	summaries, details, fetches, err := p.Players(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Players failed: %v", err)
	}
	// One teams call plus one roster call per team in the fixture.
	if len(fetches) != 5 {
		t.Errorf("fetches = %d, want 5", len(fetches))
	}
	if len(summaries) != 3 || len(details) != 3 {
		t.Fatalf("players = %d, details = %d, want 3 each", len(summaries), len(details))
	}

	var kane model.PlayerSummary
	for _, s := range summaries {
		if s.LastName == "Kane" {
			kane = s
		}
	}
	if kane.ID != ids.Player(leagueWC, "310580") {
		t.Fatalf("id = %s, want UUIDv5 of espn athlete 310580", kane.ID)
	}
	if kane.FirstName != "Harry" || kane.TeamID != ids.Team(leagueWC, "448") || kane.TeamAbbreviation != "ENG" {
		t.Errorf("summary wrong: %+v", kane)
	}
	if kane.Position != "F" || kane.JerseyNumber == nil || *kane.JerseyNumber != 9 {
		t.Errorf("position/jersey wrong: %+v", kane)
	}
	if kane.League != model.LeagueFIFAWC || kane.Status != model.PlayerActive || kane.ExternalIDs["espn"] != "310580" {
		t.Errorf("summary wrong: %+v", kane)
	}

	stats := details[kane.ID].SoccerSeasonStats
	if stats == nil {
		t.Fatal("season stats missing for athlete with a statistics block")
	}
	if stats.Appearances != 6 || stats.Minutes != 512 || stats.Goals != 5 || stats.Assists != 2 ||
		stats.Shots != 21 || stats.ShotsOnTarget != 11 || stats.YellowCards != 1 || stats.RedCards != 0 {
		t.Errorf("season stats wrong: %+v", stats)
	}

	// No statistics block → nil season stats, not zeros.
	bellingham := details[ids.Player(leagueWC, "384967")]
	if bellingham.SoccerSeasonStats != nil {
		t.Errorf("season stats should be nil without a statistics block: %+v", bellingham.SoccerSeasonStats)
	}
	// Name fallback: athletes without structured names split displayName.
	pickford := details[ids.Player(leagueWC, "293119")]
	if pickford.FirstName != "Jordan" || pickford.LastName != "Pickford" {
		t.Errorf("displayName split wrong: %+v", pickford.PlayerSummary)
	}
}

func TestMonthChunks(t *testing.T) {
	chunks := monthChunks(
		time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	)
	if len(chunks) != 3 {
		t.Fatalf("chunks = %d, want 3", len(chunks))
	}
	if !chunks[0][1].Equal(time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("june chunk end = %s, want June 30", chunks[0][1])
	}
	if !chunks[2][0].Equal(chunks[2][1]) {
		t.Errorf("final chunk should be the single day Aug 1: %v", chunks[2])
	}

	day := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	if got := monthChunks(day, day); len(got) != 1 || !got[0][0].Equal(day) || !got[0][1].Equal(day) {
		t.Errorf("single-day chunking wrong: %v", got)
	}
}

func TestProviderIdentity(t *testing.T) {
	p := NewProvider(NewClient("http://unused", time.Second), FIFAWorldCup())
	if p.League() != model.LeagueFIFAWC {
		t.Errorf("League() = %s, want FIFA_WC", p.League())
	}
	if p.Source() != sourceESPN {
		t.Errorf("Source() = %s, want %s", p.Source(), sourceESPN)
	}
	if got := p.SeasonYear(time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)); got != 2026 {
		t.Errorf("SeasonYear() = %d, want 2026", got)
	}
}

func upstream500(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
}

func TestTeamsUpstreamError(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), upstream500(t))
	if _, _, _, err := p.Teams(context.Background()); err == nil {
		t.Error("upstream 500 on teams must error")
	}
}

func TestTeamsMalformedJSON(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{invalid`))
	}))
	if _, _, _, err := p.Teams(context.Background()); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Errorf("malformed JSON must produce a decode error, got %v", err)
	}
}

func TestTeamStatsStandingsError(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), upstream500(t))
	if _, _, err := p.TeamStats(context.Background(), 2026, 0); err == nil {
		t.Error("upstream 500 on standings must error")
	}
}

func TestTeamStatsFormFetchErrorDegrades(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/standings"):
			serveFixture(t, w, "standings_fifa_world.json")
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	p.now = func() time.Time { return time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC) }

	stats, _, err := p.TeamStats(context.Background(), 2026, 0)
	if err != nil {
		t.Fatalf("TeamStats must degrade rather than fail on form errors: %v", err)
	}
	mexico := stats[ids.Team(leagueWC, "203")]
	if mexico.Stats.Soccer == nil || mexico.Stats.Soccer.FormPointsLast5 != 0 {
		t.Errorf("form should be zero when the form fetch fails: %+v", mexico.Stats.Soccer)
	}
}

func TestRecentFormSeasonNotStarted(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/standings"):
			serveFixture(t, w, "standings_fifa_world.json")
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			t.Error("scoreboard should not be fetched before the season starts")
		default:
			http.NotFound(w, r)
		}
	}))
	p.now = func() time.Time { return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC) }

	stats, fetches, err := p.TeamStats(context.Background(), 2026, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (standings only, no scoreboard)", len(fetches))
	}
	mexico := stats[ids.Team(leagueWC, "203")]
	if mexico.Stats.Soccer.FormPointsLast5 != 0 {
		t.Errorf("form must be zero before the season starts: %+v", mexico.Stats.Soccer)
	}
}

func TestPlayersUpstreamTeamsError(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), upstream500(t))
	if _, _, _, err := p.Players(context.Background(), 2026); err == nil {
		t.Error("upstream 500 on teams must error")
	}
}

func TestPlayersRosterError(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams_fifa_world.json")
		case strings.HasSuffix(r.URL.Path, "/roster"):
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	_, _, _, err := p.Players(context.Background(), 2026)
	if err == nil || !strings.Contains(err.Error(), "fetch roster for team") {
		t.Errorf("roster failure must wrap with team context, got %v", err)
	}
}

func TestNormalizeRosterSkipsEmptyID(t *testing.T) {
	body := `{"team":{"id":"1"},"athletes":[{"id":"","displayName":"Ghost"},{"id":"55","displayName":"Real Player","position":{"abbreviation":"F"},"statistics":[{"name":"goals","value":2}]}]}`
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams_fifa_world.json")
		case strings.HasSuffix(r.URL.Path, "/roster"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		default:
			http.NotFound(w, r)
		}
	}))
	summaries, details, _, err := p.Players(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Players failed: %v", err)
	}
	for _, s := range summaries {
		if s.FirstName == "Ghost" || s.LastName == "Ghost" {
			t.Errorf("athlete with empty id must be skipped: %+v", s)
		}
	}
	real := details[ids.Player(leagueWC, "55")]
	if real.SoccerSeasonStats == nil || real.SoccerSeasonStats.Goals != 2 {
		t.Errorf("goals stat wrong: %+v", real.SoccerSeasonStats)
	}
	if real.SoccerSeasonStats.Assists != 0 || real.SoccerSeasonStats.RedCards != 0 {
		t.Errorf("stats with no matching synonym must default to zero: %+v", real.SoccerSeasonStats)
	}
}

func TestBoxScoreUpstreamError(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), upstream500(t))
	if _, _, err := p.BoxScore(context.Background(), "1"); err == nil {
		t.Error("upstream 500 on summary must error")
	}
}

func TestBoxScoreNoData(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"header":{"competitions":[]}}`))
	}))
	_, _, err := p.BoxScore(context.Background(), "1")
	if err == nil || !strings.Contains(err.Error(), "no box score data") {
		t.Errorf("empty header must error, got %v", err)
	}
}

func TestBoxScoreMissingCompetitors(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"header":{"competitions":[{"competitors":[]}]}}`))
	}))
	_, _, err := p.BoxScore(context.Background(), "1")
	if err == nil || !strings.Contains(err.Error(), "no box score data") {
		t.Errorf("missing home/away competitors must error, got %v", err)
	}
}

func TestBoxScorePlayerLinesDedup(t *testing.T) {
	body := `{
		"header": {"competitions": [{"competitors": [
			{"homeAway":"home","score":"1","team":{"id":"1","displayName":"Home FC","abbreviation":"HOM"}},
			{"homeAway":"away","score":"0","team":{"id":"2","displayName":"Away FC","abbreviation":"AWY"}}
		]}]},
		"boxscore": {"players": [{"team":{"id":"1"},"statistics": [
			{"name":"general","keys":["minutesPlayed","totalGoals"],"labels":["MIN","G"],"athletes":[
				{"athlete":{"id":"","displayName":"No ID"},"stats":["90","0"]},
				{"athlete":{"id":"1001","displayName":"Dup Player"},"stats":["90","1"]}
			]},
			{"name":"extra","keys":["minutesPlayed","totalGoals"],"labels":["MIN","G"],"athletes":[
				{"athlete":{"id":"1001","displayName":"Dup Player"},"stats":["1","9"]}
			]}
		]}]}
	}`
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))

	box, _, err := p.BoxScore(context.Background(), "1")
	if err != nil {
		t.Fatalf("BoxScore failed: %v", err)
	}
	if len(box.HomeTeam.SoccerPlayers) != 1 {
		t.Fatalf("players = %d, want 1 (empty-id athlete dropped, duplicate collapsed)", len(box.HomeTeam.SoccerPlayers))
	}
	if pl := box.HomeTeam.SoccerPlayers[0]; pl.PlayerName != "Dup Player" || pl.Goals != 1 {
		t.Errorf("first-seen stat line should win over the duplicate block: %+v", pl)
	}
}

func TestScheduleUpstreamError(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), upstream500(t))
	if _, _, err := p.Schedule(context.Background(), 2026); err == nil || !strings.Contains(err.Error(), "fetch scoreboard") {
		t.Errorf("upstream 500 on scoreboard must error, got %v", err)
	}
}

func TestScheduleDedupesRepeatedEvents(t *testing.T) {
	dup := Competition{
		League:     model.LeagueFIFAWC,
		ESPNCode:   "dup.test",
		SeasonYear: func(time.Time) int { return 2026 },
		SeasonWindow: func(int) (time.Time, time.Time) {
			return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
		},
		SeasonType: func(string) model.SeasonType { return model.SeasonRegular },
	}
	const eventJSON = `{"events":[{"id":"999","date":"2026-06-15T12:00Z","season":{"year":2026,"slug":"group-stage"},
		"status":{"period":2,"type":{"name":"STATUS_FULL_TIME","state":"post","completed":true}},
		"competitions":[{"competitors":[
			{"homeAway":"home","score":"1","team":{"id":"1","displayName":"Home FC","abbreviation":"HOM"}},
			{"homeAway":"away","score":"0","team":{"id":"2","displayName":"Away FC","abbreviation":"AWY"}}]}]}]}`

	var scoreboardCalls atomic.Int32
	p := testProvider(t, dup, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scoreboardCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(eventJSON))
	}))

	games, fetches, err := p.Schedule(context.Background(), 2026)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	if scoreboardCalls.Load() != 2 {
		t.Fatalf("scoreboard calls = %d, want 2 (June and July chunks)", scoreboardCalls.Load())
	}
	if len(fetches) != 2 {
		t.Errorf("fetches = %d, want 2", len(fetches))
	}
	if len(games) != 1 {
		t.Errorf("games = %d, want 1 (the repeated event across chunks must be deduplicated)", len(games))
	}
}

func TestScoreboardUpstreamError(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), upstream500(t))
	if _, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("upstream 500 on scoreboard must error")
	}
}

func TestScoreboardMalformedEventSkipped(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"events":[{"id":"malformed"}]}`))
	}))
	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if _, ok := updates["malformed"]; ok {
		t.Error("event with no competitors should be skipped, not surfaced")
	}
}

func TestScoreboardExtraTimeSummaryFailureDegrades(t *testing.T) {
	p := testProvider(t, FIFAWorldCup(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/summary"):
			http.Error(w, "boom", http.StatusInternalServerError)
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			serveFixture(t, w, "scoreboard_knockout.json")
		default:
			http.NotFound(w, r)
		}
	}))

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	aet := updates["760493"]
	if aet.Result == nil || !aet.Result.Overtime {
		t.Fatalf("AET result wrong: %+v", aet.Result)
	}
	if aet.Result.HomeScore != 3 || aet.Result.AwayScore != 2 {
		t.Errorf("full-time score should still be present: %+v", aet.Result)
	}
	if aet.Result.RegulationHomeScore != nil || aet.Result.RegulationAwayScore != nil {
		t.Errorf("summary failure must degrade to no regulation fields: %+v", aet.Result)
	}
	if len(aet.Result.PeriodScores) != 0 {
		t.Errorf("summary failure must degrade to no period scores: %+v", aet.Result.PeriodScores)
	}
}

func TestMapStatusVariants(t *testing.T) {
	cases := []struct {
		name string
		in   espnStatusType
		want model.GameStatus
	}{
		{"postponed", espnStatusType{Name: "STATUS_POSTPONED", State: "pre"}, model.GamePostponed},
		{"canceled", espnStatusType{Name: "STATUS_CANCELED", State: "pre"}, model.GameCancelled},
		{"canceled double-l variant", espnStatusType{Name: "STATUS_CANCELLED", State: "pre"}, model.GameCancelled}, //nolint:misspell // both ESPN spellings are normalized
		{"suspended", espnStatusType{Name: "STATUS_SUSPENDED", State: "in"}, model.GameSuspended},
		{"abandoned", espnStatusType{Name: "STATUS_ABANDONED", State: "in"}, model.GameSuspended},
		{"delayed", espnStatusType{Name: "STATUS_DELAYED", State: "pre"}, model.GameSuspended},
		{"post-not-completed", espnStatusType{Name: "STATUS_HALFTIME", State: "post", Completed: false}, model.GamePostponed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapStatus(tc.in); got != tc.want {
				t.Errorf("mapStatus(%+v) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestHomeAwayCompetitorsEmpty(t *testing.T) {
	if _, _, ok := homeAwayCompetitors(espnEvent{}); ok {
		t.Error("empty event should not resolve home/away competitors")
	}
}

func TestParseLinescoresEdgeCases(t *testing.T) {
	if got := parseLinescores(espnCompetitor{}); got != nil {
		t.Errorf("no linescores should return nil, got %v", got)
	}
	malformed := espnCompetitor{Linescores: []espnLinescore{{DisplayValue: "1"}, {DisplayValue: "not-a-number"}}}
	if got := parseLinescores(malformed); got != nil {
		t.Errorf("malformed linescore should return nil, got %v", got)
	}
}

func TestNormalizeEventMalformed(t *testing.T) {
	comp := FIFAWorldCup()
	if _, ok := normalizeEvent(comp, espnEvent{ID: "1", Date: "2026-06-01T12:00Z"}, 2026, nil, nil); ok {
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
	if _, ok := normalizeEvent(comp, ev, 2026, nil, nil); ok {
		t.Error("event with unparseable date should not normalize")
	}
}

func TestFormFromGamesCapAndDraws(t *testing.T) {
	team := "team-x"
	opp := "team-y"
	var games []model.Game
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 6; i++ {
		games = append(games, model.Game{
			HomeTeam:       model.TeamRef{ID: team},
			AwayTeam:       model.TeamRef{ID: opp},
			Status:         model.GameFinal,
			ScheduledStart: base.AddDate(0, 0, i),
			Result:         &model.GameResult{HomeScore: 1, AwayScore: 1}, // draw
		})
	}
	form := formFromGames(games)
	r := form[team]
	if r.Matches != formMatches {
		t.Errorf("matches = %d, want cap of %d", r.Matches, formMatches)
	}
	if r.Points != formMatches {
		t.Errorf("draw points = %d, want %d (1 pt per drawn match, capped)", r.Points, formMatches)
	}
}

func TestStatValueMissing(t *testing.T) {
	e := standingsEntry{Stats: []standingStat{{Name: "wins", Value: 5}}}
	if got := statValue(e, "missing"); got != 0 {
		t.Errorf("statValue for missing name = %v, want 0", got)
	}
}

func TestSplitDisplayNameSingleWord(t *testing.T) {
	first, last := splitDisplayName("Pele")
	if first != "" || last != "Pele" {
		t.Errorf("single-word name: got (%q, %q), want (\"\", \"Pele\")", first, last)
	}
}

func TestPremierLeagueSeasonTypeAlwaysRegular(t *testing.T) {
	epl := PremierLeague()
	if got := epl.SeasonType("anything"); got != model.SeasonRegular {
		t.Errorf("EPL season type = %s, want REGULAR", got)
	}
}

func TestCompetitionForLeague(t *testing.T) {
	if c, err := CompetitionForLeague(model.LeagueFIFAWC); err != nil || c.ESPNCode != "fifa.world" {
		t.Errorf("FIFA_WC resolution wrong: %+v, %v", c, err)
	}
	if c, err := CompetitionForLeague(model.LeagueEPL); err != nil || c.ESPNCode != "eng.1" {
		t.Errorf("EPL resolution wrong: %+v, %v", c, err)
	}
	if _, err := CompetitionForLeague(model.LeagueNBA); err == nil {
		t.Error("non-soccer league should error")
	}
}
