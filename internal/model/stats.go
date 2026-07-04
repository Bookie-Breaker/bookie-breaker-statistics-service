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
}

// SplitRecord matches the OpenAPI SplitRecord schema.
type SplitRecord struct {
	Wins                 int     `json:"wins"`
	Losses               int     `json:"losses"`
	PointsPerGame        float64 `json:"points_per_game"`
	PointsAllowedPerGame float64 `json:"points_allowed_per_game"`
}

// StatBlocks groups the three stat categories; fields are pointers so the
// stat_type filter can omit whole blocks.
type StatBlocks struct {
	Offensive *OffensiveStats `json:"offensive,omitempty"`
	Defensive *DefensiveStats `json:"defensive,omitempty"`
	Advanced  *AdvancedStats  `json:"advanced,omitempty"`
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
}
