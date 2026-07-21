package cbbd

// Tests for the NCAA_BB adapter. The ESPN real-time fixtures
// (testdata/teams.json, testdata/scoreboard.json) are real trimmed captures of
// ESPN's men's-college-basketball site API for the completed 2025-26 season
// (recorded 2026-07-06), including a real single-overtime game (Villanova at
// UConn, 67-75). The CBBD season-stats and adjusted-ratings responses are
// DERIVED (the project has no CBBD key): they are embedded as string constants
// below, each with the required provenance header, and carry realistic 2025-26
// records/scoring for teams present in the ESPN team list.

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

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnbb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// DERIVED from CBBD documented schema 2026-07-06 — validate against live API in the November verification session.
// Team names match the ESPN team-list locations so the name index resolves them
// to canonical ids. Counting stats are season totals (divided by games in the
// normalizer); percentages are fractions in [0,1].
const derivedSeasonStats = `[
  {"season":2026,"teamId":2509,"team":"Purdue","conference":"Big Ten","games":32,"wins":24,"losses":8,"pace":67.5,
   "teamStats":{"fieldGoals":{"pct":0.478},"threePointFieldGoals":{"pct":0.372},"freeThrows":{"pct":0.735},
     "rebounds":{"total":1152},"turnovers":{"total":384},"points":{"total":2560},
     "fourFactors":{"effectiveFieldGoalPct":0.535,"turnoverRatio":0.165,"offensiveReboundPct":0.315,"freeThrowRate":0.34},
     "assists":512,"blocks":96,"steals":192,"possessions":2160,"rating":118.5,"trueShooting":0.575},
   "opponentStats":{"fieldGoals":{"pct":0.421},"threePointFieldGoals":{"pct":0.318},"points":{"total":2304},"rating":98.7}},
  {"season":2026,"teamId":150,"team":"Duke","conference":"ACC","games":34,"wins":30,"losses":4,"pace":69.0,
   "teamStats":{"fieldGoals":{"pct":0.492},"threePointFieldGoals":{"pct":0.383},"freeThrows":{"pct":0.712},
     "rebounds":{"total":1258},"turnovers":{"total":374},"points":{"total":2822},
     "fourFactors":{"effectiveFieldGoalPct":0.558,"turnoverRatio":0.158,"offensiveReboundPct":0.33,"freeThrowRate":0.36},
     "assists":578,"blocks":170,"steals":204,"possessions":2346,"rating":124.1,"trueShooting":0.592},
   "opponentStats":{"fieldGoals":{"pct":0.405},"threePointFieldGoals":{"pct":0.305},"points":{"total":2380},"rating":94.2}},
  {"season":2026,"teamId":356,"team":"Illinois","conference":"Big Ten","games":33,"wins":22,"losses":11,"pace":70.2,
   "teamStats":{"fieldGoals":{"pct":0.461},"threePointFieldGoals":{"pct":0.351},"freeThrows":{"pct":0.708},
     "rebounds":{"total":1221},"turnovers":{"total":429},"points":{"total":2607},
     "fourFactors":{"effectiveFieldGoalPct":0.521,"turnoverRatio":0.178,"offensiveReboundPct":0.352,"freeThrowRate":0.33},
     "assists":495,"blocks":132,"steals":198,"possessions":2310,"rating":114.2,"trueShooting":0.556},
   "opponentStats":{"fieldGoals":{"pct":0.436},"threePointFieldGoals":{"pct":0.332},"points":{"total":2409},"rating":101.5}}
]`

// DERIVED from CBBD documented schema 2026-07-06 — validate against live API in the November verification session.
// Adjusted efficiencies for two of the three teams; Illinois is intentionally
// absent to exercise independent degradation (its margin stays zero).
const derivedAdjusted = `[
  {"season":2026,"teamId":2509,"team":"Purdue","conference":"Big Ten","offensiveRating":121.0,"defensiveRating":96.7,"netRating":24.3},
  {"season":2026,"teamId":150,"team":"Duke","conference":"ACC","offensiveRating":126.4,"defensiveRating":92.1,"netRating":34.3}
]`

func serveFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	_, _ = w.Write(data)
}

