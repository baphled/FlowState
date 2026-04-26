package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
	"github.com/baphled/flowstate/internal/tracer"
)

// engineTestRecorder captures RecordCompressionTokensSaved and
// RecordCompressionOverheadTokens calls so the auto-compaction wiring tests
// can assert both net-saving and net-negative deltas.
type engineTestRecorder struct {
	tracer.NoopRecorder
	mu            sync.Mutex
	savedCalls    []engineSavedCall
	overheadCalls []engineSavedCall
}

type engineSavedCall struct {
	agentID     string
	tokensSaved int
}

func (r *engineTestRecorder) RecordCompressionTokensSaved(agentID string, tokensSaved int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.savedCalls = append(r.savedCalls, engineSavedCall{agentID: agentID, tokensSaved: tokensSaved})
}

func (r *engineTestRecorder) RecordCompressionOverheadTokens(agentID string, tokens int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.overheadCalls = append(r.overheadCalls, engineSavedCall{agentID: agentID, tokensSaved: tokens})
}

func (r *engineTestRecorder) saved() []engineSavedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]engineSavedCall, len(r.savedCalls))
	copy(out, r.savedCalls)
	return out
}

func (r *engineTestRecorder) overhead() []engineSavedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]engineSavedCall, len(r.overheadCalls))
	copy(out, r.overheadCalls)
	return out
}

// recordingSummariser is a ctxstore.Summariser test double that returns a
// scripted JSON summary and counts how many times it was called. The
// counter is atomic because an in-flight refactor could, in principle,
// invoke the summariser from a goroutine; the atomic keeps the race
// detector happy regardless.
type recordingSummariser struct {
	calls    atomic.Int32
	response string
	err      error
}

// Summarise satisfies ctxstore.Summariser.
func (r *recordingSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	r.calls.Add(1)
	if r.err != nil {
		return "", r.err
	}
	return r.response, nil
}

// buildSummaryJSON returns a minimal-but-valid CompactionSummary body.
// Failure to marshal aborts the spec via Gomega rather than via *testing.T.
func buildSummaryJSON() string {
	summary := ctxstore.CompactionSummary{
		Intent:             "continue T10 integration work",
		KeyDecisions:       []string{"fire compaction at threshold"},
		Errors:             []string{},
		NextSteps:          []string{"persist summary on engine"},
		FilesToRestore:     []string{"internal/engine/engine.go"},
		OriginalTokenCount: 0,
		SummaryTokenCount:  0,
	}
	data, err := json.Marshal(summary)
	Expect(err).NotTo(HaveOccurred(), "marshal summary")
	return string(data)
}

// newTestEngineWithCompactor creates an Engine wired with a real
// AutoCompactor backed by the supplied summariser. The engine is
// configured with a deliberately tiny token budget so tests can craft
// messages that straddle the 80% threshold without pushing megabytes of
// synthetic content. The caller receives the engine and the path of the
// temp context store (so it can seed messages directly).
func newTestEngineWithCompactor(
	summariser ctxstore.Summariser,
	threshold float64,
	enabled bool,
) (*engine.Engine, *recall.FileContextStore) {
	return newTestEngineWithCompactorAndRecorder(summariser, threshold, enabled, nil)
}

// newTestEngineWithCompactorRecorderAndMetrics extends
// newTestEngineWithCompactorAndRecorder with an explicit
// *ctxstore.CompressionMetrics so tests can assert engine-side accounting
// (TokensSaved, AutoCompactionCount). Passing nil matches the legacy
// behaviour where no metrics struct is wired.
func newTestEngineWithCompactorRecorderAndMetrics(
	summariser ctxstore.Summariser,
	recorder tracer.Recorder,
	metrics *ctxstore.CompressionMetrics,
) (*engine.Engine, *recall.FileContextStore) {
	return newTestEngineWithCompactorOptions(summariser, 0.60, true, recorder, metrics)
}

