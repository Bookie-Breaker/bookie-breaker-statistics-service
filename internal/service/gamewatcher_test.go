package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/pubsub"
)

func finalUpdate() sportsdata.ScoreboardUpdate {
	return sportsdata.ScoreboardUpdate{
		Status:    model.GameFinal,
		HomeScore: 112,
		AwayScore: 104,
		Result: &model.GameResult{
			HomeScore: 112, AwayScore: 104, TotalScore: 216, Margin: 8,
			PeriodScores: []model.PeriodScore{{Period: 1, Home: 30, Away: 25}},
		},
	}
}

func TestTickNoScheduleIsIdle(t *testing.T) {
	f := newFixture(t)

	if err := f.watcher.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.callCount("scoreboard"); got != 0 {
		t.Errorf("scoreboard polled with no cached schedule (calls = %d)", got)
	}
}

func TestTickOffseasonPollsNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	// All games far in the past: watchDates yields nothing.
	games := sampleGames(time.Now().UTC().Add(-90 * 24 * time.Hour))
	if err := f.cache.SetGames(ctx, "NBA", testSeason, games); err != nil {
		t.Fatal(err)
	}

	if err := f.watcher.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.provider.callCount("scoreboard"); got != 0 {
		t.Errorf("offseason tick polled the scoreboard (calls = %d)", got)
	}
}

