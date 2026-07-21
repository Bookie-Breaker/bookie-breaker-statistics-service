package httpx

import (
	"context"
	"testing"
	"time"
)

func TestThrottleFirstCallIsImmediate(t *testing.T) {
	th := NewThrottle(50 * time.Millisecond)

	start := time.Now()
	if err := th.Wait(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 25*time.Millisecond {
		t.Errorf("first call took %v, want near-immediate", elapsed)
	}
}

func TestThrottleSecondCallIsDelayed(t *testing.T) {
	interval := 40 * time.Millisecond
	th := NewThrottle(interval)

	if err := th.Wait(context.Background()); err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	start := time.Now()
	if err := th.Wait(context.Background()); err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	elapsed := time.Since(start)

	if elapsed < interval-15*time.Millisecond {
		t.Errorf("second call returned after %v, want roughly >= %v", elapsed, interval)
	}
	if elapsed > interval*5 {
		t.Errorf("second call took too long: %v", elapsed)
	}
}

// TestThrottleFastPathContextCancelled covers the branch where wait is
// already zero (nothing to space out yet), so Wait returns ctx.Err()
// directly without ever starting a timer.
func TestThrottleFastPathContextCancelled(t *testing.T) {
	th := NewThrottle(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err := th.Wait(ctx)
	if err != context.Canceled {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 25*time.Millisecond {
		t.Errorf("fast path took %v, want near-immediate", elapsed)
	}
}

// TestThrottleContextCancelledWhileWaiting covers the select's ctx.Done()
// branch: the second call must actually wait, and the context is canceled
// before the throttle interval elapses.
func TestThrottleContextCancelledWhileWaiting(t *testing.T) {
	interval := 200 * time.Millisecond
	th := NewThrottle(interval)

	if err := th.Wait(context.Background()); err != nil {
		t.Fatalf("unexpected error on first call: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := th.Wait(ctx)
	elapsed := time.Since(start)

	if err != context.DeadlineExceeded {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed >= interval {
		t.Errorf("expected cancellation to cut the wait short, took %v (interval %v)", elapsed, interval)
	}
}
