package nba

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/season"
)

// TeamSeasonStats merges the Base, Advanced, and Opponent measures of
// leaguedashteamstats for one team. Per-game values throughout.
type TeamSeasonStats struct {
	NBAID string
	Name  string
	GP    int
	W     int
	L     int

	// Base (per game)
	MIN    float64
	PTS    float64
	FGM    float64
	FGA    float64
	FGPct  float64
	FG3M   float64
	FG3Pct float64
	FTA    float64
	FTPct  float64
	OREB   float64
	DREB   float64
	REB    float64
	AST    float64
	TOV    float64
	STL    float64
	BLK    float64

	// Advanced (authoritative when present)
	HasAdvanced bool
	OffRating   float64
	DefRating   float64
	NetRating   float64
	Pace        float64
	TSPct       float64
	EFGPct      float64
	TOVPct      float64
	OREBPct     float64

	// Opponent (per game)
	OppPTS    float64
	OppFGA    float64
	OppFGPct  float64
	OppFG3Pct float64
	OppFTA    float64
	OppOREB   float64
	OppDREB   float64
	OppTOV    float64
}

// SplitTotals is one location split (home or road) for one team.
type SplitTotals struct {
	W      int
	L      int
	PTS    float64
	OppPTS float64
}

// TeamSplits carries the home/road splits for one team.
type TeamSplits struct {
	Home SplitTotals
	Road SplitTotals
}

// RosterEntry is one commonteamroster row.
type RosterEntry struct {
	NBAPlayerID string
	Name        string
	Jersey      string
	Position    string
	Experience  string // "R" for rookies, otherwise a number
}

// PlayerSeason is one leaguedashplayerstats row (per game).
type PlayerSeason struct {
	NBAPlayerID string
	Name        string
	NBATeamID   string
	TeamAbbrev  string
	GP          int
	MIN         float64
	PTS         float64
	REB         float64
	AST         float64
	STL         float64
	BLK         float64
	FGPct       float64
	FG3Pct      float64
	FTPct       float64
}

// GameLogEntry is one playergamelog row.
type GameLogEntry struct {
	NBAGameID string
	GameDate  string // e.g. "JAN 15, 2026"
	Matchup   string
	Result    string
	MIN       float64
	PTS       int
	REB       int
	AST       int
	STL       int
	BLK       int
}

// ScheduledGame is one scheduleleaguev2 game.
type ScheduledGame struct {
	NBAGameID   string
	Status      int
	StartUTC    time.Time
	HomeNBAID   string
	HomeName    string
	HomeTricode string
	HomeScore   int
	AwayNBAID   string
	AwayName    string
	AwayTricode string
	AwayScore   int
	Arena       string
	ArenaCity   string
	ArenaState  string
	GameLabel   string
}

// ScoreboardEntry is one scoreboardv3 game with period scores.
type ScoreboardEntry struct {
	NBAGameID   string
	Status      int
	StartUTC    time.Time
	HomeScore   int
	AwayScore   int
	HomePeriods []int
	AwayPeriods []int
}

// leagueDashParams is the full parameter set leaguedashteamstats and
// leaguedashplayerstats require; stats.nba.com rejects requests with
// missing keys even when values are empty.
func leagueDashParams(seasonYear int) url.Values {
	return url.Values{
		"Conference": {""}, "DateFrom": {""}, "DateTo": {""}, "Division": {""},
		"GameScope": {""}, "GameSegment": {""}, "LastNGames": {"0"}, "LeagueID": {"00"},
		"Location": {""}, "MeasureType": {"Base"}, "Month": {"0"}, "OpponentTeamID": {"0"},
		"Outcome": {""}, "PORound": {"0"}, "PaceAdjust": {"N"}, "PerMode": {"PerGame"},
		"Period": {"0"}, "PlayerExperience": {""}, "PlayerPosition": {""}, "PlusMinus": {"N"},
		"Rank": {"N"}, "Season": {season.String(seasonYear)}, "SeasonSegment": {""},
		"SeasonType": {"Regular Season"}, "ShotClockRange": {""}, "StarterBench": {""},
		"TeamID": {"0"}, "TwoWay": {"0"},
	}
}

