package pubsub

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/tests/testutil"
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

// subscribe opens a subscription and waits for the channel to be confirmed
// before returning, so a subsequent publish cannot race the subscriber.
func subscribe(t *testing.T, channel string) <-chan []byte {
	t.Helper()
	rdb := testutil.RedisClient(t)
	sub := rdb.Subscribe(context.Background(), channel)
	t.Cleanup(func() { _ = sub.Close() })
	if _, err := sub.Receive(context.Background()); err != nil {
		t.Fatalf("confirm subscription: %v", err)
	}

	out := make(chan []byte, 1)
	go func() {
		if msg, err := sub.ReceiveMessage(context.Background()); err == nil {
			out <- []byte(msg.Payload)
		}
	}()
	return out
}

func receive(t *testing.T, ch <-chan []byte) []byte {
	t.Helper()
	select {
	case payload := <-ch:
		return payload
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for published event")
		return nil
	}
}

func TestPublishStatsUpdated(t *testing.T) {
	rdb := testutil.RedisClient(t)
	messages := subscribe(t, "events:stats.updated")

	p := NewPublisher(rdb)
	err := p.PublishStatsUpdated(context.Background(), StatsUpdatedEvent{
		League:     "NBA",
		UpdateType: "team_stats",
		TeamIDs:    []string{"team-1"},
	})
	if err != nil {
		t.Fatal(err)
	}

	var got StatsUpdatedEvent
	if err := json.Unmarshal(receive(t, messages), &got); err != nil {
		t.Fatal(err)
	}
	if got.Event != "stats.updated" || got.League != "NBA" || got.UpdateType != "team_stats" {
		t.Errorf("event payload wrong: %+v", got)
	}
	if len(got.TeamIDs) != 1 || got.TeamIDs[0] != "team-1" {
		t.Errorf("team ids wrong: %v", got.TeamIDs)
	}
	if _, err := time.Parse(time.RFC3339, got.Timestamp); err != nil {
		t.Errorf("timestamp not RFC3339: %q", got.Timestamp)
	}
}

func TestPublishGameCompleted(t *testing.T) {
	rdb := testutil.RedisClient(t)
	messages := subscribe(t, "events:game.completed")

	p := NewPublisher(rdb)
	err := p.PublishGameCompleted(context.Background(), GameCompletedEvent{
		GameID:    "g-1",
		League:    "NBA",
		HomeTeam:  "LAL",
		AwayTeam:  "BOS",
		HomeScore: 112,
		AwayScore: 104,
		Total:     216,
		Margin:    8,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got GameCompletedEvent
	if err := json.Unmarshal(receive(t, messages), &got); err != nil {
		t.Fatal(err)
	}
	if got.Event != "game.completed" || got.GameID != "g-1" || got.HomeScore != 112 || got.Total != 216 {
		t.Errorf("event payload wrong: %+v", got)
	}
	if _, err := time.Parse(time.RFC3339, got.Timestamp); err != nil {
		t.Errorf("timestamp not RFC3339: %q", got.Timestamp)
	}
}

func TestPublishRedisDown(t *testing.T) {
	rdb := testutil.RedisClient(t)
	if err := rdb.Close(); err != nil {
		t.Fatal(err)
	}

	p := NewPublisher(rdb)
	if err := p.PublishStatsUpdated(context.Background(), StatsUpdatedEvent{League: "NBA"}); err == nil {
		t.Error("PublishStatsUpdated on closed client should error")
	}
	if err := p.PublishGameCompleted(context.Background(), GameCompletedEvent{GameID: "g-1"}); err == nil {
		t.Error("PublishGameCompleted on closed client should error")
	}
}
