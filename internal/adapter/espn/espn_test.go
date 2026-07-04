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
		if r.URL.Path != injuriesPath {
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

	client := NewClient(server.URL, 5*time.Second)
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

	reports := Normalize(resp, teamIDs, abbrevs, playerIDs)
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

func TestMapStatus(t *testing.T) {
	tests := map[string]model.PlayerStatus{
		"Out":          model.PlayerOut,
		"out":          model.PlayerOut,
		"Day-To-Day":   model.PlayerInjured,
		"Suspension":   model.PlayerSuspended,
		"Questionable": model.PlayerInjured,
		"???":          model.PlayerInjured,
	}
	for in, want := range tests {
		if got := MapStatus(in); got != want {
			t.Errorf("MapStatus(%q) = %s, want %s", in, got, want)
		}
	}
}
