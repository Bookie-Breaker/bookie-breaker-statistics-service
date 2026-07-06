package cfbd

import (
	"encoding/json"
	"strconv"
)

// CFBD response shapes, limited to the fields the normalizer reads. They match
// the public CollegeFootballData OpenAPI document (recorded 2026-07-06);
// because the project has no API key yet, these are validated against the live
// API in the September verification session (ADR-026).

// TeamRecords is one team's season record (CFBD /records). Only the season
// total is read; the split records (conference/home/away) are ignored.
type TeamRecords struct {
	Year       int        `json:"year"`
	TeamID     int        `json:"teamId"`
	Team       string     `json:"team"`
	Conference string     `json:"conference"`
	Total      TeamRecord `json:"total"`
}

// TeamRecord is a games/wins/losses/ties tuple.
type TeamRecord struct {
	Games  int `json:"games"`
	Wins   int `json:"wins"`
	Losses int `json:"losses"`
	Ties   int `json:"ties"`
}

// TeamSP is one team's SP+ rating (CFBD /ratings/sp). Rating is the overall
// SP+ value that maps to FootballStats.sp_plus_rating.
type TeamSP struct {
	Year   int     `json:"year"`
	Team   string  `json:"team"`
	Rating float64 `json:"rating"`
}

// TeamStat is one team-season stat line (CFBD /stats/season): a statName with
// its statValue. CFBD returns statValue as a JSON number for the scoring
// categories the normalizer reads, but as a string for others (possessionTime
// is "MM:SS"), so statValue decodes leniently — a non-numeric value becomes 0
// rather than failing the whole array decode.
type TeamStat struct {
	Season   int          `json:"season"`
	Team     string       `json:"team"`
	StatName string       `json:"statName"`
	StatVal  lenientFloat `json:"statValue"`
}

// lenientFloat decodes a JSON number, or a numeric JSON string, into a float
// without ever failing: unparseable values become 0.
type lenientFloat float64

// UnmarshalJSON accepts either a JSON number or a JSON string.
func (f *lenientFloat) UnmarshalJSON(b []byte) error {
	var n float64
	if err := json.Unmarshal(b, &n); err == nil {
		*f = lenientFloat(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		v, _ := strconv.ParseFloat(s, 64)
		*f = lenientFloat(v)
		return nil
	}
	*f = 0
	return nil
}
