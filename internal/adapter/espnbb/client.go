// Package espnbb fetches basketball data from ESPN's public site API
// (ADR-026), the real-time source for the NCAA_BB adapter. It mirrors the
// espnfb football client (same keyless JSON-over-REST site API) but differs
// in two basketball-specific ways: regulation is two halves rather than four
// quarters (overtime is period 3+), and the college-basketball scoreboard
// rejects ranged date queries (verified live 2026-07-06 — a "dates=A-B" range
// returns zero events), so the scoreboard is fetched one day at a time.
//
// Shapes were verified against ESPN's archive of the completed 2025-26 season
// and recorded as testdata fixtures in the cbbd adapter.
package espnbb

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

var tracer = otel.Tracer("espnbb-client")

const (
	// teamsLimit exceeds the ~360 Division I men's basketball teams.
	teamsLimit = 500

	// scoreboardLimit caps events per single-day scoreboard call well above
	// any observed college-basketball slate.
	scoreboardLimit = 500
)

// Client fetches ESPN basketball data for any league by sport path segment
// (e.g. "basketball/mens-college-basketball").
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an ESPN basketball client on the shared ESPN base URL.
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

// Scoreboard fetches one date's events. Unlike the football client this takes
// a single day: the college-basketball scoreboard returns nothing for ranged
// date queries (verified live).
func (c *Client) Scoreboard(ctx context.Context, path string, date time.Time) (*ScoreboardResponse, *sportsdata.Fetch, error) {
	p := fmt.Sprintf("/apis/site/v2/sports/%s/scoreboard?dates=%s&limit=%d", path, date.UTC().Format("20060102"), scoreboardLimit)
	var parsed ScoreboardResponse
	fetch, err := c.get(ctx, p, &parsed)
	return &parsed, fetch, err
}

// get performs one request and decodes the JSON body into v. The returned
// Fetch always carries the raw body when one was read, so failed calls are
// still archived.
func (c *Client) get(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "espnbb.get")
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
