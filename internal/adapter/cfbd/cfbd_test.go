package cfbd

// Tests for the NCAA_FB adapter. The ESPN real-time fixtures
// (testdata/teams.json, testdata/scoreboard.json) are real trimmed captures of
// ESPN's college-football site API for the completed 2025 season (recorded
// 2026-07-06), including a real single-overtime game and a real
// double-overtime game. The CFBD season-stats responses are DERIVED (the
// project has no CFBD key): they are embedded as string constants below, each
// with the required provenance header, and carry realistic 2025 records/scoring
// mirrored from ESPN's standings with plausible SP+ ratings.

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

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnfb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// DERIVED from CFBD documented schema 2026-07-06 — validate against live API in the September verification session.
const derivedRecords = `[
  {"year":2025,"teamId":249,"team":"North Texas","conference":"American Athletic","total":{"games":14,"wins":12,"losses":2,"ties":0}},
  {"year":2025,"teamId":194,"team":"Ohio State","conference":"Big Ten","total":{"games":14,"wins":12,"losses":2,"ties":0}},
  {"year":2025,"teamId":130,"team":"Michigan","conference":"Big Ten","total":{"games":13,"wins":9,"losses":4,"ties":0}}
]`

// DERIVED from CFBD documented schema 2026-07-06 — validate against live API in the September verification session.
const derivedSP = `[
  {"year":2025,"team":"North Texas","conference":"American Athletic","rating":18.5},
  {"year":2025,"team":"Ohio State","conference":"Big Ten","rating":28.3},
  {"year":2025,"team":"Michigan","conference":"Big Ten","rating":15.1}
]`

// DERIVED from CFBD documented schema 2026-07-06 — validate against live API in the September verification session.
// statValue is a number for scoring and a string for possessionTime, exercising
// the lenient decoder; only pointsFor/pointsAgainst are read.
const derivedSeasonStats = `[
  {"season":2025,"team":"North Texas","conference":"American Athletic","statName":"pointsFor","statValue":631},
  {"season":2025,"team":"North Texas","conference":"American Athletic","statName":"pointsAgainst","statValue":371},
  {"season":2025,"team":"North Texas","conference":"American Athletic","statName":"totalYards","statValue":6890},
  {"season":2025,"team":"North Texas","conference":"American Athletic","statName":"possessionTime","statValue":"30:41"},
  {"season":2025,"team":"Ohio State","conference":"Big Ten","statName":"pointsFor","statValue":468},
  {"season":2025,"team":"Ohio State","conference":"Big Ten","statName":"pointsAgainst","statValue":130},
  {"season":2025,"team":"Michigan","conference":"Big Ten","statName":"pointsFor","statValue":358},
  {"season":2025,"team":"Michigan","conference":"Big Ten","statName":"pointsAgainst","statValue":265}
]`

func serveFixture(t *testing.T, w http.ResponseWriter, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	_, _ = w.Write(data)
}

// mux routes ESPN requests to the recorded fixtures and CFBD requests to the
// derived string constants. cfbdCalls counts CFBD requests; when a nil counter
// is passed, any CFBD call fails the test (used by the keyless test to prove
// the provider never hits CFBD without a key).
func mux(t *testing.T, cfbdCalls *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case strings.HasSuffix(r.URL.Path, "/scoreboard"):
			serveFixture(t, w, "scoreboard.json")
		case r.URL.Path == "/records":
			cfbdHit(t, w, cfbdCalls, derivedRecords)
		case r.URL.Path == "/ratings/sp":
			cfbdHit(t, w, cfbdCalls, derivedSP)
		case r.URL.Path == "/stats/season":
			cfbdHit(t, w, cfbdCalls, derivedSeasonStats)
		default:
			http.NotFound(w, r)
		}
	})
}

func cfbdHit(t *testing.T, w http.ResponseWriter, calls *atomic.Int32, body string) {
	t.Helper()
	if calls == nil {
		t.Errorf("CFBD endpoint must not be called without an API key")
		http.Error(w, "unexpected cfbd call", http.StatusInternalServerError)
		return
	}
	calls.Add(1)
	_, _ = w.Write([]byte(body))
}

