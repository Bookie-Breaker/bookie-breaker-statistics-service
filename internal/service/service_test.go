package service

import (
	"testing"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

func TestWatchDates(t *testing.T) {
	now := time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)

	game := func(start time.Time, status model.GameStatus) model.Game {
		return model.Game{ID: start.String(), ScheduledStart: start, Status: status}
	}

	games := []model.Game{
		game(time.Date(2026, 1, 15, 19, 0, 0, 0, time.UTC), model.GameScheduled),  // today, pending
		game(time.Date(2026, 1, 14, 23, 0, 0, 0, time.UTC), model.GameInProgress), // yesterday, live
		game(time.Date(2026, 1, 15, 1, 0, 0, 0, time.UTC), model.GameFinal),       // today, done
		game(time.Date(2026, 1, 10, 19, 0, 0, 0, time.UTC), model.GameScheduled),  // stale, ignored
		game(time.Date(2026, 1, 20, 19, 0, 0, 0, time.UTC), model.GameScheduled),  // future, ignored
	}

	dates := watchDates(games, now)
	if len(dates) != 2 {
		t.Fatalf("dates = %v, want today and yesterday", dates)
	}
}

func TestWatchDatesOffseasonIdle(t *testing.T) {
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	games := []model.Game{
		{ScheduledStart: time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC), Status: model.GameFinal},
	}
	if dates := watchDates(games, now); len(dates) != 0 {
		t.Errorf("offseason should watch nothing, got %v", dates)
	}
}

// TestApplyScoreboardUpdateRegulationScores covers the soccer extra-time
// transition: a status flip to FINAL adopts the provider-built result
// including the ADR-027 regulation scores, which publishCompleted then
// carries onto events:game.completed (fields wired in Wave 0).
func TestApplyScoreboardUpdateRegulationScores(t *testing.T) {
	regHome, regAway := 2, 2
	game := model.Game{ID: "game-uuid", Status: model.GameInProgress}
	update := sportsdata.ScoreboardUpdate{
		Status:    model.GameFinal,
		HomeScore: 3,
		AwayScore: 2,
		Result: &model.GameResult{
			HomeScore: 3, AwayScore: 2, TotalScore: 5, Margin: 1,
			Overtime:            true,
			RegulationHomeScore: &regHome,
			RegulationAwayScore: &regAway,
		},
	}

	applyScoreboardUpdate(&game, update)
	if game.Status != model.GameFinal || game.Result == nil {
		t.Fatalf("final transition wrong: %+v", game)
	}
	if game.Result.ID != "game-uuid" {
		t.Errorf("result id = %q, want the canonical game id", game.Result.ID)
	}
	if game.Result.RegulationHomeScore == nil || *game.Result.RegulationHomeScore != 2 ||
		game.Result.RegulationAwayScore == nil || *game.Result.RegulationAwayScore != 2 {
		t.Errorf("regulation scores lost in transition: %+v", game.Result)
	}
	if *game.HomeScore != 3 || *game.AwayScore != 2 {
		t.Errorf("full-time scores wrong: %d-%d", *game.HomeScore, *game.AwayScore)
	}
}

func TestShapeStatBlocks(t *testing.T) {
	full := model.TeamStats{
		Stats: model.StatBlocks{
			Offensive: &model.OffensiveStats{PointsPerGame: 110},
			Defensive: &model.DefensiveStats{PointsAllowedPerGame: 105},
			Advanced:  &model.AdvancedStats{NetRating: 5},
		},
		HomeAwaySplits: &model.HomeAwaySplits{Home: &model.SplitRecord{Wins: 25}},
	}

	off := shapeStatBlocks(full, model.StatOffensive)
	if off.Stats.Offensive == nil || off.Stats.Defensive != nil || off.Stats.Advanced != nil || off.HomeAwaySplits != nil {
		t.Errorf("offensive shaping wrong: %+v", off.Stats)
	}

	overall := shapeStatBlocks(full, model.StatOverall)
	if overall.Stats.Offensive == nil || overall.Stats.Defensive == nil || overall.Stats.Advanced != nil {
		t.Errorf("overall shaping wrong: %+v", overall.Stats)
	}

	all := shapeStatBlocks(full, model.StatAll)
	if all.Stats.Advanced == nil || all.HomeAwaySplits == nil {
		t.Errorf("all shaping wrong: %+v", all.Stats)
	}
}

func TestParseDateRange(t *testing.T) {
	from, to, err := parseDateRange("2026-01-15", "2026-01-15")
	if err != nil {
		t.Fatal(err)
	}
	inRange := time.Date(2026, 1, 15, 23, 0, 0, 0, time.UTC)
	if inRange.Before(from) || !inRange.Before(to) {
		t.Error("date_to should be inclusive of the whole day")
	}

	if _, _, err := parseDateRange("garbage", ""); err == nil {
		t.Error("expected error for malformed date_from")
	}
}

func TestGameHasTeam(t *testing.T) {
	g := model.Game{
		HomeTeam: model.TeamRef{ID: "uuid-home", Abbreviation: "LAL"},
		AwayTeam: model.TeamRef{ID: "uuid-away", Abbreviation: "BOS"},
	}
	if !gameHasTeam(g, "uuid-home") || !gameHasTeam(g, "bos") || gameHasTeam(g, "GSW") {
		t.Error("gameHasTeam matching wrong")
	}
}

func TestDiffInjuries(t *testing.T) {
	old := []model.InjuryReport{{PlayerID: "p1", Status: "INJURED"}}
	current := []model.InjuryReport{
		{PlayerID: "p1", Status: "OUT"},                               // changed
		{PlayerID: "p2", Status: "INJURED"},                           // new
		{PlayerName: "No Id", TeamAbbreviation: "LAL", Status: "OUT"}, // new, unmatched
	}

	changes := diffInjuries(old, current)
	if len(changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(changes))
	}
	if changes[0].Old != "INJURED" || changes[0].New != "OUT" {
		t.Errorf("transition wrong: %+v", changes[0])
	}

	if got := diffInjuries(current, current); len(got) != 0 {
		t.Errorf("no-op diff should be empty, got %v", got)
	}
}
