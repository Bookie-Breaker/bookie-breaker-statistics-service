// Package cbbd serves NCAA Division I men's college basketball (ADR-026) from
// two sources. Real-time data — teams, schedule, and the live scoreboard —
// comes from ESPN's shared basketball site API (basketball/mens-college-
// basketball) through the espnbb client. Season stats — win/loss records,
// offensive/defensive/advanced blocks, and opponent-adjusted efficiencies —
// come from the CollegeBasketballData API (https://api.collegebasketballdata.com),
// which requires a free API key sent as "Authorization: Bearer $CBBD_API_KEY".
//
// The project has no CBBD key yet, so the CBBD request/response shapes here are
// built against the public CBBD OpenAPI document (recorded 2026-07-06) and the
// season-stats fixtures are DERIVED; both are marked for validation against the
// live API in the November verification session. CBBD is NEVER called for live
// scores (ESPN owns scoring). When the key is unset the season-stats path
// degrades gracefully — clear log, empty stats, no crash — and the league runs
// watcher-only off ESPN.
package cbbd

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

var tracer = otel.Tracer("cbbd-client")

// Client fetches CollegeBasketballData season stats. The API key is optional;
// an empty key disables the client (Enabled reports false) so the provider
// skips the season-stats path entirely.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// NewClient creates a CBBD client. An empty apiKey leaves the client disabled.
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Enabled reports whether a CBBD API key is configured. When false the
// provider serves NCAA_BB watcher-only (empty season stats).
func (c *Client) Enabled() bool { return c.apiKey != "" }

// SeasonStats fetches a season's team stat lines (CBBD /stats/team/season):
// records, pace, and the offense/defense unit stats behind the basketball stat
// blocks. season is the CBBD season value (the season's ending year).
func (c *Client) SeasonStats(ctx context.Context, season int) ([]TeamSeasonStats, *sportsdata.Fetch, error) {
	var parsed []TeamSeasonStats
	fetch, err := c.get(ctx, fmt.Sprintf("/stats/team/season?season=%d", season), &parsed)
	return parsed, fetch, err
}

// AdjustedRatings fetches a season's opponent-adjusted efficiency ratings
// (CBBD /ratings/adjusted), the source of adjusted_efficiency_margin.
func (c *Client) AdjustedRatings(ctx context.Context, season int) ([]AdjustedEfficiencyInfo, *sportsdata.Fetch, error) {
	var parsed []AdjustedEfficiencyInfo
	fetch, err := c.get(ctx, fmt.Sprintf("/ratings/adjusted?season=%d", season), &parsed)
	return parsed, fetch, err
}

// get performs one authenticated request and decodes the JSON body into v. The
// returned Fetch always carries the raw body when one was read, so failed
// calls are still archived.
func (c *Client) get(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "cbbd.get")
	defer span.End()
	span.SetAttributes(attribute.String("cbbd.path", path))

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
		return fetch, fmt.Errorf("cbbd returned %d on %s", resp.StatusCode, path)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fetch, fmt.Errorf("decode %s response: %w", path, err)
	}
	return fetch, nil
}
