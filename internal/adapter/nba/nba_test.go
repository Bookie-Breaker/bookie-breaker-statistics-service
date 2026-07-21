package nba

import (
	"context"
	"errors"
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
		case "/stats/playergamelog":
			serveFixture(t, w, "playergamelog.json")
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

// The remaining tests raise coverage on the client's error/retry branches,
// the low-level tabular result-set accessors, the normalizer's edge-case
// branches, TeamLocationSplits, PlayerGameLog, and the Provider wiring
// (League/Source/SeasonYear/Teams/TeamStats/Players/Schedule/Scoreboard/
// PlayerGameLog/BoxScore) that nba_test.go's original suite left untouched.

func TestClientCreateRequestError(t *testing.T) {
	// A NUL byte in the base URL makes http.NewRequestWithContext fail before
	// any network call happens, so no fetch is ever produced.
	client := NewClient(Options{
		BaseURL:            "http://exa\x00mple.com",
		UserAgent:          "test-agent",
		MinRequestInterval: time.Millisecond,
		Timeout:            time.Second,
		FailureThreshold:   3,
		OpenDuration:       time.Minute,
	})
	_, fetch, err := client.Schedule(context.Background(), 2025)
	if err == nil || !strings.Contains(err.Error(), "create request") {
		t.Fatalf("err = %v, want create-request error", err)
	}
	if fetch != nil {
		t.Errorf("fetch = %+v, want nil (request never sent)", fetch)
	}
}

func TestClientExecuteRequestError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveFixture(t, w, "scheduleleaguev2.json")
	}))
	server.Close() // refuse every connection

	client := NewClient(Options{
		BaseURL:            server.URL,
		UserAgent:          "test-agent",
		MinRequestInterval: time.Millisecond,
		Timeout:            time.Second,
		FailureThreshold:   3,
		OpenDuration:       time.Minute,
	})
	_, fetch, err := client.Schedule(context.Background(), 2025)
	if err == nil || !strings.Contains(err.Error(), "execute request") {
		t.Fatalf("err = %v, want execute-request error", err)
	}
	if fetch != nil {
		t.Errorf("fetch = %+v, want nil (no response received)", fetch)
	}
}

func TestClientThrottleWaitContextError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("upstream must not be called when the context is already canceled")
	}))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, fetch, err := client.Schedule(ctx, 2025)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if fetch != nil {
		t.Errorf("fetch = %+v, want nil", fetch)
	}
}

func TestClientDecodeError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{not json"))
	}))
	_, fetch, err := client.Schedule(context.Background(), 2025)
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v, want decode error", err)
	}
	if fetch == nil || len(fetch.Body) == 0 {
		t.Errorf("malformed body must still be archived: %+v", fetch)
	}
}

func TestClientRateLimitedHonorsRetryAfter(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	start := time.Now()
	_, fetch, err := client.Schedule(context.Background(), 2025)
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "rate limited (429)") {
		t.Fatalf("err = %v, want 429 rate-limited error", err)
	}
	if elapsed < time.Second {
		t.Errorf("elapsed = %s, want >= 1s (Retry-After honored before returning)", elapsed)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("fetch not archived: %+v", fetch)
	}
}

func TestClientRateLimitedContextCancelledDuringWait(t *testing.T) {
	// The context must stay alive through the full response round trip (an
	// early cancel would instead fail the request itself, hitting the
	// "execute request" branch); it is only canceled once the client has
	// moved on to honoring the 5s Retry-After, so the wait's ctx.Done()
	// branch fires well before the timer would.
	ctx, cancel := context.WithCancel(context.Background())
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.AfterFunc(50*time.Millisecond, cancel)
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	_, fetch, err := client.Schedule(ctx, 2025)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusTooManyRequests {
		t.Errorf("fetch must still be archived when the wait is interrupted: %+v", fetch)
	}
}

func TestClientRateLimitedSkipsWaitForUnusableRetryAfter(t *testing.T) {
	for _, retryAfter := range []string{"", "not-a-number", "0", "120"} {
		t.Run(retryAfter, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if retryAfter != "" {
					w.Header().Set("Retry-After", retryAfter)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}))

			start := time.Now()
			_, fetch, err := client.Schedule(context.Background(), 2025)
			if time.Since(start) > 500*time.Millisecond {
				t.Errorf("Retry-After=%q must not trigger a wait", retryAfter)
			}
			if err == nil || !strings.Contains(err.Error(), "rate limited") {
				t.Fatalf("err = %v, want rate-limited error", err)
			}
			if fetch == nil {
				t.Error("fetch missing")
			}
		})
	}
}

