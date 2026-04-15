// Package engine — H3 regression coverage for graceful shutdown of
// per-session HotColdSplitters and in-flight knowledge extractions.
//
// runServe's previous shutdown path called only http.Server.Shutdown,
// which waits for active HTTP handlers to return but does nothing
// about engine-owned goroutines: session splitters' persist workers
// and L3 extraction goroutines were killed mid-flight at process
// exit, orphaning `.tmp` files on disk. After H3 the engine has a
// Shutdown(ctx) that drains both, and runServe calls it after
// server.Shutdown.
package engine_test

import (
	"context"
	"testing"
	"time"
)

// TestEngine_Shutdown_StopsAllSessionSplitters builds splitters for
// multiple sessions, then calls Shutdown. After the call the cache
// must be empty and all splitters Stopped (their persist workers
// joined). Subsequent SessionSplitterForTest lookups return nil.
func TestEngine_Shutdown_StopsAllSessionSplitters(t *testing.T) {
	t.Parallel()

	eng, _ := newMicroCompactionTestEngine(t)
	ctx := context.Background()

	sessions := []string{"h3-session-a", "h3-session-b", "h3-session-c"}
	for _, s := range sessions {
		eng.BuildContextWindowForTesting(ctx, s, "seed")
	}
	for _, s := range sessions {
		if eng.SessionSplitterForTest(s) == nil {
			t.Fatalf("splitter for %q missing before Shutdown", s)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := eng.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	for _, s := range sessions {
		if got := eng.SessionSplitterForTest(s); got != nil {
			t.Errorf("splitter for %q still present after Shutdown (%p)", s, got)
		}
	}
}

// TestEngine_Shutdown_IsIdempotent proves calling Shutdown twice does
// not panic on the second call and returns the same nil error.
func TestEngine_Shutdown_IsIdempotent(t *testing.T) {
	t.Parallel()

	eng, _ := newMicroCompactionTestEngine(t)
	ctx := context.Background()
	eng.BuildContextWindowForTesting(ctx, "h3-idempotent", "seed")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := eng.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("first Shutdown: %v", err)
	}
	if err := eng.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

// TestEngine_Shutdown_WithNoSplitters_NoError covers the common
// case where micro-compaction is disabled or no session has built a
// window: Shutdown must still succeed cleanly.
func TestEngine_Shutdown_WithNoSplitters_NoError(t *testing.T) {
	t.Parallel()

	eng, _ := newMicroCompactionTestEngine(t)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	if err := eng.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown on empty engine returned %v", err)
	}
}
