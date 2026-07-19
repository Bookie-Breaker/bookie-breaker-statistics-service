package cache

import "fmt"

// Key scheme for all statistics-service Redis data. Values are JSON strings
// (a deliberate deviation from redis-schemas.md's Hash types, for
// consistency with lines-service; flagged in the docs). Every collection
// key has a ":stale" mirror written on refresh and served when the upstream
// circuit is open.
func keyTeams(league string) string   { return "stats:teams:" + league }
func keyPlayers(league string) string { return "stats:players:" + league }
func keyPlayerDetails(league string) string {
	return "stats:playerdetails:" + league
}
func keyInjuries(league string) string { return "stats:injuries:" + league }
func keyTeamDetails(league string) string {
	return "stats:teamdetails:" + league
}

func keyTeamStats(league string, season, window int) string {
	if window > 0 {
		return fmt.Sprintf("stats:teamstats:%s:%d:w%d", league, season, window)
	}
	return fmt.Sprintf("stats:teamstats:%s:%d:full", league, season)
}

func keyGames(league string, season int) string {
	return fmt.Sprintf("stats:games:%s:%d", league, season)
}

func keyGameLog(playerID string, season int) string {
	return fmt.Sprintf("stats:gamelog:%s:%d", playerID, season)
}

func keyBoxScore(gameID string) string { return "stats:boxscore:" + gameID }

func keyGameCompleted(gameID string) string { return "stats:gamecompleted:" + gameID }

func keyLastSuccess(source string) string { return "stats:health:" + source + ":last_success" }

func stale(key string) string { return key + ":stale" }