// TestTickFinalTransition covers the full watcher flow: a FINAL scoreboard
// update publishes game.completed once, records the result, and warms the
// box-score cache.
func TestTickFinalTransition(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.cache.SetGames(ctx, "NBA", testSeason, sampleGames(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	f.provider.scoreboard = map[string]sportsdata.ScoreboardUpdate{"ext-1": finalUpdate()}
	f.provider.boxScore = &model.BoxScore{
		HomeTeam: model.TeamBoxScore{ID: "team-lal", Score: 112},
		AwayTeam: model.TeamBoxScore{ID: "team-bos", Score: 104},
	}
	events := f.subscribe(t, "events:game.completed")

	if err := f.watcher.Tick(ctx); err != nil {
		t.Fatal(err)
	}

	var event pubsub.GameCompletedEvent
	if err := json.Unmarshal([]byte(waitForEvent(t, events)), &event); err != nil {
		t.Fatal(err)
	}
	if event.Event != "game.completed" || event.GameID != "game-1" || event.GameExternalID != "ext-1" {
		t.Errorf("event identity wrong: %+v", event)
	}
	if event.HomeTeam != "LAL" || event.AwayTeam != "BOS" || event.HomeScore != 112 || event.Total != 216 || event.Margin != 8 {
		t.Errorf("event scores wrong: %+v", event)
	}

	// The cache reflects the transition with the canonical result id.
	games, _, err := f.cache.GetGames(ctx, "NBA", testSeason, false)
	if err != nil {
		t.Fatal(err)
	}
	if games[0].Status != model.GameFinal || games[0].Result == nil || games[0].Result.ID != "game-1" {
		t.Errorf("cached game not finalized: %+v", games[0])
	}
	if *games[0].HomeScore != 112 || *games[0].AwayScore != 104 {
		t.Errorf("cached scores wrong: %d-%d", *games[0].HomeScore, *games[0].AwayScore)
	}
	// The untouched game is preserved.
	if games[1].Status != model.GameScheduled {
		t.Errorf("unrelated game mutated: %+v", games[1])
	}

	// The box score was prefetched and cached on the FINAL transition.
	box, ok, err := f.cache.GetBoxScore(ctx, "game-1")
	if err != nil || !ok || box.HomeTeam.ID != "team-lal" {
		t.Errorf("box score not prefetched: ok=%v err=%v box=%+v", ok, err, box)
	}

	// A second tick sees the game already FINAL: nothing left to watch for
	// that date, no re-publish.
	if err := f.watcher.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoEvent(t, events)
}

// TestTickDedupAcrossRestart covers the persistent dedup marker: a FINAL
// transition observed again (e.g. after a restart wiped in-memory state)
// must not re-publish.
func TestTickDedupAcrossRestart(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.cache.SetGames(ctx, "NBA", testSeason, sampleGames(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	f.provider.scoreboard = map[string]sportsdata.ScoreboardUpdate{"ext-1": finalUpdate()}
	f.provider.boxScore = &model.BoxScore{}

	// Simulate a pre-restart publish.
	if _, err := f.cache.MarkGameCompleted(ctx, "game-1"); err != nil {
		t.Fatal(err)
	}
	events := f.subscribe(t, "events:game.completed")

	if err := f.watcher.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoEvent(t, events)

	// The transition itself was still recorded.
	games, _, err := f.cache.GetGames(ctx, "NBA", testSeason, false)
	if err != nil || games[0].Status != model.GameFinal {
		t.Errorf("status not recorded despite dedup: %+v err=%v", games[0], err)
	}
}

// TestTickResultlessFinalDoesNotPublish covers publishCompleted's guard: a
// FINAL without a provider-built result publishes nothing.
func TestTickResultlessFinalDoesNotPublish(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.cache.SetGames(ctx, "NBA", testSeason, sampleGames(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	update := finalUpdate()
	update.Result = nil
	f.provider.scoreboard = map[string]sportsdata.ScoreboardUpdate{"ext-1": update}
	f.provider.boxScore = &model.BoxScore{}
	events := f.subscribe(t, "events:game.completed")

	if err := f.watcher.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoEvent(t, events)
}

func TestTickScoreboardErrorPropagates(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.cache.SetGames(ctx, "NBA", testSeason, sampleGames(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	f.provider.fail("scoreboard", errors.New("upstream down"))

	if err := f.watcher.Tick(ctx); err == nil {
		t.Error("scoreboard failure should propagate")
	}
	// The failed poll was still archived.
	if f.rawRepo.count() != 1 {
		t.Errorf("archived responses = %d, want 1", f.rawRepo.count())
	}
}

// TestTickBoxScorePrefetchFailureIsBestEffort covers prefetchBoxScore: a
// failing or unsupported box-score fetch never fails the tick or blocks the
// publish.
func TestTickBoxScorePrefetchFailureIsBestEffort(t *testing.T) {
	for name, err := range map[string]error{
		"not supported": sportsdata.ErrNotSupported,
		"upstream down": errors.New("upstream down"),
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()

			if err := f.cache.SetGames(ctx, "NBA", testSeason, sampleGames(time.Now().UTC())); err != nil {
				t.Fatal(err)
			}
			f.provider.scoreboard = map[string]sportsdata.ScoreboardUpdate{"ext-1": finalUpdate()}
			f.provider.boxScore = &model.BoxScore{}
			f.provider.fail("boxscore", err)
			events := f.subscribe(t, "events:game.completed")

			if err := f.watcher.Tick(ctx); err != nil {
				t.Fatalf("tick must survive box-score failure: %v", err)
			}
			// The publish still happened.
			waitForEvent(t, events)
			if _, ok, _ := f.cache.GetBoxScore(ctx, "game-1"); ok {
				t.Error("failed prefetch should cache nothing")
			}
		})
	}
}

// TestTickInProgressUpdatesScoresWithoutPublishing covers the live-score
// path: an IN_PROGRESS update records scores and rewrites the cache but
// publishes nothing.
func TestTickInProgressUpdatesScoresWithoutPublishing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.cache.SetGames(ctx, "NBA", testSeason, sampleGames(time.Now().UTC())); err != nil {
		t.Fatal(err)
	}
	f.provider.scoreboard = map[string]sportsdata.ScoreboardUpdate{
		"ext-1": {Status: model.GameInProgress, HomeScore: 55, AwayScore: 50},
	}
	events := f.subscribe(t, "events:game.completed")

	if err := f.watcher.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	assertNoEvent(t, events)

	games, _, err := f.cache.GetGames(ctx, "NBA", testSeason, false)
	if err != nil {
		t.Fatal(err)
	}
	if games[0].Status != model.GameInProgress || *games[0].HomeScore != 55 {
		t.Errorf("live scores not recorded: %+v", games[0])
	}
}