func TestStatsResponseSet(t *testing.T) {
	empty := &statsResponse{}
	if _, err := empty.set("Any"); err == nil {
		t.Error("empty response must error")
	}

	resp := &statsResponse{ResultSets: []resultSet{{Name: "A"}, {Name: "B"}}}
	if s, err := resp.set(""); err != nil || s.Name != "A" {
		t.Errorf("empty name must return the first set: %+v, %v", s, err)
	}
	if s, err := resp.set("B"); err != nil || s.Name != "B" {
		t.Errorf("named lookup wrong: %+v, %v", s, err)
	}
	if _, err := resp.set("Missing"); err == nil {
		t.Error("missing name must error")
	}
}

func TestRowAccessors(t *testing.T) {
	set := resultSet{Headers: []string{"NAME", "PTS", "PCT"}, RowSet: [][]any{{"Bob", 24.0, "0.5"}}}
	rows := set.rows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.str("name") != "Bob" {
		t.Errorf("header lookup must be case-insensitive: %q", r.str("name"))
	}
	if r.float("pts") != 24.0 {
		t.Errorf("float(float64) wrong: %v", r.float("pts"))
	}
	if r.float("pct") != 0.5 {
		t.Errorf("float(numeric string) wrong: %v", r.float("pct"))
	}
	if r.int("pts") != 24 {
		t.Errorf("int() wrong: %v", r.int("pts"))
	}
	if r.value("missing") != nil {
		t.Error("missing column must be nil")
	}
	if r.float("missing") != 0 {
		t.Error("missing column float must be 0")
	}
	if r.str("missing") != "" {
		t.Error("missing column str must be empty")
	}

	// value() guards against an index past the row's actual values.
	short := row{idx: map[string]int{"X": 5}, values: []any{1}}
	if short.value("X") != nil {
		t.Error("out-of-range index must return nil")
	}

	// str() formats a float64 value as a string (the endpoint's numeric ids).
	numeric := row{idx: map[string]int{"N": 0}, values: []any{42.0}}
	if numeric.str("N") != "42" {
		t.Errorf("str(float64) = %q, want 42", numeric.str("N"))
	}

	// float() falls back to 0 for an unparsable string or an unsupported type.
	bad := row{idx: map[string]int{"F": 0}, values: []any{"not-a-number"}}
	if bad.float("F") != 0 {
		t.Error("float(unparsable string) must be 0")
	}
	weird := row{idx: map[string]int{"W": 0}, values: []any{true}}
	if weird.float("W") != 0 {
		t.Error("float(unsupported type) must be 0")
	}
}

func TestStatusFromCode(t *testing.T) {
	tests := map[int]model.GameStatus{
		statusScheduled: model.GameScheduled,
		statusLive:      model.GameInProgress,
		statusFinal:     model.GameFinal,
		99:              model.GameScheduled, // unknown code falls back to scheduled
	}
	for code, want := range tests {
		if got := statusFromCode(code); got != want {
			t.Errorf("statusFromCode(%d) = %s, want %s", code, got, want)
		}
	}
}

func TestSeasonTypeFromLabel(t *testing.T) {
	tests := map[string]model.SeasonType{
		"":                   model.SeasonRegular,
		"Preseason":          model.SeasonPreseason,
		"Playoffs Round 1":   model.SeasonPostseason,
		"NBA Finals":         model.SeasonPostseason,
		"Play-In Tournament": model.SeasonPostseason,
		"Something Else":     model.SeasonRegular,
	}
	for label, want := range tests {
		if got := seasonTypeFromLabel(label); got != want {
			t.Errorf("seasonTypeFromLabel(%q) = %s, want %s", label, got, want)
		}
	}
}

func TestSplitNameEdgeCases(t *testing.T) {
	if first, last := splitName("Neymar"); first != "" || last != "Neymar" {
		t.Errorf("single-word name = %q/%q, want empty/Neymar", first, last)
	}
	// Only the first space is a split point; a repeated internal space stays
	// on the last-name side as-is (the function does not collapse spacing).
	if first, last := splitName("LeBron  James"); first != "LeBron" || last != " James" {
		t.Errorf("multi-space name = %q/%q, want LeBron/\" James\"", first, last)
	}
	if first, last := splitName("  Luka Doncic  "); first != "Luka" || last != "Doncic" {
		t.Errorf("padded name = %q/%q, want Luka/Doncic", first, last)
	}
}

