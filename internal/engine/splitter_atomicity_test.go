// Package engine — C2 regression coverage for the splitter
// Stop+delete atomicity property.
//
// Before C2 the production close path (handleSessionEnded) and the
// test helper (StopSessionSplitterForTesting) both deleted the cache
// entry under splitterMu but released the lock before calling
// Stop(). Two problems:
//
//  1. Two concurrent closers could both see the splitter in the map,
//     both delete (one is a no-op via the "ok" flag on the second),
//     and both call Stop on the same instance. Stop is guarded by
//     sync.Once so the second is safe, but the pattern makes it
//     hard to reason about invariants during code review.
//  2. A concurrent ensureSessionSplitter racing with a closer sees
//     the map transition from "present" to "absent" at the moment
//     of delete, and correctly constructs a fresh splitter — but
//     the ordering between the close's delete and the close's Stop
//     is observable from outside. A future change that promoted
//     Stop to be non-idempotent would silently break.
//
// After C2 Stop is called inside the critical section. Concurrent
// closers serialise: the first runs Stop to completion; the second
// sees an empty map and returns cleanly. Concurrent ensureSession
// callers either see the live splitter (pre-close) or the empty map
// (post-close) — never a mid-close state.
package engine_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
)

// TestSplitterLifecycle_ConcurrentCloseAndEnsure_NoPanic drives 50
// concurrent goroutines: half call BuildContextWindowForTesting
// (which invokes ensureSessionSplitter), half publish session.ended
// (which drives the close handler). Under the C2 invariant the run
// must complete without panicking on send-to-closed-channel, and
// after the dust settles the final call must construct a fresh
// splitter — proving the close path did in fact evict.
func TestSplitterLifecycle_ConcurrentCloseAndEnsure_NoPanic(t *testing.T) {
	t.Parallel()

	eng, bus := newMicroCompactionTestEngine(t)
	ctx := context.Background()
	sessionID := "session-c2-race"

	// Seed the cache so every early close has something to evict.
	eng.BuildContextWindowForTesting(ctx, sessionID, "seed")

	const workers = 50
	var wg sync.WaitGroup
	wg.Add(workers * 2)

	for range workers {
		go func() {
			defer wg.Done()
			eng.BuildContextWindowForTesting(ctx, sessionID, "hello")
		}()
		go func() {
			defer wg.Done()
			bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
				SessionID: sessionID,
				Action:    "ended",
			}))
		}()
	}
	wg.Wait()

	// One more close pass to ensure the final state is post-evict
	// regardless of scheduler ordering.
	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: sessionID,
		Action:    "ended",
	}))

	if got := eng.SessionSplitterForTest(sessionID); got != nil {
		t.Fatalf("expected splitter cache to be empty after contended close; got %p", got)
	}

	// A fresh Build must reconstruct, not reuse.
	eng.BuildContextWindowForTesting(ctx, sessionID, "hello")
	fresh := eng.SessionSplitterForTest(sessionID)
	if fresh == nil {
		t.Fatalf("Build after contended close did not construct a fresh splitter")
	}
	_ = fmt.Sprintf("%p", fresh) // retain identity for diagnostics
}

// TestSplitterLifecycle_StopSessionSplitterForTesting_IsIdempotent
// pins that the test helper is safe to call twice in a row: the
// second call returns false and does not panic on a double-close of
// the underlying channel (the splitter's sync.Once guards the real
// shutdown, but the test helper's delete+Stop sequence must also be
// self-consistent).
func TestSplitterLifecycle_StopSessionSplitterForTesting_IsIdempotent(t *testing.T) {
	t.Parallel()

	eng, _ := newMicroCompactionTestEngine(t)
	ctx := context.Background()
	sessionID := "session-c2-idempotent"

	eng.BuildContextWindowForTesting(ctx, sessionID, "seed")

	if !eng.StopSessionSplitterForTesting(sessionID) {
		t.Fatalf("first Stop expected true")
	}
	if eng.StopSessionSplitterForTesting(sessionID) {
		t.Fatalf("second Stop expected false (splitter already evicted)")
	}
}

// TestSplitterLifecycle_ConcurrentStopsOnSameSession exercises two
// goroutines calling StopSessionSplitterForTesting concurrently on
// the same session. Exactly one call must report success (the map
// entry transitions from present to absent exactly once); the other
// reports false. Neither must panic.
func TestSplitterLifecycle_ConcurrentStopsOnSameSession(t *testing.T) {
	t.Parallel()

	eng, _ := newMicroCompactionTestEngine(t)
	ctx := context.Background()
	sessionID := "session-c2-double-stop"

	eng.BuildContextWindowForTesting(ctx, sessionID, "seed")

	var results [2]bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		results[0] = eng.StopSessionSplitterForTesting(sessionID)
	}()
	go func() {
		defer wg.Done()
		results[1] = eng.StopSessionSplitterForTesting(sessionID)
	}()
	wg.Wait()

	// Exactly one must have returned true.
	trueCount := 0
	for _, r := range results {
		if r {
			trueCount++
		}
	}
	if trueCount != 1 {
		t.Fatalf("expected exactly one true from concurrent Stops, got %v", results)
	}
}