// newTestEngineWithCompactorAndRecorder extends newTestEngineWithCompactor
// with an optional tracer.Recorder so tests can assert that the engine
// emits the RecordCompressionTokensSaved counter on successful compaction.
func newTestEngineWithCompactorAndRecorder(
	summariser ctxstore.Summariser,
	threshold float64,
	enabled bool,
	recorder tracer.Recorder,
) (*engine.Engine, *recall.FileContextStore) {
	return newTestEngineWithCompactorOptions(summariser, threshold, enabled, recorder, nil)
}

// newTestEngineWithCompactorOptions is the shared body for the compactor
// test-engine constructors. Keeping a single assembly point means future
// T10 tests can add optional knobs without fanning out a new helper per
// permutation.
func newTestEngineWithCompactorOptions(
	summariser ctxstore.Summariser,
	threshold float64,
	enabled bool,
	recorder tracer.Recorder,
	metrics *ctxstore.CompressionMetrics,
) (*engine.Engine, *recall.FileContextStore) {
	tempDir, err := os.MkdirTemp("", "engine-t10-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "context.json"), "test-model")
	Expect(err).NotTo(HaveOccurred())

	// Stub counter that attributes a predictable token count per string:
	// one token per whitespace-separated word. This lets tests control
	// whether the ratio crosses the threshold by choosing word counts.
	counter := &wordTokenCounter{limit: 100}

	compactor := ctxstore.NewAutoCompactor(summariser)

	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = enabled
	cfg.AutoCompaction.Threshold = threshold

	// H3 — the T10 helper wires a global auto-compaction threshold via
	// cfg.AutoCompaction.Threshold above. The new manifest-over-global
	// precedence means DefaultContextManagement()'s 0.75 would silently
	// override the caller's `threshold` argument. Zero-ing the manifest
	// field restores the original "global wins" contract these tests
	// depend on. Tests that specifically want to exercise the per-agent
	// override live in auto_compaction_manifest_threshold_test.go.
	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0

	testManifest := agent.Manifest{
		ID:   "t10-agent",
		Name: "T10 Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "sys",
		},
		ContextManagement: cm,
	}

	eng := engine.New(engine.Config{
		ChatProvider:       &t10FakeProvider{},
		Manifest:           testManifest,
		Store:              store,
		TokenCounter:       counter,
		AutoCompactor:      compactor,
		CompressionConfig:  cfg,
		CompressionMetrics: metrics,
		Recorder:           recorder,
	})

	return eng, store
}

// wordTokenCounter is a deterministic TokenCounter for engine tests. It
// reports one token per whitespace-delimited word and returns a fixed
// model limit so tests can precisely target threshold boundaries.
type wordTokenCounter struct {
	limit int
}

