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
}

// Game matches the OpenAPI Game schema.
type Game struct {
	ID             string      `json:"id"`
	League         League      `json:"league"`
	HomeTeam       TeamRef     `json:"home_team"`
	AwayTeam       TeamRef     `json:"away_team"`
	Venue          *VenueRef   `json:"venue,omitempty"`
	ScheduledStart time.Time   `json:"scheduled_start"`
	Status         GameStatus  `json:"status"`
	Season         int         `json:"season,omitempty"`
	SeasonType     SeasonType  `json:"season_type,omitempty"`
	HomeScore      *int        `json:"home_score"`
	AwayScore      *int        `json:"away_score"`
	Result         *GameResult `json:"result,omitempty"`
	ExternalID     string      `json:"-"`
}