func testProvider(t *testing.T, apiKey string, cfbdCalls *atomic.Int32) *Provider {
	t.Helper()
	server := httptest.NewServer(mux(t, cfbdCalls))
	t.Cleanup(server.Close)
	return NewProvider(
		espnfb.NewClient(server.URL, 5*time.Second),
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
	if len(teams) != 9 || len(details) != 9 {
		t.Fatalf("teams = %d, details = %d, want 9 each", len(teams), len(details))
	}

	var unt model.TeamSummary
	for _, team := range teams {
		if team.Abbreviation == "UNT" {
			unt = team
		}
	}
	if unt.ID != ids.Team(leagueNCAAFB, "249") {
		t.Errorf("id = %s, want UUIDv5 of espn team 249", unt.ID)
	}
	if unt.Name != "North Texas Mean Green" || unt.League != model.LeagueNCAAFB {
		t.Errorf("summary wrong: %+v", unt)
	}
	if details[unt.ID].Venue != nil {
		t.Errorf("college teams carry no venue, got %+v", details[unt.ID].Venue)
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

	// Real double-overtime game (Kennesaw State 48 at Liberty 42): college OT
	// runs past the fourth quarter into a decided result — no ties (ADR-027).
	twoOT := byExternal["401757313"]
	if twoOT.ID != ids.Game(leagueNCAAFB, "401757313") || twoOT.Season != 2025 {
		t.Errorf("2OT identity wrong: %+v", twoOT)
	}
	if twoOT.HomeTeam.Abbreviation != "LIB" || twoOT.AwayTeam.Abbreviation != "KENN" {
		t.Errorf("2OT team refs wrong: %+v / %+v", twoOT.HomeTeam, twoOT.AwayTeam)
	}
	if twoOT.Result == nil || !twoOT.Result.Overtime || len(twoOT.Result.PeriodScores) != 6 {
		t.Fatalf("2OT result wrong: %+v", twoOT.Result)
	}
	if twoOT.Result.HomeScore != 42 || twoOT.Result.AwayScore != 48 || twoOT.Result.Margin != -6 {
		t.Errorf("2OT scores wrong: %+v", twoOT.Result)
	}
	if twoOT.Result.RegulationHomeScore != nil || twoOT.Result.RegulationAwayScore != nil {
		t.Errorf("football must carry no regulation fields: %+v", twoOT.Result)
	}

	// Single-overtime game: period 5.
	oneOT := byExternal["401757315"]
	if oneOT.Result == nil || !oneOT.Result.Overtime || len(oneOT.Result.PeriodScores) != 5 {
		t.Errorf("1OT result wrong: %+v", oneOT.Result)
	}

	// Regular-time final: four quarters, not overtime.
	reg := byExternal["401752921"]
	if reg.Result == nil || reg.Result.Overtime || len(reg.Result.PeriodScores) != 4 {
		t.Fatalf("regular final result wrong: %+v", reg.Result)
	}
	if reg.HomeTeam.Abbreviation != "MICH" || reg.Result.HomeScore != 9 || reg.Result.AwayScore != 27 {
		t.Errorf("regular final wrong: %+v / %+v", reg.HomeTeam, reg.Result)
	}
	if reg.Venue == nil || reg.Venue.Name != "Michigan Stadium" {
		t.Errorf("venue wrong: %+v", reg.Venue)
	}
}

func TestScoreboardFinal(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, "k", &calls)

	updates, _, err := p.Scoreboard(context.Background(), time.Date(2025, 11, 29, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	twoOT := updates["401757313"]
	if twoOT.Status != model.GameFinal || twoOT.Result == nil {
		t.Fatalf("2OT update wrong: %+v", twoOT)
	}
	if twoOT.HomeScore != 42 || twoOT.AwayScore != 48 || !twoOT.Result.Overtime {
		t.Errorf("2OT update result wrong: %+v", twoOT.Result)
	}
}

func TestTeamStats(t *testing.T) {
	var calls atomic.Int32
	p := testProvider(t, "test-key", &calls)

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if calls.Load() != 3 {
		t.Errorf("cfbd calls = %d, want 3 (records + sp + season stats)", calls.Load())
	}
	if len(stats) != 3 {
		t.Fatalf("teams = %d, want 3", len(stats))
	}

	unt := stats[ids.Team(leagueNCAAFB, "249")]
	if unt.Season != 2025 || unt.GamesPlayed != 14 || unt.Wins != 12 || unt.Losses != 2 {
		t.Errorf("North Texas record wrong: %+v", unt)
	}
	if unt.TeamAbbreviation != "UNT" {
		t.Errorf("abbreviation wrong: %+v", unt)
	}
	if unt.Stats.Offensive != nil || unt.Stats.Defensive != nil || unt.Stats.Advanced != nil ||
		unt.Stats.Soccer != nil || unt.Stats.Baseball != nil {
		t.Error("non-football blocks must stay nil for NCAA_FB")
	}
	fb := unt.Stats.Football
	if fb == nil {
		t.Fatal("football block missing")
	}
	if math.Abs(fb.PointsPerGame-631.0/14.0) > 1e-9 {
		t.Errorf("points/game = %v, want 631/14", fb.PointsPerGame)
	}
	if math.Abs(fb.PointsAllowedPerGame-371.0/14.0) > 1e-9 {
		t.Errorf("points allowed/game = %v, want 371/14", fb.PointsAllowedPerGame)
	}
	if fb.DrivesPerGame != leagueAvgDrivesPerGame {
		t.Errorf("drives/game = %v, want %v", fb.DrivesPerGame, leagueAvgDrivesPerGame)
	}
	if math.Abs(fb.PointsPerDriveOff-(631.0/14.0)/leagueAvgDrivesPerGame) > 1e-9 {
		t.Errorf("points/drive off wrong: %v", fb.PointsPerDriveOff)
	}
	// SP+ maps straight from /ratings/sp.
	if fb.SPPlusRating != 18.5 {
		t.Errorf("sp_plus_rating = %v, want 18.5", fb.SPPlusRating)
	}
	// EPA is a documented college absence; turnover margin is not sourced.
	if fb.EPAPerPlayOff != 0 || fb.EPAPerPlayDef != 0 || fb.TurnoverMarginPerGame != 0 {
		t.Errorf("college EPA/turnover fields must stay zero: %+v", fb)
	}

	osu := stats[ids.Team(leagueNCAAFB, "194")]
	if osu.Stats.Football.SPPlusRating != 28.3 || math.Abs(osu.Stats.Football.PointsAllowedPerGame-130.0/14.0) > 1e-9 {
		t.Errorf("Ohio State stats wrong: %+v", osu.Stats.Football)
	}
}

func TestTeamStatsKeylessDegradation(t *testing.T) {
	// A nil cfbdCalls counter makes any CFBD request fail the test, proving
	// the keyless provider runs watcher-only without touching CFBD.
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

func TestTeamStatsCFBDOutageDegrades(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case strings.HasPrefix(r.URL.Path, "/records"):
			http.Error(w, "boom", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnfb.NewClient(server.URL, 5*time.Second), NewClient(server.URL, "k", 5*time.Second))

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("CFBD outage must degrade, not error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("stats = %d, want 0 on CFBD records outage", len(stats))
	}
}

func TestTeamStatsRollingWindowUnsupported(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewClient("http://unused", "k", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2025, 10); err == nil ||
		!strings.Contains(err.Error(), "unsupported for college football") {
		t.Errorf("window > 0 must error, got %v", err)
	}
}

func TestPlayersDescoped(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))

	summaries, details, fetches, err := p.Players(context.Background(), 2025)
	if err != nil || summaries == nil || details == nil || len(summaries) != 0 || len(details) != 0 || fetches != nil {
		t.Errorf("Players must return empty collections with nil error: %v %v %v %v", summaries, details, fetches, err)
	}

	log, fetches, err := p.PlayerGameLog(context.Background(), model.PlayerDetail{}, 2025)
	if err != nil || log == nil || len(log) != 0 || fetches != nil {
		t.Errorf("PlayerGameLog must return an empty log with nil error: %v %v %v", log, fetches, err)
	}
}

func TestProviderIdentity(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))
	if p.League() != model.LeagueNCAAFB {
		t.Errorf("League() = %s, want NCAA_FB", p.League())
	}
	if p.Source() != sourceNCAAFB {
		t.Errorf("Source() = %s, want %s", p.Source(), sourceNCAAFB)
	}
}

