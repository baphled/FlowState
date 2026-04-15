package engine_test

// Per-session compression-metrics contract.
//
// The user-visible bug: a single engine servicing multiple sessions
// reported a monolithic, ever-growing set of counters via
// `Engine.CompressionMetrics()` — so operators reading `flowstate run
// --stats` (or the slog compression-metrics line) on a brand-new
// session saw the accumulated totals from every previous session that
// the same engine had handled. The user's complaint was exactly this:
// "the token counter doesn't reset when I start a new session".
//
// The fix introduces a parallel, per-session accounting surface:
//
//   - Engine.CompressionMetrics()                 — retained as the
//     cumulative aggregate (critical for `flowstate serve` dashboards
//     and to keep the Prometheus-mirroring semantic intact).
//   - Engine.SessionCompressionMetrics(sessionID) — new; returns a
//     snapshot of just the counters that fired under the supplied
//     session ID.
//
// These tests pin the contract from the engine side. The
// corresponding CLI-side test (run_stats_per_session_test.go) covers
// the `--stats` switch.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// wordTokenCounterForPerSession replicates the package-private
// wordTokenCounterForWiring from micro_compaction_wiring_test.go so
// this external_test file can drive the same shape of L1 micro-
// compaction scenario without reaching into the engine package.
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

// TestSessionCompressionMetrics_SegregatesAutoCompactionsBetweenSessions
// fires auto-compaction under two distinct session IDs on a single
// engine and asserts that each session's snapshot only reflects its
// own compactions, while the cumulative aggregate still counts both.
func TestSessionCompressionMetrics_SegregatesAutoCompactionsBetweenSessions(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	metrics := &ctxstore.CompressionMetrics{}
	eng, store := newTestEngineWithCompactorRecorderAndMetrics(t, summariser, nil, metrics)

	// Seed 10 messages (70 tokens) so each BuildContextWindow call
	// crosses the 0.60 threshold and triggers exactly one compaction.
	seedMessages(t, store)

	// Session A fires one compaction.
	_ = eng.BuildContextWindowForTest(context.Background(), "session-A", "first turn")

	sessA := eng.SessionCompressionMetricsForTest("session-A")
	if sessA.AutoCompactionCount != 1 {
		t.Fatalf("after session A: SessionCompressionMetrics(A).AutoCompactionCount = %d; want 1", sessA.AutoCompactionCount)
	}

	// Session B — the bug manifested here. A fresh session must see
	// zero historical activity in its own snapshot; only the ONE
	// compaction it is about to fire should register under its id.
	sessBBefore := eng.SessionCompressionMetricsForTest("session-B")
	if sessBBefore.AutoCompactionCount != 0 {
		t.Fatalf("SessionCompressionMetrics(B) before build = %d; want 0 (session B has not run yet)", sessBBefore.AutoCompactionCount)
	}

	_ = eng.BuildContextWindowForTest(context.Background(), "session-B", "first turn")

	sessB := eng.SessionCompressionMetricsForTest("session-B")
	if sessB.AutoCompactionCount != 1 {
		t.Fatalf("after session B: SessionCompressionMetrics(B).AutoCompactionCount = %d; want 1", sessB.AutoCompactionCount)
	}

	// Session A's snapshot must not have grown from session B's activity.
	sessAAfter := eng.SessionCompressionMetricsForTest("session-A")
	if sessAAfter.AutoCompactionCount != 1 {
		t.Fatalf("SessionCompressionMetrics(A) after session B ran = %d; want 1 (session B must not leak into A)", sessAAfter.AutoCompactionCount)
	}

	// The cumulative aggregate must still count both — operators
	// running flowstate serve rely on this for dashboards.
	if metrics.AutoCompactionCount != 2 {
		t.Fatalf("aggregate CompressionMetrics.AutoCompactionCount = %d; want 2 (sum of both sessions)", metrics.AutoCompactionCount)
	}
}

