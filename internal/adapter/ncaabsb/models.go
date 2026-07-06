package ncaabsb

// Raw ESPN site API shapes for college baseball, limited to the fields the
// normalizer reads. Verified against the live API on 2026-07-05 (off-season
// empty scoreboard plus archived 2026-season events); ESPN's API is
// undocumented, so drift is caught by the golden-fixture tests and
// raw-response archival (ADR-026).

// teamsResponse is /apis/site/v2/sports/baseball/college-baseball/teams.
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

// scoreboardResponse is
// /apis/site/v2/sports/baseball/college-baseball/scoreboard.
type scoreboardResponse struct {
	Events []espnEvent `json:"events"`
}

type espnEvent struct {
	ID     string `json:"id"`
	Date   string `json:"date"`
	Season struct {
		Year int `json:"year"`
		// Slug values observed live: "regular-season" (type 2) and
		// "championship-series" (type 6, the College World Series finals).
		Slug string `json:"slug"`
	} `json:"season"`
	Status struct {
		// Period counts innings; 9 is regulation. Doubleheader games are
		// scheduled for 7 innings, but the adapter treats period > 9 as
		// extra innings only — a completed 7-inning doubleheader game is
		// simply a shorter final (documented descope; the league is
		// dormant).
		Period int `json:"period"`
		Type   struct {
			Name      string `json:"name"`  // e.g. STATUS_FINAL
			State     string `json:"state"` // "pre" | "in" | "post"
			Completed bool   `json:"completed"`
		} `json:"type"`
	} `json:"status"`
	Competitions []espnCompetition `json:"competitions"`
}

type espnCompetition struct {
	Venue *struct {
		FullName string `json:"fullName"`
	} `json:"venue"`
	Competitors []espnCompetitor `json:"competitors"`
}

type espnCompetitor struct {
	HomeAway string   `json:"homeAway"`
	Score    string   `json:"score"`
	Team     espnTeam `json:"team"`
	// Linescores are per-inning runs, present directly on scoreboard events
	// (verified live; values arrive as numbers, e.g. 3.0).
	Linescores []struct {
		Value float64 `json:"value"`
	} `json:"linescores"`
}

// standingsResponse is /apis/v2/sports/baseball/college-baseball/standings.
// One child covers all of Division I.
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

// standingStat names observed live: gamesPlayed, wins, losses, pointsFor
// (runs scored), pointsAgainst (runs allowed) — the avgPointsFor/Against
// fields are zero in live responses, so per-game rates are computed here.
type standingStat struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
}
