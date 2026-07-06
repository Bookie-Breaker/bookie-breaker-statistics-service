package espnfb

// Raw ESPN site API shapes, limited to the fields the normalizer reads.
// Verified against real archived 2025-season NFL and college football
// responses (recorded in the nfl and cfbd adapters' testdata/); ESPN's API
// is undocumented, so drift is caught by the golden-fixture tests and
// raw-response archival (ADR-026).

// TeamsResponse is /apis/site/v2/sports/{path}/teams.
type TeamsResponse struct {
	Sports []struct {
		Leagues []struct {
			Teams []struct {
				Team Team `json:"team"`
			} `json:"teams"`
		} `json:"leagues"`
	} `json:"sports"`
}

// Team is the team object shared by every endpoint.
type Team struct {
	ID           string `json:"id"`
	DisplayName  string `json:"displayName"`
	Abbreviation string `json:"abbreviation"`
	Location     string `json:"location"`
	IsActive     bool   `json:"isActive"`
}

// ScoreboardResponse is /apis/site/v2/sports/{path}/scoreboard.
type ScoreboardResponse struct {
	Events []Event `json:"events"`
}

// Event is one scoreboard event.
type Event struct {
	ID           string        `json:"id"`
	Date         string        `json:"date"`
	Name         string        `json:"name"`
	Season       EventSeason   `json:"season"`
	Status       EventStatus   `json:"status"`
	Competitions []Competition `json:"competitions"`
}

// EventSeason carries the season an event belongs to. Year is the season's
// starting year even for January/February games (verified live: the January
// 2026 playoffs carry year 2025). Type values observed live: 1 preseason
// (slug "preseason"), 2 regular season ("regular-season"), 3 postseason
// ("post-season").
type EventSeason struct {
	Year int    `json:"year"`
	Type int    `json:"type"`
	Slug string `json:"slug"`
}

// EventStatus carries the live game state. Period counts quarters; overtime
// periods continue the count (a one-OT final is period 5; a real college
// 2OT final was period 6 — verified against archived 2025-season data).
type EventStatus struct {
	Period int        `json:"period"`
	Type   StatusType `json:"type"`
}

// StatusType is the status descriptor. Name values observed live:
// STATUS_SCHEDULED, STATUS_IN_PROGRESS, STATUS_FINAL.
type StatusType struct {
	Name      string `json:"name"`
	State     string `json:"state"` // "pre" | "in" | "post"
	Completed bool   `json:"completed"`
}

// Competition is an event's competition detail.
type Competition struct {
	Venue       *Venue       `json:"venue"`
	Competitors []Competitor `json:"competitors"`
}

// Venue is the competition venue.
type Venue struct {
	FullName string `json:"fullName"`
}

// Competitor is one side of a competition. Linescores (per-quarter points,
// with overtime periods appended) are present directly on the scoreboard for
// completed games (verified live).
type Competitor struct {
	HomeAway   string      `json:"homeAway"`
	Score      string      `json:"score"`
	Team       Team        `json:"team"`
	Linescores []Linescore `json:"linescores"`
}

// Linescore is one period's points.
type Linescore struct {
	Value float64 `json:"value"`
}

// StandingsResponse is /apis/v2/sports/{path}/standings. Children are the
// conferences; college children occasionally omit the standings object
// entirely (observed live on the real 2025 response), so Standings is
// optional.
type StandingsResponse struct {
	Children []struct {
		Name      string `json:"name"`
		Standings *struct {
			Entries []StandingsEntry `json:"entries"`
		} `json:"standings"`
	} `json:"children"`
}

// StandingsEntry is one team's standings row.
type StandingsEntry struct {
	Team  Team           `json:"team"`
	Stats []StandingStat `json:"stats"`
}

// StandingStat is one standings stat. Stats repeat per record split (home,
// road, division) with prefixed Type values, so lookups match on the exact
// unprefixed Type ("wins", "losses", "ties", "pointsfor", "pointsagainst").
// College entries omit the plain losses/ties stats; the overall record is
// then parsed from the Type "total" Summary (e.g. "12-2" — verified live).
type StandingStat struct {
	Name    string  `json:"name"`
	Type    string  `json:"type"`
	Value   float64 `json:"value"`
	Summary string  `json:"summary"`
}
