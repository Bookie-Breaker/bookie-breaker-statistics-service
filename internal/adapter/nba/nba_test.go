package nba

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(Options{
		BaseURL:            server.URL,
		UserAgent:          "test-agent",
		MinRequestInterval: time.Millisecond,
		Timeout:            5 * time.Second,
		FailureThreshold:   3,
		OpenDuration:       time.Minute,
	})
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

// fixtureMux routes leaguedash requests to per-measure fixtures and the
// remaining endpoints to their files.
func fixtureMux(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/stats/leaguedashteamstats":
			switch r.URL.Query().Get("MeasureType") {
			case "Advanced":
				serveFixture(t, w, "leaguedashteamstats_advanced.json")
			case "Opponent":
				serveFixture(t, w, "leaguedashteamstats_opponent.json")
			default:
				serveFixture(t, w, "leaguedashteamstats_base.json")
			}
		case "/stats/commonteamroster":
			serveFixture(t, w, "commonteamroster.json")
		case "/stats/leaguedashplayerstats":
			serveFixture(t, w, "leaguedashplayerstats.json")
		case "/stats/scheduleleaguev2":
			serveFixture(t, w, "scheduleleaguev2.json")
		case "/stats/scoreboardv3":
			serveFixture(t, w, "scoreboardv3.json")
		case "/stats/boxscoretraditionalv2":
			serveFixture(t, w, "boxscoretraditionalv2.json")
		default:
			http.NotFound(w, r)
		}
	})
}

func TestClientSendsNBAHeaders(t *testing.T) {
	var gotHeaders http.Header
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		serveFixture(t, w, "scheduleleaguev2.json")
	}))

	if _, _, err := client.Schedule(context.Background(), 2025); err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}

	for header, want := range map[string]string{
		"User-Agent":         "test-agent",
		"Referer":            "https://www.nba.com/",
		"Origin":             "https://www.nba.com",
		"X-Nba-Stats-Origin": "stats",
		"X-Nba-Stats-Token":  "true",
	} {
		if got := gotHeaders.Get(header); got != want {
			t.Errorf("header %s = %q, want %q", header, got, want)
		}
	}
}

func TestBreakerOpensAfterConsecutiveFailures(t *testing.T) {
	var calls atomic.Int32
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))

	for range 3 {
		if _, _, err := client.Schedule(context.Background(), 2025); err == nil {
			t.Fatal("expected upstream error")
		}
	}
	// Fourth call should be rejected without reaching the server.
	if _, _, err := client.Schedule(context.Background(), 2025); err == nil {
		t.Fatal("expected circuit-open error")
	}
	if calls.Load() != 3 {
		t.Errorf("upstream calls = %d, want 3 (breaker should block the 4th)", calls.Load())
	}
}

func TestLeagueTeamStatsMergesMeasures(t *testing.T) {
	client := testClient(t, fixtureMux(t))

	teams, fetches, err := client.LeagueTeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("LeagueTeamStats failed: %v", err)
	}
	if len(fetches) != 3 {
		t.Errorf("fetches = %d, want 3 (base, advanced, opponent)", len(fetches))
	}
	if len(teams) != 2 {
		t.Fatalf("teams = %d, want 2", len(teams))
	}

	celtics := teams["1610612738"]
	if celtics == nil {
		t.Fatal("Celtics missing")
	}
	if celtics.PTS != 120.6 || celtics.W != 64 {
		t.Errorf("Base merge wrong: PTS=%v W=%d", celtics.PTS, celtics.W)
	}
	if !celtics.HasAdvanced || celtics.OffRating != 122.2 || celtics.Pace != 98.5 {
		t.Errorf("Advanced merge wrong: %+v", celtics)
	}
	if celtics.OppPTS != 109.2 {
		t.Errorf("Opponent merge wrong: OppPTS=%v", celtics.OppPTS)
	}
}

