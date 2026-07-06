package espn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func TestInjuriesFetchAndNormalize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/site/v2/sports/basketball/nba/injuries" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", "injuries.json"))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	client := NewClient(server.URL, "basketball/nba", 5*time.Second)
	resp, fetch, err := client.Injuries(context.Background())
	if err != nil {
		t.Fatalf("Injuries failed: %v", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK {
		t.Fatalf("fetch = %+v", fetch)
	}

	teamIDs := map[string]string{"los angeles lakers": "team-uuid-lal"}
	abbrevs := map[string]string{"los angeles lakers": "LAL"}
	playerIDs := map[string]string{"lebron james|LAL": "player-uuid-lbj"}

	reports := Normalize(resp, model.LeagueNBA, teamIDs, abbrevs, playerIDs)
	if len(reports) != 2 {
		t.Fatalf("reports = %d, want 2", len(reports))
	}

	lbj := reports[0]
	if lbj.PlayerName != "LeBron James" || lbj.Status != string(model.PlayerOut) {
		t.Errorf("report wrong: %+v", lbj)
	}
	if lbj.PlayerID != "player-uuid-lbj" || lbj.TeamID != "team-uuid-lal" || lbj.TeamAbbreviation != "LAL" {
		t.Errorf("id mapping wrong: %+v", lbj)
	}
	if lbj.UpdatedAt.IsZero() {
		t.Error("updated_at not parsed")
	}

	// Unmatched player still yields a report, just without a canonical id.
	guard := reports[1]
	if guard.PlayerID != "" || guard.Status != string(model.PlayerInjured) {
		t.Errorf("unmatched report wrong: %+v", guard)
	}
}

// TestMLBInjuriesFetchAndNormalize runs the generalized client against the
// real MLB injuries fixture (recorded 2026-07-05): baseball's injured-list
// statuses map to OUT and its minute-precision dates still parse.
func TestMLBInjuriesFetchAndNormalize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apis/site/v2/sports/baseball/mlb/injuries" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join("testdata", "injuries_mlb.json"))
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer server.Close()

	client := NewClient(server.URL, "baseball/mlb", 5*time.Second)
	resp, fetch, err := client.Injuries(context.Background())
	if err != nil {
		t.Fatalf("Injuries failed: %v", err)
	}
	if fetch == nil || fetch.HTTPStatus != http.StatusOK {
		t.Fatalf("fetch = %+v", fetch)
	}

	teamIDs := map[string]string{"los angeles dodgers": "team-uuid-lad"}
	abbrevs := map[string]string{"los angeles dodgers": "LAD"}

	reports := Normalize(resp, model.LeagueMLB, teamIDs, abbrevs, map[string]string{})
	if len(reports) != 4 {
		t.Fatalf("reports = %d, want 4", len(reports))
	}

	byName := make(map[string]model.InjuryReport, len(reports))
	for _, r := range reports {
		if r.League != model.LeagueMLB {
			t.Errorf("league = %s, want MLB", r.League)
		}
		byName[r.PlayerName] = r
	}

	// A 60-day IL stint is OUT, and the Dodgers map to their canonical team.
	knack := byName["Landon Knack"]
	if knack.Status != string(model.PlayerOut) || knack.TeamID != "team-uuid-lad" || knack.TeamAbbreviation != "LAD" {
		t.Errorf("IL report wrong: %+v", knack)
	}
	if knack.UpdatedAt.IsZero() {
		t.Error("minute-precision MLB date not parsed")
	}
	if byName["Max Fried"].Status != string(model.PlayerOut) {
		t.Errorf("15-day IL must map to OUT: %+v", byName["Max Fried"])
	}
	if byName["Jazz Chisholm Jr."].Status != string(model.PlayerInjured) {
		t.Errorf("day-to-day must map to INJURED: %+v", byName["Jazz Chisholm Jr."])
	}
}

func TestMapStatus(t *testing.T) {
	tests := map[string]model.PlayerStatus{
		"Out":          model.PlayerOut,
		"out":          model.PlayerOut,
		"Day-To-Day":   model.PlayerInjured,
		"Suspension":   model.PlayerSuspended,
		"Questionable": model.PlayerInjured,
		"???":          model.PlayerInjured,
		// Baseball injured-list stints (observed live 2026-07-05).
		"10-Day-IL": model.PlayerOut,
		"15-Day-IL": model.PlayerOut,
		"60-Day-IL": model.PlayerOut,
	}
	for in, want := range tests {
		if got := MapStatus(in); got != want {
			t.Errorf("MapStatus(%q) = %s, want %s", in, got, want)
		}
	}
}
