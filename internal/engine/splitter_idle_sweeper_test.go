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
	"github.com/baphled/flowstate/internal/recall"
)

// newSweeperTestEngine builds an Engine with L1 enabled and a custom
// IdleTTL so the sweeper's eviction cadence is observable on millisecond
// timescales. Micro-compaction is always enabled because the sweeper only
// runs when it is; the disabled-variant is covered separately.
func newSweeperTestEngine(idleTTL time.Duration) *engine.Engine {
	bus := eventbus.NewEventBus()
	tempDir := GinkgoT().TempDir()
	storageDir := filepath.Join(tempDir, "compacted")

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	Expect(err).NotTo(HaveOccurred())

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

// newSweeperTestEngineDisabled builds an Engine with micro-compaction off
// so the sweeper MUST NOT start.
func newSweeperTestEngineDisabled() *engine.Engine {
	bus := eventbus.NewEventBus()
	tempDir := GinkgoT().TempDir()

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	Expect(err).NotTo(HaveOccurred())

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

// Item 4 — belt-and-braces idle-TTL sweeper for the session splitter
// cache.
//
// C1 landed immediate eviction on the session.ended event. This sweeper
// handles the missed-event case (plugin bug, crash in the handler) where
// a session splitter would otherwise leak persist-worker goroutines until
// engine.Shutdown fired at process exit.
var _ = Describe("Engine splitter idle-TTL sweeper", func() {
	It("evicts a stale entry whose last access exceeds IdleTTL", func() {
		idleTTL := 50 * time.Millisecond
		eng := newSweeperTestEngine(idleTTL)
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = eng.Shutdown(ctx)
		})

		Expect(eng.PrimeSessionSplitterForTest("sess-stale")).NotTo(BeNil(),
			"PrimeSessionSplitterForTest returned nil; micro-compaction did not initialise")
		Expect(eng.SessionSplitterForTest("sess-stale")).NotTo(BeNil(),
			"splitter was not cached after priming")

		Eventually(func() any {
			return eng.SessionSplitterForTest("sess-stale")
		}, idleTTL+2*time.Second, 10*time.Millisecond).Should(BeNil(),
			"idle sweeper did not evict sess-stale within IdleTTL+2s")
	})

	It("keeps a fresh entry that is touched at sub-IdleTTL intervals", func() {
		idleTTL := 200 * time.Millisecond
		eng := newSweeperTestEngine(idleTTL)
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = eng.Shutdown(ctx)
		})

		// Touch the splitter repeatedly at 40ms intervals — well inside
		// idle_ttl. The sweeper must leave it alone across the whole loop.
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			Expect(eng.PrimeSessionSplitterForTest("sess-fresh")).NotTo(BeNil(),
				"prime returned nil mid-loop; splitter was evicted despite recent access")
			time.Sleep(40 * time.Millisecond)
		}

		Expect(eng.SessionSplitterForTest("sess-fresh")).NotTo(BeNil(),
			"sweeper evicted a splitter that was accessed within idle_ttl")
	})

	It("stops the sweeper goroutine cleanly on Engine.Shutdown (idempotent)", func() {
		idleTTL := 50 * time.Millisecond
		eng := newSweeperTestEngine(idleTTL)

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		Expect(eng.Shutdown(ctx)).To(Succeed())

		// A second Shutdown must be a no-op (idempotent).
		ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
		defer cancel2()
		Expect(eng.Shutdown(ctx2)).To(Succeed())

		Expect(eng.IsIdleSweeperRunningForTest()).To(BeFalse(),
			"idle sweeper still running after Shutdown")
	})

	It("does not start the sweeper when micro-compaction is disabled", func() {
		eng := newSweeperTestEngineDisabled()
		DeferCleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = eng.Shutdown(ctx)
		})

		Expect(eng.IsIdleSweeperRunningForTest()).To(BeFalse(),
			"idle sweeper is running with micro-compaction disabled")
	})
})
