// Package mlb fetches MLB data from the official MLB StatsAPI
// (statsapi.mlb.com/api/v1; free, documented, keyless — ADR-026). Response
// shapes were verified against the live 2026 season on 2026-07-05 and
// recorded as testdata fixtures. FIP and wOBA are computed in-service from
// official counting stats using published seasonal constants (normalizer.go).
package mlb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
)

var tracer = otel.Tracer("mlb-statsapi-client")

// sportIDMLB is the StatsAPI sport id for Major League Baseball.
const sportIDMLB = 1

// scheduleHydrate attaches probable pitchers and per-inning linescores to
// schedule games. Verified live 2026-07-05: probablePitcher hydrates id +
// fullName (+ note when present); pitching hand and season stats need a
// /people call per pitcher.
const scheduleHydrate = "probablePitcher(note),linescore"

// Client fetches MLB StatsAPI data.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an MLB StatsAPI client.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Teams fetches the season's team list (with home venues).
func (c *Client) Teams(ctx context.Context, seasonYear int) (*teamsResponse, *sportsdata.Fetch, error) {
	var parsed teamsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/teams?sportId=%d&season=%d", sportIDMLB, seasonYear), &parsed)
	return &parsed, fetch, err
}

// Schedule fetches the full season schedule with probable pitchers and
// linescores. A single season-long request is accepted by the live API
// (verified 2026-07-05: ~10 MB, under two seconds), so no date chunking is
// needed.
func (c *Client) Schedule(ctx context.Context, seasonYear int) (*scheduleResponse, *sportsdata.Fetch, error) {
	var parsed scheduleResponse
	path := fmt.Sprintf("/schedule?sportId=%d&season=%d&hydrate=%s", sportIDMLB, seasonYear, scheduleHydrate)
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// Scoreboard fetches one date's games with linescores for live statuses and
// final results.
func (c *Client) Scoreboard(ctx context.Context, date time.Time) (*scheduleResponse, *sportsdata.Fetch, error) {
	var parsed scheduleResponse
	path := fmt.Sprintf("/schedule?sportId=%d&date=%s&hydrate=linescore", sportIDMLB, date.UTC().Format("2006-01-02"))
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// Person fetches one player with season pitching stats hydrated in the same
// call (pitchHand plus the FIP/K-BB% inputs; verified live — the hydrate
// spares a second request per pitcher).
func (c *Client) Person(ctx context.Context, personID, seasonYear int) (*peopleResponse, *sportsdata.Fetch, error) {
	var parsed peopleResponse
	path := fmt.Sprintf("/people/%d?hydrate=stats(group=[pitching],type=[season],season=%d)", personID, seasonYear)
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// PersonSeason fetches one player with season hitting and pitching stats
// hydrated in one call (the same Person-hydrate pattern the probable
// pitchers use, widened to both groups for roster hydration).
func (c *Client) PersonSeason(ctx context.Context, personID, seasonYear int) (*peopleResponse, *sportsdata.Fetch, error) {
	var parsed peopleResponse
	path := fmt.Sprintf("/people/%d?hydrate=stats(group=[hitting,pitching],type=[season],season=%d)", personID, seasonYear)
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// Roster fetches one team's active roster.
func (c *Client) Roster(ctx context.Context, teamID, seasonYear int) (*rosterResponse, *sportsdata.Fetch, error) {
	var parsed rosterResponse
	path := fmt.Sprintf("/teams/%d/roster?rosterType=active&season=%d", teamID, seasonYear)
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// BoxScore fetches one game's box score by StatsAPI gamePk.
func (c *Client) BoxScore(ctx context.Context, gamePk string) (*boxscoreResponse, *sportsdata.Fetch, error) {
	var parsed boxscoreResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/game/%s/boxscore", gamePk), &parsed)
	return &parsed, fetch, err
}

// TeamStats fetches league-wide team season stats for one group ("hitting"
// or "pitching"). Route verified live: /teams/stats with sportId returns all
// 30 teams in one call.
func (c *Client) TeamStats(ctx context.Context, seasonYear int, group string) (*teamStatsResponse, *sportsdata.Fetch, error) {
	var parsed teamStatsResponse
	path := fmt.Sprintf("/teams/stats?sportId=%d&season=%d&group=%s&stats=season", sportIDMLB, seasonYear, group)
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// BullpenStats fetches league-wide reliever-only pitching splits
// (stats=statSplits&sitCodes=rp). Verified live 2026-07-05: one call returns
// all 30 teams' relief-only lines including ERA — no per-team calls and no
// team-ERA fallback needed.
func (c *Client) BullpenStats(ctx context.Context, seasonYear int) (*teamStatsResponse, *sportsdata.Fetch, error) {
	var parsed teamStatsResponse
	path := fmt.Sprintf("/teams/stats?sportId=%d&season=%d&group=pitching&stats=statSplits&sitCodes=rp", sportIDMLB, seasonYear)
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// Standings fetches both leagues' regular-season standings (103 = AL,
// 104 = NL) for W/L records and games played.
func (c *Client) Standings(ctx context.Context, seasonYear int) (*standingsResponse, *sportsdata.Fetch, error) {
	var parsed standingsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/standings?leagueId=103,104&season=%d", seasonYear), &parsed)
	return &parsed, fetch, err
}

// get performs one request and decodes the JSON body into v. The returned
// Fetch always carries the raw body when one was read, so failed calls are
// still archived.
func (c *Client) get(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "mlb.get")
	defer span.End()
	span.SetAttributes(attribute.String("mlb.path", path))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response %s: %w", path, err)
	}

	fetch := &sportsdata.Fetch{
		Endpoint:   path,
		Body:       body,
		HTTPStatus: resp.StatusCode,
		CapturedAt: time.Now().UTC(),
	}

	if resp.StatusCode != http.StatusOK {
		return fetch, fmt.Errorf("mlb statsapi returned %d on %s", resp.StatusCode, path)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fetch, fmt.Errorf("decode %s response: %w", path, err)
	}
	return fetch, nil
}
