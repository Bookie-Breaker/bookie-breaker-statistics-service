package model

import "time"

// PlayerSeasonStats matches the OpenAPI PlayerSeasonStats schema.
type PlayerSeasonStats struct {
	Season          int     `json:"season"`
	GamesPlayed     int     `json:"games_played"`
	MinutesPerGame  float64 `json:"minutes_per_game"`
	PointsPerGame   float64 `json:"points_per_game"`
	ReboundsPerGame float64 `json:"rebounds_per_game"`
	AssistsPerGame  float64 `json:"assists_per_game"`
	StealsPerGame   float64 `json:"steals_per_game"`
	BlocksPerGame   float64 `json:"blocks_per_game"`
	FieldGoalPct    float64 `json:"field_goal_pct"`
	ThreePointPct   float64 `json:"three_point_pct"`
	FreeThrowPct    float64 `json:"free_throw_pct"`
}

// PlayerSummary matches the OpenAPI PlayerSummary schema.
type PlayerSummary struct {
	ID                string            `json:"id"`
	TeamID            string            `json:"team_id"`
	TeamAbbreviation  string            `json:"team_abbreviation,omitempty"`
	FirstName         string            `json:"first_name"`
	LastName          string            `json:"last_name"`
	Position          string            `json:"position"`
	JerseyNumber      *int              `json:"jersey_number,omitempty"`
	Status            PlayerStatus      `json:"status"`
	InjuryDescription *string           `json:"injury_description,omitempty"`
	ExternalIDs       map[string]string `json:"external_ids,omitempty"`
	League            League            `json:"-"`
}

// PlayerDetail matches the OpenAPI PlayerDetail schema.
type PlayerDetail struct {
	PlayerSummary
	ExperienceYears *int               `json:"experience_years,omitempty"`
	SeasonStats     *PlayerSeasonStats `json:"season_stats,omitempty"`
	GameLog         []PlayerGameLine   `json:"game_log,omitempty"`
}

// PlayerGameLine is one row of a player's per-game log (returned when
// game_log=true; shape is service-defined, the contract leaves it open).
type PlayerGameLine struct {
	GameID   string    `json:"game_id"`
	GameDate time.Time `json:"game_date"`
	Matchup  string    `json:"matchup"`
	Result   string    `json:"result"`
	Minutes  float64   `json:"minutes"`
	Points   int       `json:"points"`
	Rebounds int       `json:"rebounds"`
	Assists  int       `json:"assists"`
	Steals   int       `json:"steals"`
	Blocks   int       `json:"blocks"`
}