func TestTeamLocationSplits(t *testing.T) {
	client := testClient(t, fixtureMux(t))

	splits, fetches, err := client.TeamLocationSplits(context.Background(), 2025)
	if err != nil {
		t.Fatalf("TeamLocationSplits failed: %v", err)
	}
	if len(fetches) != 4 {
		t.Errorf("fetches = %d, want 4 (home/road x base/opponent)", len(fetches))
	}
	lal := splits["1610612747"]
	if lal == nil {
		t.Fatal("LAL splits missing")
	}
	if lal.Home.W != 47 || lal.Home.PTS != 114.9 || lal.Home.OppPTS != 113.5 {
		t.Errorf("home split wrong: %+v", lal.Home)
	}
	if lal.Road.W != 47 || lal.Road.PTS != 114.9 {
		t.Errorf("road split wrong: %+v", lal.Road)
	}
}

func TestTeamLocationSplitsUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, fetches, err := client.TeamLocationSplits(context.Background(), 2025)
	if err == nil || !strings.Contains(err.Error(), "leaguedashteamstats Home/Base") {
		t.Fatalf("err = %v, want a Home/Base leaguedashteamstats error", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (archived even on failure)", len(fetches))
	}
}

func TestPlayerGameLog(t *testing.T) {
	client := testClient(t, fixtureMux(t))

	log, fetch, err := client.PlayerGameLog(context.Background(), "2544", 2025)
	if err != nil {
		t.Fatalf("PlayerGameLog failed: %v", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK {
		t.Fatalf("fetch = %+v", fetch)
	}
	if len(log) != 2 {
		t.Fatalf("log entries = %d, want 2", len(log))
	}
	first := log[0]
	if first.NBAGameID != "0022500456" || first.Matchup != "LAL vs. BOS" || first.Result != "W" {
		t.Errorf("entry wrong: %+v", first)
	}
	if first.PTS != 30 || first.REB != 8 || first.AST != 9 {
		t.Errorf("box line wrong: %+v", first)
	}
}

func TestPlayerGameLogUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	_, fetch, err := client.PlayerGameLog(context.Background(), "2544", 2025)
	if err == nil || !strings.Contains(err.Error(), "playergamelog 2544") {
		t.Fatalf("err = %v", err)
	}
	if fetch == nil {
		t.Error("fetch not archived on error")
	}
}

func testProvider(t *testing.T) *Provider {
	t.Helper()
	client := testClient(t, fixtureMux(t))
	return NewProvider(client, func(now time.Time) int { return now.Year() })
}

func TestProviderMeta(t *testing.T) {
	p := testProvider(t)
	if p.League() != model.LeagueNBA {
		t.Errorf("League() = %s, want NBA", p.League())
	}
	if p.Source() != "nba_com" {
		t.Errorf("Source() = %s, want nba_com", p.Source())
	}
	if got := p.SeasonYear(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)); got != 2026 {
		t.Errorf("SeasonYear = %d, want 2026", got)
	}
}

func TestProviderTeams(t *testing.T) {
	p := testProvider(t)
	teams, details, fetches, err := p.Teams(context.Background())
	if err != nil {
		t.Fatalf("Teams failed: %v", err)
	}
	if fetches != nil {
		t.Errorf("Teams needs no upstream call, got fetches = %+v", fetches)
	}
	if len(teams) != 30 || len(details) != 30 {
		t.Fatalf("teams = %d, details = %d, want 30 each", len(teams), len(details))
	}
	lal := details[ids.Team(leagueNBA, "1610612747")]
	if lal.Venue == nil || lal.Venue.Name != "Crypto.com Arena" || lal.Venue.City != "Los Angeles" {
		t.Errorf("venue wrong: %+v", lal.Venue)
	}
	if lal.Abbreviation != "LAL" {
		t.Errorf("summary wrong: %+v", lal.TeamSummary)
	}
}

