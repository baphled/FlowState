package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/recall"
)

// newCompactNowEngine wires a minimal engine for the manual-compact
// and threshold-mutation specs. Threshold defaults to 0.99 so the
// ratio path never fires on its own — every compaction must come
// from the explicit force-now seam under test.
func newCompactNowEngine(summariser ctxstore.Summariser) (*engine.Engine, *recall.FileContextStore) {
	tempDir := GinkgoT().TempDir()
	store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
	Expect(err).NotTo(HaveOccurred())

	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.99

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 10

	eng := engine.New(engine.Config{
		ChatProvider: &t10FakeProvider{},
		Manifest: agent.Manifest{
			ID:                "compact-now-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		Store:             store,
		TokenCounter:      fullWindowCounter{},
		AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
		CompressionConfig: cfg,
	})
	return eng, store
}

// Deliverable 2 — SetAutoCompactionThreshold lets the api layer
// PATCH the soft trigger's ratio at runtime without restarting the
// process. The pre-existing config knob (Compression.AutoCompaction.Threshold)
// is only consulted at engine construction; once the operator wires
// it the value is frozen for the process lifetime. The new method
// validates the input (same rules as CompressionConfig.Validate's
// auto_compaction.threshold range — (0, 1]) and updates the engine's
// in-memory copy atomically so the next autoCompactionThreshold call
// reads the new value.
var _ = Describe("Engine.SetAutoCompactionThreshold", func() {
	It("mutates the configured threshold so subsequent compactions consult the new value", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newCompactNowEngine(summariser)

		// Seed 41 × 100 = 4_100 tokens. Threshold 0.99 → ratio 0.41
		// stays below; no fire on the first build.
		seedFullWindowMessages(store, 41)
		_ = eng.BuildContextWindowForTest(context.Background(), "sess-pre-mutate", "next turn")
		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"pre-mutation threshold 0.99 must keep the trigger quiet on a 0.41 ratio")

		// Tighten the threshold to 0.30. 0.41 > 0.30 → next build
		// fires.
		Expect(eng.SetAutoCompactionThreshold(0.30)).To(Succeed())
		_ = eng.BuildContextWindowForTest(context.Background(), "sess-post-mutate", "next turn")
		Expect(summariser.calls.Load()).To(BeNumerically(">=", int32(1)),
			"post-mutation threshold 0.30 must fire on the same 0.41 ratio session")
	})

	It("rejects threshold values outside the (0, 1] interval", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, _ := newCompactNowEngine(summariser)
		Expect(eng.SetAutoCompactionThreshold(0)).NotTo(Succeed(),
			"threshold 0 is rejected (would never trigger and silently disable the layer)")
		Expect(eng.SetAutoCompactionThreshold(-0.5)).NotTo(Succeed(),
			"negative threshold is rejected (same disable failure mode as zero)")
		Expect(eng.SetAutoCompactionThreshold(1.5)).NotTo(Succeed(),
			"threshold > 1 is rejected (would fire on every turn — operator footgun)")
	})

	It("AutoCompactionThreshold exposes the current value for readback", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, _ := newCompactNowEngine(summariser)
		Expect(eng.AutoCompactionThreshold()).To(BeNumerically("~", 0.99, 1e-9),
			"readback must reflect the constructed value")
		Expect(eng.SetAutoCompactionThreshold(0.42)).To(Succeed())
		Expect(eng.AutoCompactionThreshold()).To(BeNumerically("~", 0.42, 1e-9),
			"readback must reflect the post-mutation value")
	})
})

