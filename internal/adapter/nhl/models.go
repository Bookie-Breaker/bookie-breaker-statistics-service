package nhl

// Raw NHL API shapes, limited to the fields the normalizer reads. Verified
// against the live API on 2026-07-06 (the completed 2025-26 season and
// archive) and recorded in testdata/.

// localizedName is the {"default": "..."} localized-string object the NHL web
// API uses for names and venues.
type localizedName struct {
	Default string `json:"default"`
}

// standingsResponse is api-web /v1/standings/now.
type standingsResponse struct {
	Standings []standingsEntry `json:"standings"`
}

// standingsEntry is one team's standings row: names, tricode,
// conference/division, record, and goal totals.
type standingsEntry struct {
	TeamName       localizedName `json:"teamName"`
	TeamCommonName localizedName `json:"teamCommonName"`
	TeamAbbrev     localizedName `json:"teamAbbrev"`
	PlaceName      localizedName `json:"placeName"`
	ConferenceName string        `json:"conferenceName"`
	DivisionName   string        `json:"divisionName"`
	Wins           int           `json:"wins"`
	Losses         int           `json:"losses"`
	OtLosses       int           `json:"otLosses"`
	GamesPlayed    int           `json:"gamesPlayed"`
	GoalFor        int           `json:"goalFor"`
	GoalAgainst    int           `json:"goalAgainst"`
}

// periodDescriptor identifies a game's (or goal's) period. Number counts
// periods (overtime continues the count past maxRegulationPeriods, normally
// 3); periodType is "REG", "OT", or "SO".
type periodDescriptor struct {
	Number               int    `json:"number"`
	PeriodType           string `json:"periodType"`
	MaxRegulationPeriods int    `json:"maxRegulationPeriods"`
}

// gameOutcome carries how a completed game ended: lastPeriodType is "REG",
// "OT", or "SO".
type gameOutcome struct {
	LastPeriodType string `json:"lastPeriodType"`
}

// gameTeam is one side of a game. The score endpoint carries the common name
// under "name"; the schedule endpoint carries "commonName" plus "placeName".
// Score is a pointer because scheduled games have no score yet.
type gameTeam struct {
	ID         int           `json:"id"`
	Abbrev     string        `json:"abbrev"`
	Score      *int          `json:"score"`
	Name       localizedName `json:"name"`
	CommonName localizedName `json:"commonName"`
	PlaceName  localizedName `json:"placeName"`
}

// fullName builds the display name from whichever fields the endpoint
// supplied: placeName+commonName on the schedule, the plain common name on
// the score endpoint.
func (t gameTeam) fullName() string {
	switch {
	case t.PlaceName.Default != "" && t.CommonName.Default != "":
		return t.PlaceName.Default + " " + t.CommonName.Default
	case t.CommonName.Default != "":
		return t.CommonName.Default
	default:
		return t.Name.Default
	}
}

// scoreGoal is one goal event from the score endpoint. teamAbbrev is a plain
// string (unlike the localized names elsewhere); periodDescriptor.number
// places the goal in a period. The shootout-deciding goal appears as a
// single period-{maxReg+2} goal for the winner, so aggregating goals by
// period reproduces the final score including the shootout (ADR-027).
type scoreGoal struct {
	TeamAbbrev       string           `json:"teamAbbrev"`
	PeriodDescriptor periodDescriptor `json:"periodDescriptor"`
}

// game is one scheduled or completed game, shared by the schedule and score
// endpoints. Goals is populated on the score endpoint only.
type game struct {
	ID           int    `json:"id"`
	Season       int    `json:"season"`
	GameType     int    `json:"gameType"`
	StartTimeUTC string `json:"startTimeUTC"`
	GameState    string `json:"gameState"`
	// GameScheduleState carries schedule disruptions ("OK", "PPD", "CNCL",
	// "SUSP") independently of gameState (verified live).
	GameScheduleState string           `json:"gameScheduleState"`
	Venue             localizedName    `json:"venue"`
	AwayTeam          gameTeam         `json:"awayTeam"`
	HomeTeam          gameTeam         `json:"homeTeam"`
	PeriodDescriptor  periodDescriptor `json:"periodDescriptor"`
	GameOutcome       gameOutcome      `json:"gameOutcome"`
	Goals             []scoreGoal      `json:"goals"`
}

// scheduleResponse is api-web /v1/schedule/{date}: a week of games grouped by
// day.
type scheduleResponse struct {
	GameWeek []struct {
		Date  string `json:"date"`
		Games []game `json:"games"`
	} `json:"gameWeek"`
}

// scoreResponse is api-web /v1/score/{date}: one day's games with per-goal
// detail.
type scoreResponse struct {
	Games []game `json:"games"`
}

// teamSummaryResponse is stats REST /team/summary.
type teamSummaryResponse struct {
	Data  []teamSummaryRow `json:"data"`
	Total int              `json:"total"`
}

// teamSummaryRow is one team's season summary stats. Percentages are
// fractions in [0,1]; goalsAgainst is a season total used with
// shotsAgainstPerGame to derive team save percentage.
type teamSummaryRow struct {
	TeamID              int     `json:"teamId"`
	TeamFullName        string  `json:"teamFullName"`
	GamesPlayed         int     `json:"gamesPlayed"`
	Wins                int     `json:"wins"`
	Losses              int     `json:"losses"`
	GoalsAgainst        float64 `json:"goalsAgainst"`
	GoalsForPerGame     float64 `json:"goalsForPerGame"`
	GoalsAgainstPerGame float64 `json:"goalsAgainstPerGame"`
	ShotsForPerGame     float64 `json:"shotsForPerGame"`
	ShotsAgainstPerGame float64 `json:"shotsAgainstPerGame"`
	PowerPlayPct        float64 `json:"powerPlayPct"`
	PenaltyKillPct      float64 `json:"penaltyKillPct"`
}

// teamRefResponse is stats REST /team: the id↔tricode reference list.
type teamRefResponse struct {
	Data []teamRefRow `json:"data"`
}

// teamRefRow maps a numeric team id to its tricode and full name.
type teamRefRow struct {
	ID       int    `json:"id"`
	TriCode  string `json:"triCode"`
	FullName string `json:"fullName"`
}
