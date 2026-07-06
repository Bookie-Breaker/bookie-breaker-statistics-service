package espnfb

// Unit tests for the shared ESPN football helpers used by both the NFL and
// NCAA_FB adapters. Status/season mappings and the standings flattening are
// pure functions; the provider integration tests (nfl, cfbd) exercise them
// end-to-end over real archived 2025-season fixtures.

import (
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func TestMapStatus(t *testing.T) {
	tests := []struct {
		name, state string
		completed   bool
		want        model.GameStatus
	}{
		{"STATUS_SCHEDULED", "pre", false, model.GameScheduled},
		{"STATUS_IN_PROGRESS", "in", false, model.GameInProgress},
		{"STATUS_FINAL", "post", true, model.GameFinal},
		{"STATUS_POSTPONED", "post", false, model.GamePostponed},
		{"STATUS_CANCELED", "post", false, model.GameCancelled}, //nolint:misspell // contract enum spelling
		{"STATUS_SUSPENDED", "in", false, model.GameSuspended},
		{"STATUS_DELAYED", "in", false, model.GameSuspended},
		// A "post" state that never completed is treated as postponed.
		{"STATUS_UNKNOWN", "post", false, model.GamePostponed},
	}
	for _, tt := range tests {
		got := MapStatus(StatusType{Name: tt.name, State: tt.state, Completed: tt.completed})
		if got != tt.want {
			t.Errorf("MapStatus(%q,%q,%v) = %s, want %s", tt.name, tt.state, tt.completed, got, tt.want)
		}
	}
}

func TestMapSeasonType(t *testing.T) {
	for typ, want := range map[int]model.SeasonType{
		1: model.SeasonPreseason,
		2: model.SeasonRegular,
		3: model.SeasonPostseason,
		4: model.SeasonOffseason,
		0: model.SeasonRegular, // absent field falls back to regular season
	} {
		if got := MapSeasonType(EventSeason{Type: typ}); got != want {
			t.Errorf("MapSeasonType(%d) = %s, want %s", typ, got, want)
		}
	}
}

func TestParseRecordSummary(t *testing.T) {
	tests := []struct {
		in       string
		w, l, ti int
		ok       bool
	}{
		{"14-3", 14, 3, 0, true},
		{"9-7-1", 9, 7, 1, true},
		{" 12 - 2 ", 12, 2, 0, true},
		{"", 0, 0, 0, false},
		{"12", 0, 0, 0, false},
		{"a-b", 0, 0, 0, false},
	}
	for _, tt := range tests {
		w, l, ti, ok := parseRecordSummary(tt.in)
		if w != tt.w || l != tt.l || ti != tt.ti || ok != tt.ok {
			t.Errorf("parseRecordSummary(%q) = %d,%d,%d,%v, want %d,%d,%d,%v", tt.in, w, l, ti, ok, tt.w, tt.l, tt.ti, tt.ok)
		}
	}
}

func TestRecords(t *testing.T) {
	resp := &StandingsResponse{
		Children: []struct {
			Name      string `json:"name"`
			Standings *struct {
				Entries []StandingsEntry `json:"entries"`
			} `json:"standings"`
		}{
			{
				Name: "conference-with-explicit-stats",
				Standings: &struct {
					Entries []StandingsEntry `json:"entries"`
				}{
					Entries: []StandingsEntry{{
						Team: Team{ID: "17", Abbreviation: "NE"},
						Stats: []StandingStat{
							{Type: "wins", Value: 14},
							{Type: "losses", Value: 3},
							{Type: "ties", Value: 0},
							{Type: "pointsfor", Value: 490},
							{Type: "pointsagainst", Value: 320},
							{Type: "total", Summary: "14-3"},
						},
					}},
				},
			},
			{
				// College style: only the overall record summary is present.
				Name: "conference-summary-only",
				Standings: &struct {
					Entries []StandingsEntry `json:"entries"`
				}{
					Entries: []StandingsEntry{{
						Team:  Team{ID: "249", Abbreviation: "UNT"},
						Stats: []StandingStat{{Type: "total", Summary: "12-2"}},
					}},
				},
			},
			{Name: "conference-without-standings", Standings: nil}, // skipped
		},
	}

	records := Records(resp)
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (nil-standings child skipped)", len(records))
	}
	ne := records[0]
	if ne.Wins != 14 || ne.Losses != 3 || ne.Ties != 0 || ne.Games() != 17 {
		t.Errorf("NE record wrong: %+v (games %d)", ne, ne.Games())
	}
	if ne.PointsFor != 490 || ne.PointsAgainst != 320 {
		t.Errorf("NE points wrong: %+v", ne)
	}
	unt := records[1]
	if unt.Wins != 12 || unt.Losses != 2 || unt.Games() != 14 {
		t.Errorf("UNT summary-only record wrong: %+v (games %d)", unt, unt.Games())
	}
}

// TestBuildResultTie covers the ADR-027 football rule: a FINAL with equal
// scores is a valid tie whose scores flow through as-is, and overtime play
// (period > 4) sets Overtime with no regulation fields.
func TestBuildResultTie(t *testing.T) {
	home := Competitor{Score: "40", Linescores: []Linescore{{0}, {16}, {7}, {14}, {3}}}
	away := Competitor{Score: "40", Linescores: []Linescore{{7}, {6}, {7}, {17}, {3}}}
	ev := Event{Status: EventStatus{Period: 5}}
	completedAt := time.Date(2025, 9, 29, 0, 0, 0, 0, time.UTC)

	r := BuildResult(ev, home, away, completedAt)
	if r.HomeScore != 40 || r.AwayScore != 40 || r.Margin != 0 || r.TotalScore != 80 {
		t.Errorf("tie result math wrong: %+v", r)
	}
	if !r.Overtime {
		t.Error("period 5 must be overtime")
	}
	if len(r.PeriodScores) != 5 || r.PeriodScores[4].Home != 3 || r.PeriodScores[4].Away != 3 {
		t.Errorf("overtime period scores wrong: %+v", r.PeriodScores)
	}
	if r.RegulationHomeScore != nil || r.RegulationAwayScore != nil {
		t.Errorf("football carries no regulation fields: %+v", r)
	}
	if !r.CompletedAt.Equal(completedAt) {
		t.Errorf("completedAt = %s, want %s", r.CompletedAt, completedAt)
	}
}