// LeagueTeamStats fetches and merges the Base, Advanced, and Opponent
// measures for all 30 teams. lastN restricts to a rolling window when > 0.
func (c *Client) LeagueTeamStats(ctx context.Context, seasonYear, lastN int) (map[string]*TeamSeasonStats, []*Fetch, error) {
	teams := make(map[string]*TeamSeasonStats)
	var fetches []*Fetch

	for _, measure := range []string{"Base", "Advanced", "Opponent"} {
		params := leagueDashParams(seasonYear)
		params.Set("MeasureType", measure)
		if lastN > 0 {
			params.Set("LastNGames", strconv.Itoa(lastN))
		}

		var resp statsResponse
		fetch, err := c.get(ctx, "/stats/leaguedashteamstats", params, &resp)
		if fetch != nil {
			fetches = append(fetches, fetch)
		}
		if err != nil {
			return nil, fetches, fmt.Errorf("leaguedashteamstats %s: %w", measure, err)
		}

		set, err := resp.set("LeagueDashTeamStats")
		if err != nil {
			return nil, fetches, fmt.Errorf("leaguedashteamstats %s: %w", measure, err)
		}

		for _, r := range set.rows() {
			id := r.str("TEAM_ID")
			t, ok := teams[id]
			if !ok {
				t = &TeamSeasonStats{NBAID: id, Name: r.str("TEAM_NAME")}
				teams[id] = t
			}
			t.GP = r.int("GP")
			t.W = r.int("W")
			t.L = r.int("L")

			switch measure {
			case "Base":
				t.MIN = r.float("MIN")
				t.PTS = r.float("PTS")
				t.FGM = r.float("FGM")
				t.FGA = r.float("FGA")
				t.FGPct = r.float("FG_PCT")
				t.FG3M = r.float("FG3M")
				t.FG3Pct = r.float("FG3_PCT")
				t.FTA = r.float("FTA")
				t.FTPct = r.float("FT_PCT")
				t.OREB = r.float("OREB")
				t.DREB = r.float("DREB")
				t.REB = r.float("REB")
				t.AST = r.float("AST")
				t.TOV = r.float("TOV")
				t.STL = r.float("STL")
				t.BLK = r.float("BLK")
			case "Advanced":
				t.HasAdvanced = true
				t.OffRating = r.float("OFF_RATING")
				t.DefRating = r.float("DEF_RATING")
				t.NetRating = r.float("NET_RATING")
				t.Pace = r.float("PACE")
				t.TSPct = r.float("TS_PCT")
				t.EFGPct = r.float("EFG_PCT")
				t.TOVPct = r.float("TM_TOV_PCT")
				t.OREBPct = r.float("OREB_PCT")
			case "Opponent":
				t.OppPTS = r.float("OPP_PTS")
				t.OppFGA = r.float("OPP_FGA")
				t.OppFGPct = r.float("OPP_FG_PCT")
				t.OppFG3Pct = r.float("OPP_FG3_PCT")
				t.OppFTA = r.float("OPP_FTA")
				t.OppOREB = r.float("OPP_OREB")
				t.OppDREB = r.float("OPP_DREB")
				t.OppTOV = r.float("OPP_TOV")
			}
		}
	}

	return teams, fetches, nil
}

