// Package espn fetches NBA injury reports from ESPN's public site API.
// stats.nba.com has no injury endpoint (the official report is a PDF);
// ADR-008 names ESPN as the injuries fallback source. The adapter is
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

const injuriesPath = "/apis/site/v2/sports/basketball/nba/injuries"

// Fetch carries the raw response for archival.
type Fetch struct {
	Endpoint   string
	Body       []byte
	HTTPStatus int
	CapturedAt time.Time
}

// Client fetches ESPN injury data.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates an ESPN client.
func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Injuries fetches the current NBA injury report.
func (c *Client) Injuries(ctx context.Context) (*injuriesResponse, *Fetch, error) {
	ctx, span := tracer.Start(ctx, "espn.Injuries")
	defer span.End()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+injuriesPath, nil)
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
		Endpoint:   injuriesPath,
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
