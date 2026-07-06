package cfbd

import (
	"log/slog"
	"strings"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/espnfb"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// leagueNCAAFB is the league key for canonical id derivation.
const leagueNCAAFB = string(model.LeagueNCAAFB)

// leagueAvgDrivesPerGame is the league-average offensive drives per team per
// game. CFBD season stats carry no drive columns, so college drive metrics are
// the same documented approximation the NFL adapter uses: drives_per_game is
// this constant and points per drive is points per game divided by it.
const leagueAvgDrivesPerGame = 10.9

// CFBD /stats/season statName values carrying season scoring. These are the
// DERIVED assumption behind points_for/against (the OpenAPI document types
// statValue generically); validate the exact statName spelling against the
// live API in the September verification session (ADR-026).
const (
	statPointsFor     = "pointsFor"
	statPointsAgainst = "pointsAgainst"
)

// espnRef is an ESPN team's id and abbreviation, indexed by school name so
// CFBD's team names (school names, e.g. "North Texas") resolve to the same
// canonical ids the ESPN teams/schedule paths produce.
type espnRef struct {
	ID     string
	Abbrev string
}

// espnTeamIndex maps normalized school names to ESPN team refs. ESPN's team
// "location" is the school name CFBD uses; the full display name is added as a
// fallback key.
func espnTeamIndex(resp *espnfb.TeamsResponse) map[string]espnRef {
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

// teamPoints accumulates a team's scoring for/against from /stats/season.
type teamPoints struct {
	scored  float64
	allowed float64
}

// normalizeTeamStats builds canonical NCAA_FB team stats keyed by canonical
// team id from CFBD records (W/L, games), SP+ ratings, and season scoring,
// mapping CFBD team names to ESPN ids via espnIDByName. SP+ and points degrade
// independently: a team missing from the SP+ or scoring feeds keeps those
// fields zero while its record still produces a stats row.
func normalizeTeamStats(seasonYear int, records []TeamRecords, sp []TeamSP, stats []TeamStat, espnByName map[string]espnRef) map[string]model.TeamStats {
	spByTeam := make(map[string]float64, len(sp))
	for _, r := range sp {
		spByTeam[normalizeName(r.Team)] = r.Rating
	}

	pointsByTeam := make(map[string]*teamPoints)
	for _, s := range stats {
		key := normalizeName(s.Team)
		p := pointsByTeam[key]
		if p == nil {
			p = &teamPoints{}
			pointsByTeam[key] = p
		}
		switch s.StatName {
		case statPointsFor:
			p.scored = float64(s.StatVal)
		case statPointsAgainst:
			p.allowed = float64(s.StatVal)
		}
	}

	out := make(map[string]model.TeamStats, len(records))
	for _, rec := range records {
		key := normalizeName(rec.Team)
		ref, ok := espnByName[key]
		if !ok {
			slog.Warn("no ESPN team id for CFBD team; dropping its season stats", "team", rec.Team)
			continue
		}

		football := &model.FootballStats{SPPlusRating: spByTeam[key]}
		games := rec.Total.Games
		if p := pointsByTeam[key]; p != nil && games > 0 {
			football.PointsPerGame = p.scored / float64(games)
			football.PointsAllowedPerGame = p.allowed / float64(games)
			football.DrivesPerGame = leagueAvgDrivesPerGame
			football.PointsPerDriveOff = football.PointsPerGame / leagueAvgDrivesPerGame
			football.PointsPerDriveDef = football.PointsAllowedPerGame / leagueAvgDrivesPerGame
		}
		// EPA fields stay 0: CFBD season stats expose no EPA (documented
		// college absence). Turnover margin stays 0 (not sourced for NCAA_FB
		// in Phase 6; ADR-026).

		teamID := ids.Team(leagueNCAAFB, ref.ID)
		out[teamID] = model.TeamStats{
			TeamID:           teamID,
			TeamAbbreviation: ref.Abbrev,
			Season:           seasonYear,
			GamesPlayed:      games,
			Wins:             rec.Total.Wins,
			Losses:           rec.Total.Losses,
			Stats:            model.StatBlocks{Football: football},
		}
	}
	return out
}
