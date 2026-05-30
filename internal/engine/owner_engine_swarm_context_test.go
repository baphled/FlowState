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

var _ = Describe("DelegateTool.WithOwnerEngine swarm-context lookup", func() {
	var (
		leadEng      *engine.Engine
		delegateTool *engine.DelegateTool
	)

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})

		leadEng = engine.New(engine.Config{
			Manifest:      agent.Manifest{ID: "Senior-Engineer", Name: "Lead", Delegation: agent.Delegation{CanDelegate: true}},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})

		// Targets map intentionally EXCLUDES the lead — mirrors
		// buildDelegateMaps' production behaviour.
		targetEng := engine.New(engine.Config{
			Manifest:      agent.Manifest{ID: "explorer", Name: "Explorer"},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})
		engines := map[string]*engine.Engine{"explorer": targetEng}

		delegateTool = engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "Senior-Engineer")
	})

	When("the lead's engine has a swarm context set but the targets map excludes the lead", func() {
		It("finds the swarm context via WithOwnerEngine, not via the targets map", func() {
			swarmCtx := &swarm.Context{
				SwarmID:     "bug-hunt",
				LeadAgent:   "Senior-Engineer",
				Members:     []string{"explorer"},
				ChainPrefix: "bug-hunt",
				Gates: []swarm.GateSpec{{
					Name:      "test-gate",
					Kind:      "builtin:result-schema",
					When:      swarm.LifecyclePostMember,
					Target:    "explorer",
					SchemaRef: "evidence-bundle-v1",
					OutputKey: "codebase-findings",
				}},
			}
			leadEng.SetSwarmContext(swarmCtx)

			// Without WithOwnerEngine: dispatch silently no-ops because
			// the targets map doesn't contain "Senior-Engineer".
			err := delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer", "")
			Expect(err).NotTo(HaveOccurred(),
				"baseline: without owner engine, no gate runs, no error")

			// With WithOwnerEngine: the swarm context resolves through
			// the lead's engine, the gate fires, and the schema gate
			// finds NO payload at the expected coord-store key
			// (because the test didn't seed one) so it fails — proving
			// the gate actually ran.
			delegateTool.WithOwnerEngine(leadEng)
			err = delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer", "")
			// The gate would fire, but the runner is nil in this
			// fixture so it'd no-op early. The point is that the
			// activeSwarmContext lookup now succeeds — instrument by
			// installing a fake runner.
			_ = err

			// Now install a fake runner that records calls; the gate
			// should fire and the recorder should see one invocation.
			recorder := &fakeRunner{}
			delegateTool.WithGateRunner(recorder)
			err = delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer", "")
			Expect(err).NotTo(HaveOccurred(),
				"recorder reports pass; we only care that it WAS called")
			Expect(recorder.calls).To(HaveLen(1),
				"with WithOwnerEngine, the gate runner sees the dispatch — pre-fix this was zero")
			Expect(recorder.calls[0].Name).To(Equal("test-gate"))
		})
	})

	When("the lead's engine has no swarm context", func() {
		It("falls through quietly so non-swarm callers keep working", func() {
			delegateTool.WithOwnerEngine(leadEng)
			recorder := &fakeRunner{}
			delegateTool.WithGateRunner(recorder)

			err := delegateTool.DispatchPostMemberGatesForTest(context.Background(), "explorer", "")

			Expect(err).NotTo(HaveOccurred())
			Expect(recorder.calls).To(BeEmpty(),
				"no swarm context → no gates fire (the historical no-op behaviour for non-swarm dispatches)")
		})
	})
})

