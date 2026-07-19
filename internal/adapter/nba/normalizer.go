package nba

import (
	"strconv"
	"strings"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/derivedstats"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

const leagueNBA = string(model.LeagueNBA)

// NormalizeTeams builds the canonical team list from the static seed.
func NormalizeTeams() []model.TeamSummary {
	teams := make([]model.TeamSummary, 0, len(StaticTeams))
	for _, t := range StaticTeams {
		teams = append(teams, model.TeamSummary{
			ID:           ids.Team(leagueNBA, t.NBAID),
			League:       model.LeagueNBA,
			Name:         t.FullName,
			Abbreviation: t.Tricode,
			Location:     t.City,
			Mascot:       t.Mascot,
			Conference:   t.Conference,
			Division:     t.Division,
			VenueID:      ids.Venue(leagueNBA, t.Arena),
			Active:       true,
			ExternalIDs:  map[string]string{"nba": t.NBAID},
		})
	}
	return teams
}

// NormalizeTeamStats converts merged league dash rows into canonical
// TeamStats. Upstream Advanced values win; formulas fill gaps.
func NormalizeTeamStats(stats map[string]*TeamSeasonStats, splits map[string]*TeamSplits, seasonYear int) map[string]model.TeamStats {
	out := make(map[string]model.TeamStats, len(stats))
	for nbaID, t := range stats {
		in := derivedstats.Inputs{
			MIN: t.MIN, PTS: t.PTS, FGM: t.FGM, FGA: t.FGA, FG3M: t.FG3M,
			FTA: t.FTA, OREB: t.OREB, DREB: t.DREB, TOV: t.TOV,
			OppPTS: t.OppPTS, OppFGA: t.OppFGA, OppFTA: t.OppFTA,
			OppOREB: t.OppOREB, OppDREB: t.OppDREB, OppTOV: t.OppTOV,
		}

		offRating, defRating := t.OffRating, t.DefRating
		pace, tsPct, efgPct := t.Pace, t.TSPct, t.EFGPct
		tovPct, orebPct, netRating := t.TOVPct, t.OREBPct, t.NetRating
		if !t.HasAdvanced {
			offRating = derivedstats.OffensiveRating(in)
			defRating = derivedstats.DefensiveRating(in)
			pace = derivedstats.Pace(in)
			tsPct = derivedstats.TrueShootingPct(in)
			efgPct = derivedstats.EffectiveFGPct(in)
			tovPct = derivedstats.TurnoverPct(in)
			orebPct = derivedstats.OffensiveReboundPct(in)
			netRating = offRating - defRating
		}

		abbrev := ""
		if static, ok := StaticTeamByNBAID[nbaID]; ok {
			abbrev = static.Tricode
		}

		ts := model.TeamStats{
			TeamID:           ids.Team(leagueNBA, nbaID),
			TeamAbbreviation: abbrev,
			Season:           seasonYear,
			GamesPlayed:      t.GP,
			Wins:             t.W,
			Losses:           t.L,
			Stats: model.StatBlocks{
				Offensive: &model.OffensiveStats{
					PointsPerGame:    t.PTS,
					FieldGoalPct:     t.FGPct,
					ThreePointPct:    t.FG3Pct,
					FreeThrowPct:     t.FTPct,
					ReboundsPerGame:  t.REB,
					AssistsPerGame:   t.AST,
					TurnoversPerGame: t.TOV,
					OffensiveRating:  offRating,
					Pace:             pace,
					EffectiveFGPct:   efgPct,
				},
				Defensive: &model.DefensiveStats{
					PointsAllowedPerGame:  t.OppPTS,
					OpponentFGPct:         t.OppFGPct,
					OpponentThreePointPct: t.OppFG3Pct,
					StealsPerGame:         t.STL,
					BlocksPerGame:         t.BLK,
					DefensiveRating:       defRating,
				},
				Advanced: &model.AdvancedStats{
					NetRating:           netRating,
					TrueShootingPct:     tsPct,
					TurnoverPct:         tovPct,
					OffensiveReboundPct: orebPct,
				},
			},
		}

		if s, ok := splits[nbaID]; ok {
			ts.HomeAwaySplits = &model.HomeAwaySplits{
				Home: &model.SplitRecord{
					Wins: s.Home.W, Losses: s.Home.L,
					PointsPerGame: s.Home.PTS, PointsAllowedPerGame: s.Home.OppPTS,
				},
				Away: &model.SplitRecord{
					Wins: s.Road.W, Losses: s.Road.L,
					PointsPerGame: s.Road.PTS, PointsAllowedPerGame: s.Road.OppPTS,
				},
			}
		}

		out[nbaID] = ts
	}
	return out
}

// NormalizePlayers merges roster metadata with league-wide season stats.
// rosterByTeam maps NBA team id to its roster entries.
func NormalizePlayers(rosterByTeam map[string][]RosterEntry, seasons []PlayerSeason, seasonYear int) ([]model.PlayerSummary, map[string]model.PlayerDetail) {
	seasonByPlayer := make(map[string]PlayerSeason, len(seasons))
	for _, s := range seasons {
		seasonByPlayer[s.NBAPlayerID] = s
	}

	var summaries []model.PlayerSummary
	details := make(map[string]model.PlayerDetail)

	for nbaTeamID, roster := range rosterByTeam {
		abbrev := ""
		if static, ok := StaticTeamByNBAID[nbaTeamID]; ok {
			abbrev = static.Tricode
		}
		for _, entry := range roster {
			first, last := splitName(entry.Name)
			summary := model.PlayerSummary{
				ID:               ids.Player(leagueNBA, entry.NBAPlayerID),
				TeamID:           ids.Team(leagueNBA, nbaTeamID),
				TeamAbbreviation: abbrev,
				FirstName:        first,
				LastName:         last,
				Position:         entry.Position,
				Status:           model.PlayerActive,
				ExternalIDs:      map[string]string{"nba": entry.NBAPlayerID},
				League:           model.LeagueNBA,
			}
			if n, err := strconv.Atoi(entry.Jersey); err == nil {
				summary.JerseyNumber = &n
			}

			detail := model.PlayerDetail{PlayerSummary: summary}
			if entry.Experience == "R" {
				zero := 0
				detail.ExperienceYears = &zero
			} else if years, err := strconv.Atoi(entry.Experience); err == nil {
				detail.ExperienceYears = &years
			}

			if s, ok := seasonByPlayer[entry.NBAPlayerID]; ok {
				detail.SeasonStats = &model.PlayerSeasonStats{
					Season:          seasonYear,
					GamesPlayed:     s.GP,
					MinutesPerGame:  s.MIN,
					PointsPerGame:   s.PTS,
					ReboundsPerGame: s.REB,
					AssistsPerGame:  s.AST,
					StealsPerGame:   s.STL,
					BlocksPerGame:   s.BLK,
					FieldGoalPct:    s.FGPct,
					ThreePointPct:   s.FG3Pct,
					FreeThrowPct:    s.FTPct,
				}
			}

			summaries = append(summaries, summary)
			details[summary.ID] = detail
		}
	}

	return summaries, details
}

// NormalizeSchedule converts scheduleleaguev2 games to canonical games.
func NormalizeSchedule(games []ScheduledGame, seasonYear int) []model.Game {
	out := make([]model.Game, 0, len(games))
	for _, g := range games {
		game := model.Game{
			ID:         ids.Game(leagueNBA, g.NBAGameID),
			ExternalID: g.NBAGameID,
			League:     model.LeagueNBA,
			HomeTeam: model.TeamRef{
				ID:           ids.Team(leagueNBA, g.HomeNBAID),
				Name:         g.HomeName,
				Abbreviation: g.HomeTricode,
			},
			AwayTeam: model.TeamRef{
				ID:           ids.Team(leagueNBA, g.AwayNBAID),
				Name:         g.AwayName,
				Abbreviation: g.AwayTricode,
			},
			ScheduledStart: g.StartUTC,
			Status:         statusFromCode(g.Status),
			Season:         seasonYear,
			SeasonType:     seasonTypeFromLabel(g.GameLabel),
		}
		if g.Arena != "" {
			game.Venue = &model.VenueRef{
				ID:   ids.Venue(leagueNBA, g.Arena),
				Name: g.Arena,
			}
		}
		if g.Status != statusScheduled {
			home, away := g.HomeScore, g.AwayScore
			game.HomeScore = &home
			game.AwayScore = &away
		}
		if g.Status == statusFinal {
			game.Result = buildResult(game.ID, g.HomeScore, g.AwayScore, nil, nil, g.StartUTC)
		}
		out = append(out, game)
	}
	return out
}

// ApplyScoreboard folds live scoreboard data into a canonical game.
func ApplyScoreboard(game model.Game, entry ScoreboardEntry) model.Game {
	game.Status = statusFromCode(entry.Status)
	if entry.Status != statusScheduled {
		home, away := entry.HomeScore, entry.AwayScore
		game.HomeScore = &home
		game.AwayScore = &away
	}
	if entry.Status == statusFinal {
		game.Result = buildResult(game.ID, entry.HomeScore, entry.AwayScore, entry.HomePeriods, entry.AwayPeriods, time.Now().UTC())
	}
	return game
}

func buildResult(gameID string, homeScore, awayScore int, homePeriods, awayPeriods []int, completedAt time.Time) *model.GameResult {
	result := &model.GameResult{
		ID:          gameID,
		HomeScore:   homeScore,
		AwayScore:   awayScore,
		TotalScore:  homeScore + awayScore,
		Margin:      homeScore - awayScore,
		Overtime:    len(homePeriods) > 4,
		CompletedAt: completedAt,
	}
	for i := range homePeriods {
		away := 0
		if i < len(awayPeriods) {
			away = awayPeriods[i]
		}
		result.PeriodScores = append(result.PeriodScores, model.PeriodScore{
			Period: i + 1,
			Home:   homePeriods[i],
			Away:   away,
		})
	}
	return result
}

// NormalizeBoxScore converts boxscoretraditionalv2 result sets into the
// canonical box score. Teams are emitted in response order with canonical
// ids; the query layer orients home/away against the canonical game (the
// endpoint carries no home/away marker) and owns game_id/sport/status.
func NormalizeBoxScore(players []BoxScorePlayerLine, teams []BoxScoreTeamLine) *model.BoxScore {
	if len(teams) == 0 {
		return nil
	}

	playersByTeam := make(map[string][]model.BasketballPlayerBoxScore)
	for _, p := range players {
		playersByTeam[p.NBATeamID] = append(playersByTeam[p.NBATeamID], model.BasketballPlayerBoxScore{
			PlayerID:               ids.Player(leagueNBA, p.NBAPlayerID),
			PlayerName:             p.Name,
			Position:               p.Position,
			Minutes:                parseBoxMinutes(p.MIN),
			Points:                 p.PTS,
			Rebounds:               p.REB,
			Assists:                p.AST,
			Steals:                 p.STL,
			Blocks:                 p.BLK,
			Turnovers:              p.TOV,
			FieldGoalsMade:         p.FGM,
			FieldGoalsAttempted:    p.FGA,
			ThreePointersMade:      p.FG3M,
			ThreePointersAttempted: p.FG3A,
			FreeThrowsMade:         p.FTM,
			FreeThrowsAttempted:    p.FTA,
			PlusMinus:              p.PlusMinus,
		})
	}

	teamBox := func(t BoxScoreTeamLine) model.TeamBoxScore {
		return model.TeamBoxScore{
			ID:           ids.Team(leagueNBA, t.NBATeamID),
			Abbreviation: t.TeamAbbrev,
			Score:        t.PTS,
			TeamStats: &model.BasketballTeamStats{
				FieldGoalsMade:         t.FGM,
				FieldGoalsAttempted:    t.FGA,
				ThreePointersMade:      t.FG3M,
				ThreePointersAttempted: t.FG3A,
				FreeThrowsMade:         t.FTM,
				FreeThrowsAttempted:    t.FTA,
				Rebounds:               t.REB,
				OffensiveRebounds:      t.OREB,
				DefensiveRebounds:      t.DREB,
				Assists:                t.AST,
				Steals:                 t.STL,
				Blocks:                 t.BLK,
				Turnovers:              t.TOV,
			},
			Players: playersByTeam[t.NBATeamID],
		}
	}

	box := &model.BoxScore{
		Sport:    string(model.SportBasketball),
		HomeTeam: teamBox(teams[0]),
	}
	if len(teams) > 1 {
		box.AwayTeam = teamBox(teams[1])
	}
	return box
}

// parseBoxMinutes converts a box-score MIN value ("34:12", occasionally
// "34.000000:12", "" for DNPs) to decimal minutes.
func parseBoxMinutes(raw string) float64 {
	if raw == "" {
		return 0
	}
	parts := strings.SplitN(raw, ":", 2)
	minutes, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0
	}
	if len(parts) == 2 {
		if seconds, err := strconv.ParseFloat(parts[1], 64); err == nil {
			minutes += seconds / 60
		}
	}
	return minutes
}

func statusFromCode(code int) model.GameStatus {
	switch code {
	case statusLive:
		return model.GameInProgress
	case statusFinal:
		return model.GameFinal
	default:
		return model.GameScheduled
	}
}

// seasonTypeFromLabel maps the schedule's gameLabel to a season type; the
// regular season has an empty label.
func seasonTypeFromLabel(label string) model.SeasonType {
	l := strings.ToLower(label)
	switch {
	case l == "":
		return model.SeasonRegular
	case strings.Contains(l, "preseason"):
		return model.SeasonPreseason
	case strings.Contains(l, "playoff") || strings.Contains(l, "finals") || strings.Contains(l, "play-in"):
		return model.SeasonPostseason
	default:
		return model.SeasonRegular
	}
}

func splitName(full string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(full), " ", 2)
	if len(parts) == 1 {
		return "", parts[0]
	}
	return parts[0], parts[1]
}
