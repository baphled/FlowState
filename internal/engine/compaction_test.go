package engine_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
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

// Phase 5a — token-budgeted window truncation. The overflow fallback,
// the mid-loop compaction rebuild, and the token-bounded rebuild all
// bound the surviving window by the same target = usable*4/5 figure the
// main rebuild applies, with a keep-one floor that never drops the
// newest message and a fixed prefix (system prompt, todo context,
// summary) whose tokens are folded into the bound.
//
// Arithmetic (fullWindowCounter: 1 token/word, limit 10_000,
// defaultOutputReserve 4_096): usable = 5_904, target = 4_723.
// gateProxCounter arithmetic (limit 100_000): usable = 95_904,
// target = 76_723.
var _ = Describe("Engine token-budgeted window truncation", func() {
	It("never drops the newest message even when a single message exceeds the target", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, _ := newFullWindowEngine(summariser, false, 0.99)

		giant := make([]string, 9_000)
		for i := range giant {
			giant[i] = "w"
		}
		messages := []provider.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: strings.Join(giant, " ")},
		}

		bounded := eng.TruncateMessagesTokenBoundedForTest(context.Background(), messages)

		Expect(bounded).To(HaveLen(2),
			"the keep-one floor must keep both the fixed system prefix and the single tail message")
		Expect(bounded[0].Role).To(Equal("system"))
		Expect(bounded[1].Role).To(Equal("user"),
			"a single tail message larger than the target must survive the keep-one floor")
		Expect(bounded[1].Content).To(HavePrefix("w w w"),
			"the in-memory clamp keeps the head of the oversized message, not an empty husk")
	})

	It("accounts the prepended system-prompt prefix in the token-bounded rebuild", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		tempDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred())

		prompt := make([]string, 400)
		for i := range prompt {
			prompt[i] = "s"
		}

		cfg := ctxstore.DefaultCompressionConfig()
		cfg.AutoCompaction.Enabled = false
		cm := agent.DefaultContextManagement()
		cm.CompactionThreshold = 0
		cm.SlidingWindowSize = 200

		eng := engine.New(engine.Config{
			ChatProvider: &t10FakeProvider{},
			Manifest: agent.Manifest{
				ID:                "prefix-budget-agent",
				Instructions:      agent.Instructions{SystemPrompt: strings.Join(prompt, " ")},
				ContextManagement: cm,
			},
			Store:             store,
			TokenCounter:      fullWindowCounter{},
			AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
			CompressionConfig: cfg,
		})

		seedFullWindowMessages(store, 60)
		messages := store.AllMessages()
		summary := "[auto-compacted summary]: " + buildSummaryJSON()

		rebuilt := eng.RebuildContextWindowTokenBoundedForTest(context.Background(), "sess-prefix-bound", messages, summary)
		Expect(rebuilt).NotTo(BeNil())

		counter := fullWindowCounter{}
		estimated := 0
		for _, m := range rebuilt {
			estimated += counter.Count(m.Content)
		}
		Expect(estimated).To(BeNumerically("<=", 4_723),
			"the 400-token system-prompt prefix must already be paid for by the surviving tail "+
				"so the prefixed window does not overshoot the target (got %d)", estimated)
		Expect(rebuilt[0].Role).To(Equal("system"))
		Expect(rebuilt[1].Content).To(HavePrefix("[auto-compacted summary]"))
		Expect(rebuilt[len(rebuilt)-1]).To(Equal(messages[len(messages)-1]),
			"the newest tail message must survive the bound")
	})

	It("bounds the mid-loop compaction rebuild to the same token target", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newGateProxEngine(summariser, true, 0.99)
		seedGateProxMessages(store, 95)

		outChan := make(chan provider.StreamChunk, 4)
		compacted := eng.EmitMidToolLoopRefreshReportForTest(context.Background(), "sess-midloop-bound", outChan)
		close(outChan)
		for range outChan {
		}
		Expect(compacted).To(BeTrue())

		messages := []provider.Message{
			{Role: "user", Content: "rebuild this request"},
			{Role: "assistant", Content: "tool call"},
			{Role: "tool", Content: "tool output"},
		}
		rebuilt := eng.RebuildMessagesAfterCompactionForTest(context.Background(), "sess-midloop-bound", messages)
		Expect(rebuilt).NotTo(BeEmpty())

		counter := gateProxCounter{}
		estimated := 0
		for _, m := range rebuilt {
			estimated += counter.Count(m.Content)
		}
		Expect(estimated).To(BeNumerically("<=", 76_723),
			"the mid-loop rebuild must bound the reassembled window to usable*4/5 = 76_723, "+
				"not reattach the full 95_000-token store (got %d)", estimated)

		var summaryFound bool
		for _, m := range rebuilt {
			if strings.HasPrefix(m.Content, "[auto-compacted summary]") {
				summaryFound = true
				break
			}
		}
		Expect(summaryFound).To(BeTrue(),
			"the fixed prefix must preserve the auto-compacted summary the reload exists to surface")

		Expect(rebuilt[len(rebuilt)-1]).To(Equal(provider.Message{Role: "user", Content: "rebuild this request"}),
			"the rebuilt slice must preserve the original user prompt instead of dropping the newest message")
	})
})