// Orchestrator Self-Execution (May 2026) — defence-in-depth tool cap.
//
// Part 1 of the fix binds the lead manifest on the auto-dispatch path so
// the runtime gate evaluates the lead turn against the lead's declared
// tools. Part 2 is the STRUCTURAL guarantee: even if a future caller
// fails to bind the lead manifest into ctx, a swarm-lead turn's effective
// toolset can never exceed the lead manifest's declared tools. This block
// drives the gate with the leaky default-assistant manifest bound (it HAS
// bash/read/write) while the installed swarm context names a coordination-
// only lead — the cap must intersect the two and forbid execution tools.
var _ = Describe("Engine swarm-lead tool cap (Orchestrator Self-Execution)", func() {
	var (
		eng         *engine.Engine
		registry    *agent.Registry
		leadID      = "planner"
		defaultID   = "default-assistant"
	)

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})

		registry = agent.NewRegistry()
		// The lead manifest grants ONLY coordination tools — no
		// bash/read/write. Mirrors ~/.config/flowstate/agents/planner.md.
		registry.Register(&agent.Manifest{
			ID:   leadID,
			Name: "Planner",
			Capabilities: agent.Capabilities{
				Tools: []string{"delegate", "coordination_store", "skill_load", "todowrite", "plan_list", "plan_read"},
			},
		})
		// The session-default manifest HAS execution tools — this is the
		// manifest the engine carries on the leaky auto-dispatch path
		// (no lead override), the root of the self-execution bug.
		leaky := agent.Manifest{
			ID:   defaultID,
			Name: "Default Assistant",
			Capabilities: agent.Capabilities{
				Tools: []string{"bash", "read", "write"},
			},
		}
		registry.Register(&leaky)

		eng = engine.New(engine.Config{
			Manifest:      leaky,
			AgentRegistry: registry,
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})
	})

	When("a swarm-lead turn runs with the leaky default manifest bound but a coordination-only lead installed", func() {
		It("caps the effective toolset at the lead manifest — execution tools are stripped", func() {
			swarmCtx := &swarm.Context{
				SwarmID:   "planning-loop",
				LeadAgent: leadID,
				Members:   []string{"analyst", "plan-writer"},
			}
			eng.SetSwarmContext(swarmCtx)

			allowed := eng.EffectiveAllowedToolsForTest(context.Background())

			// Part 2 is a CEILING, not an addition: a swarm-lead turn's
			// effective toolset can never EXCEED the lead manifest's
			// declared tools. With the leaky default-assistant bound the
			// ceiling strips the execution tools it carries (bash/read/
			// write) — the security property. It cannot conjure the
			// lead's delegate/coordination_store into a turn that bound
			// the wrong manifest; that is Part 1's job (bind the lead
			// manifest so those tools are present in the first place).
			Expect(allowed["bash"]).To(BeFalse(),
				"a swarm-lead turn must not surface bash — the lead manifest (planner) does not declare it; the cap intersects the bound manifest's tools with the lead's so an orchestrator turn physically cannot execute bash even when default-assistant is bound")
			Expect(allowed["read"]).To(BeFalse(),
				"read is an execution tool the planner lead does not declare — it must be stripped from a swarm-lead turn")
			Expect(allowed["write"]).To(BeFalse(),
				"write is an execution tool the planner lead does not declare — it must be stripped from a swarm-lead turn")
			// Invariant: every surviving tool is in the lead's set.
			leadSet := map[string]bool{"delegate": true, "coordination_store": true, "skill_load": true, "todowrite": true, "todo_update": true, "plan_list": true, "plan_read": true, "suggest_delegate": true, "background_output": true, "background_cancel": true}
			for name, on := range allowed {
				if on {
					Expect(leadSet[name]).To(BeTrue(),
						"every tool surviving the swarm-lead cap must be declared by the lead manifest — effective ⊆ lead set; offending tool: "+name)
				}
			}
		})

		It("is a no-op when the LEAD manifest is correctly bound (Part 1 working) — delegate and coordination_store survive", func() {
			swarmCtx := &swarm.Context{
				SwarmID:   "planning-loop",
				LeadAgent: leadID,
				Members:   []string{"analyst", "plan-writer"},
			}
			eng.SetSwarmContext(swarmCtx)

			// Bind the LEAD manifest into ctx, as Part 1's
			// WithStreamAgentOverride → Engine.Stream does in production.
			leadManifest, ok := registry.Get(leadID)
			Expect(ok).To(BeTrue())
			ctx := engine.WithBoundManifest(context.Background(), *leadManifest)

			allowed := eng.EffectiveAllowedToolsForTest(ctx)

			Expect(allowed["delegate"]).To(BeTrue(),
				"with the lead manifest bound, delegate is present and the cap (a no-op against the lead's own set) leaves it — delegation is how the orchestrator drives its member pipeline")
			Expect(allowed["coordination_store"]).To(BeTrue(),
				"the lead's coordination_store survives when the lead manifest is bound")
			Expect(allowed["bash"]).To(BeFalse(),
				"the lead manifest never declared bash, so it is absent whether or not the cap fires")
		})

		It("rejects a bash tool call attributed to the lead turn at the runtime gate without invoking Execute", func() {
			swarmCtx := &swarm.Context{
				SwarmID:   "planning-loop",
				LeadAgent: leadID,
				Members:   []string{"analyst", "plan-writer"},
			}
			eng.SetSwarmContext(swarmCtx)

			fakeBash := &executableMockTool{
				name:        "bash",
				description: "fake bash",
				execResult:  tool.Result{Output: "should never run"},
			}
			eng.AddTool(fakeBash)

			result, err := eng.ExecuteToolCallForTest(context.Background(), "sess-lead-cap", &provider.ToolCall{
				ID:        "call-bash",
				Name:      "bash",
				Arguments: map[string]any{},
			})

			Expect(err).NotTo(HaveOccurred(),
				"the gate emits an IsError tool_result, not a Go error")
			Expect(result.IsError).To(BeTrue(),
				"the orchestrator's bash call must be rejected — the swarm-lead cap forbids execution tools on a lead turn")
			Expect(errors.Is(result.Error, tool.ErrToolNotFound)).To(BeTrue(),
				"the rejection wraps the shared ErrToolNotFound sentinel")
			Expect(fakeBash.execCalled).To(BeFalse(),
				"the load-bearing assertion: the gate fires BEFORE Execute — the orchestrator's bash body must never run")
		})
	})

	When("no swarm context is installed", func() {
		It("leaves the engine manifest's tools untouched — the cap is swarm-lead-only", func() {
			allowed := eng.EffectiveAllowedToolsForTest(context.Background())

			Expect(allowed["bash"]).To(BeTrue(),
				"with no swarm context the cap must not fire — a standalone default-assistant turn keeps its declared execution tools")
		})
	})

	When("a swarm context is installed but the turn manifest is NOT the lead", func() {
		It("leaves the manifest's tools untouched — the cap targets only the lead persona", func() {
			// LeadAgent names a different agent than the bound/engine
			// manifest, so this turn is a MEMBER turn, not a lead turn.
			swarmCtx := &swarm.Context{
				SwarmID:   "planning-loop",
				LeadAgent: "some-other-lead",
				Members:   []string{defaultID},
			}
			eng.SetSwarmContext(swarmCtx)

			allowed := eng.EffectiveAllowedToolsForTest(context.Background())

			Expect(allowed["bash"]).To(BeTrue(),
				"the cap keys on swarmCtx.LeadAgent == manifest.ID; a member turn (different lead) keeps its own declared tools")
		})
	})
})

type fakeRunner struct {
	calls []swarm.GateSpec
	err   error
}

func (r *fakeRunner) Run(_ context.Context, gate swarm.GateSpec, _ swarm.GateArgs) error {
	r.calls = append(r.calls, gate)
	return r.err
}

var _ = errors.New // silence unused-import in non-builder configs
