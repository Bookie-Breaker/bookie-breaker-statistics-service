package nhl

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// leagueNHL is the league key for canonical id derivation. Team ids derive
// from the tricode (the stable external id shared by every NHL endpoint).
const leagueNHL = string(model.LeagueNHL)

// regulationPeriods is a regulation hockey game's three periods; a fourth
// (overtime) or fifth (shootout) period means the game went past regulation.
const regulationPeriods = 3

// startingYear converts a concatenated NHL season id (e.g. 20252026) to its
// starting year (2025), which identifies the season everywhere else.
func startingYear(seasonID int) int {
	// Concatenated ids are eight digits (e.g. 20252026); a bare four-digit
	// year is passed through unchanged.
	if seasonID < 1000000 {
		return seasonID
	}
	return seasonID / 10000
}

// seasonID builds the concatenated NHL season id from a starting year: 2025
// becomes 20252026.
func seasonID(startYear int) int {
	return startYear*10000 + startYear + 1
}

// normalizeTeams builds the canonical team collections from the standings.
// The tricode is the external id; the common name maps to the mascot. NHL
// teams carry no venue in Phase 6 (home arenas are not modeled).
func normalizeTeams(resp *standingsResponse) ([]model.TeamSummary, map[string]model.TeamDetail) {
	summaries := make([]model.TeamSummary, 0, len(resp.Standings))
	details := make(map[string]model.TeamDetail, len(resp.Standings))
	for _, e := range resp.Standings {
		abbrev := e.TeamAbbrev.Default
		summary := model.TeamSummary{
			ID:           ids.Team(leagueNHL, abbrev),
			League:       model.LeagueNHL,
			Name:         e.TeamName.Default,
			Abbreviation: abbrev,
			Location:     e.PlaceName.Default,
			Mascot:       e.TeamCommonName.Default,
			Conference:   e.ConferenceName,
			Division:     e.DivisionName,
			Active:       true,
			ExternalIDs:  map[string]string{"nhl": abbrev},
		}
		summaries = append(summaries, summary)
		details[summary.ID] = model.TeamDetail{TeamSummary: summary}
	}
	return summaries, details
}

// mapStatus converts an NHL gameState (with the separate gameScheduleState)
// to the canonical status. Schedule disruptions live in gameScheduleState and
// are checked first; gameState values observed live: FUT/PRE (upcoming),
// LIVE/CRIT (in progress), OVER/FINAL/OFF (complete).
func mapStatus(gameState, scheduleState string) model.GameStatus {
	switch scheduleState {
	case "PPD":
		return model.GamePostponed
	case "CNCL":
		return model.GameCancelled //nolint:misspell // spelling fixed by the OpenAPI contract enum
	case "SUSP":
		return model.GameSuspended
	}
	switch gameState {
	case "FINAL", "OFF", "OVER":
		return model.GameFinal
	case "LIVE", "CRIT":
		return model.GameInProgress
	default: // "FUT", "PRE"
		return model.GameScheduled
	}
}

// mapSeasonType converts an NHL gameType to the canonical season type: 1
// preseason, 2 regular season, 3 playoffs.
func mapSeasonType(gameType int) model.SeasonType {
	switch gameType {
	case 1:
		return model.SeasonPreseason
	case 3:
		return model.SeasonPostseason
	default: // 2
		return model.SeasonRegular
	}
}

// overtime reports whether a game went past regulation. gameOutcome's
// lastPeriodType is authoritative when present ("OT"/"SO"); otherwise the
// period count past maxRegulationPeriods decides.
func overtime(g game) bool {
	if lpt := g.GameOutcome.LastPeriodType; lpt != "" {
		return lpt != "REG"
	}
	maxReg := g.PeriodDescriptor.MaxRegulationPeriods
	if maxReg == 0 {
		maxReg = regulationPeriods
	}
	return g.PeriodDescriptor.Number > maxReg
}

// teamScore reads a team's score, treating a missing score as 0.
func teamScore(t gameTeam) int {
	if t.Score != nil {
		return *t.Score
	}
	return 0
}

// teamRef builds a canonical team reference from a game's team object.
func teamRef(t gameTeam) model.TeamRef {
	return model.TeamRef{
		ID:           ids.Team(leagueNHL, t.Abbrev),
		Name:         t.fullName(),
		Abbreviation: t.Abbrev,
	}
}

// periodScores aggregates the score endpoint's per-goal detail into per-period
// scores. The shootout-deciding goal is recorded as the winner's single goal
// in the shootout period, so the period totals sum to the final score
// including the shootout (NHL convention; ADR-027). Returns nil when no goal
// detail is present (the schedule endpoint carries none).
func periodScores(g game) []model.PeriodScore {
	if len(g.Goals) == 0 {
		return nil
	}
	homeAbbrev := g.HomeTeam.Abbrev
	maxPeriod := g.PeriodDescriptor.Number
	for _, gl := range g.Goals {
		if gl.PeriodDescriptor.Number > maxPeriod {
			maxPeriod = gl.PeriodDescriptor.Number
		}
	}
	if maxPeriod <= 0 {
		return nil
	}
	scores := make([]model.PeriodScore, maxPeriod)
	for i := range scores {
		scores[i].Period = i + 1
	}
	for _, gl := range g.Goals {
		idx := gl.PeriodDescriptor.Number - 1
		if idx < 0 || idx >= len(scores) {
			continue
		}
		if gl.TeamAbbrev == homeAbbrev {
			scores[idx].Home++
		} else {
			scores[idx].Away++
		}
	}
	return scores
}

