// Package engine_test — T10 auto-compaction trigger specification.
//
// These tests pin the contract that engine.buildContextWindow fires the
// L2 auto-compactor when the recent-message token load crosses the
// configured threshold, and that the compaction summary is injected into
// the built window. The trigger lives in buildContextWindow rather than
// WindowBuilder.buildInternal because compaction requires ctx + network
// I/O, which do not belong in the pure assembler — see plan T10.
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
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tracer"
)

// engineTestRecorder captures RecordCompressionTokensSaved calls so
// the auto-compaction wiring test can assert the delta.
type engineTestRecorder struct {
	tracer.NoopRecorder
	mu         sync.Mutex
	savedCalls []engineSavedCall
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

func (r *engineTestRecorder) saved() []engineSavedCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]engineSavedCall, len(r.savedCalls))
	copy(out, r.savedCalls)
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

// Summarise satisfies ctxstore.Summariser. It ignores the inputs and
// returns the configured response (or error).
func (r *recordingSummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	r.calls.Add(1)
	if r.err != nil {
		return "", r.err
	}
	return r.response, nil
}

// buildSummaryJSON returns a minimal-but-valid CompactionSummary body.
// Kept as a helper rather than inlined so individual T10 tests stay
// focused on trigger behaviour, not JSON construction. None of the
// current callers need to vary the payload — if a future test does, it
// should build its own summary inline rather than parameterising this
// helper with a rarely-exercised override hook.
func buildSummaryJSON(t *testing.T) string {
	t.Helper()
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
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	return string(data)
}

// newTestEngineWithCompactor creates an Engine wired with a real
// AutoCompactor backed by the supplied summariser. The engine is
// configured with a deliberately tiny token budget so tests can craft
// messages that straddle the 80% threshold without pushing megabytes of
// synthetic content. The caller receives the engine and the path of the
// temp context store (so it can seed messages directly).
func newTestEngineWithCompactor(
	t *testing.T,
	summariser ctxstore.Summariser,
	threshold float64,
	enabled bool,
) (*engine.Engine, *recall.FileContextStore) {
	t.Helper()
	return newTestEngineWithCompactorAndRecorder(t, summariser, threshold, enabled, nil)
}

