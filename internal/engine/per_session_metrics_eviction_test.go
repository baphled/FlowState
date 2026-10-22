package engine_test

import (
	"context"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// newEvictionMetricsEngine builds an Engine wired to fire L1
// micro-compaction — the cheapest path that seeds a per-session
// compression-metrics entry.
func newEvictionMetricsEngine() (*engine.Engine, *eventbus.EventBus) {
	bus := eventbus.NewEventBus()
	tempDir := GinkgoT().TempDir()
	storageDir := filepath.Join(tempDir, "compacted")

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	Expect(err).NotTo(HaveOccurred())

	content := "one two three four five six seven"
	for range 10 {
		store.Append(provider.Message{Role: "assistant", Content: content})
	}

	eng := engine.New(engine.Config{
		EventBus: bus,
		Manifest: agent.Manifest{
			ID:                "o1-eviction",
			Name:              "O1 eviction",
			ContextManagement: agent.DefaultContextManagement(),
		},
		Store:              store,
		TokenCounter:       wordTokenCounterForPerSession{},
		CompressionMetrics: &ctxstore.CompressionMetrics{},
		CompressionConfig: ctxstore.CompressionConfig{
			MicroCompaction: ctxstore.MicroCompactionConfig{
				Enabled:           true,
				HotTailSize:       2,
				TokenThreshold:    5,
				StorageDir:        storageDir,
				PlaceholderTokens: 10,
			},
		},
	})
	return eng, bus
}

// expectMetricsEviction polls SessionCompressionMetrics until the ledger
// for sessionID is empty. Event delivery through the bus is synchronous,
// but the handler takes the mutex in its own critical section — a small
// poll is belt-and-braces against any future reordering and is cheap.
func expectMetricsEviction(eng *engine.Engine, sessionID string) {
	Eventually(func() ctxstore.CompressionMetrics {
		return eng.SessionCompressionMetricsForTest(sessionID)
	}, 2*time.Second, 5*time.Millisecond).Should(Equal(ctxstore.CompressionMetrics{}),
		"per-session compression-metrics entry for %q was not evicted within deadline", sessionID)
}

// O1 regression coverage for session-close eviction of the per-session
// compression-metrics ledger.
//
// Sibling to the C1 splitter-eviction tests (splitter_eviction_test.go):
// before O1 the engine evicted the sessionSplitters cache on session.ended
// but left the sessionCompressionMetrics map entry populated forever. A
// long-running `flowstate serve` handling many short sessions would
// accumulate dead per-session ledger entries that no further build or read
// would ever touch.
//
// After O1, handleSessionEnded also deletes the matching
// sessionCompressionMetrics entry under its own mutex. The subsequent
// SessionCompressionMetrics(sessionID) call returns the zero value,
// matching the "never-seen session" contract.
var _ = Describe("Engine session.ended eviction of compression metrics", func() {
	It("evicts the per-session metrics entry on session.ended", func() {
		eng, bus := newEvictionMetricsEngine()
		ctx := context.Background()
		sessionID := "session-o1-evict"

		// Build a window so L1 micro-compaction fires and records
		// against the per-session ledger. HotTailSize=2 over 10 seeded
		// messages forces 8 cold spills.
		_ = eng.BuildContextWindowForTest(ctx, sessionID, "turn 1")
		if splitter := eng.SessionSplitterForTest(sessionID); splitter != nil {
			// Drain the async persist worker so the spill counter
			// settles before we assert.
			splitter.Stop()
		}

		before := eng.SessionCompressionMetricsForTest(sessionID)
		Expect(before.MicroCompactionCount).To(BeNumerically(">", 0),
			"seeded ledger for %q must have MicroCompactionCount > 0 to make the eviction assertion meaningful", sessionID)

		bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
			SessionID: sessionID,
			Action:    "ended",
		}))

		expectMetricsEviction(eng, sessionID)
	})

	It("does not affect a sibling session's metrics on session.ended", func() {
		eng, bus := newEvictionMetricsEngine()
		ctx := context.Background()

		sessionA := "session-o1-a"
		sessionB := "session-o1-b"

		_ = eng.BuildContextWindowForTest(ctx, sessionA, "turn A")
		if splitter := eng.SessionSplitterForTest(sessionA); splitter != nil {
			splitter.Stop()
		}
		_ = eng.BuildContextWindowForTest(ctx, sessionB, "turn B")
		if splitter := eng.SessionSplitterForTest(sessionB); splitter != nil {
			splitter.Stop()
		}

		beforeA := eng.SessionCompressionMetricsForTest(sessionA)
		beforeB := eng.SessionCompressionMetricsForTest(sessionB)
		Expect(beforeA.MicroCompactionCount).To(BeNumerically(">", 0))
		Expect(beforeB.MicroCompactionCount).To(BeNumerically(">", 0))

		bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
			SessionID: sessionA,
			Action:    "ended",
		}))

		expectMetricsEviction(eng, sessionA)

		afterB := eng.SessionCompressionMetricsForTest(sessionB)
		Expect(afterB.MicroCompactionCount).To(Equal(beforeB.MicroCompactionCount),
			"session.ended for %q must not mutate %q's ledger entry", sessionA, sessionB)
	})

	It("treats session.ended for a never-compacted session as a silent no-op (no panic)", func() {
		_, bus := newEvictionMetricsEngine()

		Expect(func() {
			bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
				SessionID: "never-compacted",
				Action:    "ended",
			}))
		}).NotTo(Panic())
	})
})
