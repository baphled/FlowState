package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Phase 5b — feedback-aware auto-compaction gate. The soft trigger's
// ratio numerator becomes max(estimateRequestTokens(syntheticAll), the
// provider-reported cumulative input tokens for the session's most
// recent turn) when a reported figure exists. The specs below pin the
// three contract points:
//
//   - No reported figure: the gate falls back to the estimate with
//     today's trigger points unchanged.
//   - Reported above estimate: the gate fires earlier than the pure
//     estimate would.
//   - Reported below estimate: the estimate wins (max semantics) so a
//     stale low report can never delay firing past today's behaviour.
//
// Arithmetic (fullWindowCounter: 1 token/word, limit 10_000):
// Threshold 0.50 → fire boundary 5_000 tokens.
// Gate-proximity boundary 10_000 - 4_096 - 500 = 5_404 stays clear of
// every seeded load below, so each fire/quiet verdict is attributable
// to the soft trigger alone.
var _ = Describe("Engine auto-compaction usage feedback", func() {
	It("fires earlier when the provider-reported input tokens exceed the estimate", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newFullWindowEngine(summariser, true, 0.50)

		seedFullWindowMessages(store, 30)
		eng.RecordSessionInputTokensForTest("sess-usage-feedback-fire", 6_000)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-usage-feedback-fire", "next user turn")

		Expect(summariser.calls.Load()).To(BeNumerically(">=", int32(1)),
			"the gate must weigh the reported 6_000 input tokens (ratio 0.60) over the "+
				"~3_000-token estimate (ratio 0.30) so compaction fires under the 0.50 threshold")
	})

	It("keeps the estimate-only trigger points when no provider-reported figure exists", func() {
		quietSummariser := &recordingSummariser{response: buildSummaryJSON()}
		quietEng, quietStore := newFullWindowEngine(quietSummariser, true, 0.50)
		seedFullWindowMessages(quietStore, 30)

		_ = quietEng.BuildContextWindowForTest(context.Background(), "sess-usage-feedback-quiet", "next user turn")

		Expect(quietSummariser.calls.Load()).To(Equal(int32(0)),
			"without a reported figure the gate must stay quiet at a ~3_000-token estimate")

		fireSummariser := &recordingSummariser{response: buildSummaryJSON()}
		fireEng, fireStore := newFullWindowEngine(fireSummariser, true, 0.50)
		seedFullWindowMessages(fireStore, 51)

		_ = fireEng.BuildContextWindowForTest(context.Background(), "sess-usage-feedback-estimate", "next user turn")

		Expect(fireSummariser.calls.Load()).To(BeNumerically(">=", int32(1)),
			"without a reported figure the gate must still fire at the estimate boundary "+
				"(51 msgs × 100 tokens = 5_100 > 0.50 × 10_000)")
	})

	It("prefers the estimate when the reported figure falls below it", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newFullWindowEngine(summariser, true, 0.50)

		seedFullWindowMessages(store, 51)
		eng.RecordSessionInputTokensForTest("sess-usage-feedback-max", 1_000)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-usage-feedback-max", "next user turn")

		Expect(summariser.calls.Load()).To(BeNumerically(">=", int32(1)),
			"max semantics must let the 5_100-token estimate win over the 1_000-token report "+
				"so a low reported figure never delays firing past today's behaviour")
	})
})