func TestProviderTeamStatsFullSeasonIncludesSplits(t *testing.T) {
	p := testProvider(t)
	stats, fetches, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(fetches) != 7 {
		t.Errorf("fetches = %d, want 7 (3 league dash + 4 location split)", len(fetches))
	}
	bos := stats[ids.Team(leagueNBA, "1610612738")]
	if bos.HomeAwaySplits == nil {
		t.Fatal("full-season TeamStats must include home/away splits")
	}
	if bos.HomeAwaySplits.Home.Wins != 64 {
		t.Errorf("home split wrong: %+v", bos.HomeAwaySplits.Home)
	}
}

func TestProviderTeamStatsRollingWindowSkipsSplits(t *testing.T) {
	p := testProvider(t)
	stats, fetches, err := p.TeamStats(context.Background(), 2025, 10)
	if err != nil {
		t.Fatalf("TeamStats failed: %v", err)
	}
	if len(fetches) != 3 {
		t.Errorf("fetches = %d, want 3 (rolling window skips the splits call)", len(fetches))
	}
	for id, ts := range stats {
		if ts.HomeAwaySplits != nil {
			t.Errorf("rolling window must carry no splits: %s -> %+v", id, ts.HomeAwaySplits)
		}
	}
}

func TestProviderTeamStatsSplitsFailureIsNonFatal(t *testing.T) {
	base := fixtureMux(t)
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stats/leaguedashteamstats" && r.URL.Query().Get("Location") != "" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		base.ServeHTTP(w, r)
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })

	stats, fetches, err := p.TeamStats(context.Background(), 2025, 0)
	if err != nil {
		t.Fatalf("TeamStats must not fail when splits are unavailable: %v", err)
	}
	if len(fetches) == 0 {
		t.Error("expected some fetches even though the splits call failed")
	}
	for id, ts := range stats {
		if ts.HomeAwaySplits != nil {
			t.Errorf("splits must stay nil when the splits call errors: %s -> %+v", id, ts.HomeAwaySplits)
		}
	}
}

func TestProviderPlayers(t *testing.T) {
	p := testProvider(t)
	summaries, details, fetches, err := p.Players(context.Background(), 2025)
	if err != nil {
		t.Fatalf("Players failed: %v", err)
	}
	// One roster fetch per of the 30 static teams, plus one league-wide
	// player-stats fetch.
	if len(fetches) != 31 {
		t.Errorf("fetches = %d, want 31", len(fetches))
	}
	// The fixture roster (LeBron + a rookie guard) is served for every team,
	// so every static team contributes both entries.
	if len(summaries) != 60 {
		t.Fatalf("summaries = %d, want 60 (30 teams x 2 roster entries)", len(summaries))
	}
	found := false
	for _, s := range summaries {
		if s.LastName == "James" {
			found = true
			d := details[s.ID]
			if d.SeasonStats == nil || d.SeasonStats.PointsPerGame != 25.7 {
				t.Errorf("season stats wrong: %+v", d.SeasonStats)
			}
		}
	}
	if !found {
		t.Error("expected at least one LeBron James roster entry")
	}
}

func TestProviderPlayersRosterUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })

	_, _, fetches, err := p.Players(context.Background(), 2025)
	if err == nil {
		t.Fatal("expected roster fetch error")
	}
	if len(fetches) == 0 {
		t.Error("expected the failing roster fetch to still be archived")
	}
}

func TestProviderPlayersLeagueStatsUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stats/commonteamroster" {
			serveFixture(t, w, "commonteamroster.json")
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })

	_, _, fetches, err := p.Players(context.Background(), 2025)
	if err == nil {
		t.Fatal("expected league player stats fetch error")
	}
	// 30 successful roster fetches plus the failing league stats fetch.
	if len(fetches) != 31 {
		t.Errorf("fetches = %d, want 31", len(fetches))
	}
}

func TestProviderSchedule(t *testing.T) {
	p := testProvider(t)
	games, fetches, err := p.Schedule(context.Background(), 2025)
	if err != nil {
		t.Fatalf("Schedule failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
	if len(games) != 2 {
		t.Fatalf("games = %d, want 2", len(games))
	}
}

func TestProviderScheduleUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })
	_, fetches, err := p.Schedule(context.Background(), 2025)
	if err == nil {
		t.Fatal("expected schedule fetch error")
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (archived even on failure)", len(fetches))
	}
}

