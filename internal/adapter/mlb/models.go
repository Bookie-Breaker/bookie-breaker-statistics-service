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

// statsapiPerson is the id+fullName person reference (people carry
// fullName, not name) used by probable pitchers, box-score player blocks,
// and roster entries.
type statsapiPerson struct {
	ID       int    `json:"id"`
	FullName string `json:"fullName"`
}

// statsapiPosition is the position object on roster entries and box-score
// player blocks.
type statsapiPosition struct {
	Abbreviation string `json:"abbreviation"`
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
		FirstName string `json:"firstName"`
		LastName  string `json:"lastName"`
		PitchHand struct {
			Code string `json:"code"` // "L" | "R"
		} `json:"pitchHand"`
		Stats []statsapiStatGroup `json:"stats"`
	} `json:"people"`
}

// rosterResponse is /teams/{teamId}/roster?rosterType=active.
type rosterResponse struct {
	Roster []struct {
		Person       statsapiPerson   `json:"person"`
		JerseyNumber string           `json:"jerseyNumber"`
		Position     statsapiPosition `json:"position"`
	} `json:"roster"`
}

// boxscoreResponse is /game/{gamePk}/boxscore. Unlike the NBA box-score
// endpoint, the StatsAPI response labels home and away directly.
type boxscoreResponse struct {
	Teams struct {
		Away boxscoreTeam `json:"away"`
		Home boxscoreTeam `json:"home"`
	} `json:"teams"`
}

// boxscoreTeam is one side of the box score: the full team object (id, name,
// abbreviation), the game team totals, and the player blocks keyed
// "ID{personId}" (verified live; the key duplicates person.id).
type boxscoreTeam struct {
	Team      statsapiTeam `json:"team"`
	TeamStats struct {
		Batting statsapiStatLine `json:"batting"`
	} `json:"teamStats"`
	Players map[string]boxscorePlayer `json:"players"`
}

// boxscorePlayer is one player's block. Players who did not appear carry
// empty stats objects ({} for batting and pitching); anyone who played has
// gamesPlayed: 1 on the relevant group. Two-way players fill both.
type boxscorePlayer struct {
	Person   statsapiPerson   `json:"person"`
	Position statsapiPosition `json:"position"`
	Stats    struct {
		Batting  statsapiStatLine `json:"batting"`
		Pitching statsapiStatLine `json:"pitching"`
	} `json:"stats"`
}

// teamStatsResponse is /teams/stats?... for both season stats and the
// reliever statSplits.
type teamStatsResponse struct {
	Stats []statsapiStatGroup `json:"stats"`
}

type statsapiStatGroup struct {
	// Group discriminates hitting vs pitching on dual-group person
	// hydrations (the team-stats endpoints request one group per call and
	// never need it).
	Group struct {
		DisplayName string `json:"displayName"` // "hitting" | "pitching"
	} `json:"group"`
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
	RBI              int    `json:"rbi"`
	OBP              string `json:"obp"`
	SLG              string `json:"slg"`
	AVG              string `json:"avg"`

	// Pitching.
	ERA            string `json:"era"`
	InningsPitched string `json:"inningsPitched"` // "802.1" = 802 and 1/3
	BattersFaced   int    `json:"battersFaced"`
	EarnedRuns     int    `json:"earnedRuns"`
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
