package model

import "time"

// TeamRef matches the OpenAPI TeamRef schema.
type TeamRef struct {
	ID           string `json:"id,omitempty"`
	Name         string `json:"name,omitempty"`
	Abbreviation string `json:"abbreviation,omitempty"`
}

// VenueRef matches the OpenAPI VenueRef schema.
type VenueRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// PeriodScore matches the OpenAPI PeriodScore schema.
type PeriodScore struct {
	Period int `json:"period"`
	Home   int `json:"home"`
	Away   int `json:"away"`
}

// GameResult matches the OpenAPI GameResult schema.
type GameResult struct {
	ID           string        `json:"id,omitempty"`
	HomeScore    int           `json:"home_score"`
	AwayScore    int           `json:"away_score"`
	TotalScore   int           `json:"total_score"`
	Margin       int           `json:"margin"`
	Overtime     bool          `json:"overtime"`
	CompletedAt  time.Time     `json:"completed_at,omitzero"`
	PeriodScores []PeriodScore `json:"period_scores,omitempty"`
	// Regulation scores are set only by leagues whose markets settle on
	// regulation time (ADR-027). Absent means the final score is the
	// settlement-relevant score.
	RegulationHomeScore *int `json:"regulation_home_score,omitempty"`
	RegulationAwayScore *int `json:"regulation_away_score,omitempty"`
}

// ProbablePitcher matches the OpenAPI ProbablePitcher schema (BASEBALL
// leagues; present once announced, absent otherwise). Season pitching stats
// are embedded so consumers need no extra lookup; they are populated for
// upcoming games only (past games keep name and external id).
type ProbablePitcher struct {
	Name           string  `json:"name"`
	ExternalID     string  `json:"external_id"`
	Throws         string  `json:"throws"`
	ERA            float64 `json:"era"`
	FIP            float64 `json:"fip"`
	KBBPct         float64 `json:"k_bb_pct"`
	InningsPitched float64 `json:"innings_pitched"`
}

// Game matches the OpenAPI Game schema.
type Game struct {
	ID             string     `json:"id"`
	League         League     `json:"league"`
	HomeTeam       TeamRef    `json:"home_team"`
	AwayTeam       TeamRef    `json:"away_team"`
	Venue          *VenueRef  `json:"venue,omitempty"`
	ScheduledStart time.Time  `json:"scheduled_start"`
	Status         GameStatus `json:"status"`
	Season         int        `json:"season,omitempty"`
	SeasonType     SeasonType `json:"season_type,omitempty"`
	HomeScore      *int       `json:"home_score"`
	AwayScore      *int       `json:"away_score"`
	// Probable pitchers are set only for BASEBALL leagues (ADR-026).
	HomeProbablePitcher *ProbablePitcher `json:"home_probable_pitcher,omitempty"`
	AwayProbablePitcher *ProbablePitcher `json:"away_probable_pitcher,omitempty"`
	Result              *GameResult      `json:"result,omitempty"`
	// ExternalID is the source's game id (additive to the contract payload;
	// it must serialize because the Redis cache is the primary store).
	ExternalID string `json:"external_id,omitempty"`
}
