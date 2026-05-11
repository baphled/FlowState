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

// fullWindowCounter is the test counter for the soft-trigger / full-window
// accuracy specs. It mirrors gateProxCounter (one token per whitespace-
// delimited word) but with a smaller, easier-to-reason-about model limit so
// the boundary arithmetic stays human-readable in the spec assertions.
type fullWindowCounter struct{}

func (fullWindowCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (fullWindowCounter) ModelLimit(_ string) int { return 10_000 }

const fullWindowWordsPerMessage = 100

// seedFullWindowMessages appends count messages of fullWindowWordsPerMessage
// each. Paired with fullWindowCounter (one token per whitespace-separated
// word) the persisted store grows in units of 100 tokens per message.
func seedFullWindowMessages(store *recall.FileContextStore, count int) {
	words := make([]string, fullWindowWordsPerMessage)
	for i := range words {
		words[i] = "w"
	}
	content := strings.Join(words, " ")
	for range count {
		store.Append(provider.Message{Role: "assistant", Content: content})
	}
}

// newFullWindowEngine wires an Engine for the soft-trigger full-window
// accuracy specs. SlidingWindowSize is set to 10 (the default) so the
// pre-fix ratio path saw at most 10 * 100 = 1_000 tokens regardless of
// how much history was in the store — i.e. ratio could never exceed
// 1000 / 10_000 = 0.10 from the recent slice alone. Tests seed enough
// history that the FULL window exceeds the threshold while the
// sliding-window subset remains under it; the post-fix soft trigger
// must fire because it measures against the whole persisted history.
func newFullWindowEngine(
	summariser ctxstore.Summariser,
	enabled bool,
	ratioThreshold float64,
) (*engine.Engine, *recall.FileContextStore) {
	tempDir := GinkgoT().TempDir()
	store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
	Expect(err).NotTo(HaveOccurred())

	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = enabled
	cfg.AutoCompaction.Threshold = ratioThreshold

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 10

	testManifest := agent.Manifest{
		ID:                "full-window-agent",
		Name:              "Full Window Agent",
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: cm,
	}

	eng := engine.New(engine.Config{
		ChatProvider:      &t10FakeProvider{},
		Manifest:          testManifest,
		Store:             store,
		TokenCounter:      fullWindowCounter{},
		AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
		CompressionConfig: cfg,
	})
	return eng, store
}

// Bug Hunt (May 2026) — auto-compaction soft trigger must measure
// against the FULL persisted window, not the sliding-window subset.
//
// Pre-fix the soft trigger's `recentTokens` was the sum of token counts
// over `e.store.GetRecent(slidingWindowSize)` (default 10 messages),
// which is the SAME slice the compactor uses to summarise. Comparing
// that slice against `tokenBudget` (the model context limit) is a
// category error: the chip displays the full request size, but the
// soft trigger only ever saw 10 messages of it. A 50-message session
// at 90% of the budget on the chip stayed silent because the recent 10
// were comfortably under the 75% threshold.
//
// The fix: ratio now uses estimateRequestTokens against the FULL
// persisted store (matching what the chip and the proactive gate
// already do), aligning the soft trigger's decision with the figure
// the user sees on the chip. Slice 6a's gate-proximity tier was
// hiding this divergence — when the full window approached refusal it
// force-fired anyway — but operators tuning the soft threshold to
// 0.50 or 0.30 (e.g. via Deliverable 2's config knob) expect their
// configured ratio to actually fire at that ratio of the chip's
// figure, not 10x further along.
var _ = Describe("Engine auto-compaction soft-trigger full-window accuracy", func() {
	It("fires when the FULL persisted window crosses the threshold even if the sliding subset does not", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		// Threshold 0.40. Limit 10_000 so pivot at 4_000 tokens.
		// The gate-proximity boundary is limit - reserve - 5% =
		// 10_000 - 4_096 - 500 = 5_404. Seeding 40 messages × 100 =
		// 4_000 tokens sits BELOW the gate-proximity boundary (5_404)
		// — so gate-proximity stays silent and the soft trigger is
		// the sole reason compaction can fire. Pre-fix the recent
		// slice (10 messages = 1_000 tokens, ratio 0.10) stayed
		// under the 0.40 threshold; post-fix the full window
		// (4_000 tokens, ratio 0.40 = boundary) trips the threshold.
		eng, store := newFullWindowEngine(summariser, true, 0.40)

		// 41 messages × 100 = 4_100 tokens → ratio 0.41 (strictly
		// above 0.40), still under the gate-proximity boundary at
		// 5_404, so this isolates the soft-trigger.
		seedFullWindowMessages(store, 41)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-full-window-fire", "next user turn")

		Expect(summariser.calls.Load()).To(BeNumerically(">=", int32(1)),
			"soft trigger must fire when the full-window ratio crosses the threshold, "+
				"even though the sliding-window subset alone would stay quiet")
	})

	It("stays silent when the FULL persisted window is below the threshold", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newFullWindowEngine(summariser, true, 0.50)

		// 30 messages × 100 tokens = 3_000 tokens (ratio 0.30). Below
		// the 0.50 threshold. Soft trigger must stay silent; the
		// gate-proximity tier is also well clear at this load.
		seedFullWindowMessages(store, 30)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-full-window-quiet", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"soft trigger must NOT fire when the full-window ratio is under the threshold")
	})

	It("respects a per-agent CompactionThreshold against the full-window estimate", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		// Global threshold 0.99 (effectively inert) but per-agent
		// override at 0.40. This pins the manifest-override precedence
		// remains intact post-fix — only the metric (sliding-window
		// → full-window) changed, not the threshold-resolution rules.
		tempDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred())

		cfg := ctxstore.DefaultCompressionConfig()
		cfg.AutoCompaction.Enabled = true
		cfg.AutoCompaction.Threshold = 0.99

		cm := agent.DefaultContextManagement()
		cm.CompactionThreshold = 0.40
		cm.SlidingWindowSize = 10

		eng := engine.New(engine.Config{
			ChatProvider: &t10FakeProvider{},
			Manifest: agent.Manifest{
				ID:                "manifest-override-agent",
				Instructions:      agent.Instructions{SystemPrompt: "sys"},
				ContextManagement: cm,
			},
			Store:             store,
			TokenCounter:      fullWindowCounter{},
			AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
			CompressionConfig: cfg,
		})

		// 50 × 100 = 5_000 tokens (ratio 0.50 > 0.40 manifest threshold).
		seedFullWindowMessages(store, 50)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-manifest-override", "next user turn")

		Expect(summariser.calls.Load()).To(BeNumerically(">=", int32(1)),
			"per-agent CompactionThreshold (0.40) must fire on full-window ratio 0.50")
	})
})
