package model

// OffensiveStats matches the OpenAPI OffensiveStats schema.
type OffensiveStats struct {
	PointsPerGame    float64 `json:"points_per_game"`
	FieldGoalPct     float64 `json:"field_goal_pct"`
	ThreePointPct    float64 `json:"three_point_pct"`
	FreeThrowPct     float64 `json:"free_throw_pct"`
	ReboundsPerGame  float64 `json:"rebounds_per_game"`
	AssistsPerGame   float64 `json:"assists_per_game"`
	TurnoversPerGame float64 `json:"turnovers_per_game"`
	OffensiveRating  float64 `json:"offensive_rating"`
	Pace             float64 `json:"pace"`
	EffectiveFGPct   float64 `json:"effective_fg_pct"`
}

// DefensiveStats matches the OpenAPI DefensiveStats schema.
type DefensiveStats struct {
	PointsAllowedPerGame  float64 `json:"points_allowed_per_game"`
	OpponentFGPct         float64 `json:"opponent_fg_pct"`
	OpponentThreePointPct float64 `json:"opponent_three_point_pct"`
	StealsPerGame         float64 `json:"steals_per_game"`
	BlocksPerGame         float64 `json:"blocks_per_game"`
	DefensiveRating       float64 `json:"defensive_rating"`
}

// AdvancedStats matches the OpenAPI AdvancedStats schema.
type AdvancedStats struct {
	NetRating           float64 `json:"net_rating"`
	TrueShootingPct     float64 `json:"true_shooting_pct"`
	TurnoverPct         float64 `json:"turnover_pct"`
	OffensiveReboundPct float64 `json:"offensive_rebound_pct"`
	// AdjustedEfficiencyMargin is the opponent-adjusted efficiency margin
	// (NCAA_BB via CBBD; ADR-026). Absent for leagues without an
	// adjusted-ratings source, so it stays omitted when zero.
	AdjustedEfficiencyMargin float64 `json:"adjusted_efficiency_margin,omitempty"`
}

// SoccerStats matches the OpenAPI SoccerStats schema (SOCCER-sport leagues
// only; ADR-026). Strengths are multiplicative factors relative to the
// competition average (1.0 = average), shrunk toward 1.0 by matches played
// to damp small samples. Form fields cover the last (up to) five completed
// matches: goals are per-match averages, points are the summed 3/1/0.
type SoccerStats struct {
	GoalsForPerMatch      float64 `json:"goals_for_per_match"`
	GoalsAgainstPerMatch  float64 `json:"goals_against_per_match"`
	AttackStrength        float64 `json:"attack_strength"`
	DefenseStrength       float64 `json:"defense_strength"`
	Draws                 int     `json:"draws"`
	FormGoalsForLast5     float64 `json:"form_goals_for_last5"`
	FormGoalsAgainstLast5 float64 `json:"form_goals_against_last5"`
	FormPointsLast5       int     `json:"form_points_last5"`
}

// BaseballStats matches the OpenAPI BaseballStats schema (BASEBALL-sport
// leagues only; ADR-026). FIP and wOBA are computed in-service from official
// counting stats using published seasonal constants (see the mlb adapter).
// Fields a source cannot provide stay zero (documented for NCAA_BSB).
type BaseballStats struct {
	RunsScoredPerGame   float64 `json:"runs_scored_per_game"`
	RunsAllowedPerGame  float64 `json:"runs_allowed_per_game"`
	TeamWOBA            float64 `json:"team_woba"`
	TeamOBP             float64 `json:"team_obp"`
	TeamSLG             float64 `json:"team_slg"`
	BattingStrikeoutPct float64 `json:"batting_strikeout_pct"`
	BattingWalkPct      float64 `json:"batting_walk_pct"`
	TeamERA             float64 `json:"team_era"`
	TeamFIP             float64 `json:"team_fip"`
	BullpenERA          float64 `json:"bullpen_era"`
}

