package engine_test

import (
	"context"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// wordTokenCounterForPerSession replicates the package-private
// wordTokenCounterForWiring from micro_compaction_wiring_test.go so this
// external_test file can drive the same shape of L1 micro-compaction
// scenario without reaching into the engine package.
type wordTokenCounterForPerSession struct{}

func (wordTokenCounterForPerSession) Count(text string) int {
	count := 0
	inWord := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' {
			if inWord {
				count++
				inWord = false
			}
			continue
		}
		inWord = true
	}
	if inWord {
		count++
	}
	return count
}

func (wordTokenCounterForPerSession) ModelLimit(_ string) int { return 10000 }

// Per-session compression-metrics contract.
//
// The user-visible bug: a single engine servicing multiple sessions
// reported a monolithic, ever-growing set of counters via
// `Engine.CompressionMetrics()` — so operators reading `flowstate run
// --stats` (or the slog compression-metrics line) on a brand-new session
// saw the accumulated totals from every previous session that the same
// engine had handled. The user's complaint was exactly this: "the token
// counter doesn't reset when I start a new session".
//
// The fix introduces a parallel, per-session accounting surface:
//
//   - Engine.CompressionMetrics()                 — retained as the
//     cumulative aggregate (critical for `flowstate serve` dashboards and
//     to keep the Prometheus-mirroring semantic intact).
//   - Engine.SessionCompressionMetrics(sessionID) — new; returns a
//     snapshot of just the counters that fired under the supplied session
//     ID.
//
// These tests pin the contract from the engine side. The corresponding
// CLI-side test (run_stats_per_session_test.go) covers the `--stats` switch.
var _ = Describe("Engine per-session CompressionMetrics", func() {
	It("segregates auto-compaction counts between two sessions sharing one engine", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		metrics := &ctxstore.CompressionMetrics{}
		eng, store := newTestEngineWithCompactorRecorderAndMetrics(summariser, nil, metrics)

		// Seed 10 messages (70 tokens) so each BuildContextWindow call
		// crosses the 0.60 threshold and triggers exactly one compaction.
		seedMessages(store)

		// Session A fires one compaction.
		_ = eng.BuildContextWindowForTest(context.Background(), "session-A", "first turn")

		sessA := eng.SessionCompressionMetricsForTest("session-A")
		Expect(sessA.AutoCompactionCount).To(Equal(1))

		// Session B — the bug manifested here. A fresh session must see
		// zero historical activity in its own snapshot; only the ONE
		// compaction it is about to fire should register under its id.
		sessBBefore := eng.SessionCompressionMetricsForTest("session-B")
		Expect(sessBBefore.AutoCompactionCount).To(Equal(0),
			"fresh session B must not inherit session A's totals")

		_ = eng.BuildContextWindowForTest(context.Background(), "session-B", "first turn")

		sessB := eng.SessionCompressionMetricsForTest("session-B")
		Expect(sessB.AutoCompactionCount).To(Equal(1))

		// Session A's snapshot must not have grown from session B's activity.
		sessAAfter := eng.SessionCompressionMetricsForTest("session-A")
		Expect(sessAAfter.AutoCompactionCount).To(Equal(1),
			"session B must not leak into session A")

		// The cumulative aggregate must still count both — operators
		// running flowstate serve rely on this for dashboards.
		Expect(metrics.AutoCompactionCount).To(Equal(2),
			"aggregate must reflect the sum of both sessions")
	})

	It("returns a zero-value struct for an unknown session id", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		metrics := &ctxstore.CompressionMetrics{}
		eng, _ := newTestEngineWithCompactorRecorderAndMetrics(summariser, nil, metrics)

		got := eng.SessionCompressionMetricsForTest("never-seen")
		Expect(got).To(Equal(ctxstore.CompressionMetrics{}))
	})

	It("segregates micro-compaction counts between two sessions sharing one engine", func() {
		tempDir := GinkgoT().TempDir()
		storageDir := filepath.Join(tempDir, "compacted")

		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())

		content := "one two three four five six seven"
		for range 10 {
			store.Append(provider.Message{Role: "assistant", Content: content})
		}

		manifest := agent.Manifest{
			ID:                "per-session-micro",
			Name:              "Per-session micro metrics",
			ContextManagement: agent.DefaultContextManagement(),
		}

		metrics := &ctxstore.CompressionMetrics{}

		eng := engine.New(engine.Config{
			Manifest:           manifest,
			Store:              store,
			TokenCounter:       wordTokenCounterForPerSession{},
			CompressionMetrics: metrics,
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

		// Session A — drive one build. With HotTailSize=2 of 10 messages,
		// expect 8 cold spills. The splitter must be Stopped explicitly
		// so the async persist worker drains deterministically before
		// assertion.
		_ = eng.BuildContextWindowForTest(context.Background(), "session-micro-A", "turn 1")
		if splitter := eng.SessionSplitterForTest("session-micro-A"); splitter != nil {
			splitter.Stop()
		}

		sessA := eng.SessionCompressionMetricsForTest("session-micro-A")
		Expect(sessA.MicroCompactionCount).To(BeNumerically(">", 0),
			"HotTailSize=2 over 10 messages must spill cold ranges")
		aCount := sessA.MicroCompactionCount

		// Session B — brand new session, must start from zero.
		sessBBefore := eng.SessionCompressionMetricsForTest("session-micro-B")
		Expect(sessBBefore.MicroCompactionCount).To(Equal(0),
			"fresh session B must not inherit A's totals — this is the user-reported bug")

		_ = eng.BuildContextWindowForTest(context.Background(), "session-micro-B", "turn 1")
		if splitter := eng.SessionSplitterForTest("session-micro-B"); splitter != nil {
			splitter.Stop()
		}

		sessB := eng.SessionCompressionMetricsForTest("session-micro-B")
		Expect(sessB.MicroCompactionCount).To(BeNumerically(">", 0))
		Expect(sessB.MicroCompactionCount).To(BeNumerically("<=", aCount),
			"session B count must not exceed session A's figure")

		// Session A's figure must stay stable — session B's activity
		// must not leak backwards into it.
		sessAAfter := eng.SessionCompressionMetricsForTest("session-micro-A")
		Expect(sessAAfter.MicroCompactionCount).To(Equal(aCount),
			"session A figure must remain stable")

		// The cumulative aggregate must sum both sessions —
		// Prometheus-facing semantics are preserved.
		Expect(metrics.MicroCompactionCount).To(Equal(sessAAfter.MicroCompactionCount+sessB.MicroCompactionCount),
			"aggregate must sum A + B")
	})
})
