package httpx

import (
	"sync"
	"testing"
	"time"
)

func TestBreakerAllowsUnderThreshold(t *testing.T) {
	b := NewBreaker(3, 50*time.Millisecond)
	for i := 0; i < 2; i++ {
		if !b.Allow() {
			t.Fatalf("call %d: expected Allow to be true below threshold", i)
		}
		b.Failure()
	}
	if !b.Allow() {
		t.Error("expected breaker to remain closed below threshold")
	}
}

func TestBreakerOpensAtThreshold(t *testing.T) {
	b := NewBreaker(2, 50*time.Millisecond)
	b.Failure()
	b.Failure()

	if b.Allow() {
		t.Fatal("expected breaker to reject calls once the failure threshold is reached")
	}
}

func TestBreakerHalfOpenSingleProbe(t *testing.T) {
	openFor := 15 * time.Millisecond
	b := NewBreaker(1, openFor)
	b.Failure() // opens the breaker

	if b.Allow() {
		t.Fatal("expected breaker to be open immediately after crossing threshold")
	}

	time.Sleep(openFor + 15*time.Millisecond)

	if !b.Allow() {
		t.Fatal("expected a single probe to be allowed once openFor has elapsed")
	}
	if b.Allow() {
		t.Fatal("expected a second concurrent probe to be rejected while one is in flight")
	}
}

func TestBreakerProbeFailureReopens(t *testing.T) {
	openFor := 15 * time.Millisecond
	b := NewBreaker(1, openFor)
	b.Failure()
	time.Sleep(openFor + 15*time.Millisecond)

	if !b.Allow() {
		t.Fatal("expected probe to be allowed")
	}
	b.Failure() // probe fails, breaker re-opens

	if b.Allow() {
		t.Fatal("expected breaker to re-open after a failed probe")
	}
}

func TestBreakerSuccessResetsAndCloses(t *testing.T) {
	openFor := 15 * time.Millisecond
	b := NewBreaker(1, openFor)
	b.Failure()
	time.Sleep(openFor + 15*time.Millisecond)

	if !b.Allow() {
		t.Fatal("expected probe to be allowed")
	}
	b.Success()

	if b.failures != 0 {
		t.Errorf("failures = %d, want 0 after Success", b.failures)
	}
	if !b.Allow() {
		t.Error("expected breaker closed after a successful probe")
	}
}

// TestBreakerConcurrentAccess exercises Allow/Failure from many goroutines at
// once. It asserts on outcomes rather than exact counts, since the breaker
// intentionally allows a benign TOCTOU race between the threshold check and
// the increment (a handful of extra failures may land before it opens).
func TestBreakerConcurrentAccess(t *testing.T) {
	const threshold = 3
	b := NewBreaker(threshold, time.Minute)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if b.Allow() {
				b.Failure()
			}
		}()
	}
	wg.Wait()

	b.mu.Lock()
	failures := b.failures
	b.mu.Unlock()

	if failures < threshold {
		t.Fatalf("failures = %d, want >= threshold %d after concurrent failures", failures, threshold)
	}
	if b.Allow() {
		t.Error("expected breaker to be open once the threshold was reached concurrently")
	}
}
