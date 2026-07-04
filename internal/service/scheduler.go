package service

import (
	"context"
	"log/slog"
	"time"
)

// Intervals configures the scheduler cadences.
type Intervals struct {
	TeamStats  time.Duration
	Rosters    time.Duration
	Schedule   time.Duration
	Injuries   time.Duration
	Scoreboard time.Duration
}

// Scheduler runs the refresh jobs on their cadences. Each job runs
// immediately at startup, then on its ticker; failures are logged and the
// loop continues.
type Scheduler struct {
	refresh   *RefreshService
	watcher   *GameWatcher
	intervals Intervals
}

// NewScheduler creates a scheduler.
func NewScheduler(refresh *RefreshService, watcher *GameWatcher, intervals Intervals) *Scheduler {
	return &Scheduler{refresh: refresh, watcher: watcher, intervals: intervals}
}

// Start launches all refresh loops. It blocks until ctx is canceled.
func (s *Scheduler) Start(ctx context.Context) {
	slog.Info("starting refresh scheduler",
		"team_stats", s.intervals.TeamStats.String(),
		"rosters", s.intervals.Rosters.String(),
		"schedule", s.intervals.Schedule.String(),
		"injuries", s.intervals.Injuries.String(),
		"scoreboard", s.intervals.Scoreboard.String(),
	)

	// Teams are static and the schedule feeds the watcher: run those first
	// so dependent jobs have their inputs.
	if err := s.refresh.RefreshTeams(ctx); err != nil {
		slog.Error("initial team refresh failed", "error", err)
	}

	go s.loop(ctx, "schedule", s.intervals.Schedule, s.refresh.RefreshSchedule)
	go s.loop(ctx, "team_stats", s.intervals.TeamStats, s.refresh.RefreshTeamStats)
	go s.loop(ctx, "players", s.intervals.Rosters, s.refresh.RefreshPlayers)
	go s.loop(ctx, "injuries", s.intervals.Injuries, s.refresh.RefreshInjuries)
	go s.loop(ctx, "scoreboard", s.intervals.Scoreboard, s.watcher.Tick)

	<-ctx.Done()
	slog.Info("refresh scheduler stopped")
}

func (s *Scheduler) loop(ctx context.Context, name string, interval time.Duration, fn func(context.Context) error) {
	run := func() {
		if err := fn(ctx); err != nil && ctx.Err() == nil {
			slog.Error("refresh job failed", "job", name, "error", err)
		}
	}

	run()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
