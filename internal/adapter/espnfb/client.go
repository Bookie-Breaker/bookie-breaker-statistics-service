// Package espnfb fetches American football data from ESPN's public site API
// (ADR-026), shared by the NFL and NCAA_FB adapters through a per-league
// config (the soccer adapter's competition-config pattern generalized to
// football). The API is free, keyless JSON-over-REST; its shapes were
// verified against ESPN's archive of the completed 2025 season (regular
// season, overtime, the real GB–DAL 40-40 tie, preseason, and the January
// 2026 playoffs) and recorded as testdata fixtures in the nfl and cfbd
// adapters.
package espnfb

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

var tracer = otel.Tracer("espnfb-client")

const (
	// teamsLimit exceeds the 755 college football teams returned live
	// (the NFL list is 32).
	teamsLimit = 900

	// scoreboardLimit caps events per scoreboard call well above any chunk
	// of the schedule walk: the busiest observed range was a college
	// football Saturday with 53 FBS events (verified live), and ESPN's
	// observed hard response cap is 1000.
	scoreboardLimit = 500
)

// Client fetches ESPN football data for any league by sport path segment
// (e.g. "football/nfl", "football/college-football").
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an ESPN football client on the shared ESPN base URL.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Teams fetches the league's team list.
func (c *Client) Teams(ctx context.Context, path string) (*TeamsResponse, *sportsdata.Fetch, error) {
	var parsed TeamsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/site/v2/sports/%s/teams?limit=%d", path, teamsLimit), &parsed)
	return &parsed, fetch, err
}

// Scoreboard fetches events for a date range (inclusive; a single day when
// from == to). Completed events carry per-quarter linescores directly on the
// scoreboard (verified live — no summary call needed, unlike ESPN soccer).
func (c *Client) Scoreboard(ctx context.Context, path string, from, to time.Time) (*ScoreboardResponse, *sportsdata.Fetch, error) {
	dates := from.UTC().Format("20060102")
	if !to.UTC().Truncate(24 * time.Hour).Equal(from.UTC().Truncate(24 * time.Hour)) {
		dates += "-" + to.UTC().Format("20060102")
	}
	p := fmt.Sprintf("/apis/site/v2/sports/%s/scoreboard?dates=%s&limit=%d", path, dates, scoreboardLimit)

	var parsed ScoreboardResponse
	fetch, err := c.get(ctx, p, &parsed)
	return &parsed, fetch, err
}

// Standings fetches the league standings for a season: per-team W/L(/T) and
// points for/against. Note the /apis/v2 prefix (same as soccer; the
// /apis/site/v2 route returns an empty object). Verified live: season=2025
// returns the completed 2025 season even in the July off-season.
func (c *Client) Standings(ctx context.Context, path string, seasonYear int) (*StandingsResponse, *sportsdata.Fetch, error) {
	var parsed StandingsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/v2/sports/%s/standings?season=%d", path, seasonYear), &parsed)
	return &parsed, fetch, err
}

// get performs one request and decodes the JSON body into v. The returned
// Fetch always carries the raw body when one was read, so failed calls are
// still archived.
func (c *Client) get(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "espnfb.get")
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