func TestBoxScoreNotSupported(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))
	if _, _, err := p.BoxScore(context.Background(), "1"); err == nil {
		t.Error("BoxScore must return an error (tracked Phase 7 deferral)")
	}
}

func TestTeamsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnfb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))
	if _, _, _, err := p.Teams(context.Background()); err == nil {
		t.Error("upstream 500 on ESPN teams must error")
	}
}

func TestTeamStatsESPNTeamsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnfb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))
	if _, _, err := p.TeamStats(context.Background(), 2025, 0); err == nil {
		t.Error("upstream 500 on ESPN teams must error")
	}
}

func TestTeamStatsSPRatingsOutageDegrades(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case r.URL.Path == "/records":
			_, _ = w.Write([]byte(derivedRecords))
		case r.URL.Path == "/ratings/sp":
			http.Error(w, "boom", http.StatusServiceUnavailable)
		case r.URL.Path == "/stats/season":
			_, _ = w.Write([]byte(derivedSeasonStats))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnfb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("SP+ outage must degrade, not error: %v", err)
	}
	unt := stats[ids.Team(leagueNCAAFB, "249")]
	if unt.Stats.Football == nil || unt.Stats.Football.SPPlusRating != 0 {
		t.Errorf("sp_plus_rating must stay zero on an SP+ outage: %+v", unt.Stats.Football)
	}
	if unt.Stats.Football.PointsPerGame == 0 {
		t.Error("points per game should still be sourced from season stats despite the SP+ outage")
	}
}

