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

// newEngineWithManifestThreshold is a small variant of the T10 test helper
// that threads an explicit CompactionThreshold onto the manifest's
// ContextManagement — the field H3 wires up.
func newEngineWithManifestThreshold(
	summariser ctxstore.Summariser,
	globalThreshold float64,
	manifestThreshold float64,
) (*engine.Engine, *recall.FileContextStore) {
	tempDir := GinkgoT().TempDir()
	store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
	Expect(err).NotTo(HaveOccurred())

	counter := &wordTokenCounter{limit: 100}
	compactor := ctxstore.NewAutoCompactor(summariser)
	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = globalThreshold

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = manifestThreshold

	testManifest := agent.Manifest{
		ID:                "h3-agent",
		Name:              "H3 Agent",
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: cm,
	}

	eng := engine.New(engine.Config{
		ChatProvider:      &t10FakeProvider{},
		Manifest:          testManifest,
		Store:             store,
		TokenCounter:      counter,
		AutoCompactor:     compactor,
		CompressionConfig: cfg,
	})
	return eng, store
}

// H3 audit coverage.
//
// Before H3 the manifest.ContextManagement.CompactionThreshold field was
// declared, loaded, and defaulted to 0.75 by the manifest loader but never
// read by the auto-compaction decision logic: engine.go's
// autoCompactionThreshold pulled only from
// e.compressionConfig.AutoCompaction.Threshold. Per-agent overrides
// configured in a manifest were silently ignored.
//
// The fix makes manifest.ContextManagement.CompactionThreshold the
// preferred source, falling back to the global threshold when the manifest
// value is zero. Precedence: manifest > global > 0 (disabled).
var _ = Describe("Engine auto-compaction threshold precedence", func() {
	It("manifest threshold overrides global when both are set (manifest higher)", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}

		// Global threshold is 0.60; manifest raises it to 0.90. At the
		// seeded token load (70 tokens / 100 budget = 0.70) the global
		// would fire but the manifest must suppress.
		eng, store := newEngineWithManifestThreshold(summariser, 0.60, 0.90)
		seedMessages(store)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-manifest-override", "turn 1")

		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"manifest threshold 0.90 must override global 0.60 at ratio 0.70")
	})

	It("falls back to the global threshold when the manifest value is zero", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}

		// Global 0.60, manifest 0 → expect global wins. Ratio 0.70
		// crosses 0.60 so compaction fires.
		eng, store := newEngineWithManifestThreshold(summariser, 0.60, 0.0)
		seedMessages(store)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-manifest-zero", "turn 1")

		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"manifest 0 must fall back to global 0.60 at ratio 0.70")
	})

	It("manifest threshold overrides global even when the manifest value is lower", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}

		// Global 0.90, manifest 0.50 → manifest wins, ratio 0.70 fires.
		eng, store := newEngineWithManifestThreshold(summariser, 0.90, 0.50)
		seedMessages(store)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-manifest-lower", "turn 1")

		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"manifest 0.50 must override global 0.90 at ratio 0.70")
	})
})
