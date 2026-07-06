package soccer

import (
	"sort"
	"strconv"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// shrinkGames is the K in the strength shrinkage formula: the number of
// average-strength phantom matches blended into every team's rate.
const shrinkGames = 5.0

// formMatches is the recency window for the form_*_last5 fields.
const formMatches = 5

// espnDateLayout parses event dates like "2026-07-01T16:00Z".
const espnDateLayout = "2006-01-02T15:04Z07:00"

// normalizeTeams builds the canonical team collections from the ESPN team
// list. Soccer teams carry no venue (home stadiums are not modeled in
// Phase 6) and no conference/division.
func normalizeTeams(comp Competition, resp *teamsResponse) ([]model.TeamSummary, map[string]model.TeamDetail) {
	league := string(comp.League)

	var summaries []model.TeamSummary
	details := make(map[string]model.TeamDetail)
	for _, sport := range resp.Sports {
		for _, l := range sport.Leagues {
			for _, entry := range l.Teams {
				t := entry.Team
				summary := model.TeamSummary{
					ID:           ids.Team(league, t.ID),
					League:       comp.League,
					Name:         t.DisplayName,
					Abbreviation: t.Abbreviation,
					Location:     t.Location,
					Active:       t.IsActive,
					ExternalIDs:  map[string]string{"espn": t.ID},
				}
				summaries = append(summaries, summary)
				details[summary.ID] = model.TeamDetail{TeamSummary: summary}
			}
		}
	}
	return summaries, details
}

// mapStatus converts an ESPN status type to the canonical game status.
func mapStatus(t espnStatusType) model.GameStatus {
	switch t.Name {
	case "STATUS_POSTPONED":
		return model.GamePostponed
	case "STATUS_CANCELED", "STATUS_CANCELLED": //nolint:misspell // ESPN uses the US spelling
		return model.GameCancelled
	case "STATUS_SUSPENDED", "STATUS_ABANDONED", "STATUS_DELAYED":
		return model.GameSuspended
	}
	switch t.State {
	case "in":
		return model.GameInProgress
	case "post":
		if t.Completed {
			return model.GameFinal
		}
		return model.GamePostponed
	default: // "pre"
		return model.GameScheduled
	}
}

// wentToExtraTime reports whether a completed event needed extra time.
// status.period counts halves; regulation ends at 2 (a shootout is period
// 5, so it also satisfies this).
func wentToExtraTime(ev espnEvent) bool {
	return ev.Status.Period > 2
}

// homeAwayCompetitors splits an event's competitors; ok is false when the
// event is malformed.
func homeAwayCompetitors(ev espnEvent) (home, away espnCompetitor, ok bool) {
	if len(ev.Competitions) == 0 {
		return home, away, false
	}
	var haveHome, haveAway bool
	for _, c := range ev.Competitions[0].Competitors {
		switch c.HomeAway {
		case "home":
			home, haveHome = c, true
		case "away":
			away, haveAway = c, true
		}
	}
	return home, away, haveHome && haveAway
}

// parseLinescores converts a summary competitor's per-period goals to ints.
// On shootout matches ESPN appends the shootout score as a final linescore
// (verified live); it is not a playing period, so it is dropped.
func parseLinescores(c espnCompetitor) []int {
	if len(c.Linescores) == 0 {
		return nil
	}
	lines := make([]int, 0, len(c.Linescores))
	for _, ls := range c.Linescores {
		n, err := strconv.Atoi(ls.DisplayValue)
		if err != nil {
			return nil // a malformed period would corrupt regulation math
		}
		lines = append(lines, n)
	}
	if c.ShootoutScore != nil && len(lines) > 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// buildResult builds a game result from a final event. Home/away scores are
// the full-time score (including extra time, excluding shootout goals —
// standard soccer markets ignore the shootout). homeLines/awayLines are the
// per-period goals from the event summary when fetched (extra-time matches
// only); when the match went to extra time and lines are present, the
// ADR-027 regulation scores are the sum of the first two periods.
func buildResult(home, away espnCompetitor, homeLines, awayLines []int, overtime bool, completedAt time.Time) *model.GameResult {
	homeScore := parseScore(home.Score)
	awayScore := parseScore(away.Score)
	result := &model.GameResult{
		HomeScore:   homeScore,
		AwayScore:   awayScore,
		TotalScore:  homeScore + awayScore,
		Margin:      homeScore - awayScore,
		Overtime:    overtime,
		CompletedAt: completedAt,
	}

	for i := range homeLines {
		a := 0
		if i < len(awayLines) {
			a = awayLines[i]
		}
		result.PeriodScores = append(result.PeriodScores, model.PeriodScore{
			Period: i + 1,
			Home:   homeLines[i],
			Away:   a,
		})
	}

	if overtime && len(homeLines) >= 2 && len(awayLines) >= 2 {
		regHome := homeLines[0] + homeLines[1]
		regAway := awayLines[0] + awayLines[1]
		result.RegulationHomeScore = &regHome
		result.RegulationAwayScore = &regAway
	}
	return result
}

// normalizeEvent converts one scoreboard event to a canonical game.
// homeLines/awayLines carry summary linescores for extra-time finals.
func normalizeEvent(comp Competition, ev espnEvent, seasonYear int, homeLines, awayLines []int) (model.Game, bool) {
	league := string(comp.League)
	home, away, ok := homeAwayCompetitors(ev)
	if !ok {
		return model.Game{}, false
	}

	start, err := time.Parse(espnDateLayout, ev.Date)
	if err != nil {
		return model.Game{}, false
	}

	game := model.Game{
		ID:         ids.Game(league, ev.ID),
		ExternalID: ev.ID,
		League:     comp.League,
		HomeTeam: model.TeamRef{
			ID:           ids.Team(league, home.Team.ID),
			Name:         home.Team.DisplayName,
			Abbreviation: home.Team.Abbreviation,
		},
		AwayTeam: model.TeamRef{
			ID:           ids.Team(league, away.Team.ID),
			Name:         away.Team.DisplayName,
			Abbreviation: away.Team.Abbreviation,
		},
		ScheduledStart: start,
		Status:         mapStatus(ev.Status.Type),
		Season:         seasonYear,
		SeasonType:     comp.SeasonType(ev.Season.Slug),
	}
	if v := ev.Competitions[0].Venue; v != nil && v.FullName != "" {
		game.Venue = &model.VenueRef{
			ID:   ids.Venue(league, v.FullName),
			Name: v.FullName,
		}
	}

	if game.Status != model.GameScheduled {
		h, a := parseScore(home.Score), parseScore(away.Score)
		game.HomeScore = &h
		game.AwayScore = &a
	}
	if game.Status == model.GameFinal {
		game.Result = buildResult(home, away, homeLines, awayLines, wentToExtraTime(ev), start)
		game.Result.ID = game.ID
	}
	return game, true
}

// formRecord aggregates one team's last (up to) formMatches completed games.
type formRecord struct {
	GoalsFor     int
	GoalsAgainst int
	Points       int
	Matches      int
}

// formFromGames computes recent-form records keyed by canonical team id
// from completed games. Goals are full-time (including extra time,
// excluding shootouts); points are 3/1/0 on the full-time score.
func formFromGames(games []model.Game) map[string]formRecord {
	completed := make([]model.Game, 0, len(games))
	for _, g := range games {
		if g.Status == model.GameFinal && g.Result != nil {
			completed = append(completed, g)
		}
	}
	// Most recent first, so each team's first formMatches games are its form.
	sort.Slice(completed, func(i, j int) bool {
		return completed[i].ScheduledStart.After(completed[j].ScheduledStart)
	})

	form := make(map[string]formRecord)
	record := func(teamID string, goalsFor, goalsAgainst int) {
		r := form[teamID]
		if r.Matches >= formMatches {
			return
		}
		r.Matches++
		r.GoalsFor += goalsFor
		r.GoalsAgainst += goalsAgainst
		switch {
		case goalsFor > goalsAgainst:
			r.Points += 3
		case goalsFor == goalsAgainst:
			r.Points++
		}
		form[teamID] = r
	}
	for _, g := range completed {
		record(g.HomeTeam.ID, g.Result.HomeScore, g.Result.AwayScore)
		record(g.AwayTeam.ID, g.Result.AwayScore, g.Result.HomeScore)
	}
	return form
}

// normalizeTeamStats converts a standings response into canonical team
// stats keyed by canonical team id.
//
// Attack/defense strengths are multiplicative factors versus the
// competition average with small-sample shrinkage toward 1.0:
//
//	raw_attack  = team_goals_for_per_match     / competition_avg_goals_per_match
//	raw_defense = team_goals_against_per_match / competition_avg_goals_per_match
//	strength    = (matches_played*raw + K) / (matches_played + K),  K = shrinkGames
//
// The competition average is computed across this standings response (all
// groups for the World Cup). form carries recent-form records from the
// provider's schedule/scoreboard results; missing entries yield zero form.
func normalizeTeamStats(comp Competition, seasonYear int, resp *standingsResponse, form map[string]formRecord) map[string]model.TeamStats {
	league := string(comp.League)

	entries := make([]standingsEntry, 0, len(resp.Children))
	var totalGoals, totalGames float64
	for _, child := range resp.Children {
		for _, e := range child.Standings.Entries {
			entries = append(entries, e)
			totalGoals += statValue(e, "pointsFor")
			totalGames += statValue(e, "gamesPlayed")
		}
	}
	avgGoalsPerMatch := 0.0
	if totalGames > 0 {
		avgGoalsPerMatch = totalGoals / totalGames
	}

	out := make(map[string]model.TeamStats, len(entries))
	for _, e := range entries {
		games := statValue(e, "gamesPlayed")
		goalsFor := statValue(e, "pointsFor")
		goalsAgainst := statValue(e, "pointsAgainst")

		var gfPerMatch, gaPerMatch float64
		attack, defense := 1.0, 1.0
		if games > 0 {
			gfPerMatch = goalsFor / games
			gaPerMatch = goalsAgainst / games
			if avgGoalsPerMatch > 0 {
				attack = shrink(gfPerMatch/avgGoalsPerMatch, games)
				defense = shrink(gaPerMatch/avgGoalsPerMatch, games)
			}
		}

		teamID := ids.Team(league, e.Team.ID)
		soccer := &model.SoccerStats{
			GoalsForPerMatch:     gfPerMatch,
			GoalsAgainstPerMatch: gaPerMatch,
			AttackStrength:       attack,
			DefenseStrength:      defense,
			Draws:                int(statValue(e, "ties")),
		}
		if f, ok := form[teamID]; ok && f.Matches > 0 {
			soccer.FormGoalsForLast5 = float64(f.GoalsFor) / float64(f.Matches)
			soccer.FormGoalsAgainstLast5 = float64(f.GoalsAgainst) / float64(f.Matches)
			soccer.FormPointsLast5 = f.Points
		}

		out[teamID] = model.TeamStats{
			TeamID:           teamID,
			TeamAbbreviation: e.Team.Abbreviation,
			Season:           seasonYear,
			GamesPlayed:      int(games),
			Wins:             int(statValue(e, "wins")),
			Losses:           int(statValue(e, "losses")),
			Draws:            int(statValue(e, "ties")),
			Stats:            model.StatBlocks{Soccer: soccer},
		}
	}
	return out
}

// shrink damps a raw strength toward 1.0 by blending in shrinkGames
// phantom average matches.
func shrink(raw, matches float64) float64 {
	return (matches*raw + shrinkGames) / (matches + shrinkGames)
}

func statValue(e standingsEntry, name string) float64 {
	for _, s := range e.Stats {
		if s.Name == name {
			return s.Value
		}
	}
	return 0
}

func parseScore(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
