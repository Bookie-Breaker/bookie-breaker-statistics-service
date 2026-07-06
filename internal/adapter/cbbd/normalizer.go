package cbbd

import (
	"log/slog"
	"strings"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnbb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// leagueNCAABB is the league key for canonical id derivation.
const leagueNCAABB = string(model.LeagueNCAABB)

// espnRef is an ESPN team's id and abbreviation, indexed by school name so
// CBBD's team names resolve to the same canonical ids the ESPN teams/schedule
// paths produce.
type espnRef struct {
	ID     string
	Abbrev string
}

// espnTeamIndex maps normalized school names to ESPN team refs. ESPN's team
// "location" is the school name CBBD uses; the full display name is added as a
// fallback key.
func espnTeamIndex(resp *espnbb.TeamsResponse) map[string]espnRef {
	idx := make(map[string]espnRef)
	for _, sport := range resp.Sports {
		for _, l := range sport.Leagues {
			for _, entry := range l.Teams {
				t := entry.Team
				ref := espnRef{ID: t.ID, Abbrev: t.Abbreviation}
				if t.Location != "" {
					idx[normalizeName(t.Location)] = ref
				}
				if _, ok := idx[normalizeName(t.DisplayName)]; !ok {
					idx[normalizeName(t.DisplayName)] = ref
				}
			}
		}
	}
	return idx
}

func normalizeName(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// perGame divides a season total by games, guarding against a zero-game team.
func perGame(total float64, games int) float64 {
	if games <= 0 {
		return 0
	}
	return total / float64(games)
}

// normalizeTeamStats builds canonical NCAA_BB team stats keyed by canonical
// team id from CBBD season stats and adjusted-efficiency ratings, mapping CBBD
// team names to ESPN ids via espnByName. Adjusted ratings degrade
// independently: a team missing from the adjusted feed keeps
// adjusted_efficiency_margin zero while its other blocks still populate.
func normalizeTeamStats(seasonYear int, seasonStats []TeamSeasonStats, adjusted []AdjustedEfficiencyInfo, espnByName map[string]espnRef) map[string]model.TeamStats {
	adjByTeam := make(map[string]float64, len(adjusted))
	for _, a := range adjusted {
		adjByTeam[normalizeName(a.Team)] = a.NetRating
	}

	out := make(map[string]model.TeamStats, len(seasonStats))
	for _, s := range seasonStats {
		key := normalizeName(s.Team)
		ref, ok := espnByName[key]
		if !ok {
			slog.Warn("no ESPN team id for CBBD team; dropping its season stats", "team", s.Team)
			continue
		}

		team := s.TeamStats
		opp := s.OpponentStats
		games := s.Games

		offensive := &model.OffensiveStats{
			PointsPerGame:    perGame(team.Points.Total, games),
			FieldGoalPct:     team.FieldGoals.Pct,
			ThreePointPct:    team.ThreePointFieldGoals.Pct,
			FreeThrowPct:     team.FreeThrows.Pct,
			ReboundsPerGame:  perGame(team.Rebounds.Total, games),
			AssistsPerGame:   perGame(team.Assists, games),
			TurnoversPerGame: perGame(team.Turnovers.Total, games),
			OffensiveRating:  team.Rating,
			Pace:             s.Pace,
			EffectiveFGPct:   team.FourFactors.EffectiveFieldGoalPct,
		}
		defensive := &model.DefensiveStats{
			PointsAllowedPerGame:  perGame(opp.Points.Total, games),
			OpponentFGPct:         opp.FieldGoals.Pct,
			OpponentThreePointPct: opp.ThreePointFieldGoals.Pct,
			StealsPerGame:         perGame(team.Steals, games),
			BlocksPerGame:         perGame(team.Blocks, games),
			DefensiveRating:       opp.Rating,
		}
		advanced := &model.AdvancedStats{
			NetRating:                team.Rating - opp.Rating,
			TrueShootingPct:          team.TrueShooting,
			TurnoverPct:              team.FourFactors.TurnoverRatio,
			OffensiveReboundPct:      team.FourFactors.OffensiveReboundPct,
			AdjustedEfficiencyMargin: adjByTeam[key],
		}

		teamID := ids.Team(leagueNCAABB, ref.ID)
		out[teamID] = model.TeamStats{
			TeamID:           teamID,
			TeamAbbreviation: ref.Abbrev,
			Season:           seasonYear,
			GamesPlayed:      games,
			Wins:             int(s.Wins),
			Losses:           int(s.Losses),
			Stats: model.StatBlocks{
				Offensive: offensive,
				Defensive: defensive,
				Advanced:  advanced,
			},
		}
	}
	return out
}
