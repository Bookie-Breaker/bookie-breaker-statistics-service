package service

import (
	"context"
	"errors"
	"testing"
	"time"
)

func testIntervals() Intervals {
	return Intervals{
		TeamStats:  20 * time.Millisecond,
		Rosters:    20 * time.Millisecond,
		Schedule:   20 * time.Millisecond,
		Injuries:   20 * time.Millisecond,
		Scoreboard: 20 * time.Millisecond,
	}
}

// TestSchedulerRunsAllJobsAndStops covers Start end to end: every job runs
// immediately, keeps ticking on its cadence, and Start returns on cancel.
func TestSchedulerRunsAllJobsAndStops(t *testing.T) {
	f := newFixture(t)
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()
	f.provider.teamStats = sampleTeamStats()
	f.provider.players = samplePlayers()
	f.provider.playerDetails = samplePlayerDetails()

	s := NewScheduler(f.refresh, f.watcher, testIntervals())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Start(ctx)
		close(done)
	}()

	// Wait until every loop has run at least twice (startup run + one tick).
	deadline := time.After(10 * time.Second)
	for f.provider.callCount("teams") < 1 ||
		f.provider.callCount("teamstats") < 2 ||
		f.provider.callCount("players") < 2 ||
		f.provider.callCount("schedule") < 2 {
		select {
		case <-deadline:
			t.Fatalf("jobs did not run: teams=%d teamstats=%d players=%d schedule=%d",
				f.provider.callCount("teams"), f.provider.callCount("teamstats"),
				f.provider.callCount("players"), f.provider.callCount("schedule"))
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}

	// The refreshes actually landed in the cache.
	if _, ok, _ := f.cache.GetTeams(context.Background(), "NBA", false); !ok {
		t.Error("teams were not cached by the scheduler run")
	}
	if _, ok, _ := f.cache.GetTeamStats(context.Background(), "NBA", testSeason, 0, false); !ok {
		t.Error("team stats were not cached by the scheduler run")
	}
}

// TestSchedulerSurvivesJobFailures covers the loop's error handling: a
// persistently failing job is logged and retried, and never kills Start or
// the other loops.
func TestSchedulerSurvivesJobFailures(t *testing.T) {
	f := newFixture(t)
	f.provider.teams = sampleTeams()
	f.provider.teamDetails = sampleTeamDetails()
	f.provider.players = samplePlayers()
	f.provider.playerDetails = samplePlayerDetails()
	f.provider.fail("teamstats", errors.New("upstream down"))

	s := NewScheduler(f.refresh, f.watcher, testIntervals())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Start(ctx)
		close(done)
	}()

	// The failing job keeps retrying while healthy jobs keep running.
	deadline := time.After(10 * time.Second)
	for f.provider.callCount("teamstats") < 2 || f.provider.callCount("players") < 2 {
		select {
		case <-deadline:
			t.Fatalf("loops stalled: teamstats=%d players=%d",
				f.provider.callCount("teamstats"), f.provider.callCount("players"))
		case <-time.After(5 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}
