package pubsub

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestGameCompletedEventNBAPayloadUnchanged guards the ADR-027 contract
// change: regulation-score fields are additive and optional, so an NBA
// event (which never sets them) must serialize byte-identically to the
// pre-ADR-027 payload — no "regulation" keys at all.
func TestGameCompletedEventNBAPayloadUnchanged(t *testing.T) {
	event := GameCompletedEvent{
		Event:          "game.completed",
		Timestamp:      "2026-01-15T22:30:00Z",
		GameID:         "8d7c9b3a-0000-0000-0000-000000000000",
		GameExternalID: "0022500456",
		League:         "NBA",
		HomeTeam:       "LAL",
		AwayTeam:       "BOS",
		HomeScore:      112,
		AwayScore:      104,
		Total:          216,
		Margin:         8,
		Overtime:       false,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "regulation") {
		t.Errorf("NBA payload must omit regulation fields, got %s", payload)
	}

	want := `{"event":"game.completed","timestamp":"2026-01-15T22:30:00Z",` +
		`"game_id":"8d7c9b3a-0000-0000-0000-000000000000","game_external_id":"0022500456",` +
		`"league":"NBA","home_team":"LAL","away_team":"BOS","home_score":112,` +
		`"away_score":104,"total":216,"margin":8,"overtime":false}`
	if string(payload) != want {
		t.Errorf("payload drifted from the pre-ADR-027 contract:\n got %s\nwant %s", payload, want)
	}
}

func TestGameCompletedEventRegulationScores(t *testing.T) {
	home, away := 1, 1
	event := GameCompletedEvent{
		League:              "FIFA_WC",
		HomeScore:           2,
		AwayScore:           1,
		RegulationHomeScore: &home,
		RegulationAwayScore: &away,
	}

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"regulation_home_score":1`) ||
		!strings.Contains(string(payload), `"regulation_away_score":1`) {
		t.Errorf("regulation scores missing when set: %s", payload)
	}
}