func TestProviderScoreboard(t *testing.T) {
	p := testProvider(t)
	updates, fetches, err := p.Scoreboard(context.Background(), time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Scoreboard failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
	update, ok := updates["0022500456"]
	if !ok {
		t.Fatal("expected an update for game 0022500456")
	}
	if update.Status != model.GameFinal || update.HomeScore != 128 || update.AwayScore != 124 {
		t.Errorf("update wrong: %+v", update)
	}
	if update.Result == nil || !update.Result.Overtime || update.Result.ID != "" {
		t.Errorf("result wrong (ID must be empty until the service matches it): %+v", update.Result)
	}
}

func TestProviderScoreboardUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })
	_, fetches, err := p.Scoreboard(context.Background(), time.Now())
	if err == nil {
		t.Fatal("expected scoreboard fetch error")
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (archived even on failure)", len(fetches))
	}
}

func TestProviderPlayerGameLog(t *testing.T) {
	p := testProvider(t)
	player := model.PlayerDetail{PlayerSummary: model.PlayerSummary{
		ID:          ids.Player(leagueNBA, "2544"),
		ExternalIDs: map[string]string{"nba": "2544"},
	}}

	log, fetches, err := p.PlayerGameLog(context.Background(), player, 2025)
	if err != nil {
		t.Fatalf("PlayerGameLog failed: %v", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1", len(fetches))
	}
	if len(log) != 2 {
		t.Fatalf("log entries = %d, want 2", len(log))
	}
	if log[0].GameID != ids.Game(leagueNBA, "0022500456") {
		t.Errorf("game id not minted with ids.Game: %+v", log[0])
	}
	if log[0].GameDate.IsZero() {
		t.Error("GAME_DATE (\"JAN 15, 2026\") must parse via capitalizeDate")
	}
	if log[1].GameDate.IsZero() {
		t.Error("second entry's date must also parse")
	}
}

func TestProviderPlayerGameLogMissingExternalID(t *testing.T) {
	p := testProvider(t)
	player := model.PlayerDetail{PlayerSummary: model.PlayerSummary{ID: "no-external-id"}}

	_, fetches, err := p.PlayerGameLog(context.Background(), player, 2025)
	if err == nil || !strings.Contains(err.Error(), "has no NBA external id") {
		t.Fatalf("err = %v, want missing-external-id error", err)
	}
	if fetches != nil {
		t.Errorf("fetches = %+v, want nil (no upstream call attempted)", fetches)
	}
}

func TestProviderPlayerGameLogUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })
	player := model.PlayerDetail{PlayerSummary: model.PlayerSummary{ExternalIDs: map[string]string{"nba": "2544"}}}

	_, fetches, err := p.PlayerGameLog(context.Background(), player, 2025)
	if err == nil || !strings.Contains(err.Error(), "fetch game log") {
		t.Fatalf("err = %v, want wrapped fetch-game-log error", err)
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (archived even on failure)", len(fetches))
	}
}

const emptyBoxScoreFixture = `{
  "resource": "boxscoretraditional",
  "resultSets": [
    {"name": "PlayerStats", "headers": ["TEAM_ID"], "rowSet": []},
    {"name": "TeamStats", "headers": ["TEAM_ID"], "rowSet": []}
  ]
}`

func TestProviderBoxScoreNoData(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(emptyBoxScoreFixture))
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })

	_, _, err := p.BoxScore(context.Background(), "0000000000")
	if err == nil || !strings.Contains(err.Error(), "no box score data for game") {
		t.Fatalf("err = %v, want no-box-score-data error", err)
	}
}

func TestProviderBoxScoreUpstreamError(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	p := NewProvider(client, func(time.Time) int { return 2025 })

	_, fetches, err := p.BoxScore(context.Background(), "0022500001")
	if err == nil {
		t.Fatal("expected box score fetch error")
	}
	if len(fetches) != 1 {
		t.Errorf("fetches = %d, want 1 (archived even on failure)", len(fetches))
	}
}

func TestCapitalizeDate(t *testing.T) {
	tests := map[string]string{
		"JAN 15, 2026": "Jan 15, 2026",
		"OCT 21, 2025": "Oct 21, 2025",
		"a":            "a", // too short to touch, returned unchanged
		"":             "",
	}
	for in, want := range tests {
		if got := capitalizeDate(in); got != want {
			t.Errorf("capitalizeDate(%q) = %q, want %q", in, got, want)
		}
	}
}