// TestSessionCompressionMetrics_UnknownSession_ReturnsZero asserts the
// safe default: asking for a session that has never run compression
// returns a zero-valued struct rather than nil or an error. This is
// the shape `flowstate run --stats` wants — on the very first turn
// of a new session it should see zeros, not panic.
func TestSessionCompressionMetrics_UnknownSession_ReturnsZero(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	metrics := &ctxstore.CompressionMetrics{}
	eng, _ := newTestEngineWithCompactorRecorderAndMetrics(t, summariser, nil, metrics)

	got := eng.SessionCompressionMetricsForTest("never-seen")
	if got != (ctxstore.CompressionMetrics{}) {
		t.Fatalf("SessionCompressionMetrics(unknown) = %+v; want zero value", got)
	}
}

// TestSessionCompressionMetrics_SegregatesMicroCompactionsBetweenSessions
// drives L1 — HotColdSplitter-based — micro-compaction under two
// sessions and pins the same per-session segregation contract. This
// is the layer the user actually exercised: each `flowstate run` with
// --stats lights up L1 when MicroCompaction is enabled, and the
// reporting surface must reflect only the current session's spill
// count.
func TestSessionCompressionMetrics_SegregatesMicroCompactionsBetweenSessions(t *testing.T) {
	t.Parallel()

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
	// expect 8 cold spills. The splitter must be Stopped explicitly so
	// the async persist worker drains deterministically before assertion.
	_ = eng.BuildContextWindowForTest(context.Background(), "session-micro-A", "turn 1")
	if splitter := eng.SessionSplitterForTest("session-micro-A"); splitter != nil {
		splitter.Stop()
	}

	sessA := eng.SessionCompressionMetricsForTest("session-micro-A")
	if sessA.MicroCompactionCount == 0 {
		t.Fatalf("session A micro count = 0; expected > 0 (HotTailSize=2 over 10 messages must spill 8)")
	}
	aCount := sessA.MicroCompactionCount

	// Session B — brand new session, must start from zero.
	sessBBefore := eng.SessionCompressionMetricsForTest("session-micro-B")
	if sessBBefore.MicroCompactionCount != 0 {
		t.Fatalf("session B micro count before build = %d; want 0 (fresh session must not inherit A's totals — this is the user-reported bug)", sessBBefore.MicroCompactionCount)
	}

	_ = eng.BuildContextWindowForTest(context.Background(), "session-micro-B", "turn 1")
	if splitter := eng.SessionSplitterForTest("session-micro-B"); splitter != nil {
		splitter.Stop()
	}

	sessB := eng.SessionCompressionMetricsForTest("session-micro-B")
	if sessB.MicroCompactionCount == 0 {
		t.Fatalf("session B micro count after build = 0; expected > 0")
	}
	if sessB.MicroCompactionCount > aCount {
		t.Fatalf("session B micro count = %d; must not exceed per-session A figure %d (sessions must not leak into each other)", sessB.MicroCompactionCount, aCount)
	}

	// Session A's figure must stay stable — session B's activity must
	// not leak backwards into it.
	sessAAfter := eng.SessionCompressionMetricsForTest("session-micro-A")
	if sessAAfter.MicroCompactionCount != aCount {
		t.Fatalf("session A micro count after session B ran = %d; want stable %d", sessAAfter.MicroCompactionCount, aCount)
	}

	// The cumulative aggregate must sum both sessions — Prometheus-
	// facing semantics are preserved.
	if metrics.MicroCompactionCount != sessAAfter.MicroCompactionCount+sessB.MicroCompactionCount {
		t.Fatalf("aggregate micro = %d; want %d (A=%d + B=%d)",
			metrics.MicroCompactionCount, sessAAfter.MicroCompactionCount+sessB.MicroCompactionCount,
			sessAAfter.MicroCompactionCount, sessB.MicroCompactionCount)
	}
}
