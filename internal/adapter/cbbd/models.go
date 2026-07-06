package cbbd

// CBBD response shapes, limited to the fields the normalizer reads. They match
// the public CollegeBasketballData OpenAPI document (recorded 2026-07-06);
// because the project has no API key yet, these are validated against the live
// API in the November verification session (ADR-026).

// TeamSeasonStats is one team-season stat line (CBBD /stats/team/season).
// teamStats are the team's own totals; opponentStats are what opponents did
// against them (the defensive side). Counting stats are season totals divided
// by games in the normalizer.
type TeamSeasonStats struct {
	Season        int                 `json:"season"`
	TeamID        int                 `json:"teamId"`
	Team          string              `json:"team"`
	Conference    string              `json:"conference"`
	Games         int                 `json:"games"`
	Wins          float64             `json:"wins"`
	Losses        float64             `json:"losses"`
	Pace          float64             `json:"pace"`
	TeamStats     TeamSeasonUnitStats `json:"teamStats"`
	OpponentStats TeamSeasonUnitStats `json:"opponentStats"`
}

// TeamSeasonUnitStats is one unit's (team or opponent) season totals and rate
// stats.
type TeamSeasonUnitStats struct {
	FieldGoals           shooting    `json:"fieldGoals"`
	ThreePointFieldGoals shooting    `json:"threePointFieldGoals"`
	FreeThrows           shooting    `json:"freeThrows"`
	Rebounds             rebounds    `json:"rebounds"`
	Turnovers            turnovers   `json:"turnovers"`
	Points               points      `json:"points"`
	FourFactors          fourFactors `json:"fourFactors"`
	Assists              float64     `json:"assists"`
	Blocks               float64     `json:"blocks"`
	Steals               float64     `json:"steals"`
	Possessions          float64     `json:"possessions"`
	Rating               float64     `json:"rating"`
	TrueShooting         float64     `json:"trueShooting"`
}

// shooting is a made/attempted/pct triple. pct is a fraction in [0,1].
type shooting struct {
	Pct       float64 `json:"pct"`
	Attempted float64 `json:"attempted"`
	Made      float64 `json:"made"`
}

// rebounds splits total rebounds into offensive and defensive.
type rebounds struct {
	Total     float64 `json:"total"`
	Defensive float64 `json:"defensive"`
	Offensive float64 `json:"offensive"`
}

// turnovers carries the team's turnover totals.
type turnovers struct {
	TeamTotal float64 `json:"teamTotal"`
	Total     float64 `json:"total"`
}

// points breaks down scoring; Total is the season points total.
type points struct {
	Total float64 `json:"total"`
}

// fourFactors carries the Dean Oliver four-factor rate stats (fractions in
// [0,1] except turnoverRatio, a per-possession rate).
type fourFactors struct {
	FreeThrowRate         float64 `json:"freeThrowRate"`
	OffensiveReboundPct   float64 `json:"offensiveReboundPct"`
	TurnoverRatio         float64 `json:"turnoverRatio"`
	EffectiveFieldGoalPct float64 `json:"effectiveFieldGoalPct"`
}

// AdjustedEfficiencyInfo is one team's opponent-adjusted efficiency ratings
// (CBBD /ratings/adjusted). netRating is the adjusted efficiency margin that
// maps to AdvancedStats.adjusted_efficiency_margin.
type AdjustedEfficiencyInfo struct {
	Season          int     `json:"season"`
	TeamID          int     `json:"teamId"`
	Team            string  `json:"team"`
	Conference      string  `json:"conference"`
	OffensiveRating float64 `json:"offensiveRating"`
	DefensiveRating float64 `json:"defensiveRating"`
	NetRating       float64 `json:"netRating"`
}
