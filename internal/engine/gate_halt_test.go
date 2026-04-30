package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// gateHaltFakeTool returns the configured error from Execute. Used to
// simulate a tool whose post-stream gate dispatch refused validation.
type gateHaltFakeTool struct {
	name string
	err  error
}

func (f *gateHaltFakeTool) Name() string        { return f.name }
func (f *gateHaltFakeTool) Description() string { return "fake" }
func (f *gateHaltFakeTool) Schema() tool.Schema { return tool.Schema{Type: "object"} }
func (f *gateHaltFakeTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{Output: "fake output"}, f.err
}

var _ = Describe("Engine.executeToolCall gate-error promotion", func() {
	var eng *engine.Engine

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		eng = engine.New(engine.Config{
			Manifest:      agent.Manifest{ID: "lead", Name: "Lead"},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})
	})

	When("the tool returns a *swarm.GateError", func() {
		It("propagates the error as the OUTER return so streamWithTools terminates", func() {
			gateErr := &swarm.GateError{
				GateName: "post-explorer-evidence",
				GateKind: "builtin:result-schema",
				When:     swarm.LifecyclePostMember,
				SwarmID:  "bug-hunt",
				MemberID: "explorer",
				Reason:   "schema validation failed: required: missing properties: [file]",
			}
			eng.AddTool(&gateHaltFakeTool{name: "fake-gate-tool", err: gateErr})

			result, err := eng.ExecuteToolCallForTest(context.Background(), "sess-1", &provider.ToolCall{
				ID:        "call-1",
				Name:      "fake-gate-tool",
				Arguments: map[string]any{},
			})

			Expect(err).To(HaveOccurred(),
				"a *swarm.GateError must propagate to the outer error so streamWithTools issues a Done:true Error chunk and aborts the dispatch — historic soft-fail behaviour was the bug-hunt enforcement gap")
			var got *swarm.GateError
			Expect(errors.As(err, &got)).To(BeTrue())
			Expect(got.GateName).To(Equal("post-explorer-evidence"))
			Expect(result.Error).To(MatchError(gateErr),
				"result.Error stays populated for in-stream observability; the outer return is what aborts")
		})
	})

	When("the tool returns a non-gate error", func() {
		It("keeps the historical soft-fail behaviour: outer error is nil, result.Error carries the cause", func() {
			toolErr := errors.New("simulated transient bash failure")
			eng.AddTool(&gateHaltFakeTool{name: "fake-soft-tool", err: toolErr})

			result, err := eng.ExecuteToolCallForTest(context.Background(), "sess-1", &provider.ToolCall{
				ID:        "call-2",
				Name:      "fake-soft-tool",
				Arguments: map[string]any{},
			})

			Expect(err).NotTo(HaveOccurred(),
				"non-gate tool errors must NOT terminate the stream; the agent's tool loop sees the IsError tool_result chunk and decides whether to retry, replan, or move on")
			Expect(result.Error).To(MatchError(toolErr))
			Expect(result.Output).To(Equal("fake output"))
		})
	})

	When("the tool returns no error at all", func() {
		It("returns a clean result", func() {
			eng.AddTool(&gateHaltFakeTool{name: "fake-clean-tool", err: nil})

			result, err := eng.ExecuteToolCallForTest(context.Background(), "sess-1", &provider.ToolCall{
				ID:        "call-3",
				Name:      "fake-clean-tool",
				Arguments: map[string]any{},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Error).NotTo(HaveOccurred())
			Expect(result.Output).To(Equal("fake output"))
		})
	})
})
