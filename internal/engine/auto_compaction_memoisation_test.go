// Package engine_test — H2 audit coverage for L2 auto-compaction
// memoisation.
//
// Before H2 the engine's maybeAutoCompact invoked autoCompactor.Compact
// on every turn whose recent-message token load exceeded the threshold
// ratio. Turn N fired; turn N+1 with the same cold prefix fired again,
// making the summariser pay for repeated identical work.
//
// The fix memoises the cold-range hash: if the current range's
// SHA-256 matches the stored hash and a summary was previously
// produced, the stored summary is reused instead of invoking the
// summariser. Cache invalidates automatically when the cold-range
// composition changes (different messages, different order, different
// content).
package engine_test

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/provider"
)

// TestAutoCompaction_StableColdPrefix_DoesNotResummarise pins the
// core H2 contract: back-to-back builds over the same cold prefix
// must invoke the summariser exactly once. The second turn reuses
// the cached summary.
func TestAutoCompaction_StableColdPrefix_DoesNotResummarise(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)

	// Seed 10 messages (70 tokens). Ratio 0.70 crosses 0.60 so turn
	// 1 fires compaction.
	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo", "turn 1")
	if summariser.calls.Load() != 1 {
		t.Fatalf("turn 1: summariser.calls = %d; want 1", summariser.calls.Load())
	}

	// Turn 2 with no new history — the cold prefix (the recent
	// sliding window) is byte-for-byte identical. H2 memoisation
	// must suppress the re-call.
	_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo", "turn 2")
	if summariser.calls.Load() != 1 {
		t.Fatalf("turn 2 (stable cold prefix): summariser.calls = %d; want 1 (must not re-summarise)", summariser.calls.Load())
	}

	// A third identical turn locks the contract.
	_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo", "turn 3")
	if summariser.calls.Load() != 1 {
		t.Fatalf("turn 3 (still stable): summariser.calls = %d; want 1", summariser.calls.Load())
	}
}

// TestAutoCompaction_ColdPrefixChanges_ReFires pins cache
// invalidation. When a new message is appended to the store between
// turns, the cold prefix's hash changes and the memoisation must
// let the summariser fire a second time.
func TestAutoCompaction_ColdPrefixChanges_ReFires(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)

	seedMessages(t, store)
	_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-invalidate", "turn 1")
	if summariser.calls.Load() != 1 {
		t.Fatalf("turn 1: summariser.calls = %d; want 1", summariser.calls.Load())
	}

	// Append a new assistant message so the sliding window shifts.
	// The oldest message falls off the tail, a new one enters — the
	// cold range composition has changed and the hash must no longer
	// match the stored one.
	store.Append(provider.Message{Role: "assistant", Content: "one two three four five six seven eight"})
	_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-invalidate", "turn 2")
	if summariser.calls.Load() != 2 {
		t.Fatalf("turn 2 (cold prefix changed): summariser.calls = %d; want 2 (hash must invalidate)", summariser.calls.Load())
	}
}

// TestAutoCompaction_LastCompactionSummary_ReusedAcrossTurns pins
// the reuse contract on the engine-observable side: after turn 1
// fires, turn 2 with a stable cold prefix must surface the SAME
// summary via LastCompactionSummary — proving the memo's stored
// value is served up rather than being dropped by the "clear at
// entry" logic in maybeAutoCompact.
func TestAutoCompaction_LastCompactionSummary_ReusedAcrossTurns(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)
	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-reuse", "turn 1")
	first := eng.LastCompactionSummaryForTest()
	if first == nil {
		t.Fatalf("turn 1 LastCompactionSummary = nil; want non-nil after firing")
	}

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-reuse", "turn 2")
	second := eng.LastCompactionSummaryForTest()
	if second == nil {
		t.Fatalf("turn 2 LastCompactionSummary = nil; memo must preserve the previous summary across an unchanged cold prefix")
	}
	if first.Intent != second.Intent {
		t.Fatalf("turn 2 summary Intent diverged from turn 1: %q vs %q", first.Intent, second.Intent)
	}
	if summariser.calls.Load() != 1 {
		t.Fatalf("turn 2 summariser.calls = %d; want 1 (stable hash must suppress)", summariser.calls.Load())
	}
}
