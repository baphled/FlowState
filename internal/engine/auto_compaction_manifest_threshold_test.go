// Package engine_test — H3 audit coverage.
//
// Before H3 the manifest.ContextManagement.CompactionThreshold field
// was declared, loaded, and defaulted to 0.75 by the manifest loader
// but never read by the auto-compaction decision logic: engine.go's
// autoCompactionThreshold pulled only from
// e.compressionConfig.AutoCompaction.Threshold. Per-agent overrides
// configured in a manifest were silently ignored.
//
// The fix makes manifest.ContextManagement.CompactionThreshold the
// preferred source, falling back to the global threshold when the
// manifest value is zero. Precedence: manifest > global > 0
// (disabled). These tests pin that contract.
package engine_test

import (
	"context"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/recall"
)

// TestAutoCompactionThreshold_ManifestOverridesGlobal is the happy
// path: the manifest specifies a higher threshold than the global
// config, so a build at a token ratio between the two must NOT fire
// compaction. Proves the manifest value wins.
func TestAutoCompactionThreshold_ManifestOverridesGlobal(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}

	// Global threshold is 0.60; manifest raises it to 0.90. At the
	// seeded token load (70 tokens / 100 budget = 0.70) the global
	// would fire but the manifest must suppress.
	eng, store := newEngineWithManifestThreshold(t, summariser, 0.60, 0.90)
	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-manifest-override", "turn 1")

	if summariser.calls.Load() != 0 {
		t.Fatalf("manifest threshold 0.90 must override global 0.60 at ratio 0.70; summariser fired %d times", summariser.calls.Load())
	}
}

// TestAutoCompactionThreshold_ManifestZeroFallsBackToGlobal pins the
// fallback leg. A manifest value of 0 means "no per-agent override";
// the global threshold is used. Without this contract the global
// config would be dead for any agent constructed with an explicit
// zero (e.g. the test helpers that intentionally do not set it).
func TestAutoCompactionThreshold_ManifestZeroFallsBackToGlobal(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}

	// Global 0.60, manifest 0 → expect global wins. Ratio 0.70
	// crosses 0.60 so compaction fires.
	eng, store := newEngineWithManifestThreshold(t, summariser, 0.60, 0.0)
	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-manifest-zero", "turn 1")

	if summariser.calls.Load() != 1 {
		t.Fatalf("manifest 0 must fall back to global 0.60 at ratio 0.70; summariser fired %d times, want 1", summariser.calls.Load())
	}
}

// TestAutoCompactionThreshold_ManifestLowerThanGlobal proves the
// manifest can also lower the threshold — not only raise it. A
// manifest of 0.50 with global 0.90 still fires at ratio 0.70.
func TestAutoCompactionThreshold_ManifestLowerThanGlobal(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}

	// Global 0.90, manifest 0.50 → manifest wins, ratio 0.70 fires.
	eng, store := newEngineWithManifestThreshold(t, summariser, 0.90, 0.50)
	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-manifest-lower", "turn 1")

	if summariser.calls.Load() != 1 {
		t.Fatalf("manifest 0.50 must override global 0.90 at ratio 0.70; summariser fired %d times, want 1", summariser.calls.Load())
	}
}

// newEngineWithManifestThreshold is a small variant of the T10 test
// helper that threads an explicit CompactionThreshold onto the
// manifest's ContextManagement — the field H3 wires up.
func newEngineWithManifestThreshold(
	t *testing.T,
	summariser ctxstore.Summariser,
	globalThreshold float64,
	manifestThreshold float64,
) (*engine.Engine, *recall.FileContextStore) {
	t.Helper()

	tempDir := t.TempDir()
	store, err := recall.NewFileContextStore(tempDir+"/ctx.json", "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

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
