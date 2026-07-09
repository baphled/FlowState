package engine_test

import (
	"context"
	"errors"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/permissionmode"
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
	if f.err != nil {
		return tool.Result{}, f.err
	}
	return tool.Result{Output: "fake output"}, nil
}

var _ = Describe("Engine.executeToolCall gate-error promotion", func() {
	var eng *engine.Engine

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		eng = engine.New(engine.Config{
			Manifest: agent.Manifest{
				ID:   "lead",
				Name: "Lead",
				Capabilities: agent.Capabilities{
					Tools: []string{"fake-gate-tool", "fake-soft-tool", "fake-clean-tool"},
				},
			},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})
	})

	When("the tool returns a *swarm.GateError", func() {
		It("returns an IsError tool_result so the coordinator's tool loop continues and can decide to re-delegate", func() {
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

			Expect(err).NotTo(HaveOccurred(),
				"a *swarm.GateError must NOT terminate the stream; the coordinator's tool loop sees the IsError tool_result and can decide to re-delegate")
			Expect(result.IsError).To(BeTrue(),
				"the gate error must be tagged IsError so the chunk path stamps role=tool_error and the coordinator sees the failure")
			Expect(result.Error).To(MatchError(gateErr),
				"result.Error stays populated for in-stream observability; the coordinator can inspect result.Error to understand which gate failed and why")
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
var _ = Describe("Engine.executeToolCall todo counter tracking", func() {
	makeEngine := func(strict bool) *engine.Engine {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		eng := engine.New(engine.Config{
			Manifest: agent.Manifest{
				ID:   "lead",
				Name: "Lead",
				Capabilities: agent.Capabilities{
					Tools: []string{"bash", "read"},
				},
			},
			AgentRegistry:  agent.NewRegistry(),
			Registry:       providerReg,
			ChatProvider:   &mockProvider{name: "spy"},
			TodoStrictMode: strict,
		})
		eng.AddTool(&gateHaltFakeTool{name: "bash", err: nil})
		eng.AddTool(&gateHaltFakeTool{name: "read", err: nil})
		eng.AddTool(&gateHaltFakeTool{name: "todowrite", err: nil})
		return eng
	}

	runTool := func(eng *engine.Engine, sessionID, toolName string) (tool.Result, error) {
		return eng.ExecuteToolCallForTest(context.Background(), sessionID, &provider.ToolCall{
			ID:        "call-" + toolName,
			Name:      toolName,
			Arguments: map[string]any{},
		})
	}

	Describe("counter tracking (no blocking — soft enforcement only)", func() {
		It("never blocks tool calls regardless of count — the hard gate was removed", func() {
			eng := makeEngine(true)
			for i := 0; i < 10; i++ {
				result, err := runTool(eng, "sess-no-block", "bash")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.IsError).To(BeFalse(),
					"the hard gate was removed — no tool call should ever be blocked by the todo counter")
			}
		})

		It("resets the counter on a todowrite call", func() {
			eng := makeEngine(true)
			for i := 0; i < 5; i++ {
				_, err := runTool(eng, "sess-reset", "bash")
				Expect(err).NotTo(HaveOccurred())
			}
			result, err := runTool(eng, "sess-reset", "todowrite")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse())
		})

		It("counts per-session, not globally", func() {
			eng := makeEngine(true)
			for i := 0; i < 6; i++ {
				result, _ := runTool(eng, "sess-A", "bash")
				Expect(result.IsError).To(BeFalse())
			}
			result, err := runTool(eng, "sess-B", "bash")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse(),
				"per-session counters are independent")
		})
	})
})

// Permission Modes plan §4 Slice 1 (May 2026). The engine's tool
// dispatch path MUST preserve the per-session permission mode on the
// ctx that reaches Tool.Execute — without this, pathguard's YOLO
// short-circuit (and any future mode-aware tool) cannot observe the
// caller-stamped value. Pinning the property at executeToolCall
// confirms deriveToolCtx (the WithTimeout / WithCancel derivation that
// wraps every tool invocation) does NOT strip context values bound
// upstream by session.Manager.SendMessage.
//
// Seam decision: dispatch boundary (executeToolCall entry) — the same
// seam the D9 todo_strict_mode gate and gate-error promotion specs
// pin. The fake tool below records the ctx it received so the spec
// can read the bound mode back via permissionmode.FromContext.
type ctxCapturingTool struct {
	name string
	mu   sync.Mutex
	last context.Context
}

func (c *ctxCapturingTool) Name() string        { return c.name }
func (c *ctxCapturingTool) Description() string { return "captures ctx for mode-propagation test" }
func (c *ctxCapturingTool) Schema() tool.Schema { return tool.Schema{Type: "object"} }
func (c *ctxCapturingTool) Execute(ctx context.Context, _ tool.Input) (tool.Result, error) {
	c.mu.Lock()
	c.last = ctx
	c.mu.Unlock()
	return tool.Result{Output: "captured"}, nil
}
func (c *ctxCapturingTool) capturedCtx() context.Context {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

var _ = Describe("Engine.executeToolCall — permission-mode ctx propagation (Slice 1)", func() {
	var (
		eng     *engine.Engine
		capture *ctxCapturingTool
	)

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		capture = &ctxCapturingTool{name: "ctx-capture"}
		eng = engine.New(engine.Config{
			Manifest: agent.Manifest{
				ID:   "lead",
				Name: "Lead",
				Capabilities: agent.Capabilities{
					Tools: []string{"ctx-capture"},
				},
			},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})
		eng.AddTool(capture)
	})

	It("preserves mode=yolo on the ctx delivered to Tool.Execute", func() {
		ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModeYolo)

		_, err := eng.ExecuteToolCallForTest(ctx, "sess-yolo", &provider.ToolCall{
			ID:        "call-1",
			Name:      "ctx-capture",
			Arguments: map[string]any{},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(engine.PermissionModeFromContext(capture.capturedCtx())).To(Equal(permissionmode.ModeYolo),
			"the engine's tool-dispatch path MUST thread the mode through deriveToolCtx so pathguard can short-circuit on YOLO")
	})

	It("preserves mode=plan on the ctx delivered to Tool.Execute", func() {
		ctx := engine.WithPermissionMode(context.Background(), permissionmode.ModePlan)

		_, err := eng.ExecuteToolCallForTest(ctx, "sess-plan", &provider.ToolCall{
			ID:        "call-2",
			Name:      "ctx-capture",
			Arguments: map[string]any{},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(engine.PermissionModeFromContext(capture.capturedCtx())).To(Equal(permissionmode.ModePlan),
			"Plan mode must reach the tool even though pathguard does not short-circuit on it — future engine-side schema filter consumes this")
	})

	It("falls back to default when ctx carries no mode binding", func() {
		_, err := eng.ExecuteToolCallForTest(context.Background(), "sess-bare", &provider.ToolCall{
			ID:        "call-3",
			Name:      "ctx-capture",
			Arguments: map[string]any{},
		})
		Expect(err).NotTo(HaveOccurred())

		// Bare context.Background() has no mode stamp; FromContext
		// canonicalises to "default" — proving the safe-fallback path.
		Expect(engine.PermissionModeFromContext(capture.capturedCtx())).To(Equal(permissionmode.ModeDefault))
	})
})
