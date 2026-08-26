package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

// H2 audit coverage for L2 auto-compaction memoisation.
//
// Before H2 the engine's maybeAutoCompact invoked autoCompactor.Compact on
// every turn whose recent-message token load exceeded the threshold ratio.
// Turn N fired; turn N+1 with the same cold prefix fired again, making the
// summariser pay for repeated identical work.
//
// The fix memoises the cold-range hash: if the current range's SHA-256
// matches the stored hash and a summary was previously produced, the
// stored summary is reused instead of invoking the summariser. Cache
// invalidates automatically when the cold-range composition changes
// (different messages, different order, different content).
var _ = Describe("Engine auto-compaction memoisation", func() {
	It("does not re-summarise when the cold prefix is byte-for-byte identical across turns", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)

		// Seed 10 messages (70 tokens). Ratio 0.70 crosses 0.60 so turn 1
		// fires compaction.
		seedMessages(store)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo", "turn 1")
		Expect(summariser.calls.Load()).To(Equal(int32(1)))

		// Turn 2 with no new history — the cold prefix (the recent
		// sliding window) is byte-for-byte identical. H2 memoisation
		// must suppress the re-call.
		_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo", "turn 2")
		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"turn 2 with stable cold prefix must not re-summarise")

		// A third identical turn locks the contract.
		_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo", "turn 3")
		Expect(summariser.calls.Load()).To(Equal(int32(1)))
	})

	It("re-fires the summariser when a new message shifts the cold prefix", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)

		seedMessages(store)
		_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-invalidate", "turn 1")
		Expect(summariser.calls.Load()).To(Equal(int32(1)))

		// Append a new assistant message so the sliding window shifts.
		// The oldest message falls off the tail, a new one enters — the
		// cold range composition has changed and the hash must no longer
		// match the stored one.
		store.Append(provider.Message{Role: "assistant", Content: "one two three four five six seven eight"})
		_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-invalidate", "turn 2")
		Expect(summariser.calls.Load()).To(Equal(int32(2)),
			"changed cold prefix must invalidate the memo")
	})

	It("preserves LastCompactionSummary across turns when the cold prefix is stable", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)
		seedMessages(store)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-reuse", "turn 1")
		first := eng.LastCompactionSummaryForTest()
		Expect(first).NotTo(BeNil(),
			"turn 1 LastCompactionSummary must be non-nil after firing")

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-memo-reuse", "turn 2")
		second := eng.LastCompactionSummaryForTest()
		Expect(second).NotTo(BeNil(),
			"memo must preserve the previous summary across an unchanged cold prefix")
		Expect(second.Intent).To(Equal(first.Intent))
		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"stable hash must suppress the re-call")
	})
})