// TeamLocationSplits fetches home/road records and scoring for all teams.
func (c *Client) TeamLocationSplits(ctx context.Context, seasonYear int) (map[string]*TeamSplits, []*Fetch, error) {
	splits := make(map[string]*TeamSplits)
	var fetches []*Fetch

	for _, location := range []string{"Home", "Road"} {
		for _, measure := range []string{"Base", "Opponent"} {
			params := leagueDashParams(seasonYear)
			params.Set("Location", location)
			params.Set("MeasureType", measure)

			var resp statsResponse
			fetch, err := c.get(ctx, "/stats/leaguedashteamstats", params, &resp)
			if fetch != nil {
				fetches = append(fetches, fetch)
			}
			if err != nil {
				return nil, fetches, fmt.Errorf("leaguedashteamstats %s/%s: %w", location, measure, err)
			}

			set, err := resp.set("LeagueDashTeamStats")
			if err != nil {
				return nil, fetches, fmt.Errorf("leaguedashteamstats %s/%s: %w", location, measure, err)
			}

			for _, r := range set.rows() {
				id := r.str("TEAM_ID")
				s, ok := splits[id]
				if !ok {
					s = &TeamSplits{}
					splits[id] = s
				}
				target := &s.Home
				if location == "Road" {
					target = &s.Road
				}
				if measure == "Base" {
					target.W = r.int("W")
					target.L = r.int("L")
					target.PTS = r.float("PTS")
				} else {
					target.OppPTS = r.float("OPP_PTS")
				}
			}
		}
	}

	return splits, fetches, nil
}

// TeamRoster fetches one team's roster.
func (c *Client) TeamRoster(ctx context.Context, nbaTeamID string, seasonYear int) ([]RosterEntry, *Fetch, error) {
	params := url.Values{
		"TeamID":   {nbaTeamID},
		"Season":   {season.String(seasonYear)},
		"LeagueID": {"00"},
	}

	var resp statsResponse
	fetch, err := c.get(ctx, "/stats/commonteamroster", params, &resp)
	if err != nil {
		return nil, fetch, fmt.Errorf("commonteamroster %s: %w", nbaTeamID, err)
	}

	set, err := resp.set("CommonTeamRoster")
	if err != nil {
		return nil, fetch, fmt.Errorf("commonteamroster %s: %w", nbaTeamID, err)
	}

	var roster []RosterEntry
	for _, r := range set.rows() {
		roster = append(roster, RosterEntry{
			NBAPlayerID: r.str("PLAYER_ID"),
			Name:        r.str("PLAYER"),
			Jersey:      r.str("NUM"),
			Position:    r.str("POSITION"),
			Experience:  r.str("EXP"),
		})
	}
	return roster, fetch, nil
}

// LeaguePlayerStats fetches per-game season stats for all players.
func (c *Client) LeaguePlayerStats(ctx context.Context, seasonYear int) ([]PlayerSeason, *Fetch, error) {
	params := leagueDashParams(seasonYear)
	params.Set("College", "")
	params.Set("Country", "")
	params.Set("DraftPick", "")
	params.Set("DraftYear", "")
	params.Set("Height", "")
	params.Set("Weight", "")

	var resp statsResponse
	fetch, err := c.get(ctx, "/stats/leaguedashplayerstats", params, &resp)
	if err != nil {
		return nil, fetch, fmt.Errorf("leaguedashplayerstats: %w", err)
	}

	set, err := resp.set("LeagueDashPlayerStats")
	if err != nil {
		return nil, fetch, fmt.Errorf("leaguedashplayerstats: %w", err)
	}

	var players []PlayerSeason
	for _, r := range set.rows() {
		players = append(players, PlayerSeason{
			NBAPlayerID: r.str("PLAYER_ID"),
			Name:        r.str("PLAYER_NAME"),
			NBATeamID:   r.str("TEAM_ID"),
			TeamAbbrev:  r.str("TEAM_ABBREVIATION"),
			GP:          r.int("GP"),
			MIN:         r.float("MIN"),
			PTS:         r.float("PTS"),
			REB:         r.float("REB"),
			AST:         r.float("AST"),
			STL:         r.float("STL"),
			BLK:         r.float("BLK"),
			FGPct:       r.float("FG_PCT"),
			FG3Pct:      r.float("FG3_PCT"),
			FTPct:       r.float("FT_PCT"),
		})
	}
	return players, fetch, nil
}

