package ncaabsb

import (
	"strconv"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// leagueNCAABSB is the league key for canonical id derivation.
const leagueNCAABSB = string(model.LeagueNCAABSB)

// regulationInnings is a regulation college baseball game. Doubleheader
// games are 7 innings by rule but are not distinguished here (documented
// descope; only extra innings — period > 9 — set Overtime).
const regulationInnings = 9

// espnDateLayout parses event dates like "2026-06-21T18:30Z".
const espnDateLayout = "2006-01-02T15:04Z07:00"

// normalizeTeams builds the canonical team collections from the ESPN team
// list. College teams carry no venue (home fields are not modeled) and no
// conference (ESPN's flat team list omits it).
func normalizeTeams(resp *teamsResponse) ([]model.TeamSummary, map[string]model.TeamDetail) {
	var summaries []model.TeamSummary
	details := make(map[string]model.TeamDetail)
	for _, sport := range resp.Sports {
		for _, l := range sport.Leagues {
			for _, entry := range l.Teams {
				t := entry.Team
				summary := model.TeamSummary{
					ID:           ids.Team(leagueNCAABSB, t.ID),
					League:       model.LeagueNCAABSB,
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
func mapStatus(name, state string, completed bool) model.GameStatus {
	switch name {
	case "STATUS_POSTPONED":
		return model.GamePostponed
	case "STATUS_CANCELED", "STATUS_CANCELLED": //nolint:misspell // ESPN uses the US spelling
		return model.GameCancelled
	case "STATUS_SUSPENDED", "STATUS_DELAYED":
		return model.GameSuspended
	}
	switch state {
	case "in":
		return model.GameInProgress
	case "post":
		if completed {
			return model.GameFinal
		}
		return model.GamePostponed
	default: // "pre"
		return model.GameScheduled
	}
}

// mapSeasonType converts an ESPN event season slug to the canonical season
// type: the regular season maps to REGULAR and every tournament round
// (e.g. "championship-series" for the College World Series) to POSTSEASON.
func mapSeasonType(slug string) model.SeasonType {
	switch slug {
	case "", "regular-season":
		return model.SeasonRegular
	case "preseason":
		return model.SeasonPreseason
	default:
		return model.SeasonPostseason
	}
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

// buildResult builds a final event's result: scores from the competitor
// totals, period scores from the per-inning linescores (present directly on
// the scoreboard, verified live), and Overtime for extra innings. A home
// team ahead after the top of the ninth does not bat, so its linescore list
// can be one entry shorter than the away list (observed in real 2026-season
// data); the longer list drives the walk and the missing half-inning records
// 0. Baseball settles on the final score, so no regulation fields are set
// (ADR-027).
func buildResult(ev espnEvent, home, away espnCompetitor, completedAt time.Time) *model.GameResult {
	homeScore := parseScore(home.Score)
	awayScore := parseScore(away.Score)
	result := &model.GameResult{
		HomeScore:   homeScore,
		AwayScore:   awayScore,
		TotalScore:  homeScore + awayScore,
		Margin:      homeScore - awayScore,
		Overtime:    ev.Status.Period > regulationInnings,
		CompletedAt: completedAt,
	}
	periods := max(len(home.Linescores), len(away.Linescores))
	for i := range periods {
		ps := model.PeriodScore{Period: i + 1}
		if i < len(home.Linescores) {
			ps.Home = int(home.Linescores[i].Value)
		}
		if i < len(away.Linescores) {
			ps.Away = int(away.Linescores[i].Value)
		}
		result.PeriodScores = append(result.PeriodScores, ps)
	}
	return result
}

// normalizeEvent converts one scoreboard event to a canonical game.
func normalizeEvent(ev espnEvent, seasonYear int) (model.Game, bool) {
	home, away, ok := homeAwayCompetitors(ev)
	if !ok {
		return model.Game{}, false
	}
	start, err := time.Parse(espnDateLayout, ev.Date)
	if err != nil {
		return model.Game{}, false
	}

	game := model.Game{
		ID:         ids.Game(leagueNCAABSB, ev.ID),
		ExternalID: ev.ID,
		League:     model.LeagueNCAABSB,
		HomeTeam: model.TeamRef{
			ID:           ids.Team(leagueNCAABSB, home.Team.ID),
			Name:         home.Team.DisplayName,
			Abbreviation: home.Team.Abbreviation,
		},
		AwayTeam: model.TeamRef{
			ID:           ids.Team(leagueNCAABSB, away.Team.ID),
			Name:         away.Team.DisplayName,
			Abbreviation: away.Team.Abbreviation,
		},
		ScheduledStart: start,
		Status:         mapStatus(ev.Status.Type.Name, ev.Status.Type.State, ev.Status.Type.Completed),
		Season:         seasonYear,
		SeasonType:     mapSeasonType(ev.Season.Slug),
	}
	if v := ev.Competitions[0].Venue; v != nil && v.FullName != "" {
		game.Venue = &model.VenueRef{
			ID:   ids.Venue(leagueNCAABSB, v.FullName),
			Name: v.FullName,
		}
	}

	if game.Status != model.GameScheduled {
		h, a := parseScore(home.Score), parseScore(away.Score)
		game.HomeScore = &h
		game.AwayScore = &a
	}
	if game.Status == model.GameFinal {
		game.Result = buildResult(ev, home, away, start)
		game.Result.ID = game.ID
	}
	return game, true
}

// normalizeTeamStats converts a standings response into canonical team
// stats keyed by canonical team id. Only what ESPN standings source is
// populated: W/L records, games played, and runs scored/allowed per game;
// the remaining baseball-block fields stay zero (documented — the league is
// dormant and ESPN carries no college wOBA/FIP inputs).
func normalizeTeamStats(seasonYear int, resp *standingsResponse) map[string]model.TeamStats {
	out := make(map[string]model.TeamStats)
	for _, child := range resp.Children {
		for _, e := range child.Standings.Entries {
			games := statValue(e, "gamesPlayed")
			baseball := &model.BaseballStats{}
			if games > 0 {
				baseball.RunsScoredPerGame = statValue(e, "pointsFor") / games
				baseball.RunsAllowedPerGame = statValue(e, "pointsAgainst") / games
			}
			teamID := ids.Team(leagueNCAABSB, e.Team.ID)
			out[teamID] = model.TeamStats{
				TeamID:           teamID,
				TeamAbbreviation: e.Team.Abbreviation,
				Season:           seasonYear,
				GamesPlayed:      int(games),
				Wins:             int(statValue(e, "wins")),
				Losses:           int(statValue(e, "losses")),
				Stats:            model.StatBlocks{Baseball: baseball},
			}
		}
	}
	return out
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
