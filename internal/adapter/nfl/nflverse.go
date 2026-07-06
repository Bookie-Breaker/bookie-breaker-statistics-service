// Package nfl fetches NFL data from two free, keyless sources (ADR-026):
// ESPN's public site API (football/nfl) for teams, schedule, and the live
// scoreboard, and nflverse's static CSV releases on GitHub for season EPA
// and turnover aggregates. The nflverse asset family and CSV schema were
// verified by downloading the real 2025-season files (2026-07-06): the
// stats_team release publishes stats_team_reg_{year}.csv,
// stats_team_post_{year}.csv, stats_team_regpost_{year}.csv, and
// stats_team_week_{year}.csv. The adapter fetches the week-level file — it
// is the only one that carries opponent_team, which is what makes defensive
// EPA derivable (the season aggregates have no defensive EPA columns).
package nfl

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
)

var nflverseTracer = otel.Tracer("nflverse-client")

// NFLVerseClient fetches nflverse static CSV release assets. The files are
// static (rebuilt nightly by nflverse automation) and cacheable; the client
// just fetches, and the service's Redis-backed refresh flow caches the
// normalized stats.
type NFLVerseClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewNFLVerseClient creates an nflverse client on the release download base
// URL (https://github.com/nflverse/nflverse-data/releases/download).
func NewNFLVerseClient(baseURL string, timeout time.Duration) *NFLVerseClient {
	return &NFLVerseClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// teamWeekRow is one team-game row of stats_team_week_{year}.csv, limited to
// the columns the normalizer reads (column names verified against the real
// 2025 file).
type teamWeekRow struct {
	Team       string
	Opponent   string
	SeasonType string // REG | POST

	// Offensive plays: pass attempts, sacks taken, and rush carries.
	Attempts      float64
	SacksSuffered float64
	Carries       float64

	// Offensive EPA totals. receiving_epa is the same plays from the
	// receiver's perspective and must not be added on top of passing_epa.
	PassingEPA float64
	RushingEPA float64

	// Giveaways.
	PassingInterceptions float64
	SackFumblesLost      float64
	RushingFumblesLost   float64
	ReceivingFumblesLost float64

	// Takeaways.
	DefInterceptions  float64
	FumbleRecoveryOpp float64
}

// TeamWeekStats fetches and parses one season's team-week stats CSV.
func (c *NFLVerseClient) TeamWeekStats(ctx context.Context, seasonYear int) ([]teamWeekRow, *sportsdata.Fetch, error) {
	path := fmt.Sprintf("/stats_team/stats_team_week_%d.csv", seasonYear)

	ctx, span := nflverseTracer.Start(ctx, "nflverse.team_week_stats")
	defer span.End()
	span.SetAttributes(attribute.String("nflverse.path", path))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute request %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response %s: %w", path, err)
	}

	fetch := &sportsdata.Fetch{
		Endpoint:   path,
		Body:       body,
		HTTPStatus: resp.StatusCode,
		CapturedAt: time.Now().UTC(),
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fetch, fmt.Errorf("nflverse returned %d on %s", resp.StatusCode, path)
	}

	rows, err := parseTeamWeekCSV(body)
	if err != nil {
		return nil, fetch, fmt.Errorf("parse %s: %w", path, err)
	}
	return rows, fetch, nil
}

// parseTeamWeekCSV parses the team-week CSV by header name, so column
// reordering upstream cannot silently corrupt the mapping.
func parseTeamWeekCSV(body []byte) ([]teamWeekRow, error) {
	r := csv.NewReader(bytes.NewReader(body))
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}
	col := make(map[string]int, len(header))
	for i, name := range header {
		col[name] = i
	}
	for _, required := range []string{"team", "opponent_team", "season_type"} {
		if _, ok := col[required]; !ok {
			return nil, fmt.Errorf("missing column %q", required)
		}
	}

	field := func(record []string, name string) string {
		i, ok := col[name]
		if !ok || i >= len(record) {
			return ""
		}
		return record[i]
	}
	num := func(record []string, name string) float64 {
		v, _ := strconv.ParseFloat(field(record, name), 64)
		return v
	}

	var rows []teamWeekRow
	for {
		record, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row: %w", err)
		}
		rows = append(rows, teamWeekRow{
			Team:                 field(record, "team"),
			Opponent:             field(record, "opponent_team"),
			SeasonType:           field(record, "season_type"),
			Attempts:             num(record, "attempts"),
			SacksSuffered:        num(record, "sacks_suffered"),
			Carries:              num(record, "carries"),
			PassingEPA:           num(record, "passing_epa"),
			RushingEPA:           num(record, "rushing_epa"),
			PassingInterceptions: num(record, "passing_interceptions"),
			SackFumblesLost:      num(record, "sack_fumbles_lost"),
			RushingFumblesLost:   num(record, "rushing_fumbles_lost"),
			ReceivingFumblesLost: num(record, "receiving_fumbles_lost"),
			DefInterceptions:     num(record, "def_interceptions"),
			FumbleRecoveryOpp:    num(record, "fumble_recovery_opp"),
		})
	}
	return rows, nil
}
