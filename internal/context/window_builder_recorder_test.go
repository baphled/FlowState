// Package context_test — WindowBuilder tracer.Recorder wiring.
//
// The WindowBuilder emits a context-window gauge via a tracer.Recorder
// when one is attached with WithRecorder. The gauge carries the agent
// ID supplied on the manifest so operators can distinguish windows
// built for different agents during the same run.
package context_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// recordingRecorder captures RecordContextWindowTokens calls for spec
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
func (r *recordingRecorder) RecordCompressionOverheadTokens(string, int) {}

var _ = Describe("WindowBuilder Recorder wiring", func() {
	// WithRecorder_ReturnsReceiver asserts the fluent setter follows
	// the same convention as WithMetrics / WithSplitter.
	It("WithRecorder returns the receiver for fluent chaining", func() {
		builder := contextpkg.NewWindowBuilder(stubCounter{})
		rec := &recordingRecorder{}
		Expect(builder.WithRecorder(rec)).To(BeIdenticalTo(builder),
			"WithRecorder did not return the receiver")
	})

	// Build_EmitsContextWindowGauge: Build calls
	// RecordContextWindowTokens with the manifest's agent ID and the
	// assembled window's token count. The stubCounter assigns one
	// token per character so the expected value can be computed from
	// the message contents.
	It("Build emits the context-window gauge with manifest agent ID + assembled token count", func() {
		rec := &recordingRecorder{}
		builder := contextpkg.NewWindowBuilder(stubCounter{}).WithRecorder(rec)

		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred(), "store")
		store.Append(provider.Message{Role: "user", Content: "hello"})

		manifest := &agent.Manifest{
			ID:                "metrics-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: agent.DefaultContextManagement(),
		}
		result := builder.Build(manifest, store, 10_000)

		Expect(rec.windowCalls).To(HaveLen(1), "expected 1 gauge emission")
		call := rec.windowCalls[0]
		Expect(call.agentID).To(Equal("metrics-agent"))
		Expect(call.tokens).To(Equal(result.TokensUsed),
			"gauge tokens must match BuildResult.TokensUsed")
		Expect(call.tokens).To(BeNumerically(">", 0),
			"gauge tokens should be positive for a non-empty window")
	})

	// Build_NoRecorder_NoEmission: the builder stays silent when no
	// recorder is attached. Deployments that never enable metrics must
	// pay no recorder overhead. If the builder dereferences a nil
	// recorder, this spec panics — caught by the Ginkgo deferred Fail.
	It("Build stays silent (no panic) when no recorder is attached", func() {
		builder := contextpkg.NewWindowBuilder(stubCounter{})
		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "test-model")
		Expect(err).NotTo(HaveOccurred(), "store")
		store.Append(provider.Message{Role: "user", Content: "hi"})

		manifest := &agent.Manifest{
			ID:                "silent-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: agent.DefaultContextManagement(),
		}
		Expect(func() { _ = builder.Build(manifest, store, 10_000) }).
			NotTo(Panic(), "Build must not panic without a recorder")
	})
})
