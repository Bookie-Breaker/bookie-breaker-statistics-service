package soccer

// Raw ESPN site API shapes, limited to the fields the normalizer reads.
// Verified against live 2026 World Cup and 2025-26 EPL responses (recorded
// in testdata/); ESPN's API is undocumented, so drift is caught by the
// golden-fixture tests and raw-response archival (ADR-026).

// teamsResponse is /apis/site/v2/sports/soccer/{code}/teams.
type teamsResponse struct {
	Sports []struct {
		Leagues []struct {
			Teams []struct {
				Team espnTeam `json:"team"`
			} `json:"teams"`
		} `json:"leagues"`
	} `json:"sports"`
}

// espnTeam is the team object shared by every endpoint.
type espnTeam struct {
	ID           string `json:"id"`
	DisplayName  string `json:"displayName"`
	Abbreviation string `json:"abbreviation"`
	Location     string `json:"location"`
	IsActive     bool   `json:"isActive"`
}

// scoreboardResponse is /apis/site/v2/sports/soccer/{code}/scoreboard.
type scoreboardResponse struct {
	Events []espnEvent `json:"events"`
}

type espnEvent struct {
	ID           string            `json:"id"`
	Date         string            `json:"date"`
	Name         string            `json:"name"`
	Season       espnEventSeason   `json:"season"`
	Status       espnEventStatus   `json:"status"`
	Competitions []espnCompetition `json:"competitions"`
}

type espnEventSeason struct {
	Year int    `json:"year"`
	Slug string `json:"slug"`
}

type espnEventStatus struct {
	// Period counts halves and extra-time periods; a penalty shootout is
	// period 5 (full time = 2, extra time = 4; verified live).
	Period int            `json:"period"`
	Type   espnStatusType `json:"type"`
}

type espnStatusType struct {
	// Name values observed live: STATUS_SCHEDULED, STATUS_FIRST_HALF,
	// STATUS_SECOND_HALF, STATUS_FULL_TIME, STATUS_FINAL_AET,
	// STATUS_FINAL_PEN.
	Name      string `json:"name"`
	State     string `json:"state"` // "pre" | "in" | "post"
	Completed bool   `json:"completed"`
}

type espnCompetition struct {
	Venue       *espnVenue       `json:"venue"`
	Competitors []espnCompetitor `json:"competitors"`
}

type espnVenue struct {
	FullName string `json:"fullName"`
}

type espnCompetitor struct {
	HomeAway string `json:"homeAway"`
	// Score is the full-time score including extra time but excluding
	// shootout goals (verified live: a 1-1 FT-Pens match keeps score "1").
	Score string `json:"score"`
	// ShootoutScore is present only on penalty-shootout matches.
	ShootoutScore *int     `json:"shootoutScore"`
	Team          espnTeam `json:"team"`
	// Linescores (per-period goals) appear only in summary responses; the
	// scoreboard endpoint omits them entirely (verified live). On shootout
	// matches the final entry is the shootout score, not a period.
	Linescores []espnLinescore `json:"linescores"`
}

type espnLinescore struct {
	DisplayValue string `json:"displayValue"`
}

// summaryResponse is /apis/site/v2/sports/soccer/{code}/summary?event=.
type summaryResponse struct {
	Header struct {
		Competitions []espnCompetition `json:"competitions"`
	} `json:"header"`
}

// standingsResponse is /apis/v2/sports/soccer/{code}/standings. Children
// are groups for the World Cup and the single table for club leagues.
type standingsResponse struct {
	Children []struct {
		Name      string `json:"name"`
		Standings struct {
			Entries []standingsEntry `json:"entries"`
		} `json:"standings"`
	} `json:"children"`
}

type standingsEntry struct {
	Team  espnTeam       `json:"team"`
	Stats []standingStat `json:"stats"`
}

// standingStat names observed live: gamesPlayed, wins, losses, ties,
// pointsFor (goals for), pointsAgainst (goals against), points.
type standingStat struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}
