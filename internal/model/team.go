package model

// TeamSummary matches the OpenAPI TeamSummary schema.
type TeamSummary struct {
	ID           string            `json:"id"`
	League       League            `json:"league"`
	Name         string            `json:"name"`
	Abbreviation string            `json:"abbreviation"`
	Location     string            `json:"location,omitempty"`
	Mascot       string            `json:"mascot,omitempty"`
	Conference   string            `json:"conference,omitempty"`
	Division     string            `json:"division,omitempty"`
	VenueID      string            `json:"venue_id,omitempty"`
	Active       bool              `json:"active"`
	ExternalIDs  map[string]string `json:"external_ids,omitempty"`
}

// VenueSummary matches the OpenAPI VenueSummary schema.
type VenueSummary struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	City  string `json:"city,omitempty"`
	State string `json:"state,omitempty"`
}

// SeasonSummary matches the OpenAPI SeasonSummary schema.
type SeasonSummary struct {
	Season               int     `json:"season"`
	Wins                 int     `json:"wins"`
	Losses               int     `json:"losses"`
	WinPct               float64 `json:"win_pct"`
	PointsPerGame        float64 `json:"points_per_game"`
	PointsAllowedPerGame float64 `json:"points_allowed_per_game"`
	OffensiveRating      float64 `json:"offensive_rating"`
	DefensiveRating      float64 `json:"defensive_rating"`
}

// TeamDetail matches the OpenAPI TeamDetail schema (TeamSummary + extras).
type TeamDetail struct {
	TeamSummary
	Venue         *VenueSummary  `json:"venue,omitempty"`
	SeasonSummary *SeasonSummary `json:"season_summary,omitempty"`
}
