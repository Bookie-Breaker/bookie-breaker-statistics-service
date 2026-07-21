// Package telemetry tests exercise Init/Shutdown against a bogus, unreachable
// OTLP gRPC endpoint. The gRPC exporters dial lazily, so construction
// succeeds without a live collector; only Shutdown (which flushes) actually
// attempts a connection, and it is expected to fail there. That failure is
// tolerated -- the assertion is that Init succeeds, returns a working
// Shutdown func, and Shutdown returns (an error, but no panic/hang) within
// the caller-supplied context.
package telemetry

import (
	"context"
	"testing"
	"time"
)

func TestInitConstructsProvidersWithoutALiveCollector(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	shutdown, err := Init(ctx, "test-service", "127.0.0.1:4319")
	if err != nil {
		t.Fatalf("Init unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned a nil Shutdown func")
	}

	sctx, scancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer scancel()

	// The bogus endpoint has nothing listening, so Shutdown's attempt to
	// flush the metric reader is expected to fail; asserting it returns
	// within our short context (rather than hanging or panicking) is the
	// meaningful, reliable behavior here.
	done := make(chan error, 1)
	go func() { done <- shutdown(sctx) }()

	select {
	case shutdownErr := <-done:
		if shutdownErr == nil {
			t.Log("shutdown succeeded unexpectedly quickly against a bogus endpoint; that's fine")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not return within the expected bound")
	}
}

// TestInitReturnValueInvariant checks Init's error/return contract holds
// even against an already-canceled context: a non-nil error must never come
// paired with a usable Shutdown func, and vice versa. resource.New does no
// network I/O, so this does not panic or block regardless of ctx state.
func TestInitReturnValueInvariant(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	shutdown, err := Init(ctx, "test-service", "127.0.0.1:4319")
	if err != nil {
		if shutdown != nil {
			t.Error("expected a nil Shutdown func when Init returns an error")
		}
		return
	}
	if shutdown == nil {
		t.Fatal("expected a non-nil Shutdown func when Init returns no error")
	}
}
