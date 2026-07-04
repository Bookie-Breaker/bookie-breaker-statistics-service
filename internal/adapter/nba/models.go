package nba

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// statsResponse is the classic stats.nba.com envelope: tabular result sets
// with parallel headers and rowSet arrays.
type statsResponse struct {
	Resource   string      `json:"resource"`
	ResultSets []resultSet `json:"resultSets"`
}

type resultSet struct {
	Name    string   `json:"name"`
	Headers []string `json:"headers"`
	RowSet  [][]any  `json:"rowSet"`
}

// set returns the result set with the given name, or the first one when
// name is empty. Lookup by header name (not position) keeps parsing
// resilient to upstream column reordering.
func (r *statsResponse) set(name string) (*resultSet, error) {
	if len(r.ResultSets) == 0 {
		return nil, fmt.Errorf("no result sets in response")
	}
	if name == "" {
		return &r.ResultSets[0], nil
	}
	for i := range r.ResultSets {
		if r.ResultSets[i].Name == name {
			return &r.ResultSets[i], nil
		}
	}
	return nil, fmt.Errorf("result set %q not found", name)
}

// rows materializes header-indexed accessors for every row.
func (s *resultSet) rows() []row {
	idx := make(map[string]int, len(s.Headers))
	for i, h := range s.Headers {
		idx[strings.ToUpper(h)] = i
	}
	out := make([]row, 0, len(s.RowSet))
	for _, r := range s.RowSet {
		out = append(out, row{idx: idx, values: r})
	}
	return out
}

type row struct {
	idx    map[string]int
	values []any
}

func (r row) value(col string) any {
	i, ok := r.idx[strings.ToUpper(col)]
	if !ok || i >= len(r.values) {
		return nil
	}
	return r.values[i]
}

func (r row) float(col string) float64 {
	switch v := r.value(col).(type) {
	case float64:
		return v
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}

func (r row) int(col string) int {
	return int(r.float(col))
}

func (r row) str(col string) string {
	switch v := r.value(col).(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return ""
	}
}

// scheduleResponse is the scheduleleaguev2 shape: the full season schedule
// grouped by date.
type scheduleResponse struct {
	LeagueSchedule struct {
		SeasonYear string `json:"seasonYear"`
		LeagueID   string `json:"leagueId"`
		GameDates  []struct {
			GameDate string         `json:"gameDate"`
			Games    []scheduleGame `json:"games"`
		} `json:"gameDates"`
	} `json:"leagueSchedule"`
}

type scheduleGame struct {
	GameID          string       `json:"gameId"`
	GameStatus      int          `json:"gameStatus"`
	GameStatusText  string       `json:"gameStatusText"`
	GameDateTimeUTC time.Time    `json:"gameDateTimeUTC"`
	GameLabel       string       `json:"gameLabel"`
	WeekNumber      int          `json:"weekNumber"`
	ArenaName       string       `json:"arenaName"`
	ArenaCity       string       `json:"arenaCity"`
	ArenaState      string       `json:"arenaState"`
	HomeTeam        scheduleTeam `json:"homeTeam"`
	AwayTeam        scheduleTeam `json:"awayTeam"`
}

type scheduleTeam struct {
	TeamID      int64  `json:"teamId"`
	TeamName    string `json:"teamName"`
	TeamCity    string `json:"teamCity"`
	TeamTricode string `json:"teamTricode"`
	Score       int    `json:"score"`
	Wins        int    `json:"wins"`
	Losses      int    `json:"losses"`
}

// scoreboardResponse is the scoreboardv3 shape: live statuses and period
// scores for one date.
type scoreboardResponse struct {
	Scoreboard struct {
		GameDate string           `json:"gameDate"`
		Games    []scoreboardGame `json:"games"`
	} `json:"scoreboard"`
}

type scoreboardGame struct {
	GameID         string         `json:"gameId"`
	GameStatus     int            `json:"gameStatus"`
	GameStatusText string         `json:"gameStatusText"`
	Period         int            `json:"period"`
	GameTimeUTC    time.Time      `json:"gameTimeUTC"`
	HomeTeam       scoreboardTeam `json:"homeTeam"`
	AwayTeam       scoreboardTeam `json:"awayTeam"`
}

type scoreboardTeam struct {
	TeamID      int64  `json:"teamId"`
	TeamTricode string `json:"teamTricode"`
	Score       int    `json:"score"`
	Periods     []struct {
		Period int `json:"period"`
		Score  int `json:"score"`
	} `json:"periods"`
}

// Upstream game status codes shared by scheduleleaguev2 and scoreboardv3.
const (
	statusScheduled = 1
	statusLive      = 2
	statusFinal     = 3
)
