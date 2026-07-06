// Package espn fetches injury reports from ESPN's public site API for any
// league with an injuries path (NBA per ADR-008 — stats.nba.com has no
// injury endpoint — and MLB since Phase 6 Wave 2; ADR-026 generalizes the
// client across leagues via per-league sport paths). The adapter is
// best-effort: failures degrade to an empty report, never an error page.
package espn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
)

var tracer = otel.Tracer("espn-client")

// Fetch carries the raw response for archival.
type Fetch struct {
	Endpoint   string
	Body       []byte
	HTTPStatus int
	CapturedAt time.Time
}

// Client fetches ESPN injury data for one sport/league.
type Client struct {
	baseURL      string
	injuriesPath string
	httpClient   *http.Client
}

// NewClient creates an ESPN client. sportPath is the site API's
// sport/league path segment, e.g. "basketball/nba" (ADR-026 generalizes
// this client across leagues).
func NewClient(baseURL, sportPath string, timeout time.Duration) *Client {
	return &Client{
		baseURL:      baseURL,
		injuriesPath: "/apis/site/v2/sports/" + sportPath + "/injuries",
		httpClient:   &http.Client{Timeout: timeout},
	}
}

// Injuries fetches the current injury report.
func (c *Client) Injuries(ctx context.Context) (*injuriesResponse, *Fetch, error) {
	ctx, span := tracer.Start(ctx, "espn.Injuries")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+c.injuriesPath, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("read response: %w", err)
	}

	fetch := &Fetch{
		Endpoint:   c.injuriesPath,
		Body:       body,
		HTTPStatus: resp.StatusCode,
		CapturedAt: time.Now().UTC(),
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fetch, fmt.Errorf("espn returned %d", resp.StatusCode)
	}

	var parsed injuriesResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fetch, fmt.Errorf("decode response: %w", err)
	}
	return &parsed, fetch, nil
}