// buildResult builds a final game's result. Scores are the finals including
// overtime and the shootout (never stripped); Overtime flags any game past
// regulation; period scores come from the score endpoint's goal detail when
// present. Hockey settles on the final score, so no regulation fields are set
// (ADR-027).
func buildResult(g game, completedAt time.Time) *model.GameResult {
	home, away := teamScore(g.HomeTeam), teamScore(g.AwayTeam)
	return &model.GameResult{
		HomeScore:    home,
		AwayScore:    away,
		TotalScore:   home + away,
		Margin:       home - away,
		Overtime:     overtime(g),
		CompletedAt:  completedAt,
		PeriodScores: periodScores(g),
	}
}

// normalizeGame converts one game (schedule or score endpoint) to a canonical
// game. ok is false for a malformed start time.
func normalizeGame(g game) (model.Game, bool) {
	start, err := time.Parse(time.RFC3339, g.StartTimeUTC)
	if err != nil {
		return model.Game{}, false
	}
	externalID := strconv.Itoa(g.ID)
	out := model.Game{
		ID:             ids.Game(leagueNHL, externalID),
		ExternalID:     externalID,
		League:         model.LeagueNHL,
		HomeTeam:       teamRef(g.HomeTeam),
		AwayTeam:       teamRef(g.AwayTeam),
		ScheduledStart: start,
		Status:         mapStatus(g.GameState, g.GameScheduleState),
		Season:         startingYear(g.Season),
		SeasonType:     mapSeasonType(g.GameType),
	}
	if g.Venue.Default != "" {
		out.Venue = &model.VenueRef{
			ID:   ids.Venue(leagueNHL, g.Venue.Default),
			Name: g.Venue.Default,
		}
	}
	if out.Status != model.GameScheduled {
		h, a := teamScore(g.HomeTeam), teamScore(g.AwayTeam)
		out.HomeScore = &h
		out.AwayScore = &a
	}
	if out.Status == model.GameFinal {
		out.Result = buildResult(g, start)
		out.Result.ID = out.ID
	}
	return out, true
}

// scoreboardUpdates converts one date's score response into league-neutral
// live updates keyed by NHL game id.
func scoreboardUpdates(resp *scoreResponse, now time.Time) map[string]sportsdata.ScoreboardUpdate {
	updates := make(map[string]sportsdata.ScoreboardUpdate, len(resp.Games))
	for _, g := range resp.Games {
		update := sportsdata.ScoreboardUpdate{
			Status:    mapStatus(g.GameState, g.GameScheduleState),
			HomeScore: teamScore(g.HomeTeam),
			AwayScore: teamScore(g.AwayTeam),
		}
		if update.Status == model.GameFinal {
			update.Result = buildResult(g, now)
		}
		updates[strconv.Itoa(g.ID)] = update
	}
	return updates
}

// teamSavePct derives team save percentage from the summary row: shots on
// goal against are the per-game rate times games played, and save percentage
// is one minus goals against over shots against. The summary endpoint carries
// no save-percentage field, so it is computed in-service.
func teamSavePct(row teamSummaryRow) float64 {
	shotsAgainst := row.ShotsAgainstPerGame * float64(row.GamesPlayed)
	if shotsAgainst <= 0 {
		return 0
	}
	return 1 - row.GoalsAgainst/shotsAgainst
}

// normalizeTeamStats builds canonical HockeyStats keyed by canonical team id
// from the team-summary rows, resolving each row's numeric team id to its
// tricode via triByID. Rows whose id is missing from the reference list are
// dropped with a warning.
func normalizeTeamStats(seasonYear int, summary *teamSummaryResponse, triByID map[int]string) map[string]model.TeamStats {
	out := make(map[string]model.TeamStats, len(summary.Data))
	for _, row := range summary.Data {
		abbrev, ok := triByID[row.TeamID]
		if !ok {
			slog.Warn("no tricode for NHL team id; dropping its season stats", "team_id", row.TeamID, "team", row.TeamFullName)
			continue
		}
		hockey := &model.HockeyStats{
			GoalsForPerGame:     row.GoalsForPerGame,
			GoalsAgainstPerGame: row.GoalsAgainstPerGame,
			ShotsForPerGame:     row.ShotsForPerGame,
			ShotsAgainstPerGame: row.ShotsAgainstPerGame,
			PowerPlayPct:        row.PowerPlayPct,
			PenaltyKillPct:      row.PenaltyKillPct,
			TeamSavePct:         teamSavePct(row),
		}
		teamID := ids.Team(leagueNHL, abbrev)
		out[teamID] = model.TeamStats{
			TeamID:           teamID,
			TeamAbbreviation: abbrev,
			Season:           seasonYear,
			GamesPlayed:      row.GamesPlayed,
			Wins:             row.Wins,
			Losses:           row.Losses,
			Stats:            model.StatBlocks{Hockey: hockey},
		}
	}
	return out
}