// mux routes ESPN requests to the recorded fixtures and CBBD requests to the
// derived string constants. cbbdCalls counts CBBD requests; when a nil counter
// is passed, any CBBD call fails the test (used by the keyless test to prove
// the provider never hits CBBD without a key).
func mux(t *testing.T, cbbdCalls *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			serveFixture(t, w, "scoreboard.json")
		case r.URL.Path == "/stats/team/season":
			cbbdHit(t, w, cbbdCalls, derivedSeasonStats)
		case r.URL.Path == "/ratings/adjusted":
			cbbdHit(t, w, cbbdCalls, derivedAdjusted)
		default:
			http.NotFound(w, r)
		}
	})
}

func cbbdHit(t *testing.T, w http.ResponseWriter, calls *atomic.Int32, body string) {
	t.Helper()
	if calls == nil {
		t.Errorf("CBBD endpoint must not be called without an API key")
		http.Error(w, "unexpected cbbd call", http.StatusInternalServerError)
		return
	}
	calls.Add(1)
	_, _ = w.Write([]byte(body))
}

func testProvider(t *testing.T, apiKey string, cbbdCalls *atomic.Int32) *Provider {
	t.Helper()
	server := httptest.NewServer(mux(t, cbbdCalls))
	t.Cleanup(server.Close)
	return NewProvider(
		espnbb.NewClient(server.URL, 5*time.Second),
		NewClient(server.URL, apiKey, 5*time.Second),
	)
}

func TestTeamsNormalization(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, "k", &calls)

	teams, details, fetches, err := p.Teams(context.Background())
	if err != nil {
		t.Fatalf("Teams failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
	if len(teams) != 6 || len(details) != 6 {
		t.Fatalf("teams = %d, details = %d, want 6 each", len(teams), len(details))
	}

	var purdue model.TeamSummary
	for _, team := range teams {
		if team.Abbreviation == "PUR" {
			purdue = team
		}
	}
	if purdue.ID != ids.Team(leagueNCAABB, "2509") {
		t.Errorf("id = %s, want UUIDv5 of espn team 2509", purdue.ID)
	}
	if purdue.Name != "Purdue Boilermakers" || purdue.League != model.LeagueNCAABB {
		t.Errorf("summary wrong: %+v", purdue)
	}
	if details[purdue.ID].Venue != nil {
		t.Errorf("college teams carry no venue, got %+v", details[purdue.ID].Venue)
	}
}

func TestScheduleNormalization(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, "k", &calls)

	games, _, err := p.Schedule(context.Background(), 2025)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	if len(games) != 3 {
		t.Fatalf("games = %d, want 3 unique", len(games))
	}

	byExternal := make(map[string]model.Game, len(games))
	for _, g := range games {
		byExternal[g.ExternalID] = g
	}

	// Real overtime game (Villanova 67 at UConn 75): college basketball is two
	// halves, so overtime is period 3 — three period scores, Overtime set, and
	// no regulation fields (ADR-027).
	ot := byExternal["401822913"]
	if ot.ID != ids.Game(leagueNCAABB, "401822913") {
		t.Errorf("OT identity wrong: %+v", ot)
	}
	if ot.HomeTeam.Abbreviation != "CONN" || ot.AwayTeam.Abbreviation != "VILL" {
		t.Errorf("OT team refs wrong: %+v / %+v", ot.HomeTeam, ot.AwayTeam)
	}
	if ot.Result == nil || !ot.Result.Overtime || len(ot.Result.PeriodScores) != 3 {
		t.Fatalf("OT result wrong: %+v", ot.Result)
	}
	if ot.Result.HomeScore != 75 || ot.Result.AwayScore != 67 || ot.Result.Margin != 8 {
		t.Errorf("OT scores wrong: %+v", ot.Result)
	}
	if ot.Result.RegulationHomeScore != nil || ot.Result.RegulationAwayScore != nil {
		t.Errorf("basketball must carry no regulation fields: %+v", ot.Result)
	}

	// Regulation final (Illinois 88 at Purdue 82): two halves, not overtime.
	reg := byExternal["401825468"]
	if reg.Result == nil || reg.Result.Overtime || len(reg.Result.PeriodScores) != 2 {
		t.Fatalf("regulation final result wrong: %+v", reg.Result)
	}
	if reg.Result.HomeScore != 82 || reg.Result.AwayScore != 88 {
		t.Errorf("regulation scores wrong: %+v", reg.Result)
	}
}

func TestScoreboard(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, "k", &calls)

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2026, 1, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	ot := updates["401822913"]
	if ot.Status != model.GameFinal || ot.Result == nil {
		t.Fatalf("OT update wrong: %+v", ot)
	}
	if ot.HomeScore != 75 || ot.AwayScore != 67 || !ot.Result.Overtime {
		t.Errorf("OT update result wrong: %+v", ot.Result)
	}
	if len(ot.Result.PeriodScores) != 3 {
		t.Errorf("OT update period scores = %d, want 3", len(ot.Result.PeriodScores))
	}
}