// PlayerGameLog fetches one player's per-game log.
func (c *Client) PlayerGameLog(ctx context.Context, nbaPlayerID string, seasonYear int) ([]GameLogEntry, *Fetch, error) {
	params := url.Values{
		"PlayerID":   {nbaPlayerID},
		"Season":     {season.String(seasonYear)},
		"SeasonType": {"Regular Season"},
	}

	var resp statsResponse
	fetch, err := c.get(ctx, "/stats/playergamelog", params, &resp)
	if err != nil {
		return nil, fetch, fmt.Errorf("playergamelog %s: %w", nbaPlayerID, err)
	}

	set, err := resp.set("PlayerGameLog")
	if err != nil {
		return nil, fetch, fmt.Errorf("playergamelog %s: %w", nbaPlayerID, err)
	}

	var log []GameLogEntry
	for _, r := range set.rows() {
		log = append(log, GameLogEntry{
			NBAGameID: r.str("Game_ID"),
			GameDate:  r.str("GAME_DATE"),
			Matchup:   r.str("MATCHUP"),
			Result:    r.str("WL"),
			MIN:       r.float("MIN"),
			PTS:       r.int("PTS"),
			REB:       r.int("REB"),
			AST:       r.int("AST"),
			STL:       r.int("STL"),
			BLK:       r.int("BLK"),
		})
	}
	return log, fetch, nil
}

// Schedule fetches the full season schedule.
func (c *Client) Schedule(ctx context.Context, seasonYear int) ([]ScheduledGame, *Fetch, error) {
	params := url.Values{
		"Season":   {season.String(seasonYear)},
		"LeagueID": {"00"},
	}

	var resp scheduleResponse
	fetch, err := c.get(ctx, "/stats/scheduleleaguev2", params, &resp)
	if err != nil {
		return nil, fetch, fmt.Errorf("scheduleleaguev2: %w", err)
	}

	var games []ScheduledGame
	for _, date := range resp.LeagueSchedule.GameDates {
		for _, g := range date.Games {
			games = append(games, ScheduledGame{
				NBAGameID:   g.GameID,
				Status:      g.GameStatus,
				StartUTC:    g.GameDateTimeUTC,
				HomeNBAID:   strconv.FormatInt(g.HomeTeam.TeamID, 10),
				HomeName:    g.HomeTeam.TeamCity + " " + g.HomeTeam.TeamName,
				HomeTricode: g.HomeTeam.TeamTricode,
				HomeScore:   g.HomeTeam.Score,
				AwayNBAID:   strconv.FormatInt(g.AwayTeam.TeamID, 10),
				AwayName:    g.AwayTeam.TeamCity + " " + g.AwayTeam.TeamName,
				AwayTricode: g.AwayTeam.TeamTricode,
				AwayScore:   g.AwayTeam.Score,
				Arena:       g.ArenaName,
				ArenaCity:   g.ArenaCity,
				ArenaState:  g.ArenaState,
				GameLabel:   g.GameLabel,
			})
		}
	}
	return games, fetch, nil
}

// Scoreboard fetches live statuses and period scores for one date.
func (c *Client) Scoreboard(ctx context.Context, date time.Time) ([]ScoreboardEntry, *Fetch, error) {
	params := url.Values{
		"GameDate": {date.Format("2006-01-02")},
		"LeagueID": {"00"},
	}

	var resp scoreboardResponse
	fetch, err := c.get(ctx, "/stats/scoreboardv3", params, &resp)
	if err != nil {
		return nil, fetch, fmt.Errorf("scoreboardv3: %w", err)
	}

	var entries []ScoreboardEntry
	for _, g := range resp.Scoreboard.Games {
		entry := ScoreboardEntry{
			NBAGameID: g.GameID,
			Status:    g.GameStatus,
			StartUTC:  g.GameTimeUTC,
			HomeScore: g.HomeTeam.Score,
			AwayScore: g.AwayTeam.Score,
		}
		for _, p := range g.HomeTeam.Periods {
			entry.HomePeriods = append(entry.HomePeriods, p.Score)
		}
		for _, p := range g.AwayTeam.Periods {
			entry.AwayPeriods = append(entry.AwayPeriods, p.Score)
		}
		entries = append(entries, entry)
	}
	return entries, fetch, nil
}