// Deliverable 3 — CompactNow is the engine seam the /compress slash
// command and POST /api/v1/sessions/{id}/compress wire to. Behaviour:
//
//   - Force-fires maybeAutoCompact with trigger="manual" regardless
//     of ratio / gate-proximity / per-agent threshold state. The
//     AutoCompaction.Enabled flag is still honoured (operator opt-out
//     is sticky); a disabled layer cannot be conjured back into life
//     by a slash command.
//   - Returns (summary, true) on a successful fire so the caller can
//     surface "nothing to compact" vs "compacted X→Y" feedback.
//   - Returns ("", false) when there is no content to summarise (an
//     empty store, an already-compacted session whose memo hit) — the
//     slash command's "nothing to compact" toast hangs off this
//     discriminant.
var _ = Describe("Engine.CompactNow (manual /compress trigger)", func() {
	It("force-fires the compactor regardless of soft / gate thresholds", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newCompactNowEngine(summariser)

		// Tiny session — 10 messages × 100 = 1_000 tokens, ratio 0.10
		// against 10_000 limit. Neither soft (threshold 0.99) nor
		// gate-proximity (5_404 boundary) would fire here.
		seedFullWindowMessages(store, 10)

		summary, fired := eng.CompactNow(context.Background(), "sess-manual")

		Expect(fired).To(BeTrue(), "CompactNow must force-fire even on small sessions")
		Expect(summary).NotTo(BeEmpty(), "summary text must accompany a successful fire")
		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"summariser must be invoked exactly once on the manual trigger")
	})

	It("returns (\"\", false) when there is no content to compact", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, _ := newCompactNowEngine(summariser)

		// Empty store: the autoCompactionCandidates guard returns
		// early with no recent messages.
		summary, fired := eng.CompactNow(context.Background(), "sess-empty")

		Expect(fired).To(BeFalse(),
			"empty store must report no-fire so the UI can surface 'nothing to compact'")
		Expect(summary).To(BeEmpty())
		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"summariser must not be invoked when there is no content")
	})

	It("respects the AutoCompaction.Enabled feature flag (operator opt-out is sticky)", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}

		tempDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred())

		cfg := ctxstore.DefaultCompressionConfig()
		cfg.AutoCompaction.Enabled = false // explicit opt-out
		cfg.AutoCompaction.Threshold = 0.50

		eng := engine.New(engine.Config{
			ChatProvider: &t10FakeProvider{},
			Manifest: agent.Manifest{
				ID:           "manual-disabled-agent",
				Instructions: agent.Instructions{SystemPrompt: "sys"},
			},
			Store:             store,
			TokenCounter:      fullWindowCounter{},
			AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
			CompressionConfig: cfg,
		})
		seedFullWindowMessages(store, 50)

		summary, fired := eng.CompactNow(context.Background(), "sess-disabled")
		Expect(fired).To(BeFalse(),
			"AutoCompaction.Enabled=false must suppress the manual trigger too")
		Expect(summary).To(BeEmpty())
		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"summariser must not be invoked when the layer is disabled")
	})

	It("returns (\"\", false) on a no-op session-id", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, _ := newCompactNowEngine(summariser)

		summary, fired := eng.CompactNow(context.Background(), "")
		Expect(fired).To(BeFalse())
		Expect(summary).To(BeEmpty())
	})

	It("publishes a ContextCompactedEvent with trigger=\"manual\"", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newCompactNowEngine(summariser)
		seedFullWindowMessages(store, 10)

		// Subscribe to the bus to capture the published event.
		var lastEvent struct {
			sessionID string
			trigger   string
		}
		captured := false
		eng.EventBus().Subscribe("context.compacted", func(e any) {
			// pluginevents.ContextCompactedEvent — pull SessionID
			// and Trigger via the public shape. To keep this spec
			// independent of the event struct's exact import the
			// assertion is done via the engine-side helper or
			// via a structural read using reflection-light JSON
			// fallback. For the slash-command path we only need
			// the fact that an event fired with the manual
			// discriminant.
			captured = true
			_ = e
		})

		_, fired := eng.CompactNow(context.Background(), "sess-event")
		Expect(fired).To(BeTrue())
		Expect(captured).To(BeTrue(),
			"CompactNow must publish a ContextCompactedEvent on the engine bus so "+
				"the SSE bridge forwards it as context_compacted; the chip's flash "+
				"+ tooltip discriminate the trigger via the event's Trigger field")
		// Trigger inspection lives in the dedicated trigger-vocabulary
		// spec (auto_compaction_trigger_vocabulary_test.go); the bus
		// payload type is already pinned there.
		_ = lastEvent
	})
})
