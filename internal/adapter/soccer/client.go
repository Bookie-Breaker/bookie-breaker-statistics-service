// Package soccer fetches soccer data from ESPN's public site API (ADR-026),
// serving every configured competition (FIFA_WC, EPL) through one
// competition-config-driven provider. The API is free, keyless
// JSON-over-REST; its shapes were verified against the live 2026 World Cup
// and recorded as testdata fixtures.
package soccer

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

var tracer = otel.Tracer("soccer-espn-client")

// scoreboardLimit caps events per scoreboard call well above any month of
// fixtures (ESPN's default page size is smaller than a full EPL month).
const scoreboardLimit = 400

// Client fetches ESPN soccer data for any competition by league code.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an ESPN soccer client on the shared ESPN base URL.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Teams fetches the competition's team list. Always dynamic — the 2026
// World Cup has 48 teams and club leagues change by promotion/relegation.
func (c *Client) Teams(ctx context.Context, code string) (*teamsResponse, *sportsdata.Fetch, error) {
	var parsed teamsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/site/v2/sports/soccer/%s/teams", code), &parsed)
	return &parsed, fetch, err
}

// Scoreboard fetches events for a date range (inclusive; a single day when
// from == to). It carries schedule, live status, and final scores — but no
// per-period linescores; those require Summary (verified live).
func (c *Client) Scoreboard(ctx context.Context, code string, from, to time.Time) (*scoreboardResponse, *sportsdata.Fetch, error) {
	dates := from.UTC().Format("20060102")
	if !to.UTC().Truncate(24 * time.Hour).Equal(from.UTC().Truncate(24 * time.Hour)) {
		dates += "-" + to.UTC().Format("20060102")
	}
	path := fmt.Sprintf("/apis/site/v2/sports/soccer/%s/scoreboard?dates=%s&limit=%d", code, dates, scoreboardLimit)

	var parsed scoreboardResponse
	fetch, err := c.get(ctx, path, &parsed)
	return &parsed, fetch, err
}

// Summary fetches one event's detail page; its header carries the
// per-period linescores needed to split regulation from extra time.
func (c *Client) Summary(ctx context.Context, code, eventID string) (*summaryResponse, *sportsdata.Fetch, error) {
	var parsed summaryResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/site/v2/sports/soccer/%s/summary?event=%s", code, eventID), &parsed)
	return &parsed, fetch, err
}

// Roster fetches one team's player list (Phase 7 soccer player surfaces).
func (c *Client) Roster(ctx context.Context, code, teamID string) (*rosterResponse, *sportsdata.Fetch, error) {
	var parsed rosterResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/site/v2/sports/soccer/%s/teams/%s/roster", code, teamID), &parsed)
	return &parsed, fetch, err
}

// Standings fetches the competition table(s) for a season: per-team W/D/L,
// goals for/against, and games played. Note the /apis/v2 prefix — the
// /apis/site/v2 standings route returns an empty object (verified live).
func (c *Client) Standings(ctx context.Context, code string, seasonYear int) (*standingsResponse, *sportsdata.Fetch, error) {
	var parsed standingsResponse
	fetch, err := c.get(ctx, fmt.Sprintf("/apis/v2/sports/soccer/%s/standings?season=%d", code, seasonYear), &parsed)
	return &parsed, fetch, err
}

// get performs one request and decodes the JSON body into v. The returned
// Fetch always carries the raw body when one was read, so failed calls are
// still archived.
func (c *Client) get(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "soccer.get")
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
