package nfl

import (
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnfb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// leagueNFL is the league key for canonical id derivation.
const leagueNFL = string(model.LeagueNFL)

// leagueAvgDrivesPerGame is the league-average offensive drives per team per
// game. The nflverse stats_team files carry no drive columns (verified
// against the real 2025 file), so drive metrics are a documented
// approximation: drives_per_game is this constant and points per drive is
// points per game divided by it.
const leagueAvgDrivesPerGame = 10.9

// nflverseAliases maps ESPN team abbreviations to their nflverse team codes
// where the two differ (verified against the real 2025 files: every other
// abbreviation matches).
var nflverseAliases = map[string]string{
	"LAR": "LA",
	"WSH": "WAS",
}

// nflverseCode resolves the nflverse team code for an ESPN abbreviation.
func nflverseCode(espnAbbrev string) string {
	if code, ok := nflverseAliases[espnAbbrev]; ok {
		return code
	}
	return espnAbbrev
}

// teamAggregate accumulates one team's nflverse regular-season totals.
type teamAggregate struct {
	Games int

	OffEPA   float64
	OffPlays float64
	DefEPA   float64
	DefPlays float64

	Giveaways float64
	Takeaways float64
}

// aggregateTeamWeeks folds team-week rows into per-team regular-season
// aggregates keyed by nflverse team code. Offensive EPA and plays come from
// a team's own rows; defensive EPA and plays are the offensive numbers of
// every row played against the team (the week file's opponent_team column
// is what makes this derivable — the season-aggregate files have no
// defensive EPA). Postseason rows are excluded so the aggregates match the
// regular-season standings the points metrics come from.
func aggregateTeamWeeks(rows []teamWeekRow) map[string]*teamAggregate {
	aggregates := make(map[string]*teamAggregate)
	get := func(code string) *teamAggregate {
		if aggregates[code] == nil {
			aggregates[code] = &teamAggregate{}
		}
		return aggregates[code]
	}
	for _, row := range rows {
		if row.SeasonType != "REG" || row.Team == "" || row.Opponent == "" {
			continue
		}
		epa := row.PassingEPA + row.RushingEPA
		plays := row.Attempts + row.SacksSuffered + row.Carries

		team := get(row.Team)
		team.Games++
		team.OffEPA += epa
		team.OffPlays += plays
		// Giveaways: interceptions thrown plus offensive fumbles lost.
		// Special-teams fumbles have no lost-fumble column in the file, so
		// the margin is offense/defense only (documented approximation).
		team.Giveaways += row.PassingInterceptions + row.SackFumblesLost + row.RushingFumblesLost + row.ReceivingFumblesLost
		team.Takeaways += row.DefInterceptions + row.FumbleRecoveryOpp

		opponent := get(row.Opponent)
		opponent.DefEPA += epa
		opponent.DefPlays += plays
	}
	return aggregates
}

// normalizeTeamStats builds canonical team stats keyed by canonical team id
// from the ESPN standings (points, W/L/T) and nflverse aggregates (EPA,
// turnover margin). aggregates may be nil when nflverse is unavailable; the
// EPA and turnover fields then stay zero (documented degradation — the
// points-based fields still populate).
func normalizeTeamStats(seasonYear int, records []espnfb.Record, aggregates map[string]*teamAggregate) map[string]model.TeamStats {
	out := make(map[string]model.TeamStats, len(records))
	for _, rec := range records {
		games := float64(rec.Games())
		football := &model.FootballStats{}
		if games > 0 {
			football.PointsPerGame = rec.PointsFor / games
			football.PointsAllowedPerGame = rec.PointsAgainst / games
			football.DrivesPerGame = leagueAvgDrivesPerGame
			football.PointsPerDriveOff = football.PointsPerGame / leagueAvgDrivesPerGame
			football.PointsPerDriveDef = football.PointsAllowedPerGame / leagueAvgDrivesPerGame
		}
		if agg := aggregates[nflverseCode(rec.Team.Abbreviation)]; agg != nil {
			if agg.OffPlays > 0 {
				football.EPAPerPlayOff = agg.OffEPA / agg.OffPlays
			}
			if agg.DefPlays > 0 {
				football.EPAPerPlayDef = agg.DefEPA / agg.DefPlays
			}
			if agg.Games > 0 {
				football.TurnoverMarginPerGame = (agg.Takeaways - agg.Giveaways) / float64(agg.Games)
			}
		}
		// sp_plus_rating stays 0 for the NFL (SP+ is a college rating;
		// ADR-026).

		teamID := ids.Team(leagueNFL, rec.Team.ID)
		out[teamID] = model.TeamStats{
			TeamID:           teamID,
			TeamAbbreviation: rec.Team.Abbreviation,
			Season:           seasonYear,
			GamesPlayed:      rec.Games(),
			Wins:             rec.Wins,
			Losses:           rec.Losses,
			Stats:            model.StatBlocks{Football: football},
		}
	}
	return out
}
