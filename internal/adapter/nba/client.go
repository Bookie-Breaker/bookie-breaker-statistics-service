// Package nba fetches data from the undocumented stats.nba.com endpoints
// (the same ones the nba_api Python package wraps; see ADR-020). Everything
// NBA-specific lives in this package so seasonal endpoint breakage stays
// contained here.
package nba

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/adapter/sportsdata"
	"github.com/Bookie-Breaker/bookie-breaker-statistics-service/internal/httpx"
)

var tracer = otel.Tracer("nba-client")

// Fetch carries the raw response of one upstream call for archival. It is
// the shared sportsdata envelope; the alias keeps this package's existing
// call sites unchanged.
type Fetch = sportsdata.Fetch

// Client is a throttled, circuit-broken stats.nba.com client.
type Client struct {
	baseURL    string
	userAgent  string
	httpClient *http.Client
	throttle   *httpx.Throttle
	breaker    *httpx.Breaker
}

// Options configures the client.
type Options struct {
	BaseURL            string
	UserAgent          string
	MinRequestInterval time.Duration
	Timeout            time.Duration
	FailureThreshold   int
	OpenDuration       time.Duration
}

// NewClient creates a stats.nba.com client.
func NewClient(opts Options) *Client {
	return &Client{
		baseURL:    opts.BaseURL,
		userAgent:  opts.UserAgent,
		httpClient: &http.Client{Timeout: opts.Timeout},
		throttle:   httpx.NewThrottle(opts.MinRequestInterval),
		breaker:    httpx.NewBreaker(opts.FailureThreshold, opts.OpenDuration),
	}
}

// get performs one throttled request and decodes the JSON body into v.
// The returned Fetch always carries the raw body when one was read.
func (c *Client) get(ctx context.Context, path string, params url.Values, v any) (*Fetch, error) {
	ctx, span := tracer.Start(ctx, "nba.get")
	defer span.End()
	span.SetAttributes(attribute.String("nba.endpoint", path))

	if !c.breaker.Allow() {
		return nil, httpx.ErrCircuitOpen
	}
	if err := c.throttle.Wait(ctx); err != nil {
		return nil, err
	}

	reqURL := c.baseURL + path
	if len(params) > 0 {
		reqURL += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	// Header parity with nba_api: stats.nba.com rejects non-browser clients.
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Referer", "https://www.nba.com/")
	req.Header.Set("Origin", "https://www.nba.com")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("x-nba-stats-origin", "stats")
	req.Header.Set("x-nba-stats-token", "true")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.breaker.Failure()
		return nil, fmt.Errorf("execute request %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.breaker.Failure()
		return nil, fmt.Errorf("read response %s: %w", path, err)
	}

	fetch := &Fetch{
		Endpoint:   path,
		Body:       body,
		HTTPStatus: resp.StatusCode,
		CapturedAt: time.Now().UTC(),
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		c.breaker.Failure()
		retryAfter := resp.Header.Get("Retry-After")
		if seconds, parseErr := strconv.Atoi(retryAfter); parseErr == nil && seconds > 0 && seconds <= 60 {
			slog.Warn("stats.nba.com rate limited, honoring Retry-After", "seconds", seconds)
			timer := time.NewTimer(time.Duration(seconds) * time.Second)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return fetch, ctx.Err()
			case <-timer.C:
			}
		}
		return fetch, fmt.Errorf("stats.nba.com rate limited (429) on %s", path)
	}

	if resp.StatusCode != http.StatusOK {
		c.breaker.Failure()
		return fetch, fmt.Errorf("stats.nba.com returned %d on %s", resp.StatusCode, path)
	}

	if err := json.Unmarshal(body, v); err != nil {
		c.breaker.Failure()
		return fetch, fmt.Errorf("decode %s response: %w", path, err)
	}

	c.breaker.Success()
	return fetch, nil
}
