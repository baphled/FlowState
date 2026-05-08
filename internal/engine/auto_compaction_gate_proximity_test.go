package engine_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// gateProxCounter is a deterministic TokenCounter sized for the gate-
// proximity specs. It reports one token per whitespace-delimited word
// (mirroring wordTokenCounter) but advertises a 100_000-token model
// limit so the gate's reserve formula (limit - defaultOutputReserve -
// safetyMargin) yields a meaningful threshold rather than the
// degenerate `usable < 1` clamp the existing 100-token-limit fixture
// produces.
//
// The 100_000 figure mirrors a typical glm-4.6-class context window,
// pairing with a defaultOutputReserve of 4096 and a 5% safety margin
// (5_000) for a fire boundary at estimated > 90_904.
type gateProxCounter struct{}

func (gateProxCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (gateProxCounter) ModelLimit(_ string) int { return 100_000 }

// gateProxWordsPerMessage is the per-message word count
// seedGateProxMessages uses. 1_000 words pairs with gateProxCounter
// (one token per whitespace-delimited word) so each call to
// seedGateProxMessages(store, N) deposits N_000 tokens — easy to
// reason about against the 90_904 gate-proximity boundary.
const gateProxWordsPerMessage = 1_000

// seedGateProxMessages appends count messages to the store, each
// carrying gateProxWordsPerMessage whitespace-separated words.
// Combined with gateProxCounter (one token per word) the total
// recent-message token load equals count * 1_000, allowing the
// gate-proximity specs to target the boundary exactly.
func seedGateProxMessages(store *recall.FileContextStore, count int) {
	words := make([]string, gateProxWordsPerMessage)
	for i := range words {
		words[i] = "w"
	}
	content := strings.Join(words, " ")
	for range count {
		store.Append(provider.Message{Role: "assistant", Content: content})
	}
}

// newGateProxEngine constructs an Engine wired with gateProxCounter
// (limit=100_000), a real AutoCompactor, and a high-only ratio threshold
// so the existing soft-trigger path stays inert and the gate-proximity
// check is the sole reason compaction can fire. Tests then seed the
// store to push estimated input tokens above or below the gate-
// proximity boundary. The manifest CompactionThreshold is fixed at 0
// so the global ratio threshold is the sole soft-trigger source under
// test — per-agent overrides have their own dedicated specs in
// auto_compaction_manifest_threshold_test.go.
func newGateProxEngine(
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
	// The default sliding window is 10. Bump it so seedGateProxMessages
	// can place enough message volume in the store for the gate-
	// proximity boundary to be reachable without burning through the
	// per-message word count.
	cm.SlidingWindowSize = 200

	testManifest := agent.Manifest{
		ID:                "gate-prox-agent",
		Name:              "Gate Proximity Agent",
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: cm,
	}

	eng := engine.New(engine.Config{
		ChatProvider:      &t10FakeProvider{},
		Manifest:          testManifest,
		Store:             store,
		TokenCounter:      gateProxCounter{},
		AutoCompactor:     ctxstore.NewAutoCompactor(summariser),
		CompressionConfig: cfg,
	})
	return eng, store
}

// Slice 6a — Engine gate-proximity force-trigger.
//
// The L2 auto-compaction trigger fires on `recent / budget > threshold`,
// which the planner-agnostic 0.75 default treats as a soft heuristic
// disconnected from the actual saturation gate. Phase 1 of the May 2026
// follow-ups added a hard floor at `estimated > limit - reserve` (the
// proactive gate in checkContextWindowOverflow). Slice 6a closes the
// gap by adding a fourth tier to the trigger: when the next request
// would land within 5% of the gate's refusal boundary, fire compaction
// regardless of the configured ratio. The existing ratio trigger and
// per-agent override remain — gate-proximity ORs onto the existing
// fire signal rather than replacing it.
//
// Boundary: gateThreshold = limit - reserve - (limit / 20)
//
//	limit = 100_000, reserve = 4096, safetyMargin = 5_000
//	→ fire when estimated > 90_904
//
// Specs use gateProxCounter (limit=100_000) so the boundary is
// meaningful; the existing 100-token-limit fixtures stay in the
// degenerate territory where the gate-proximity check yields false
// (usable < safetyMargin) and the ratio path is the sole signal.
var _ = Describe("Engine auto-compaction gate-proximity force-trigger", func() {
	It("forces compaction when the request would trip the saturation gate", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		// Ratio threshold 0.99, manifest threshold 0 → ratio path stays
		// inert at any realistic seeded load. Only the gate-proximity
		// path can fire compaction.
		eng, store := newGateProxEngine(summariser, true, 0.99)

		// Seed 95_000 tokens — well above the 90_904 gate-proximity
		// boundary but only 0.95 ratio (below the 0.99 ratio
		// threshold). The ratio trigger says "do not fire"; the new
		// gate-proximity tier says "fire".
		seedGateProxMessages(store, 95)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-gate-prox-fire", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"gate-proximity tier must force compaction when estimated > limit - reserve - safetyMargin")
		Expect(eng.LastCompactionSummaryForTest()).NotTo(BeNil(),
			"summary must be persisted on a gate-proximity-driven compaction")
	})

	It("does not fire when the request comfortably fits under the gate-proximity boundary", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newGateProxEngine(summariser, true, 0.99)

		// 50_000 tokens — well under both 0.99 ratio and the
		// 90_904 gate-proximity boundary.
		seedGateProxMessages(store, 50)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-gate-prox-quiet", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"gate-proximity must stay silent when estimated is comfortably under the boundary")
		Expect(eng.LastCompactionSummaryForTest()).To(BeNil())
	})

	It("preserves the existing ratio trigger when ratio crosses but gate-proximity does not", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		// Ratio 0.10 (very low) — even small loads cross it. Gate-
		// proximity stays inert at low loads. This pins the
		// pre-Slice-6a path remains intact: the new tier is additive,
		// not a replacement.
		eng, store := newGateProxEngine(summariser, true, 0.10)

		// 20_000 tokens — ratio 0.20 > 0.10 (fires via ratio); well
		// under the 90_904 gate-proximity boundary.
		seedGateProxMessages(store, 20)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-ratio-only", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"existing ratio trigger must continue firing when its threshold is crossed")
	})

	It("respects the AutoCompaction.Enabled flag even when the gate-proximity boundary is crossed", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		// Enabled = false. The feature flag is the operator's opt-out;
		// gate-proximity must not bypass it. The hard saturation gate
		// (Phase 1) remains as the floor that refuses the request if
		// compaction was the only thing keeping it under budget.
		eng, store := newGateProxEngine(summariser, false, 0.10)

		seedGateProxMessages(store, 95)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-disabled", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"AutoCompaction.Enabled = false must suppress every trigger tier including gate-proximity")
	})

	It("does not double-fire when both ratio and gate-proximity say fire on the same turn", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		// Both tiers active: ratio low enough to fire, AND seeded
		// load above the gate-proximity boundary. Memoisation
		// (per-session H2) plus single emission point should yield
		// exactly one compactor call and one ContextCompactedEvent.
		eng, store := newGateProxEngine(summariser, true, 0.10)

		var counter atomic.Int32
		eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(_ any) {
			counter.Add(1)
		})

		seedGateProxMessages(store, 95)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-both-fire", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"both tiers true must still produce exactly one compactor invocation")

		// The bus dispatches synchronously; allow a brief poll for any
		// future async change.
		Eventually(counter.Load, 500*time.Millisecond).Should(Equal(int32(1)))
	})

	It("publishes EventContextCompacted carrying the compaction telemetry on a gate-proximity fire", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newGateProxEngine(summariser, true, 0.99)

		var (
			mu       sync.Mutex
			observed []pluginevents.ContextCompactedEventData
		)
		eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(evt any) {
			e, ok := evt.(*pluginevents.ContextCompactedEvent)
			Expect(ok).To(BeTrue())
			mu.Lock()
			observed = append(observed, e.Data)
			mu.Unlock()
		})

		seedGateProxMessages(store, 95)
		_ = eng.BuildContextWindowForTest(context.Background(), "sess-gate-prox-event", "next user turn")

		Eventually(func() int {
			mu.Lock()
			defer mu.Unlock()
			return len(observed)
		}, 500*time.Millisecond).Should(Equal(1))

		mu.Lock()
		got := observed[0]
		mu.Unlock()

		Expect(got.SessionID).To(Equal("sess-gate-prox-event"))
		Expect(got.AgentID).To(Equal("gate-prox-agent"))
		Expect(got.OriginalTokens).To(BeNumerically(">", 0))
		Expect(got.SummaryTokens).To(BeNumerically(">", 0))
		Expect(got.LatencyMS).To(BeNumerically(">=", 0))
	})
})