// newTestEngineWithCompactorAndRecorder extends newTestEngineWithCompactor
// with an optional tracer.Recorder so tests can assert that the engine
// emits the RecordCompressionTokensSaved counter on successful compaction.
func newTestEngineWithCompactorAndRecorder(
	t *testing.T,
	summariser ctxstore.Summariser,
	threshold float64,
	enabled bool,
	recorder tracer.Recorder,
) (*engine.Engine, *recall.FileContextStore) {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "engine-t10-*")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "context.json"), "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	// Stub counter that attributes a predictable token count per string:
	// one token per whitespace-separated word. This lets tests control
	// whether the ratio crosses the threshold by choosing word counts.
	counter := &wordTokenCounter{limit: 100}

	compactor := ctxstore.NewAutoCompactor(summariser)

	cfg := ctxstore.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = enabled
	cfg.AutoCompaction.Threshold = threshold

	testManifest := agent.Manifest{
		ID:   "t10-agent",
		Name: "T10 Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "sys",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}

	eng := engine.New(engine.Config{
		ChatProvider:      &t10FakeProvider{},
		Manifest:          testManifest,
		Store:             store,
		TokenCounter:      counter,
		AutoCompactor:     compactor,
		CompressionConfig: cfg,
		Recorder:          recorder,
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
// useful. Engine construction requires a ChatProvider; the T10 trigger
// path never invokes Chat or Stream, so empty stubs are sufficient.
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
func seedMessages(t *testing.T, store *recall.FileContextStore) {
	t.Helper()
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

// TestBuildContextWindowAutoCompaction_AboveThreshold_FiresCompact
// asserts that when the recent messages exceed threshold*budget, the
// AutoCompactor is invoked and the produced summary lands in the built
// context as a summary message. With budget=100 and threshold=0.60, a
// recent-message load of 70 tokens must trigger.
func TestBuildContextWindowAutoCompaction_AboveThreshold_FiresCompact(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)

	// 10 messages × 7 words = 70 tokens. Sliding window default keeps
	// all 10. Budget is 100; ratio is 0.70 > 0.60 threshold → fire.
	seedMessages(t, store)

	messages := eng.BuildContextWindowForTest(context.Background(), "session-1", "next user turn")

	if summariser.calls.Load() != 1 {
		t.Fatalf("summariser calls = %d; want 1", summariser.calls.Load())
	}

	var foundSummary bool
	for _, m := range messages {
		if strings.Contains(m.Content, "[auto-compacted summary]") {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Fatalf("built context missing [auto-compacted summary] marker")
	}

	summary := eng.LastCompactionSummaryForTest()
	if summary == nil {
		t.Fatalf("LastCompactionSummary is nil after trigger; T11 rehydration will have no source")
	}
	if summary.Intent == "" {
		t.Fatalf("stored summary has empty Intent; parse+validate path skipped")
	}
}

// TestBuildContextWindowAutoCompaction_BelowThreshold_DoesNotFire
// asserts that the trigger stays silent when the recent-message load is
// under threshold. With budget=100 and threshold=0.80, a 70-token load
// is 0.70 ratio and must NOT trigger.
func TestBuildContextWindowAutoCompaction_BelowThreshold_DoesNotFire(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.80, true)

	seedMessages(t, store)

	messages := eng.BuildContextWindowForTest(context.Background(), "session-2", "next user turn")

	if summariser.calls.Load() != 0 {
		t.Fatalf("summariser calls = %d; want 0", summariser.calls.Load())
	}
	for _, m := range messages {
		if strings.Contains(m.Content, "[auto-compacted summary]") {
			t.Fatalf("built context contains summary marker but threshold was not crossed")
		}
	}
	if got := eng.LastCompactionSummaryForTest(); got != nil {
		t.Fatalf("LastCompactionSummary should be nil when trigger did not fire; got %+v", got)
	}
}

// TestBuildContextWindowAutoCompaction_Disabled_DoesNotFire asserts that
// the trigger is inert when CompressionConfig.AutoCompaction.Enabled is
// false, regardless of token load. This is the safety rail for existing
// deployments that have not opted into compression.
func TestBuildContextWindowAutoCompaction_Disabled_DoesNotFire(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.10, false)

	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "session-3", "next user turn")

	if summariser.calls.Load() != 0 {
		t.Fatalf("summariser calls = %d; want 0 (feature disabled)", summariser.calls.Load())
	}
}

// TestBuildContextWindowAutoCompaction_EmitsContextCompactedEvent asserts
// the T10b invariant from [[ADR - Tool-Call Atomicity in Context
// Compaction]]: a successful compaction pass publishes a
// plugin-events ContextCompactedEvent (topic EventContextCompacted) so
// subscribers can observe compaction frequency, latency, and savings
// without overloading the recall.summarized topic.
func TestBuildContextWindowAutoCompaction_EmitsContextCompactedEvent(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)

	var (
		mu       sync.Mutex
		observed []pluginevents.ContextCompactedEventData
	)
	eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(evt any) {
		e, ok := evt.(*pluginevents.ContextCompactedEvent)
		if !ok {
			t.Errorf("subscriber received %T; want *ContextCompactedEvent", evt)
			return
		}
		mu.Lock()
		observed = append(observed, e.Data)
		mu.Unlock()
	})

	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "session-t10b", "next user turn")

	// The bus dispatches synchronously but we still guard against a
	// future async change by polling briefly.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(observed)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(observed) != 1 {
		t.Fatalf("observed %d ContextCompactedEvent payloads; want 1", len(observed))
	}
	got := observed[0]
	if got.SessionID != "session-t10b" {
		t.Fatalf("event SessionID = %q; want session-t10b", got.SessionID)
	}
	if got.AgentID != "t10-agent" {
		t.Fatalf("event AgentID = %q; want t10-agent", got.AgentID)
	}
	if got.OriginalTokens <= 0 {
		t.Fatalf("event OriginalTokens = %d; want > 0", got.OriginalTokens)
	}
	if got.SummaryTokens <= 0 {
		t.Fatalf("event SummaryTokens = %d; want > 0 (summary text has length)", got.SummaryTokens)
	}
	if got.LatencyMS < 0 {
		t.Fatalf("event LatencyMS = %d; want >= 0", got.LatencyMS)
	}
}

