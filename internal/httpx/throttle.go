// Package httpx holds small HTTP client utilities: a minimum-interval
// throttle and a circuit breaker, both used by external API adapters.
package httpx

import (
	"context"
	"sync"
	"time"
)

// Throttle enforces a minimum interval between calls across goroutines.
// stats.nba.com rate-limits aggressively; serializing requests behind a
// spacing gate keeps the service under the radar.
type Throttle struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

// NewThrottle creates a throttle with the given minimum interval.
func NewThrottle(interval time.Duration) *Throttle {
	return &Throttle{interval: interval}
}

// Wait blocks until the caller may proceed or the context is done.
func (t *Throttle) Wait(ctx context.Context) error {
	t.mu.Lock()
	now := time.Now()
	wait := t.next.Sub(now)
	if wait < 0 {
		wait = 0
	}
	t.next = now.Add(wait + t.interval)
	t.mu.Unlock()

	if wait == 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