func (w *wordTokenCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (w *wordTokenCounter) ModelLimit(_ string) int {
	return w.limit
}

// t10FakeProvider satisfies provider.Provider without doing anything
// useful. Engine construction requires a ChatProvider; the T10 trigger path
// never invokes Chat or Stream, so empty stubs are sufficient.
type t10FakeProvider struct{}

func (t10FakeProvider) Name() string { return "t10-fake" }
func (t10FakeProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (t10FakeProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (t10FakeProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (t10FakeProvider) Models() ([]provider.Model, error) { return nil, nil }

// seedMessages appends 10 messages to the store (matching the default
// sliding window size) with a content string sized so that the
// wordTokenCounter reports 7 tokens per message — 70 tokens total. The
// 70-token figure is chosen to sit at ratio 0.70 against the 100-token
// budget used by newTestEngineWithCompactor: above a 0.60 threshold
// (triggers compaction) but below a 0.80 threshold (does not).
func seedMessages(store *recall.FileContextStore) {
	const wordsPerMessage = 7
	words := make([]string, wordsPerMessage)
	for i := range words {
		words[i] = "w"
	}
	content := strings.Join(words, " ")
	for range 10 {
		store.Append(provider.Message{Role: "assistant", Content: content})
	}
}

// T10 auto-compaction trigger specification.
//
// These tests pin the contract that engine.buildContextWindow fires the L2
// auto-compactor when the recent-message token load crosses the configured
// threshold, and that the compaction summary is injected into the built
// window. The trigger lives in buildContextWindow rather than
// WindowBuilder.buildInternal because compaction requires ctx + network
// I/O, which do not belong in the pure assembler — see plan T10.
var _ = Describe("Engine.buildContextWindow auto-compaction trigger", func() {
	It("fires the compactor when the recent-message ratio exceeds the threshold", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)

		// 10 messages × 7 words = 70 tokens. Sliding window default keeps
		// all 10. Budget is 100; ratio is 0.70 > 0.60 threshold → fire.
		seedMessages(store)

		messages := eng.BuildContextWindowForTest(context.Background(), "session-1", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(1)))

		var foundSummary bool
		for _, m := range messages {
			if strings.Contains(m.Content, "[auto-compacted summary]") {
				foundSummary = true
				break
			}
		}
		Expect(foundSummary).To(BeTrue(), "built context missing [auto-compacted summary] marker")

		summary := eng.LastCompactionSummaryForTest()
		Expect(summary).NotTo(BeNil(),
			"LastCompactionSummary is nil after trigger; T11 rehydration will have no source")
		Expect(summary.Intent).NotTo(BeEmpty(),
			"stored summary has empty Intent; parse+validate path skipped")
	})

	It("stays silent when the recent-message ratio is below the threshold", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.80, true)

		seedMessages(store)

		messages := eng.BuildContextWindowForTest(context.Background(), "session-2", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(0)))
		for _, m := range messages {
			Expect(m.Content).NotTo(ContainSubstring("[auto-compacted summary]"),
				"built context contains summary marker but threshold was not crossed")
		}
		Expect(eng.LastCompactionSummaryForTest()).To(BeNil(),
			"LastCompactionSummary should be nil when trigger did not fire")
	})

	It("is inert when CompressionConfig.AutoCompaction.Enabled is false, regardless of ratio", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.10, false)

		seedMessages(store)

		_ = eng.BuildContextWindowForTest(context.Background(), "session-3", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(0)),
			"summariser must not fire when feature is disabled")
	})

	It("publishes a ContextCompactedEvent on every successful compaction", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)

		var (
			mu       sync.Mutex
			observed []pluginevents.ContextCompactedEventData
		)
		eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(evt any) {
			e, ok := evt.(*pluginevents.ContextCompactedEvent)
			Expect(ok).To(BeTrue(), "subscriber received %T; want *ContextCompactedEvent", evt)
			mu.Lock()
			observed = append(observed, e.Data)
			mu.Unlock()
		})

		seedMessages(store)
		_ = eng.BuildContextWindowForTest(context.Background(), "session-t10b", "next user turn")

		// The bus dispatches synchronously but we still guard against a
		// future async change by polling briefly.
		Eventually(func() int {
			mu.Lock()
			defer mu.Unlock()
			return len(observed)
		}, 500*time.Millisecond).Should(Equal(1))

		mu.Lock()
		got := observed[0]
		mu.Unlock()
		Expect(got.SessionID).To(Equal("session-t10b"))
		Expect(got.AgentID).To(Equal("t10-agent"))
		Expect(got.OriginalTokens).To(BeNumerically(">", 0))
		Expect(got.SummaryTokens).To(BeNumerically(">", 0),
			"summary text has length so its token count must be > 0")
		Expect(got.LatencyMS).To(BeNumerically(">=", 0))
	})

	It("does not publish a ContextCompactedEvent on summariser error", func() {
		summariser := &recordingSummariser{err: errors.New("sim outage")}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)

		var counter atomic.Int32
		eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(_ any) {
			counter.Add(1)
		})

		seedMessages(store)
		_ = eng.BuildContextWindowForTest(context.Background(), "session-t10b-err", "next user turn")

		Expect(counter.Load()).To(Equal(int32(0)),
			"ContextCompactedEvent must not fire on summariser error")
	})

	It("fails open on summariser error, returning the uncompacted window", func() {
		summariser := &recordingSummariser{err: errors.New("simulated summariser outage")}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)

		seedMessages(store)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		messages := eng.BuildContextWindowForTest(ctx, "session-4", "next user turn")

		Expect(messages).NotTo(BeEmpty(),
			"engine returned no messages on compaction failure; must fail open")
		for _, m := range messages {
			Expect(m.Content).NotTo(ContainSubstring("[auto-compacted summary]"),
				"compaction errored but marker still present; wrong fallback")
		}
		Expect(eng.LastCompactionSummaryForTest()).To(BeNil())
	})

	It("emits RecordCompressionTokensSaved with the OriginalTokens-SummaryTokens delta", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		rec := &engineTestRecorder{}
		eng, store := newTestEngineWithCompactorAndRecorder(summariser, 0.60, true, rec)

		var (
			mu       sync.Mutex
			observed []pluginevents.ContextCompactedEventData
		)
		eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(evt any) {
			e, ok := evt.(*pluginevents.ContextCompactedEvent)
			Expect(ok).To(BeTrue(), "subscriber received %T; want *ContextCompactedEvent", evt)
			mu.Lock()
			observed = append(observed, e.Data)
			mu.Unlock()
		})

		seedMessages(store)
		_ = eng.BuildContextWindowForTest(context.Background(), "session-metrics", "next user turn")

		calls := rec.saved()
		Expect(calls).To(HaveLen(1))
		Expect(calls[0].agentID).To(Equal("t10-agent"))

		mu.Lock()
		Expect(observed).To(HaveLen(1))
		evt := observed[0]
		mu.Unlock()

		expectedDelta := evt.OriginalTokens - evt.SummaryTokens
		Expect(calls[0].tokensSaved).To(Equal(expectedDelta),
			"recorder tokensSaved must equal OriginalTokens - SummaryTokens")
	})

	It("records overhead and skips savings when the summary is larger than the range", func() {
		// Build a summary whose JSON, wrapped with the
		// "[auto-compacted summary]: " prefix, has more whitespace-split
		// words than the 70-token compacted range. The wordTokenCounter
		// counts one token per whitespace-separated word.
		overheadSummary := ctxstore.CompactionSummary{
			Intent: strings.Repeat("an intentionally verbose intent string that pads the summary ", 8) +
				"past the seventy-word compacted range it replaces",
			KeyDecisions: []string{"accept the overhead", "let the guards swallow the delta"},
			Errors:       []string{},
			NextSteps:    []string{"assert metrics stay at zero"},
		}
		data, err := json.Marshal(overheadSummary)
		Expect(err).NotTo(HaveOccurred())

		summariser := &recordingSummariser{response: string(data)}
		rec := &engineTestRecorder{}
		metrics := &ctxstore.CompressionMetrics{}
		eng, store := newTestEngineWithCompactorRecorderAndMetrics(summariser, rec, metrics)

		seedMessages(store)
		_ = eng.BuildContextWindowForTest(context.Background(), "session-overhead", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(1)),
			"overhead path must still invoke the compactor exactly once")
		Expect(metrics.AutoCompactionCount).To(Equal(1))

		Expect(metrics.TokensSaved).To(Equal(0),
			"engine metrics must guard against negative savings")
		Expect(rec.saved()).To(BeEmpty(),
			"savings recorder must not be invoked on overhead compaction")

		overhead := rec.overhead()
		Expect(overhead).To(HaveLen(1),
			"overhead recorder MUST fire exactly once with the absolute cost")
		Expect(overhead[0].tokensSaved).To(BeNumerically(">", 0),
			"overhead must be strictly positive (abs of the delta)")
	})

	It("does not invoke the overhead recorder on a net-savings compaction", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		rec := &engineTestRecorder{}
		metrics := &ctxstore.CompressionMetrics{}
		eng, store := newTestEngineWithCompactorRecorderAndMetrics(summariser, rec, metrics)

		seedMessages(store)
		_ = eng.BuildContextWindowForTest(context.Background(), "session-savings", "next user turn")

		Expect(summariser.calls.Load()).To(Equal(int32(1)))
		Expect(rec.saved()).To(HaveLen(1),
			"savings recorder must fire on net-savings path")
		Expect(rec.overhead()).To(BeEmpty(),
			"overhead recorder must NOT fire on net-savings path")
	})

	It("does not panic when no Recorder is configured", func() {
		summariser := &recordingSummariser{response: buildSummaryJSON()}
		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)

		seedMessages(store)
		Expect(func() {
			_ = eng.BuildContextWindowForTest(context.Background(), "session-no-rec", "next user turn")
		}).NotTo(Panic())
	})
})
