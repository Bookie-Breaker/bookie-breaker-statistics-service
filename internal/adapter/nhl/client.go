// Package nhl fetches NHL data from the official NHL APIs (free, keyless —
// ADR-026). Two hosts serve complementary data: the web API
// (api-web.nhle.com) provides the weekly schedule, the per-date scoreboard
// (with per-goal detail for period scores), and the league standings; the
// stats REST API (api.nhle.com/stats/rest/en) provides the team-summary
// season stats behind the HockeyStats block and the team reference list that
// maps numeric team ids to tricodes.
//
// Routes were verified against the live API on 2026-07-06, which serves the
// completed 2025-26 season and full historical archive even in the July
// off-season; real trimmed responses are recorded as testdata fixtures.
// NHL markets settle on the final score including overtime and the shootout
// (a shootout win counts as one goal in the final per NHL convention — the
// score is never stripped), so no regulation-score fields are set (ADR-027).
package nhl

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

var tracer = otel.Tracer("nhl-client")

// Client fetches NHL data from the web API and the stats REST API.
type Client struct {
	webBaseURL   string
	statsBaseURL string
	httpClient   *http.Client
}

// NewClient creates an NHL client. webBaseURL is the api-web.nhle.com host;
// statsBaseURL is the api.nhle.com/stats/rest/en host.
func NewClient(webBaseURL, statsBaseURL string, timeout time.Duration) *Client {
	return &Client{
		webBaseURL:   webBaseURL,
		statsBaseURL: statsBaseURL,
		httpClient:   &http.Client{Timeout: timeout},
	}
}

// Standings fetches the current standings (api-web /v1/standings/now): one
// row per team with names, tricodes, conference/division, W/L records, and
// goals for/against. Verified live 2026-07-06: /now serves the completed
// 2025-26 season's final standings in the off-season.
func (c *Client) Standings(ctx context.Context) (*standingsResponse, *sportsdata.Fetch, error) {
	var parsed standingsResponse
	fetch, err := c.getWeb(ctx, "/v1/standings/now", &parsed)
	return &parsed, fetch, err
}

// Schedule fetches one week of games starting at date (api-web
// /v1/schedule/{date}); the response's gameWeek carries seven days of games.
// Season schedules are built by walking the season a week at a time.
func (c *Client) Schedule(ctx context.Context, date time.Time) (*scheduleResponse, *sportsdata.Fetch, error) {
	var parsed scheduleResponse
	path := fmt.Sprintf("/v1/schedule/%s", date.UTC().Format("2006-01-02"))
	fetch, err := c.getWeb(ctx, path, &parsed)
	return &parsed, fetch, err
}

// Score fetches one date's games with per-goal detail (api-web
// /v1/score/{date}); the goals array drives per-period scores in the
// watcher path.
func (c *Client) Score(ctx context.Context, date time.Time) (*scoreResponse, *sportsdata.Fetch, error) {
	var parsed scoreResponse
	path := fmt.Sprintf("/v1/score/%s", date.UTC().Format("2006-01-02"))
	fetch, err := c.getWeb(ctx, path, &parsed)
	return &parsed, fetch, err
}

// TeamSummary fetches league-wide team season stats (stats REST
// /team/summary) for one season and game type (2 = regular season). One call
// returns all teams with goals/shots per game and special-teams percentages.
func (c *Client) TeamSummary(ctx context.Context, seasonID, gameType int) (*teamSummaryResponse, *sportsdata.Fetch, error) {
	var parsed teamSummaryResponse
	// The cayenne filter's spaces are percent-encoded (matching the verified
	// live call); the "=" signs stay literal, as the API expects.
	path := fmt.Sprintf("/team/summary?cayenneExp=seasonId=%d%%20and%%20gameTypeId=%d", seasonID, gameType)
	fetch, err := c.getStats(ctx, path, &parsed)
	return &parsed, fetch, err
}

// TeamRef fetches the team reference list (stats REST /team): numeric id,
// full name, and tricode for every franchise. It anchors the tricode-keyed
// canonical ids the summary stats (which carry only numeric ids) resolve to.
func (c *Client) TeamRef(ctx context.Context) (*teamRefResponse, *sportsdata.Fetch, error) {
	var parsed teamRefResponse
	fetch, err := c.getStats(ctx, "/team", &parsed)
	return &parsed, fetch, err
}

func (c *Client) getWeb(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	return c.get(ctx, c.webBaseURL+path, path, v)
}

func (c *Client) getStats(ctx context.Context, path string, v any) (*sportsdata.Fetch, error) {
	return c.get(ctx, c.statsBaseURL+path, path, v)
}

// get performs one request and decodes the JSON body into v. The returned
// Fetch always carries the raw body when one was read, so failed calls are
// still archived. endpoint is the archival label (path only, no host).
func (c *Client) get(ctx context.Context, url, endpoint string, v any) (*sportsdata.Fetch, error) {
	ctx, span := tracer.Start(ctx, "nhl.get")
	defer span.End()
	span.SetAttributes(attribute.String("nhl.path", endpoint))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response %s: %w", endpoint, err)
	}

	fetch := &sportsdata.Fetch{
		Endpoint:   endpoint,
		Body:       body,
		HTTPStatus: resp.StatusCode,
		CapturedAt: time.Now().UTC(),
	}

	if resp.StatusCode != http.StatusOK {
		return fetch, fmt.Errorf("nhl returned %d on %s", resp.StatusCode, endpoint)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fetch, fmt.Errorf("decode %s response: %w", endpoint, err)
	}
	return fetch, nil
}
