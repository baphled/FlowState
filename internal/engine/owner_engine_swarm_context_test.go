package engine_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
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
		eng       *engine.Engine
		registry  *agent.Registry
		leadID    = "planner"
		defaultID = "default-assistant"
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

	When("the swarm scope is attached via ctx but SetSwarmContext was NEVER called (swarm-id dispatch path)", func() {
		// The swarm-id entry path (agent_id == a SWARM ID) never reached
		// the dispatcher's `if swarmActive` block before the fix, so
		// SetSwarmContext was never called and e.swarmContext stayed nil
		// — but the dispatcher STILL attaches the per-turn scope to ctx
		// via swarm.WithScope(streamCtx, swarmCtx) at dispatcher.go:765
		// on EVERY turn. The cap previously keyed solely on the engine
		// field e.swarmContext, so with the field nil the cap's first
		// guard returned `allowed` unchanged and bash leaked through.
		//
		// Re-keying the cap to swarm.ScopeFromContext(ctx) — the value
		// EVERY dispatch path attaches — makes the cap fire on this entry
		// path too, regardless of whether SetSwarmContext was called.
		// This drives that exact shape: scope in ctx, e.swarmContext nil,
		// leaky default manifest bound, lead names a coordination-only
		// persona — the cap MUST still strip bash.
		It("caps the effective toolset at the lead manifest using the ctx scope — execution tools are stripped", func() {
			scope := &swarm.Context{
				SwarmID:   "planning-loop",
				LeadAgent: leadID,
				Members:   []string{"analyst", "plan-writer"},
			}
			// Attach the scope via ctx, bind the LEAKY default manifest —
			// but deliberately do NOT call eng.SetSwarmContext, so
			// e.swarmContext stays nil. This is the swarm-id path: the
			// pre-fix leak where Engine.Stream fell through to the
			// default-assistant manifest (which HAS bash).
			ctx := swarm.WithScope(context.Background(), scope)
			leaky, ok := registry.Get(defaultID)
			Expect(ok).To(BeTrue())
			ctx = engine.WithBoundManifest(ctx, *leaky)

			allowed := eng.EffectiveAllowedToolsForTest(ctx)

			Expect(allowed["bash"]).To(BeFalse(),
				"the cap must fire off the ctx-attached swarm scope even though SetSwarmContext was never called — the swarm-id dispatch path attaches the scope via ctx but never sets e.swarmContext, and the leaky default-assistant manifest is bound; bash must be stripped")
			Expect(allowed["read"]).To(BeFalse(),
				"read must be stripped off the ctx-scope path — the planner lead does not declare it")
			Expect(allowed["write"]).To(BeFalse(),
				"write must be stripped off the ctx-scope path — the planner lead does not declare it")
		})

		It("rejects a lead bash call at the runtime gate when only the ctx scope is set", func() {
			scope := &swarm.Context{
				SwarmID:   "planning-loop",
				LeadAgent: leadID,
				Members:   []string{"analyst", "plan-writer"},
			}
			ctx := swarm.WithScope(context.Background(), scope)
			leaky, ok := registry.Get(defaultID)
			Expect(ok).To(BeTrue())
			ctx = engine.WithBoundManifest(ctx, *leaky)

			fakeBash := &executableMockTool{
				name:        "bash",
				description: "fake bash",
				execResult:  tool.Result{Output: "should never run"},
			}
			eng.AddTool(fakeBash)

			result, err := eng.ExecuteToolCallForTest(ctx, "sess-swarmid-cap", &provider.ToolCall{
				ID:        "call-bash",
				Name:      "bash",
				Arguments: map[string]any{},
			})

			Expect(err).NotTo(HaveOccurred(),
				"the gate emits an IsError tool_result, not a Go error")
			Expect(result.IsError).To(BeTrue(),
				"the orchestrator's bash call on the swarm-id path must be rejected — the cap reads the ctx scope, not e.swarmContext")
			Expect(errors.Is(result.Error, tool.ErrToolNotFound)).To(BeTrue(),
				"the rejection wraps the shared ErrToolNotFound sentinel")
			Expect(fakeBash.execCalled).To(BeFalse(),
				"the gate must fire BEFORE Execute — the orchestrator's bash body must never run on the swarm-id path")
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

// Planning-Loop Async-Member Pipeline Halt (May 2026).
//
// The planning-loop swarm stalled after its first delegation wave and
// produced no plan: the lead (planner) delegated its roster members
// (explorer, librarian) with run_in_background:true. The async path
// (executeAsync → executeBackgroundTask) carries NO post-member gate
// and NO lifecycle flush, so member output is never gated, never
// surfaced to the root coordination_store, and the lead is never
// re-entered to sequence the next member — the lead's turn "succeeds"
// after firing two detached goroutines and the pipeline dies.
//
// The pipeline is LEAD-LLM-driven: a swarm lead MUST delegate each
// roster member SYNCHRONOUSLY (executeSync → post-member gate →
// lead-resume). Background delegation of a roster member must be
// impossible regardless of what the model requested. These specs pin
// that invariant at the Execute boundary while guarding the two paths
// that must stay async-capable: standalone (non-swarm) background
// delegations, and the sub-swarm dispatch path.
var _ = Describe("Swarm lead member delegation is forced synchronous", func() {
	var (
		leadEng      *engine.Engine
		memberEng    *engine.Engine
		bgMgr        *engine.BackgroundTaskManager
		delegateTool *engine.DelegateTool
	)

	BeforeEach(func() {
		providerReg := provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})

		leadEng = engine.New(engine.Config{
			Manifest:      agent.Manifest{ID: "planner", Name: "Planner", Delegation: agent.Delegation{CanDelegate: true}},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})
		memberEng = engine.New(engine.Config{
			Manifest:      agent.Manifest{ID: "explorer", Name: "Explorer"},
			AgentRegistry: agent.NewRegistry(),
			Registry:      providerReg,
			ChatProvider:  &mockProvider{name: "spy"},
		})

		bgMgr = engine.NewBackgroundTaskManager()
		delegateTool = engine.NewDelegateToolWithBackground(
			map[string]*engine.Engine{"explorer": memberEng},
			agent.Delegation{CanDelegate: true},
			"planner",
			bgMgr,
			nil,
		).WithStreamers(map[string]streaming.Streamer{
			"explorer": streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "explorer findings", Done: true}
				close(ch)
				return ch, nil
			}),
		})
	})

	When("a swarm lead delegates a roster member with run_in_background:true", func() {
		It("forces the delegation synchronous — never returns a background task_id and never launches a background task", func() {
			// Install the active swarm context on the lead engine and
			// route the lookup through the owner engine, exactly as the
			// production lead turn does.
			leadEng.SetSwarmContext(&swarm.Context{
				SwarmID:     "planning-loop",
				LeadAgent:   "planner",
				Members:     []string{"explorer"},
				ChainPrefix: "planning",
			})
			delegateTool.WithOwnerEngine(leadEng)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "lead-sess")
			result, err := delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type":     "explorer",
					"message":           "investigate the codebase",
					"run_in_background": true,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			// Async path returns {"task_id":...,"status":"running"}
			// immediately. The forced-sync path returns the member's
			// actual response inline. The load-bearing distinction: a
			// swarm-member delegation must NEVER yield a background task
			// handle, because that handle means the post-member gate and
			// lead-resume were skipped.
			Expect(result.Output).NotTo(ContainSubstring("task_id"),
				"a swarm lead delegating a roster member must go through executeSync, not executeAsync — a task_id means the member ran detached with no gate and no lead-resume")
			Expect(result.Output).NotTo(ContainSubstring(`"status": "running"`),
				"forced-sync delegation must not report the async 'running' status")
			Expect(result.Output).To(ContainSubstring("explorer findings"),
				"the forced-sync path returns the member's response inline so the lead can sequence the next member")

			// No background task may be launched for a roster member.
			Consistently(func() int {
				return bgMgr.ActiveCount()
			}, 200*time.Millisecond, 20*time.Millisecond).Should(Equal(0),
				"forcing sync means no goroutine is detached — the background manager must see zero active tasks for a swarm-member delegation")
			Expect(bgMgr.List()).To(BeEmpty(),
				"no background task record may exist for a force-synced swarm-member delegation")
		})
	})

	When("a standalone (non-swarm) caller delegates with run_in_background:true", func() {
		It("still runs asynchronously — the force-sync rule is swarm-member-only", func() {
			// No SetSwarmContext, no WithOwnerEngine → no active swarm
			// context. Legitimate standalone background delegation must
			// stay async.
			ctx := context.WithValue(context.Background(), session.IDKey{}, "standalone-sess")
			result, err := delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type":     "explorer",
					"message":           "background investigation",
					"run_in_background": true,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("task_id"),
				"a standalone background delegation has no active swarm context, so the force-sync rule must not fire — it stays async and returns a task handle")
			Expect(result.Output).To(ContainSubstring("running"),
				"standalone background delegation reports the async 'running' status")
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
