package model

// BoxScore matches the OpenAPI BoxScore schema: one game's per-player
// lines plus team totals (Phase 7 prop grading and player modeling). The
// Sport discriminator selects which per-sport player slice is populated on
// the team blocks (BASKETBALL → players, SOCCER → soccer_players,
// BASEBALL → baseball_players).
type BoxScore struct {
	GameID   string       `json:"game_id"`
	Sport    string       `json:"sport"`
	Status   string       `json:"status"`
	HomeTeam TeamBoxScore `json:"home_team"`
	AwayTeam TeamBoxScore `json:"away_team"`
}

// TeamBoxScore is one team's side of a box score.
type TeamBoxScore struct {
	ID           string `json:"id"`
	Abbreviation string `json:"abbreviation"`
	Score        int    `json:"score"`
	// TeamStats carries basketball team totals; absent for other sports.
	TeamStats *BasketballTeamStats `json:"team_stats,omitempty"`
	// Players carries basketball player lines (Sport == BASKETBALL).
	Players []BasketballPlayerBoxScore `json:"players,omitempty"`
	// SoccerPlayers carries soccer player lines (Sport == SOCCER).
	SoccerPlayers []SoccerPlayerBoxScore `json:"soccer_players,omitempty"`
	// BaseballPlayers carries baseball player lines (Sport == BASEBALL).
	BaseballPlayers []BaseballPlayerBoxScore `json:"baseball_players,omitempty"`
}

// BasketballTeamStats matches the contract's typed basketball team totals.
type BasketballTeamStats struct {
	FieldGoalsMade         int `json:"field_goals_made"`
	FieldGoalsAttempted    int `json:"field_goals_attempted"`
	ThreePointersMade      int `json:"three_pointers_made"`
	ThreePointersAttempted int `json:"three_pointers_attempted"`
	FreeThrowsMade         int `json:"free_throws_made"`
	FreeThrowsAttempted    int `json:"free_throws_attempted"`
	Rebounds               int `json:"rebounds"`
	OffensiveRebounds      int `json:"offensive_rebounds"`
	DefensiveRebounds      int `json:"defensive_rebounds"`
	Assists                int `json:"assists"`
	Steals                 int `json:"steals"`
	Blocks                 int `json:"blocks"`
	Turnovers              int `json:"turnovers"`
}

// BasketballPlayerBoxScore is one basketball player's game line.
type BasketballPlayerBoxScore struct {
	PlayerID               string  `json:"player_id"`
	PlayerName             string  `json:"player_name"`
	Position               string  `json:"position"`
	Minutes                float64 `json:"minutes"`
	Points                 int     `json:"points"`
	Rebounds               int     `json:"rebounds"`
	Assists                int     `json:"assists"`
	Steals                 int     `json:"steals"`
	Blocks                 int     `json:"blocks"`
	Turnovers              int     `json:"turnovers"`
	FieldGoalsMade         int     `json:"field_goals_made"`
	FieldGoalsAttempted    int     `json:"field_goals_attempted"`
	ThreePointersMade      int     `json:"three_pointers_made"`
	ThreePointersAttempted int     `json:"three_pointers_attempted"`
	FreeThrowsMade         int     `json:"free_throws_made"`
	FreeThrowsAttempted    int     `json:"free_throws_attempted"`
	PlusMinus              int     `json:"plus_minus"`
}

// BaseballPlayerBoxScore is one baseball player's game line: a batting
// block plus a pitching block on the same row (both are filled for two-way
// players; pure pitchers carry a zero batting block and vice versa). The
// emulator grades player props against the pinned canonical stat keys:
// batter_hits → Hits, batter_total_bases → TotalBases, batter_home_runs →
// HomeRuns, pitcher_strikeouts → StrikeoutsPitching.
type BaseballPlayerBoxScore struct {
	PlayerID           string  `json:"player_id"`
	PlayerName         string  `json:"player_name"`
	Position           string  `json:"position"`
	AtBats             int     `json:"at_bats"`
	Hits               int     `json:"hits"`
	TotalBases         int     `json:"total_bases"`
	HomeRuns           int     `json:"home_runs"`
	RunsBattedIn       int     `json:"runs_batted_in"`
	Runs               int     `json:"runs"`
	Walks              int     `json:"walks"`
	StrikeoutsBatting  int     `json:"strikeouts_batting"`
	InningsPitched     float64 `json:"innings_pitched"`
	StrikeoutsPitching int     `json:"strikeouts_pitching"`
	EarnedRuns         int     `json:"earned_runs"`
}

// SoccerPlayerBoxScore is one soccer player's match line.
type SoccerPlayerBoxScore struct {
	PlayerID      string  `json:"player_id"`
	PlayerName    string  `json:"player_name"`
	Position      string  `json:"position"`
	Minutes       float64 `json:"minutes"`
	Goals         int     `json:"goals"`
	Assists       int     `json:"assists"`
	Shots         int     `json:"shots"`
	ShotsOnTarget int     `json:"shots_on_target"`
	YellowCards   int     `json:"yellow_cards"`
	RedCards      int     `json:"red_cards"`
}