func TestTeamStats(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, "test-key", &calls)

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("cbbd calls = %d, want 2 (season stats + adjusted ratings)", calls.Load())
	}
	if len(stats) != 3 {
		t.Fatalf("teams = %d, want 3", len(stats))
	}

	pur := stats[ids.Team(leagueNCAABB, "2509")]
	if pur.Season != 2025 || pur.GamesPlayed != 32 || pur.Wins != 24 || pur.Losses != 8 {
		t.Errorf("Purdue record wrong: %+v", pur)
	}
	if pur.TeamAbbreviation != "PUR" {
		t.Errorf("abbreviation wrong: %+v", pur)
	}
	if pur.Stats.Hockey != nil || pur.Stats.Soccer != nil || pur.Stats.Baseball != nil || pur.Stats.Football != nil {
		t.Error("non-basketball blocks must stay nil for NCAA_BB")
	}
	off, def, adv := pur.Stats.Offensive, pur.Stats.Defensive, pur.Stats.Advanced
	if off == nil || def == nil || adv == nil {
		t.Fatal("basketball blocks missing")
	}
	if math.Abs(off.PointsPerGame-2560.0/32.0) > 1e-9 {
		t.Errorf("points/game = %v, want 2560/32", off.PointsPerGame)
	}
	if off.FieldGoalPct != 0.478 || off.OffensiveRating != 118.5 || off.Pace != 67.5 || off.EffectiveFGPct != 0.535 {
		t.Errorf("offensive block wrong: %+v", off)
	}
	if math.Abs(off.ReboundsPerGame-36.0) > 1e-9 || math.Abs(off.AssistsPerGame-16.0) > 1e-9 {
		t.Errorf("offensive per-game rebounds/assists wrong: %+v", off)
	}
	if math.Abs(def.PointsAllowedPerGame-72.0) > 1e-9 || def.DefensiveRating != 98.7 || def.OpponentFGPct != 0.421 {
		t.Errorf("defensive block wrong: %+v", def)
	}
	if math.Abs(adv.NetRating-(118.5-98.7)) > 1e-9 || adv.TrueShootingPct != 0.575 {
		t.Errorf("advanced block wrong: %+v", adv)
	}
	// adjusted_efficiency_margin maps straight from /ratings/adjusted netRating.
	if adv.AdjustedEfficiencyMargin != 24.3 {
		t.Errorf("adjusted_efficiency_margin = %v, want 24.3", adv.AdjustedEfficiencyMargin)
	}

	// Illinois is absent from the adjusted feed, so its margin degrades to zero
	// while its other blocks still populate.
	ill := stats[ids.Team(leagueNCAABB, "356")]
	if ill.Stats.Advanced.AdjustedEfficiencyMargin != 0 {
		t.Errorf("Illinois adjusted margin must be zero (missing from feed): %v", ill.Stats.Advanced.AdjustedEfficiencyMargin)
	}
	if ill.Stats.Offensive == nil || ill.Stats.Offensive.OffensiveRating != 114.2 {
		t.Errorf("Illinois offensive block wrong: %+v", ill.Stats.Offensive)
	}
}

func TestTeamStatsKeylessDegradation(t *testing.T) {
	// A nil cbbdCalls counter makes any CBBD request fail the test, proving
	// the keyless provider runs watcher-only without touching CBBD.
	p := testProvider(t, "", nil)

	stats, fetches, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("keyless TeamStats must not error: %v", err)
	}
	if stats == nil || len(stats) != 0 {
		t.Errorf("keyless TeamStats must return empty stats, got %d", len(stats))
	}
	// The ESPN team list is still fetched (it anchors ids), so one fetch.
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (ESPN teams only)", len(fetches))
	}
}

func TestTeamStatsCBBDOutageDegrades(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case r.URL.Path == "/stats/team/season":
			http.Error(w, "boom", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnbb.NewClient(server.URL, 5*time.Second), NewClient(server.URL, "k", 5*time.Second))

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("CBBD outage must degrade, not error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("stats = %d, want 0 on CBBD season-stats outage", len(stats))
	}
}

