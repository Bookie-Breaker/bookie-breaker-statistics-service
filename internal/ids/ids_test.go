package ids

import (
	"testing"

	"github.com/google/uuid"
)

// Golden values: these UUIDs are shared identifiers across services and
// restarts; a change here is a breaking change for every consumer.
func TestDeterministicIDs(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"team", Team("NBA", "1610612747"), "943078ea-8142-5017-8aa8-12db30b1fed0"},
		{"player", Player("NBA", "2544"), "1166d2a3-ce4e-5e58-b699-b34516f5c984"},
		{"game", Game("NBA", "0022500001"), "0ee6afda-173f-57c4-a05b-040d2fd637a6"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s id = %s, want %s", tt.name, tt.got, tt.want)
		}
	}
}

func TestIDKindsDoNotCollide(t *testing.T) {
	if Team("NBA", "42") == Player("NBA", "42") {
		t.Error("team and player ids must not collide for the same external id")
	}
	if Team("NBA", "42") == Team("NFL", "42") {
		t.Error("ids must be league-scoped")
	}
	if Venue("NBA", "Chase Center") == Team("NBA", "Chase Center") {
		t.Error("venue and team ids must not collide for the same name")
	}
}

// Golden value: Venue is keyed by name rather than an external numeric id;
// pin the derived UUID like the other kinds.
func TestVenueID(t *testing.T) {
	got := Venue("NBA", "Chase Center")
	want := uuid.NewSHA1(namespace, []byte("NBA:venue:Chase Center")).String()
	if got != want {
		t.Errorf("Venue id = %s, want %s", got, want)
	}
}
