package httpx

import (
	"errors"
	"sync"
	"time"
)

// ErrCircuitOpen is returned when the breaker is rejecting calls.
var ErrCircuitOpen = errors.New("circuit breaker open")

// Breaker is a minimal circuit breaker: it opens after N consecutive
// failures, rejects calls while open, and allows a single probe once the
// open window has elapsed (half-open).
type Breaker struct {
	mu        sync.Mutex
	threshold int
	openFor   time.Duration

	failures int
	openedAt time.Time
	probing  bool
}

// NewBreaker creates a breaker that opens after threshold consecutive
// failures for the openFor duration.
func NewBreaker(threshold int, openFor time.Duration) *Breaker {
	return &Breaker{threshold: threshold, openFor: openFor}
}

// Allow reports whether a call may proceed. Callers must report the outcome
// with Success or Failure.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.failures < b.threshold {
		return true
	}
	if time.Since(b.openedAt) < b.openFor {
		return false
	}
	// Half-open: let one probe through at a time.
	if b.probing {
		return false
	}
	b.probing = true
	return true
}

// Success records a successful call and closes the breaker.
func (b *Breaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.probing = false
}

// Failure records a failed call, opening the breaker at the threshold.
func (b *Breaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	b.probing = false
	if b.failures >= b.threshold {
		b.openedAt = time.Now()
	}
}