func TestTeamStatsSeasonStatsOutageDegrades(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case r.URL.Path == "/records":
			_, _ = w.Write([]byte(derivedRecords))
		case r.URL.Path == "/ratings/sp":
			_, _ = w.Write([]byte(derivedSP))
		case r.URL.Path == "/stats/season":
			http.Error(w, "boom", http.StatusServiceUnavailable)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnfb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("season stats outage must degrade, not error: %v", err)
	}
	unt := stats[ids.Team(leagueNCAAFB, "249")]
	if unt.Stats.Football == nil || unt.Stats.Football.PointsPerGame != 0 {
		t.Errorf("points per game must stay zero on a season-stats outage: %+v", unt.Stats.Football)
	}
	if unt.Stats.Football.SPPlusRating != 18.5 {
		t.Error("SP+ rating should still be sourced despite the season-stats outage")
	}
}

func TestScoreboardUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnfb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))
	if _, _, err := p.Scoreboard(context.Background(), time.Date(2025, 11, 29, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("upstream 500 on scoreboard must error")
	}
}

func TestTeamStatsRecordsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/teams"):
			serveFixture(t, w, "teams.json")
		case r.URL.Path == "/records":
			_, _ = w.Write([]byte(`{invalid`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	p := NewProvider(espnfb.NewClient(server.URL, time.Second), NewClient(server.URL, "k", time.Second))

	stats, _, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("malformed records JSON must degrade (records outage), not error: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("stats = %d, want 0 on a records decode failure", len(stats))
	}
}

func TestNormalizeTeamStatsUnknownESPNTeamDropped(t *testing.T) {
	records := []TeamRecords{{Team: "Ghost State", Total: TeamRecord{Games: 10, Wins: 5, Losses: 5}}}
	out := normalizeTeamStats(2025, records, nil, nil, map[string]espnRef{})
	if len(out) != 0 {
		t.Errorf("unmatched CFBD team must be dropped, got %+v", out)
	}
}

func TestLenientFloatUnmarshalFallback(t *testing.T) {
	var f lenientFloat
	if err := f.UnmarshalJSON([]byte("true")); err != nil {
		t.Fatalf("UnmarshalJSON must never error: %v", err)
	}
	if f != 0 {
		t.Errorf("non-numeric, non-string JSON should decode to 0, got %v", f)
	}
}

func TestSeasonYear(t *testing.T) {
	p := NewProvider(espnfb.NewClient("http://unused", time.Second), NewClient("http://unused", "", time.Second))
	for now, want := range map[time.Time]int{
		time.Date(2025, 10, 4, 0, 0, 0, 0, time.UTC): 2025, // mid-season Saturday
		time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC): 2025, // national championship
		time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC):  2025, // off-season, season just completed
		time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC): 2026, // new season underway
	} {
		if got := p.SeasonYear(now); got != want {
			t.Errorf("SeasonYear(%s) = %d, want %d", now.Format("2006-01-02"), got, want)
		}
	}
}