// FootballStats matches the OpenAPI FootballStats schema (FOOTBALL-sport
// leagues only; ADR-026). EPA metrics come from nflverse team stats (NFL)
// and are absent for NCAA_FB, which carries SP+ ratings from CFBD instead.
// Drive metrics derive from scoring totals divided by a league-average
// drives-per-game constant when the source has no drive columns (see the
// nfl adapter).
type FootballStats struct {
	PointsPerGame         float64 `json:"points_per_game"`
	PointsAllowedPerGame  float64 `json:"points_allowed_per_game"`
	DrivesPerGame         float64 `json:"drives_per_game"`
	PointsPerDriveOff     float64 `json:"points_per_drive_off"`
	PointsPerDriveDef     float64 `json:"points_per_drive_def"`
	EPAPerPlayOff         float64 `json:"epa_per_play_off"`
	EPAPerPlayDef         float64 `json:"epa_per_play_def"`
	TurnoverMarginPerGame float64 `json:"turnover_margin_per_game"`
	SPPlusRating          float64 `json:"sp_plus_rating"`
}

// HockeyStats matches the OpenAPI HockeyStats schema (HOCKEY-sport leagues
// only; ADR-026). Per-game rates and special-teams percentages come straight
// from the NHL team-summary stats endpoint; team_save_pct is computed
// in-service as 1 - goals_against / shots_against (the endpoint carries no
// save-percentage field). Percentages are fractions in [0,1].
type HockeyStats struct {
	GoalsForPerGame     float64 `json:"goals_for_per_game"`
	GoalsAgainstPerGame float64 `json:"goals_against_per_game"`
	ShotsForPerGame     float64 `json:"shots_for_per_game"`
	ShotsAgainstPerGame float64 `json:"shots_against_per_game"`
	PowerPlayPct        float64 `json:"power_play_pct"`
	PenaltyKillPct      float64 `json:"penalty_kill_pct"`
	TeamSavePct         float64 `json:"team_save_pct"`
}

// SplitRecord matches the OpenAPI SplitRecord schema.
type SplitRecord struct {
	Wins                 int     `json:"wins"`
	Losses               int     `json:"losses"`
	PointsPerGame        float64 `json:"points_per_game"`
	PointsAllowedPerGame float64 `json:"points_allowed_per_game"`
}

// StatBlocks groups the stat categories; fields are pointers so the
// stat_type filter can omit whole blocks. Basketball leagues populate the
// offensive/defensive/advanced blocks; soccer, baseball, football, and
// hockey leagues populate only their sport's block (ADR-026).
type StatBlocks struct {
	Offensive *OffensiveStats `json:"offensive,omitempty"`
	Defensive *DefensiveStats `json:"defensive,omitempty"`
	Advanced  *AdvancedStats  `json:"advanced,omitempty"`
	Soccer    *SoccerStats    `json:"soccer,omitempty"`
	Baseball  *BaseballStats  `json:"baseball,omitempty"`
	Football  *FootballStats  `json:"football,omitempty"`
	Hockey    *HockeyStats    `json:"hockey,omitempty"`
}

// HomeAwaySplits groups home/road records.
type HomeAwaySplits struct {
	Home *SplitRecord `json:"home,omitempty"`
	Away *SplitRecord `json:"away,omitempty"`
}

// TeamStats matches the OpenAPI TeamStats schema.
type TeamStats struct {
	TeamID           string          `json:"team_id"`
	TeamAbbreviation string          `json:"team_abbreviation"`
	Season           int             `json:"season"`
	GamesPlayed      int             `json:"games_played"`
	Stats            StatBlocks      `json:"stats"`
	HomeAwaySplits   *HomeAwaySplits `json:"home_away_splits,omitempty"`
	// Wins and Losses feed SeasonSummary. Additive to the contract payload;
	// they must serialize because the Redis cache is the primary store.
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	// Draws is set only for sports with three-way results (soccer; ADR-027).
	Draws int `json:"draws,omitempty"`
}