// TestBuildContextWindowAutoCompaction_NoEventOnError asserts that a
// failed compaction pass does NOT emit the success event. Subscribers
// must not see phantom compactions; fail-soft means both the summary
// and the event are suppressed.
func TestBuildContextWindowAutoCompaction_NoEventOnError(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{err: errors.New("sim outage")}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)

	var counter atomic.Int32
	eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(_ any) {
		counter.Add(1)
	})

	seedMessages(t, store)
	_ = eng.BuildContextWindowForTest(context.Background(), "session-t10b-err", "next user turn")

	if counter.Load() != 0 {
		t.Fatalf("ContextCompactedEvent fired %d times on error; want 0", counter.Load())
	}
}

// TestBuildContextWindowAutoCompaction_CompactError_FailsOpen asserts
// that when the compactor errors, the engine still returns a window
// (falling back to the uncompacted path) rather than panicking or
// returning empty. The threshold is crossed and the summariser errors —
// the built messages must still contain the system prompt and the
// original recent messages.
func TestBuildContextWindowAutoCompaction_CompactError_FailsOpen(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{err: errors.New("simulated summariser outage")}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)

	seedMessages(t, store)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	messages := eng.BuildContextWindowForTest(ctx, "session-4", "next user turn")

	if len(messages) == 0 {
		t.Fatalf("engine returned no messages on compaction failure; must fail open")
	}
	for _, m := range messages {
		if strings.Contains(m.Content, "[auto-compacted summary]") {
			t.Fatalf("compaction errored but marker still present; wrong fallback")
		}
	}
	if eng.LastCompactionSummaryForTest() != nil {
		t.Fatalf("stored summary should be nil when compaction errored")
	}
}

// TestBuildContextWindowAutoCompaction_EmitsCompressionTokensSavedCounter
// asserts that a successful compaction invokes the configured
// tracer.Recorder with a RecordCompressionTokensSaved observation whose
// delta equals OriginalTokens - SummaryTokens (the same numbers carried
// on the ContextCompactedEvent). Operators rely on this counter to
// validate the L2 path end-to-end against Prometheus.
func TestBuildContextWindowAutoCompaction_EmitsCompressionTokensSavedCounter(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	rec := &engineTestRecorder{}
	eng, store := newTestEngineWithCompactorAndRecorder(t, summariser, 0.60, true, rec)

	var observed []pluginevents.ContextCompactedEventData
	var mu sync.Mutex
	eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(evt any) {
		e, ok := evt.(*pluginevents.ContextCompactedEvent)
		if !ok {
			t.Errorf("subscriber received %T; want *ContextCompactedEvent", evt)
			return
		}
		mu.Lock()
		observed = append(observed, e.Data)
		mu.Unlock()
	})

	seedMessages(t, store)
	_ = eng.BuildContextWindowForTest(context.Background(), "session-metrics", "next user turn")

	calls := rec.saved()
	if len(calls) != 1 {
		t.Fatalf("RecordCompressionTokensSaved calls = %d; want 1", len(calls))
	}
	if calls[0].agentID != "t10-agent" {
		t.Fatalf("recorder agent ID = %q; want %q", calls[0].agentID, "t10-agent")
	}

	mu.Lock()
	if len(observed) != 1 {
		mu.Unlock()
		t.Fatalf("observed %d compacted events; want 1", len(observed))
	}
	evt := observed[0]
	mu.Unlock()

	expectedDelta := evt.OriginalTokens - evt.SummaryTokens
	if calls[0].tokensSaved != expectedDelta {
		t.Fatalf("recorder tokensSaved = %d; want OriginalTokens-SummaryTokens = %d",
			calls[0].tokensSaved, expectedDelta)
	}
}

// TestBuildContextWindowAutoCompaction_NoRecorder_NoPanic asserts the
// engine is safe to use without a Recorder configured — the compaction
// path must not dereference a nil recorder.
func TestBuildContextWindowAutoCompaction_NoRecorder_NoPanic(t *testing.T) {
	t.Parallel()

	summariser := &recordingSummariser{response: buildSummaryJSON(t)}
	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)

	seedMessages(t, store)
	_ = eng.BuildContextWindowForTest(context.Background(), "session-no-rec", "next user turn")
	// Reaching here without panicking is the assertion.
}
