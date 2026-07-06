// Package cfbd serves NCAA Division I FBS football (ADR-026) from two sources.
// Real-time data — teams, schedule, and the live scoreboard — comes from
// ESPN's shared football site API (football/college-football) through the
// espnfb client. Season stats — win/loss records, SP+ ratings, and scoring —
// come from the CollegeFootballData API (https://api.collegefootballdata.com),
// which requires a free API key sent as "Authorization: Bearer $CFBD_API_KEY".
//
// The project has no CFBD key yet, so the CFBD request/response shapes here are
// built against the public CFBD OpenAPI document (recorded 2026-07-06) and the
// season-stats fixtures are DERIVED; both are marked for validation against the
// live API in the September verification session. CFBD is NEVER called for live
// scores (ESPN owns scoring). When the key is unset the season-stats path
// degrades gracefully — clear log, empty stats, no crash — and the league runs
// watcher-only off ESPN.
package cfbd

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

var tracer = otel.Tracer("cfbd-client")

// Client fetches CollegeFootballData season stats. The API key is optional; an
// empty key disables the client (Enabled reports false) so the provider skips
// the season-stats path entirely.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a CFBD client. An empty apiKey leaves the client disabled.
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a CFBD API key is configured. When false the
// provider serves NCAA_FB watcher-only (empty season stats).
func (c *Client) Enabled() bool { return c.apiKey != "" }

// Records fetches a season's win/loss records (CFBD /records).
func (c *Client) Records(ctx context.Context, seasonYear int) ([]TeamRecords, *sportsdata.Fetch, error) {
	var parsed []TeamRecords
	fetch, err := c.get(ctx, fmt.Sprintf("/records?year=%d", seasonYear), &parsed)
	return parsed, fetch, err
}

// SPRatings fetches a season's SP+ ratings (CFBD /ratings/sp).
func (c *Client) SPRatings(ctx context.Context, seasonYear int) ([]TeamSP, *sportsdata.Fetch, error) {
	var parsed []TeamSP
	fetch, err := c.get(ctx, fmt.Sprintf("/ratings/sp?year=%d", seasonYear), &parsed)
	return parsed, fetch, err
}

// SeasonStats fetches a season's team stat lines (CFBD /stats/season), the
// source of the scoring totals behind points_for/against per game.
func (c *Client) SeasonStats(ctx context.Context, seasonYear int) ([]TeamStat, *sportsdata.Fetch, error) {
	var parsed []TeamStat
	fetch, err := c.get(ctx, fmt.Sprintf("/stats/season?year=%d", seasonYear), &parsed)
	return parsed, fetch, err
}

// get performs one authenticated request and decodes the JSON body into v. The
// returned Fetch always carries the raw body when one was read, so failed
// calls are still archived.
func (c *Client) get(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "cfbd.get")
	defer span.End()
	span.SetAttributes(attribute.String("cfbd.path", path))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

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
		return fetch, fmt.Errorf("cfbd returned %d on %s", resp.StatusCode, path)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fetch, fmt.Errorf("decode %s response: %w", path, err)
	}
	return fetch, nil
}