func TestNormalizeTeamStatsPrefersUpstreamAdvanced(t *testing.T) {
	client := testClient(t, fixtureMux(t))
	raw, _, err := client.LeagueTeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatal(err)
	}

	stats := NormalizeTeamStats(raw, nil, 2025)
	celtics := stats["1610612738"]
	if celtics.Stats.Offensive.OffensiveRating != 122.2 {
		t.Errorf("off_rating = %v, want upstream 122.2", celtics.Stats.Offensive.OffensiveRating)
	}
	if celtics.TeamAbbreviation != "BOS" {
		t.Errorf("abbreviation = %q, want BOS", celtics.TeamAbbreviation)
	}

	// The formula fallback should agree with upstream within tolerance —
	// this doubles as a regression check on the derived-stat math.
	raw["1610612738"].HasAdvanced = false
	fallback := NormalizeTeamStats(raw, nil, 2025)["1610612738"]
	if diff := math.Abs(fallback.Stats.Offensive.OffensiveRating - 122.2); diff > 3.0 {
		t.Errorf("fallback off_rating %v differs from upstream 122.2 by %v", fallback.Stats.Offensive.OffensiveRating, diff)
	}
	if diff := math.Abs(fallback.Stats.Offensive.Pace - 98.5); diff > 2.0 {
		t.Errorf("fallback pace %v differs from upstream 98.5 by %v", fallback.Stats.Offensive.Pace, diff)
	}
}

func TestSchedultAndScoreboardNormalization(t *testing.T) {
	client := testClient(t, fixtureMux(t))

	scheduled, _, err := client.Schedule(context.Background(), 2025)
	if err != nil {
		t.Fatal(err)
	}
	if len(scheduled) != 2 {
		t.Fatalf("scheduled games = %d, want 2", len(scheduled))
	}

	games := NormalizeSchedule(scheduled, 2025)

	final := games[0]
	if final.Status != "FINAL" || final.HomeTeam.Abbreviation != "BOS" {
		t.Errorf("final game wrong: %+v", final)
	}
	if final.Result == nil || final.Result.TotalScore != 228 || final.Result.Margin != 8 {
		t.Errorf("final result wrong: %+v", final.Result)
	}

	upcoming := games[1]
	if upcoming.Status != "SCHEDULED" || upcoming.HomeScore != nil {
		t.Errorf("scheduled game wrong: %+v", upcoming)
	}
	if upcoming.Venue == nil || upcoming.Venue.Name != "Crypto.com Arena" {
		t.Errorf("venue wrong: %+v", upcoming.Venue)
	}

	// Apply the OT scoreboard to the scheduled game.
	entries, _, err := client.Scoreboard(context.Background(), time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("scoreboard entries = %d, want 1", len(entries))
	}

	updated := ApplyScoreboard(upcoming, entries[0])
	if updated.Status != "FINAL" {
		t.Errorf("status = %s, want FINAL", updated.Status)
	}
	if updated.Result == nil || !updated.Result.Overtime {
		t.Errorf("OT result wrong: %+v", updated.Result)
	}
	if len(updated.Result.PeriodScores) != 5 || updated.Result.PeriodScores[4].Home != 13 {
		t.Errorf("period scores wrong: %+v", updated.Result.PeriodScores)
	}
	if *updated.HomeScore != 128 || *updated.AwayScore != 124 {
		t.Errorf("scores wrong: %d-%d", *updated.HomeScore, *updated.AwayScore)
	}
}

func TestNormalizePlayers(t *testing.T) {
	client := testClient(t, fixtureMux(t))

	roster, _, err := client.TeamRoster(context.Background(), "1610612747", 2025)
	if err != nil {
		t.Fatal(err)
	}
	seasons, _, err := client.LeaguePlayerStats(context.Background(), 2025)
	if err != nil {
		t.Fatal(err)
	}

	summaries, details := NormalizePlayers(map[string][]RosterEntry{"1610612747": roster}, seasons, 2025)
	if len(summaries) != 2 {
		t.Fatalf("players = %d, want 2", len(summaries))
	}

	for _, p := range summaries {
		if p.LastName == "James" {
			if p.FirstName != "LeBron" || p.TeamAbbreviation != "LAL" || *p.JerseyNumber != 23 {
				t.Errorf("summary wrong: %+v", p)
			}
			d := details[p.ID]
			if d.ExperienceYears == nil || *d.ExperienceYears != 22 {
				t.Errorf("experience wrong: %+v", d.ExperienceYears)
			}
			if d.SeasonStats == nil || d.SeasonStats.PointsPerGame != 25.7 {
				t.Errorf("season stats wrong: %+v", d.SeasonStats)
			}
		}
		if p.LastName == "Guard" {
			d := details[p.ID]
			if d.ExperienceYears == nil || *d.ExperienceYears != 0 {
				t.Errorf("rookie experience should be 0: %+v", d.ExperienceYears)
			}
		}
	}
}

