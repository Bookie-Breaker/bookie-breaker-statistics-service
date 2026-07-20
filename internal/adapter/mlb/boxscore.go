package mlb

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/ids"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/model"
)

// normalizeBoxScore converts /game/{gamePk}/boxscore into the canonical box
// score. The StatsAPI response labels home and away directly (unlike the
// NBA endpoint), so the team blocks are emitted already oriented;
// orientBoxScore's swap check is a no-op and the service only stamps the
// canonical game_id/sport/status fields.
func normalizeBoxScore(gamePk string, resp *boxscoreResponse) *model.BoxScore {
	if resp.Teams.Home.Team.ID == 0 || resp.Teams.Away.Team.ID == 0 {
		return nil
	}
	return &model.BoxScore{
		GameID:   ids.Game(leagueMLB, gamePk),
		Sport:    string(model.SportBaseball),
		HomeTeam: teamBoxScore(resp.Teams.Home),
		AwayTeam: teamBoxScore(resp.Teams.Away),
	}
}

// teamBoxScore builds one side's block. The score is the game batting
// total's runs (the box score's own team totals; the linescore agrees but
// needs no second call).
func teamBoxScore(t boxscoreTeam) model.TeamBoxScore {
	return model.TeamBoxScore{
		ID:              ids.Team(leagueMLB, strconv.Itoa(t.Team.ID)),
		Abbreviation:    t.Team.Abbreviation,
		Score:           t.TeamStats.Batting.Runs,
		BaseballPlayers: playerLines(t.Players),
	}
}

// playerLines converts one team's "ID{personId}"-keyed player map into
// canonical lines, one row per player who appeared: the batting block and
// the pitching block share the row (two-way players fill both). Bench
// players carry empty stats objects and are skipped. The map iteration
// order is random, so lines are sorted by name for a stable payload.
func playerLines(players map[string]boxscorePlayer) []model.BaseballPlayerBoxScore {
	lines := make([]model.BaseballPlayerBoxScore, 0, len(players))
	for _, p := range players {
		bat, pit := p.Stats.Batting, p.Stats.Pitching
		if !appeared(bat) && !appeared(pit) {
			continue
		}
		lines = append(lines, model.BaseballPlayerBoxScore{
			PlayerID:           ids.Player(leagueMLB, strconv.Itoa(p.Person.ID)),
			PlayerName:         p.Person.FullName,
			Position:           p.Position.Abbreviation,
			AtBats:             bat.AtBats,
			Hits:               bat.Hits,
			TotalBases:         totalBases(bat),
			HomeRuns:           bat.HomeRuns,
			RunsBattedIn:       bat.RBI,
			Runs:               bat.Runs,
			Walks:              bat.BaseOnBalls,
			StrikeoutsBatting:  bat.StrikeOuts,
			InningsPitched:     parseInnings(pit.InningsPitched),
			StrikeoutsPitching: pit.StrikeOuts,
			EarnedRuns:         pit.EarnedRuns,
		})
	}
	slices.SortFunc(lines, func(a, b model.BaseballPlayerBoxScore) int {
		if c := strings.Compare(a.PlayerName, b.PlayerName); c != 0 {
			return c
		}
		return strings.Compare(a.PlayerID, b.PlayerID)
	})
	return lines
}

// appeared reports whether a stat block belongs to a player who took part:
// the box score stamps gamesPlayed: 1 on the groups a player appeared in
// and leaves bench players' blocks empty (verified live).
func appeared(st statsapiStatLine) bool { return st.GamesPlayed > 0 }

// totalBases computes total bases from the counting line. TB is defined as
// 1B + 2*2B + 3*3B + 4*HR; the StatsAPI carries hits (all of them), not
// singles, and 1B = H - 2B - 3B - HR, so substituting gives
// TB = H + 2B + 2*3B + 3*HR.
func totalBases(st statsapiStatLine) int {
	return st.Hits + st.Doubles + 2*st.Triples + 3*st.HomeRuns
}
