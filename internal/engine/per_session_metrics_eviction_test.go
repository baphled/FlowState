// Package engine — O1 regression coverage for session-close eviction of
// the per-session compression-metrics ledger.
//
// Sibling to the C1 splitter-eviction tests (splitter_eviction_test.go):
// before O1 the engine evicted the sessionSplitters cache on
// session.ended but left the sessionCompressionMetrics map entry
// populated forever. A long-running `flowstate serve` handling many
// short sessions would accumulate dead per-session ledger entries that
// no further build or read would ever touch.
//
// After O1, handleSessionEnded also deletes the matching
// sessionCompressionMetrics entry under its own mutex. The subsequent
// SessionCompressionMetrics(sessionID) call returns the zero value,
// matching the "never-seen session" contract already pinned in
// TestSessionCompressionMetrics_UnknownSession_ReturnsZero.
package engine_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// newEvictionMetricsEngine builds an Engine wired to fire L1 micro-
// compaction — the cheapest path that seeds a per-session
// compression-metrics entry. We reuse the same shape as the other
// eviction tests so the assertion side stays readable.
func newEvictionMetricsEngine(t *testing.T) (*engine.Engine, *eventbus.EventBus) {
	t.Helper()

	bus := eventbus.NewEventBus()
	tempDir := t.TempDir()
	storageDir := filepath.Join(tempDir, "compacted")

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	if err != nil {
		t.Fatalf("NewFileContextStore: %v", err)
	}

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

// waitForMetricsEviction polls SessionCompressionMetrics until the
// ledger for sessionID is empty. Event delivery through the bus is
// synchronous, but the handler takes the mutex in its own critical
// section — a small poll is belt-and-braces against any future
// reordering and is cheap.
func waitForMetricsEviction(t *testing.T, eng *engine.Engine, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if eng.SessionCompressionMetricsForTest(sessionID) == (ctxstore.CompressionMetrics{}) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("per-session compression-metrics entry for %q was not evicted within deadline", sessionID)
}

// TestSessionEnded_EvictsSessionCompressionMetrics is the core O1
// regression: after a build populates the per-session ledger,
// publishing session.ended must clear that session's entry. The zero
// value returned afterwards proves the map slot is gone, not just
// reset to zeros in place — the map read path returns {} when the key
// is absent, which is the contract we want.
func TestSessionEnded_EvictsSessionCompressionMetrics(t *testing.T) {
	t.Parallel()

	eng, bus := newEvictionMetricsEngine(t)
	ctx := context.Background()
	sessionID := "session-o1-evict"

	// Build a window so L1 micro-compaction fires and records against
	// the per-session ledger. HotTailSize=2 over 10 seeded messages
	// forces 8 cold spills.
	_ = eng.BuildContextWindowForTest(ctx, sessionID, "turn 1")
	if splitter := eng.SessionSplitterForTest(sessionID); splitter != nil {
		// Drain the async persist worker so the spill counter settles
		// before we assert. The metrics increment happens synchronously
		// in recordSessionMicroCompaction — Stop is defence-in-depth.
		splitter.Stop()
	}

	before := eng.SessionCompressionMetricsForTest(sessionID)
	if before.MicroCompactionCount == 0 {
		t.Fatalf("seeded ledger for %q = zero value; expected MicroCompactionCount > 0 to make the eviction assertion meaningful", sessionID)
	}

	// Publish session.ended. The subscription registered in New must
	// delete the per-session metrics entry — the same critical section
	// that evicts the splitter cache.
	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: sessionID,
		Action:    "ended",
	}))

	waitForMetricsEviction(t, eng, sessionID)
}

// TestSessionEnded_OtherSessionMetricsUnaffected pins the eviction's
// specificity: a session.ended for session A must not touch session
// B's per-session ledger. This mirrors the splitter-cache contract —
// the two maps have independent lifetimes but the same per-session
// eviction semantics.
func TestSessionEnded_OtherSessionMetricsUnaffected(t *testing.T) {
	t.Parallel()

	eng, bus := newEvictionMetricsEngine(t)
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
	if beforeA.MicroCompactionCount == 0 || beforeB.MicroCompactionCount == 0 {
		t.Fatalf("expected both sessions to have non-zero micro counts before eviction; got A=%d B=%d", beforeA.MicroCompactionCount, beforeB.MicroCompactionCount)
	}

	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: sessionA,
		Action:    "ended",
	}))

	waitForMetricsEviction(t, eng, sessionA)

	// B must still carry its pre-eviction figure — session A's end
	// event must not reach across into B's ledger entry.
	afterB := eng.SessionCompressionMetricsForTest(sessionB)
	if afterB.MicroCompactionCount != beforeB.MicroCompactionCount {
		t.Fatalf("session.ended for %q wrongly mutated %q ledger: before=%d after=%d", sessionA, sessionB, beforeB.MicroCompactionCount, afterB.MicroCompactionCount)
	}
}

// TestSessionEnded_NoMetricsLedger_NoOp ensures that publishing
// session.ended for a session that never fired compression is a
// silent no-op. The lazy map allocation means this codepath must
// tolerate the missing key without panicking on delete — Go's
// builtin delete is a no-op on absent keys, but we pin the
// behaviour explicitly because engine-level handlers must stay
// crash-free under any event sequence.
func TestSessionEnded_NoMetricsLedger_NoOp(t *testing.T) {
	t.Parallel()

	_, bus := newEvictionMetricsEngine(t)

	// Must not panic.
	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: "never-compacted",
		Action:    "ended",
	}))
}