func TestTeamStatsRollingWindowUnsupported(t *testing.T) {
	p := NewProvider(espnbb.NewClient("http://unused", time.Second), NewClient("http://unused", "k", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2025, 10); err == nil ||
		!strings.Contains(err.Error(), "unsupported for college basketball") {
		t.Errorf("window > 0 must error, got %v", err)
	}
}

func TestSeasonYear(t *testing.T) {
	p := NewProvider(espnbb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))
	for now, want := range map[time.Time]int{
		time.Date(2025, 12, 20, 0, 0, 0, 0, time.UTC): 2025, // mid-season
		time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC):  2025, // March Madness
		time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC):   2025, // off-season, season just completed
		time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC):  2026, // new season underway
	} {
		if got := p.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}

func TestCBBDSeasonIsEndingYear(t *testing.T) {
	if got := cbbdSeason(2025); got != 2026 {
		t.Errorf("cbbdSeason(2025) = %d, want 2026 (ending year)", got)
	}
}

func TestProviderIdentity(t *testing.T) {
	p := NewProvider(espnbb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))
	if p.League() != model.LeagueNCAABB {
		t.Errorf("League() = %s, want NCAA_BB", p.League())
	}
	if p.Source() != sourceNCAABB {
		t.Errorf("Source() = %s, want %s", p.Source(), sourceNCAABB)
	}
}

func TestBoxScoreNotSupported(t *testing.T) {
	p := NewProvider(espnbb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))
	if _, _, err := p.BoxScore(context.Background(), "1"); err == nil {
		t.Error("BoxScore must return an error (tracked Phase 7 deferral)")
	}
}

func TestTeamsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnbb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))
	if _, _, _, err := p.Teams(context.Background()); err == nil {
		t.Error("upstream 500 on ESPN teams must error")
	}
}

func TestTeamStatsESPNTeamsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnbb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2025, 0); err == nil {
		t.Error("upstream 500 on ESPN teams must error")
	}
}

func TestTeamStatsAdjustedRatingsOutageDegrades(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case r.URL.Path == "/stats/team/season":
			_, _ = w.Write([]byte(derivedSeasonStats))
		case r.URL.Path == "/ratings/adjusted":
			http.Error(w, "boom", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnbb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("adjusted-ratings outage must degrade, not error: %v", err)
	}
	pur := stats[ids.Team(leagueNCAABB, "2509")]
	if pur.Stats.Advanced == nil || pur.Stats.Advanced.AdjustedEfficiencyMargin != 0 {
		t.Errorf("adjusted margin must stay zero on an outage: %+v", pur.Stats.Advanced)
	}
	if pur.Stats.Offensive == nil || pur.Stats.Offensive.OffensiveRating != 118.5 {
		t.Error("other blocks should still populate despite the adjusted-ratings outage")
	}
}

func TestScoreboardUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnbb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))
	if _, _, err := p.Scoreboard(context.Background(), time.Date(2026, 1, 24, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("upstream 500 on scoreboard must error")
	}
}

func TestTeamStatsSeasonStatsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case r.URL.Path == "/stats/team/season":
			_, _ = w.Write([]byte(`{invalid`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnbb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("malformed season-stats JSON must degrade, not error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("stats = %d, want 0 on a season-stats decode failure", len(stats))
	}
}

func TestNormalizeTeamStatsUnknownESPNTeamDropped(t *testing.T) {
	stats := []TeamSeasonStats{{Team: "Ghost State", Games: 10, Wins: 5, Losses: 5}}
	out := normalizeTeamStats(2025, stats, nil, map[string]espnRef{})
	if len(out) != 0 {
		t.Errorf("unmatched CBBD team must be dropped, got %+v", out)
	}
}

func TestPerGameZeroGames(t *testing.T) {
	if got := perGame(100, 0); got != 0 {
		t.Errorf("perGame with zero games = %v, want 0", got)
	}
}

func TestPlayersDescoped(t *testing.T) {
	p := NewProvider(espnbb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))

	summaries, details, fetches, err := p.Players(context.Background(), 2025)
	if err != nil || summaries == nil || details == nil || len(summaries) != 0 || len(details) != 0 || fetches != nil {
		t.Errorf("Players must return empty collections with nil error: %v %v %v %v", summaries, details, fetches, err)
	}

	log, fetches, err := p.PlayerGameLog(context.Background(), model.PlayerDetail{}, 2025)
	if err != nil || log == nil || len(log) != 0 || fetches != nil {
		t.Errorf("PlayerGameLog must return an empty log with nil error: %v %v %v", log, fetches, err)
	}
}
