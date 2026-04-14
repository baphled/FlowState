// Package context_test — WindowBuilder tracer.Recorder wiring.
//
// The WindowBuilder emits a context-window gauge via a tracer.Recorder
// when one is attached with WithRecorder. The gauge carries the agent
// ID supplied on the manifest so operators can distinguish windows
// built for different agents during the same run.
package context_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// recordingRecorder captures RecordContextWindowTokens calls for test
// assertion. It ignores every other Recorder method since
// WindowBuilder is only expected to touch the context-window gauge.
type recordingRecorder struct {
	windowCalls []windowObservation
	savedCalls  []savedObservation
}

type windowObservation struct {
	agentID string
	tokens  int
}

type savedObservation struct {
	agentID     string
	tokensSaved int
}

func (r *recordingRecorder) RecordRetry(string)                    {}
func (r *recordingRecorder) RecordValidationScore(string, float64) {}
func (r *recordingRecorder) RecordCriticResult(string, bool)       {}
func (r *recordingRecorder) RecordProviderLatency(string, string, float64) {
}
func (r *recordingRecorder) RecordContextWindowTokens(agentID string, tokens int) {
	r.windowCalls = append(r.windowCalls, windowObservation{agentID: agentID, tokens: tokens})
}
func (r *recordingRecorder) RecordCompressionTokensSaved(agentID string, tokensSaved int) {
	r.savedCalls = append(r.savedCalls, savedObservation{agentID: agentID, tokensSaved: tokensSaved})
}

// TestWindowBuilder_WithRecorder_ReturnsReceiver asserts the fluent
// setter follows the same convention as WithMetrics / WithSplitter.
func TestWindowBuilder_WithRecorder_ReturnsReceiver(t *testing.T) {
	t.Parallel()

	builder := contextpkg.NewWindowBuilder(stubCounter{})
	rec := &recordingRecorder{}
	if got := builder.WithRecorder(rec); got != builder {
		t.Fatalf("WithRecorder did not return the receiver")
	}
}

// TestWindowBuilder_Build_EmitsContextWindowGauge asserts that Build
// calls RecordContextWindowTokens with the manifest's agent ID and the
// assembled window's token count. The stubCounter assigns one token per
// character so the expected value can be computed from the message
// contents.
func TestWindowBuilder_Build_EmitsContextWindowGauge(t *testing.T) {
	t.Parallel()

	rec := &recordingRecorder{}
	builder := contextpkg.NewWindowBuilder(stubCounter{}).WithRecorder(rec)

	store, err := recall.NewFileContextStore(t.TempDir()+"/ctx.json", "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	store.Append(provider.Message{Role: "user", Content: "hello"})

	manifest := &agent.Manifest{
		ID:                "metrics-agent",
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: agent.DefaultContextManagement(),
	}
	result := builder.Build(manifest, store, 10_000)

	if len(rec.windowCalls) != 1 {
		t.Fatalf("expected 1 gauge emission, got %d", len(rec.windowCalls))
	}
	call := rec.windowCalls[0]
	if call.agentID != "metrics-agent" {
		t.Fatalf("gauge agent ID = %q; want %q", call.agentID, "metrics-agent")
	}
	if call.tokens != result.TokensUsed {
		t.Fatalf("gauge tokens = %d; want BuildResult.TokensUsed = %d", call.tokens, result.TokensUsed)
	}
	if call.tokens <= 0 {
		t.Fatalf("gauge tokens should be positive for a non-empty window, got %d", call.tokens)
	}
}

// TestWindowBuilder_Build_NoRecorder_NoEmission asserts the builder
// stays silent when no recorder is attached. Deployments that never
// enable metrics must pay no recorder overhead.
func TestWindowBuilder_Build_NoRecorder_NoEmission(t *testing.T) {
	t.Parallel()

	builder := contextpkg.NewWindowBuilder(stubCounter{})
	store, err := recall.NewFileContextStore(t.TempDir()+"/ctx.json", "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	store.Append(provider.Message{Role: "user", Content: "hi"})

	manifest := &agent.Manifest{
		ID:                "silent-agent",
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: agent.DefaultContextManagement(),
	}
	// If the builder dereferences a nil recorder, this will panic.
	_ = builder.Build(manifest, store, 10_000)
}
