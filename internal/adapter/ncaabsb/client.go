// Package ncaabsb fetches NCAA Division I baseball data from ESPN's public
// site API (baseball/college-baseball; ADR-026). The league ships dormant —
// its season resumes February 2027 — so the adapter is deliberately minimal:
// teams, schedule/scoreboard, and runs-for/against team stats. Shapes were
// verified live on 2026-07-05: the off-season scoreboard fixture is today's
// real (empty) response, and the completed-game fixtures come from ESPN's
// archive of the 2026 season (May regular season and the June College World
// Series) — no derived fixtures.
package ncaabsb

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

var tracer = otel.Tracer("ncaabsb-espn-client")

const (
	// espnPath is the site API sport/league path segment.
	espnPath = "baseball/college-baseball"

	// teamsLimit exceeds the 437 Division I teams returned live.
	teamsLimit = 900

	// scoreboardLimit is ESPN's observed response cap: a month-ranged
	// request returned exactly 1000 events (truncated), so schedule fetches
	// chunk by week (busiest observed day was 95 games, so a week stays
	// well under the cap).
	scoreboardLimit = 1000
)

// Client fetches ESPN college baseball data.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an ESPN college baseball client on the shared ESPN base
// URL.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Teams fetches the Division I team list.
func (c *Client) Teams(ctx context.Context) (*teamsResponse, *sportsdata.Fetch, error) {
	var parsed teamsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/site/v2/sports/%s/teams?limit=%d", espnPath, teamsLimit), &parsed)
	return &parsed, fetch, err
}

// Scoreboard fetches events for a date range (inclusive; a single day when
// from == to). Completed events carry per-inning linescores directly on the
// scoreboard (verified live — unlike ESPN soccer, no summary call needed).
func (c *Client) Scoreboard(ctx context.Context, from, to time.Time) (*scoreboardResponse, *sportsdata.Fetch, error) {
	dates := from.UTC().Format("20060102")
	if !to.UTC().Truncate(24 * time.Hour).Equal(from.UTC().Truncate(24 * time.Hour)) {
		dates += "-" + to.UTC().Format("20060102")
	}
	path := fmt.Sprintf("/apis/site/v2/sports/%s/scoreboard?dates=%s&limit=%d", espnPath, dates, scoreboardLimit)

	var parsed scoreboardResponse
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// Standings fetches the Division I standings for a season: per-team W/L,
// runs for/against, and games played. Note the /apis/v2 prefix (same as
// soccer); verified live — season=2026 returns the completed 2026 season
// even in the July off-season.
func (c *Client) Standings(ctx context.Context, seasonYear int) (*standingsResponse, *sportsdata.Fetch, error) {
	var parsed standingsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/v2/sports/%s/standings?season=%d", espnPath, seasonYear), &parsed)
	return &parsed, fetch, err
}

// get performs one request and decodes the JSON body into v. The returned
// Fetch always carries the raw body when one was read, so failed calls are
// still archived.
func (c *Client) get(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "ncaabsb.get")
	defer span.End()
	span.SetAttributes(attribute.String("espn.path", path))

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
		return fetch, fmt.Errorf("espn returned %d on %s", resp.StatusCode, path)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fetch, fmt.Errorf("decode %s response: %w", path, err)
	}
	return fetch, nil
}
