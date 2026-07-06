package espnbb

// Raw ESPN site API shapes, limited to the fields the normalizer reads (the
// same site-API structure the espnfb football client uses). Verified against
// real archived 2025-26 men's college basketball responses (recorded in the
// cbbd adapter's testdata/); ESPN's API is undocumented, so drift is caught by
// the golden-fixture tests and raw-response archival (ADR-026).

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

// EventSeason carries the season an event belongs to. Type values observed
// live: 1 preseason, 2 regular season, 3 postseason (conference tournaments
// and the NCAA tournament).
type EventSeason struct {
	Year int    `json:"year"`
	Type int    `json:"type"`
	Slug string `json:"slug"`
}

// EventStatus carries the live game state. Period counts halves; overtime
// periods continue the count (a one-overtime final is period 3).
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

// Competitor is one side of a competition. Linescores (per-half points, with
// overtime periods appended) are present directly on the scoreboard for
// completed games.
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
