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
	return tool.Result{Output: "fake output"}, f.err
}

var _ = Describe("Engine.executeToolCall gate-error promotion", func() {
	var eng *engine.Engine

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		eng = engine.New(engine.Config{
			// PR7 (Coordinator Over-Execution, May 2026) — the
			// runtime tool gate now rejects tool calls that are not
			// in the agent's effective toolset. The fakes below
			// (`fake-gate-tool`, `fake-soft-tool`, `fake-clean-tool`)
			// must therefore be declared in capabilities.tools or
			// the gate intercepts before the original gate-error /
			// soft-fail assertions can fire. The pre-PR7 fail-open
			// runtime would have run them regardless of manifest
			// declaration; the pin update keeps these specs
			// focused on the swarm.GateError promotion contract
			// rather than on the manifest gate that PR7 adds.
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

// D9 (Agent Runtime Quality plan, May 2026): opt-in hard-gate that
// rejects non-todowrite tool calls once the per-session counter passes
// the >3 threshold. Default-off (the soft-nudge D6 default ships
// separately); flag-on closes the user constraint "mandatory AND stick
// to them".
//
// Seam decision: dispatch boundary (executeToolCall entry) rather than
// the tool-availability path (buildAllowedToolSetFor). Tool-availability
// is per-manifest and cannot observe per-session chain state; the
// dispatch boundary already sees every call and has the sessionID in
// scope. The brief flagged this as a non-blocking caveat and asked the
// implementer to spike the seam — chose the dispatch path because the
// chain-counter contract is a turn-time property, not a tool-set
// property.
var _ = Describe("Engine.executeToolCall D9 todo_strict_mode gate", func() {
	makeEngine := func(strict bool) *engine.Engine {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})
		eng := engine.New(engine.Config{
			// PR7 (Coordinator Over-Execution, May 2026) — the
			// D9 specs drive bash/read/todowrite through the
			// dispatch path to exercise the chain counter. The
			// runtime tool gate requires those tools to be in
			// capabilities.tools; under the inheritance floor
			// todowrite is implicit but bash and read must be
			// declared. Without this declaration the new gate
			// fires before todoStrictGate's chain counter
			// observes the call.
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

	When("TodoStrictMode is false (the v1 default)", func() {
		It("does not reject even after many non-todowrite tool calls in a row", func() {
			eng := makeEngine(false)
			// Six non-todowrite calls — would trip the strict mode if enabled.
			for i := 0; i < 6; i++ {
				result, err := runTool(eng, "sess-soft", "bash")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.IsError).To(BeFalse(),
					"strict mode is off — no gate should fire")
				Expect(result.Output).To(Equal("fake output"))
			}
		})
	})

	When("TodoStrictMode is true", func() {
		It("permits the first 3 non-todowrite tool calls without intervention", func() {
			eng := makeEngine(true)
			for i := 0; i < 3; i++ {
				result, err := runTool(eng, "sess-strict", "bash")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.IsError).To(BeFalse(),
					"strict mode rejects the FOURTH call, not the first three")
				Expect(result.Output).To(Equal("fake output"))
			}
		})

		It("rejects the FOURTH non-todowrite tool call with a structured tool_result error", func() {
			eng := makeEngine(true)
			// Fire 3 non-todowrite calls (all succeed).
			for i := 0; i < 3; i++ {
				_, err := runTool(eng, "sess-strict", "bash")
				Expect(err).NotTo(HaveOccurred())
			}
			// Fourth call must be rejected.
			result, err := runTool(eng, "sess-strict", "read")
			Expect(err).NotTo(HaveOccurred(),
				"the gate is an IsError tool_result, not a Go error — the agent's tool loop sees it and is expected to call todowrite next")
			Expect(result.IsError).To(BeTrue())
			Expect(result.Output).To(ContainSubstring("todo_strict_mode"))
			Expect(result.Output).To(ContainSubstring("todowrite"))
			Expect(result.Output).To(ContainSubstring("'read'"),
				"the message must name the tool that was rejected so the model knows which call to defer")
		})

		It("resets the counter on a todowrite call so the next batch fires fresh", func() {
			eng := makeEngine(true)
			// Run 3 non-todowrite calls.
			for i := 0; i < 3; i++ {
				_, err := runTool(eng, "sess-strict", "bash")
				Expect(err).NotTo(HaveOccurred())
			}
			// Call todowrite — resets the counter.
			result, err := runTool(eng, "sess-strict", "todowrite")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse(),
				"todowrite itself is never gated by D9 — it is the gate's resolution path")

			// Next 3 non-todowrite calls should succeed again.
			for i := 0; i < 3; i++ {
				result, err := runTool(eng, "sess-strict", "bash")
				Expect(err).NotTo(HaveOccurred())
				Expect(result.IsError).To(BeFalse(),
					"counter must reset after todowrite; the gate fires only on the next batch's 4th call")
			}
		})

		It("counts per-session, not globally — session A's tool calls do not gate session B", func() {
			eng := makeEngine(true)
			// Session A burns its budget.
			for i := 0; i < 4; i++ {
				result, _ := runTool(eng, "sess-A", "bash")
				if i < 3 {
					Expect(result.IsError).To(BeFalse())
				} else {
					Expect(result.IsError).To(BeTrue())
				}
			}
			// Session B starts fresh — no gate, even though session A is gated.
			result, err := runTool(eng, "sess-B", "bash")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.IsError).To(BeFalse(),
				"per-session counters prevent one agent's behaviour from gating another")
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
