package engine_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
)

// C2 regression coverage for the splitter Stop+delete atomicity property.
//
// Before C2 the production close path (handleSessionEnded) and the test
// helper (StopSessionSplitterForTesting) both deleted the cache entry
// under splitterMu but released the lock before calling Stop(). Two
// problems:
//
//  1. Two concurrent closers could both see the splitter in the map, both
//     delete (one is a no-op via the "ok" flag on the second), and both
//     call Stop on the same instance.
//  2. A concurrent ensureSessionSplitter racing with a closer sees the
//     map transition from "present" to "absent" at the moment of delete,
//     and correctly constructs a fresh splitter — but the ordering
//     between the close's delete and the close's Stop is observable from
//     outside.
//
// After C2 Stop is called inside the critical section. Concurrent closers
// serialise: the first runs Stop to completion; the second sees an empty
// map and returns cleanly. Concurrent ensureSession callers either see
// the live splitter (pre-close) or the empty map (post-close) — never a
// mid-close state.
var _ = Describe("Engine splitter Stop+delete atomicity", func() {
	It("survives 50x concurrent ensureSessionSplitter / session.ended without panicking", func() {
		eng, bus := newMicroCompactionTestEngine(GinkgoT())
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

		Expect(eng.SessionSplitterForTest(sessionID)).To(BeNil(),
			"expected splitter cache to be empty after contended close")

		// A fresh Build must reconstruct, not reuse.
		eng.BuildContextWindowForTesting(ctx, sessionID, "hello")
		Expect(eng.SessionSplitterForTest(sessionID)).NotTo(BeNil(),
			"Build after contended close did not construct a fresh splitter")
	})

	It("StopSessionSplitterForTesting is idempotent (returns true once, false thereafter)", func() {
		eng, _ := newMicroCompactionTestEngine(GinkgoT())
		ctx := context.Background()
		sessionID := "session-c2-idempotent"

		eng.BuildContextWindowForTesting(ctx, sessionID, "seed")

		Expect(eng.StopSessionSplitterForTesting(sessionID)).To(BeTrue(),
			"first Stop expected true")
		Expect(eng.StopSessionSplitterForTesting(sessionID)).To(BeFalse(),
			"second Stop expected false (splitter already evicted)")
	})

	It("two concurrent StopSessionSplitterForTesting on the same session yield exactly one true result", func() {
		eng, _ := newMicroCompactionTestEngine(GinkgoT())
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

		trueCount := 0
		for _, r := range results {
			if r {
				trueCount++
			}
		}
		Expect(trueCount).To(Equal(1),
			"expected exactly one true from concurrent Stops, got %v", results)
	})
})
