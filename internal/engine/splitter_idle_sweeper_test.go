package engine_test

// Item 4 — belt-and-braces idle-TTL sweeper for the session splitter
// cache.
//
// C1 landed immediate eviction on the session.ended event. This sweeper
// handles the missed-event case (plugin bug, crash in the handler)
// where a session splitter would otherwise leak persist-worker
// goroutines until engine.Shutdown fired at process exit.

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/recall"
)

// newSweeperTestEngine builds an Engine with L1 enabled and a custom
// IdleTTL so the sweeper's eviction cadence is observable on millisecond
// timescales. Micro-compaction is always enabled because the sweeper
// only runs when it is; the disabled-variant is covered separately.
func newSweeperTestEngine(t *testing.T, idleTTL time.Duration) *engine.Engine {
	t.Helper()

	bus := eventbus.NewEventBus()
	tempDir := t.TempDir()
	storageDir := filepath.Join(tempDir, "compacted")

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	if err != nil {
		t.Fatalf("NewFileContextStore: %v", err)
	}

	return engine.New(engine.Config{
		ChatProvider: noopChatProvider{},
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "You are helpful."},
			ContextManagement: agent.DefaultContextManagement(),
		},
		EventBus:     bus,
		Store:        store,
		TokenCounter: evictionTokenCounter{},
		CompressionConfig: ctxstore.CompressionConfig{
			MicroCompaction: ctxstore.MicroCompactionConfig{
				Enabled:           true,
				HotTailSize:       5,
				TokenThreshold:    1000,
				StorageDir:        storageDir,
				PlaceholderTokens: 50,
				IdleTTL:           idleTTL,
			},
		},
	})
}

// newSweeperTestEngineDisabled builds an Engine with micro-compaction
// off so the sweeper MUST NOT start.
func newSweeperTestEngineDisabled(t *testing.T) *engine.Engine {
	t.Helper()

	bus := eventbus.NewEventBus()
	tempDir := t.TempDir()

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	if err != nil {
		t.Fatalf("NewFileContextStore: %v", err)
	}

	return engine.New(engine.Config{
		ChatProvider: noopChatProvider{},
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "You are helpful."},
			ContextManagement: agent.DefaultContextManagement(),
		},
		EventBus:     bus,
		Store:        store,
		TokenCounter: evictionTokenCounter{},
		CompressionConfig: ctxstore.CompressionConfig{
			MicroCompaction: ctxstore.MicroCompactionConfig{
				Enabled: false,
				IdleTTL: 30 * time.Minute,
			},
		},
	})
}

// TestIdleSweeper_EvictsStaleEntries is the core regression: if a
// session splitter sits untouched for longer than idle_ttl, the
// sweeper must drop it from the cache so its persist worker can
// finalise.
func TestIdleSweeper_EvictsStaleEntries(t *testing.T) {
	t.Parallel()

	idleTTL := 50 * time.Millisecond
	eng := newSweeperTestEngine(t, idleTTL)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = eng.Shutdown(ctx)
	})

	// Prime the cache with a splitter for the sweep-target session.
	if eng.PrimeSessionSplitterForTest("sess-stale") == nil {
		t.Fatal("PrimeSessionSplitterForTest returned nil; micro-compaction did not initialise")
	}
	if eng.SessionSplitterForTest("sess-stale") == nil {
		t.Fatal("splitter was not cached after priming")
	}

	// Wait long enough for the sweeper to run past idle_ttl plus one
	// sweep interval.
	deadline := time.Now().Add(idleTTL + 2*time.Second)
	for time.Now().Before(deadline) {
		if eng.SessionSplitterForTest("sess-stale") == nil {
			return // evicted as expected
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("idle sweeper did not evict sess-stale within %v", idleTTL+2*time.Second)
}

// TestIdleSweeper_KeepsFreshEntries guards the inverse: fresh entries
// (touched recently) must NOT be evicted. A sweeper that drops live
// entries would tear down the persist worker mid-turn.
func TestIdleSweeper_KeepsFreshEntries(t *testing.T) {
	t.Parallel()

	idleTTL := 200 * time.Millisecond
	eng := newSweeperTestEngine(t, idleTTL)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = eng.Shutdown(ctx)
	})

	// Touch the splitter repeatedly at 40ms intervals — well inside
	// idle_ttl. The sweeper must leave it alone across the whole loop.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := eng.PrimeSessionSplitterForTest("sess-fresh"); got == nil {
			t.Fatal("prime returned nil mid-loop; splitter was evicted despite recent access")
		}
		time.Sleep(40 * time.Millisecond)
	}

	if eng.SessionSplitterForTest("sess-fresh") == nil {
		t.Fatal("sweeper evicted a splitter that was accessed within idle_ttl")
	}
}

// TestIdleSweeper_StopsCleanlyOnShutdown pins the shutdown contract:
// calling Engine.Shutdown must stop the sweeper goroutine before
// returning so the process does not leak the ticker.
func TestIdleSweeper_StopsCleanlyOnShutdown(t *testing.T) {
	t.Parallel()

	idleTTL := 50 * time.Millisecond
	eng := newSweeperTestEngine(t, idleTTL)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := eng.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown returned error: %v", err)
	}

	// A second Shutdown must be a no-op (idempotent).
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	if err := eng.Shutdown(ctx2); err != nil {
		t.Fatalf("second Shutdown returned error: %v", err)
	}

	// The sweeper must not be running after shutdown. Checking the
	// exported flag is a proxy — the more important property (no
	// goroutine leak) is caught by the race detector and the
	// test-binary not hanging on exit.
	if eng.IsIdleSweeperRunningForTest() {
		t.Fatal("idle sweeper still running after Shutdown")
	}
}

// TestIdleSweeper_DisabledWhenMicroCompactionOff asserts the sweeper
// does not run when micro-compaction is disabled. A dormant goroutine
// is still a goroutine; the "allocation-free when disabled" property
// this engine's micro-compaction honours elsewhere must survive here.
func TestIdleSweeper_DisabledWhenMicroCompactionOff(t *testing.T) {
	t.Parallel()

	eng := newSweeperTestEngineDisabled(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = eng.Shutdown(ctx)
	})

	if eng.IsIdleSweeperRunningForTest() {
		t.Fatal("idle sweeper is running with micro-compaction disabled")
	}
}