func TestBoxScoreNormalization(t *testing.T) {
	client := testClient(t, fixtureMux(t))
	provider := NewProvider(client, func(time.Time) int { return 2025 })

	box, fetches, err := provider.BoxScore(context.Background(), "0022500001")
	if err != nil {
		t.Fatalf("BoxScore failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}

	if box.Sport != "BASKETBALL" {
		t.Errorf("sport = %q, want BASKETBALL", box.Sport)
	}
	// The provider emits teams in response order (LAL first in the
	// fixture); the query layer swaps against the canonical game.
	lal, bos := box.HomeTeam, box.AwayTeam
	if lal.Abbreviation != "LAL" || bos.Abbreviation != "BOS" {
		t.Fatalf("teams = %s/%s, want LAL/BOS", lal.Abbreviation, bos.Abbreviation)
	}
	if lal.ID != ids.Team("NBA", "1610612747") || bos.ID != ids.Team("NBA", "1610612738") {
		t.Errorf("team ids not minted with ids.Team: %s / %s", lal.ID, bos.ID)
	}
	if lal.Score != 112 || bos.Score != 104 {
		t.Errorf("scores = %d-%d, want 112-104", lal.Score, bos.Score)
	}
	if lal.TeamStats == nil || lal.TeamStats.FieldGoalsMade != 42 || lal.TeamStats.Turnovers != 12 {
		t.Errorf("team stats wrong: %+v", lal.TeamStats)
	}
	if len(lal.SoccerPlayers) != 0 || len(bos.SoccerPlayers) != 0 {
		t.Error("soccer players must be empty for basketball")
	}

	if len(lal.Players) != 3 || len(bos.Players) != 1 {
		t.Fatalf("players = %d/%d, want 3/1", len(lal.Players), len(bos.Players))
	}
	byName := make(map[string]model.BasketballPlayerBoxScore)
	for _, p := range append(lal.Players, bos.Players...) {
		byName[p.PlayerName] = p
	}

	lebron := byName["LeBron James"]
	if lebron.PlayerID != ids.Player("NBA", "2544") {
		t.Errorf("player id not minted with ids.Player: %s", lebron.PlayerID)
	}
	if lebron.Position != "F" || lebron.Points != 28 || lebron.Rebounds != 8 || lebron.Assists != 9 ||
		lebron.Steals != 1 || lebron.Blocks != 1 || lebron.Turnovers != 4 ||
		lebron.FieldGoalsMade != 10 || lebron.FieldGoalsAttempted != 18 ||
		lebron.ThreePointersMade != 2 || lebron.ThreePointersAttempted != 6 ||
		lebron.FreeThrowsMade != 6 || lebron.FreeThrowsAttempted != 8 || lebron.PlusMinus != 6 {
		t.Errorf("line wrong: %+v", lebron)
	}
	if math.Abs(lebron.Minutes-37.75) > 1e-9 {
		t.Errorf("minutes = %v, want 37.75 (parsed from 37:45)", lebron.Minutes)
	}

	// "39.000000:12" is the endpoint's occasional decimal-minutes form.
	if luka := byName["Luka Doncic"]; math.Abs(luka.Minutes-39.2) > 1e-9 {
		t.Errorf("minutes = %v, want 39.2 (parsed from 39.000000:12)", luka.Minutes)
	}
	// DNPs keep a zero line.
	if bench := byName["Bench Guy"]; bench.Minutes != 0 || bench.Points != 0 {
		t.Errorf("DNP line should be zero: %+v", bench)
	}
}

func TestNormalizeTeamsStaticSeed(t *testing.T) {
	teams := NormalizeTeams()
	if len(teams) != 30 {
		t.Fatalf("teams = %d, want 30", len(teams))
	}
	seen := make(map[string]bool)
	for _, team := range teams {
		if team.ID == "" || team.Abbreviation == "" || team.ExternalIDs["nba"] == "" {
			t.Errorf("incomplete team: %+v", team)
		}
		if seen[team.ID] {
			t.Errorf("duplicate id %s", team.ID)
		}
		seen[team.ID] = true
	}
}
