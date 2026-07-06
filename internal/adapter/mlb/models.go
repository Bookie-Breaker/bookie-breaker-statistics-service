package mlb

// Raw MLB StatsAPI shapes, limited to the fields the normalizer reads.
// Verified against the live 2026 season on 2026-07-05 and recorded in
// testdata/. Rate stats (avg/obp/slg/era) and inningsPitched arrive as
// strings; counting stats as numbers.

// teamsResponse is /teams?sportId=1&season={year}.
type teamsResponse struct {
	Teams []statsapiTeam `json:"teams"`
}

type statsapiTeam struct {
	ID           int         `json:"id"`
	Name         string      `json:"name"`
	Abbreviation string      `json:"abbreviation"`
	LocationName string      `json:"locationName"`
	Venue        statsapiRef `json:"venue"`
	League       statsapiRef `json:"league"`
	Division     statsapiRef `json:"division"`
	Active       bool        `json:"active"`
}

// statsapiRef is the id+name reference object used throughout the API.
type statsapiRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// scheduleResponse is /schedule?sportId=1&... (both the season schedule and
// the single-date scoreboard use it).
type scheduleResponse struct {
	Dates []struct {
		Date  string         `json:"date"`
		Games []statsapiGame `json:"games"`
	} `json:"dates"`
}

type statsapiGame struct {
	GamePk int `json:"gamePk"`
	// GameType values observed live: S (spring), R (regular), A (all-star),
	// E (exhibition); postseason F/D/L/W per the StatsAPI meta docs.
	GameType string `json:"gameType"`
	GameDate string `json:"gameDate"` // RFC 3339
	Status   struct {
		// AbstractGameState is Preview | Live | Final. Verified live:
		// postponed games report abstractGameState "Final" with
		// detailedState "Postponed", so detailedState is checked first.
		AbstractGameState string `json:"abstractGameState"`
		DetailedState     string `json:"detailedState"`
	} `json:"status"`
	Teams struct {
		Away statsapiGameTeam `json:"away"`
		Home statsapiGameTeam `json:"home"`
	} `json:"teams"`
	Venue     statsapiRef        `json:"venue"`
	Linescore *statsapiLinescore `json:"linescore"`
	// RescheduleDate is set on postponed originals that have a scheduled
	// makeup game; the makeup appears under its own date with the same
	// gamePk (verified live), so originals carrying it are skipped.
	RescheduleDate string `json:"rescheduleDate"`
}

type statsapiGameTeam struct {
	Team            statsapiRef     `json:"team"`
	Score           *int            `json:"score"`
	ProbablePitcher *statsapiPerson `json:"probablePitcher"`
}

// statsapiPerson is the hydrated probable-pitcher reference (people carry
// fullName, not name).
type statsapiPerson struct {
	ID       int    `json:"id"`
	FullName string `json:"fullName"`
}

// statsapiLinescore carries per-inning runs and totals. An unplayed bottom
// of the ninth (home team already ahead) has no "runs" key at all — hence
// the pointer (verified live).
type statsapiLinescore struct {
	Innings []struct {
		Num  int `json:"num"`
		Home struct {
			Runs *int `json:"runs"`
		} `json:"home"`
		Away struct {
			Runs *int `json:"runs"`
		} `json:"away"`
	} `json:"innings"`
	Teams struct {
		Home struct {
			Runs int `json:"runs"`
		} `json:"home"`
		Away struct {
			Runs int `json:"runs"`
		} `json:"away"`
	} `json:"teams"`
}

// peopleResponse is /people/{id}?hydrate=stats(...).
type peopleResponse struct {
	People []struct {
		ID        int    `json:"id"`
		FullName  string `json:"fullName"`
		PitchHand struct {
			Code string `json:"code"` // "L" | "R"
		} `json:"pitchHand"`
		Stats []statsapiStatGroup `json:"stats"`
	} `json:"people"`
}

// teamStatsResponse is /teams/stats?... for both season stats and the
// reliever statSplits.
type teamStatsResponse struct {
	Stats []statsapiStatGroup `json:"stats"`
}

type statsapiStatGroup struct {
	Splits []struct {
		Team statsapiRef      `json:"team"`
		Stat statsapiStatLine `json:"stat"`
	} `json:"splits"`
}

// statsapiStatLine is the union of the hitting and pitching stat fields the
// normalizer reads (each response carries its group's subset).
type statsapiStatLine struct {
	GamesPlayed int `json:"gamesPlayed"`

	// Hitting.
	Runs             int    `json:"runs"` // runs allowed in the pitching group
	AtBats           int    `json:"atBats"`
	Hits             int    `json:"hits"`
	Doubles          int    `json:"doubles"`
	Triples          int    `json:"triples"`
	HomeRuns         int    `json:"homeRuns"`
	BaseOnBalls      int    `json:"baseOnBalls"`
	IntentionalWalks int    `json:"intentionalWalks"`
	HitByPitch       int    `json:"hitByPitch"`
	SacFlies         int    `json:"sacFlies"`
	StrikeOuts       int    `json:"strikeOuts"`
	PlateAppearances int    `json:"plateAppearances"`
	OBP              string `json:"obp"`
	SLG              string `json:"slg"`

	// Pitching.
	ERA            string `json:"era"`
	InningsPitched string `json:"inningsPitched"` // "802.1" = 802 and 1/3
	BattersFaced   int    `json:"battersFaced"`
}

// standingsResponse is /standings?leagueId=103,104&season={year}.
type standingsResponse struct {
	Records []struct {
		TeamRecords []struct {
			Team        statsapiRef `json:"team"`
			Wins        int         `json:"wins"`
			Losses      int         `json:"losses"`
			GamesPlayed int         `json:"gamesPlayed"`
		} `json:"teamRecords"`
	} `json:"records"`
}
