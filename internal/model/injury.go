package model

import "time"

// InjuryReport matches the OpenAPI InjuryReport schema.
type InjuryReport struct {
	PlayerID         string    `json:"player_id,omitempty"`
	PlayerName       string    `json:"player_name"`
	TeamID           string    `json:"team_id,omitempty"`
	TeamAbbreviation string    `json:"team_abbreviation,omitempty"`
	League           League    `json:"league"`
	Position         string    `json:"position,omitempty"`
	Status           string    `json:"status"`
	Description      string    `json:"description,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitzero"`
}
