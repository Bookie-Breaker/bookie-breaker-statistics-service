package espnbb

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// regulationPeriods is a regulation basketball game's two halves; any period
// beyond it is overtime (a one-overtime final is period 3).
const regulationPeriods = 2

// espnDateLayout parses event dates like "2026-01-24T17:00Z".
const espnDateLayout = "2006-01-02T15:04Z07:00"

// NormalizeTeams builds the canonical team collections from the ESPN team
// list. Basketball teams carry no venue in Phase 6 and no conference/division.
func NormalizeTeams(league model.League, resp *TeamsResponse) ([]model.TeamSummary, map[string]model.TeamDetail) {
	key := string(league)

	var summaries []model.TeamSummary
	details := make(map[string]model.TeamDetail)
	for _, sport := range resp.Sports {
		for _, l := range sport.Leagues {
			for _, entry := range l.Teams {
				t := entry.Team
				summary := model.TeamSummary{
					ID:           ids.Team(key, t.ID),
					League:       league,
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

// MapStatus converts an ESPN status type to the canonical game status.
func MapStatus(t StatusType) model.GameStatus {
	switch t.Name {
	case "STATUS_POSTPONED":
		return model.GamePostponed
	case "STATUS_CANCELED", "STATUS_CANCELLED": //nolint:misspell // ESPN uses the US spelling
		return model.GameCancelled
	case "STATUS_SUSPENDED", "STATUS_DELAYED":
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

// MapSeasonType converts an ESPN event season to the canonical season type: 1
// preseason, 2 regular season, 3 postseason.
func MapSeasonType(s EventSeason) model.SeasonType {
	switch s.Type {
	case 1:
		return model.SeasonPreseason
	case 3:
		return model.SeasonPostseason
	case 4:
		return model.SeasonOffseason
	default: // 2, or 0 when the field is absent
		return model.SeasonRegular
	}
}

// homeAwayCompetitors splits an event's competitors; ok is false when the
// event is malformed.
func homeAwayCompetitors(ev Event) (home, away Competitor, ok bool) {
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

// BuildResult builds a final event's result: scores from the competitor
// totals, half (and overtime) period scores from the linescores present
// directly on the scoreboard, and Overtime when play went past the second
// half. Basketball settles on the final score, so no regulation fields are set
// (ADR-027); college basketball cannot end in a tie.
func BuildResult(ev Event, home, away Competitor, completedAt time.Time) *model.GameResult {
	homeScore := parseScore(home.Score)
	awayScore := parseScore(away.Score)
	result := &model.GameResult{
		HomeScore:   homeScore,
		AwayScore:   awayScore,
		TotalScore:  homeScore + awayScore,
		Margin:      homeScore - awayScore,
		Overtime:    ev.Status.Period > regulationPeriods,
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

// NormalizeEvent converts one scoreboard event to a canonical game. The season
// year comes from the event itself; seasonYear is the fallback for events that
// omit it.
func NormalizeEvent(league model.League, ev Event, seasonYear int) (model.Game, bool) {
	key := string(league)
	home, away, ok := homeAwayCompetitors(ev)
	if !ok {
		return model.Game{}, false
	}
	start, err := time.Parse(espnDateLayout, ev.Date)
	if err != nil {
		return model.Game{}, false
	}

	season := ev.Season.Year
	if season == 0 {
		season = seasonYear
	}
	game := model.Game{
		ID:         ids.Game(key, ev.ID),
		ExternalID: ev.ID,
		League:     league,
		HomeTeam: model.TeamRef{
			ID:           ids.Team(key, home.Team.ID),
			Name:         home.Team.DisplayName,
			Abbreviation: home.Team.Abbreviation,
		},
		AwayTeam: model.TeamRef{
			ID:           ids.Team(key, away.Team.ID),
			Name:         away.Team.DisplayName,
			Abbreviation: away.Team.Abbreviation,
		},
		ScheduledStart: start,
		Status:         MapStatus(ev.Status.Type),
		Season:         season,
		SeasonType:     MapSeasonType(ev.Season),
	}
	if v := ev.Competitions[0].Venue; v != nil && v.FullName != "" {
		game.Venue = &model.VenueRef{
			ID:   ids.Venue(key, v.FullName),
			Name: v.FullName,
		}
	}

	if game.Status != model.GameScheduled {
		h, a := parseScore(home.Score), parseScore(away.Score)
		game.HomeScore = &h
		game.AwayScore = &a
	}
	if game.Status == model.GameFinal {
		game.Result = BuildResult(ev, home, away, start)
		game.Result.ID = game.ID
	}
	return game, true
}

// SeasonGames fetches and normalizes every event in [start, end] by walking
// the window one day at a time (the college-basketball scoreboard has no
// working ranged query).
func SeasonGames(ctx context.Context, c *Client, league model.League, path string, seasonYear int, start, end time.Time) ([]model.Game, []*sportsdata.Fetch, error) {
	var fetches []*sportsdata.Fetch
	var games []model.Game
	seen := make(map[string]bool)
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		resp, fetch, err := c.Scoreboard(ctx, path, day)
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			return nil, fetches, fmt.Errorf("fetch scoreboard %s: %w", day.Format("2006-01-02"), err)
		}
		for _, ev := range resp.Events {
			if seen[ev.ID] {
				continue
			}
			seen[ev.ID] = true
			if game, ok := NormalizeEvent(league, ev, seasonYear); ok {
				games = append(games, game)
			}
		}
	}
	return games, fetches, nil
}

// ScoreboardUpdates converts one date's scoreboard into league-neutral live
// updates keyed by ESPN event id. FINAL results carry half period scores and
// Overtime for play past the second half (ADR-027).
func ScoreboardUpdates(resp *ScoreboardResponse, now time.Time) map[string]sportsdata.ScoreboardUpdate {
	updates := make(map[string]sportsdata.ScoreboardUpdate, len(resp.Events))
	for _, ev := range resp.Events {
		home, away, ok := homeAwayCompetitors(ev)
		if !ok {
			continue
		}
		update := sportsdata.ScoreboardUpdate{
			Status:    MapStatus(ev.Status.Type),
			HomeScore: parseScore(home.Score),
			AwayScore: parseScore(away.Score),
		}
		if update.Status == model.GameFinal {
			update.Result = BuildResult(ev, home, away, now)
		}
		updates[ev.ID] = update
	}
	return updates
}

func parseScore(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
