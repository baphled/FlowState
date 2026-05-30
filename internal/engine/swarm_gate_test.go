package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

func reviewerProvider() *mockProvider {
	return &mockProvider{
		name: "reviewer-provider",
		streamChunks: []provider.StreamChunk{
			{Content: "review complete", Done: true},
		},
	}
}

func leadProvider() *mockProvider {
	return &mockProvider{
		name: "lead-provider",
		streamChunks: []provider.StreamChunk{
			{Content: "lead", Done: true},
		},
	}
}

func planningLoopSwarmContext() *swarm.Context {
	return &swarm.Context{
		SwarmID:     "planning-loop",
		LeadAgent:   "planner",
		Members:     []string{"plan-reviewer"},
		ChainPrefix: "planning",
		Gates: []swarm.GateSpec{
			{
				Name:      "post-member-plan-reviewer-result-schema",
				Kind:      "builtin:result-schema",
				SchemaRef: swarm.ReviewVerdictV1Name,
				When:      swarm.LifecyclePostMember,
				Target:    "plan-reviewer",
			},
		},
	}
}

type recordingRunner struct {
	calls []swarm.GateSpec
	fail  map[string]error
}

func (r *recordingRunner) Run(_ context.Context, gate swarm.GateSpec, _ swarm.GateArgs) error {
	r.calls = append(r.calls, gate)
	if r.fail != nil {
		if err, ok := r.fail[gate.Name]; ok {
			return err
		}
	}
	return nil
}

func swarmContextWithGates(gates []swarm.GateSpec) *swarm.Context {
	return &swarm.Context{
		SwarmID:     "planning-loop",
		LeadAgent:   "planner",
		Members:     []string{"plan-reviewer"},
		ChainPrefix: "planning",
		Gates:       gates,
	}
}

// defaultPrefixSwarmContextWithGates mirrors swarmContextWithGates but
// leaves ChainPrefix at the manifest default (equal to SwarmID) — the
// state NewContext produces when a manifest does not pin chain_prefix.
// AssignRunChainID only stamps a per-run namespace over the default, so
// specs exercising engine-assignment must start from this shape.
func defaultPrefixSwarmContextWithGates(gates []swarm.GateSpec) *swarm.Context {
	return &swarm.Context{
		SwarmID:     "planning-loop",
		LeadAgent:   "planner",
		Members:     []string{"plan-reviewer"},
		ChainPrefix: "planning-loop",
		Gates:       gates,
	}
}

func reviewerEnginesWithContext(swarmCtx *swarm.Context) (map[string]*engine.Engine, *engine.Engine) {
	leadEng := engine.New(engine.Config{
		ChatProvider: leadProvider(),
		Manifest: agent.Manifest{
			ID:                "planner",
			Name:              "Planner",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			ContextManagement: agent.DefaultContextManagement(),
		},
		SwarmContext: swarmCtx,
	})
	reviewerEng := engine.New(engine.Config{
		ChatProvider: reviewerProvider(),
		Manifest: agent.Manifest{
			ID:                "plan-reviewer",
			Name:              "Plan Reviewer",
			Instructions:      agent.Instructions{SystemPrompt: "review"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{
		"planner":       leadEng,
		"plan-reviewer": reviewerEng,
	}
	return engines, leadEng
}

func newDelegateToolWithRunner(engines map[string]*engine.Engine, store coordination.Store, runner swarm.GateRunner) *engine.DelegateTool {
	tool := engine.NewDelegateToolWithBackground(
		engines,
		agent.Delegation{CanDelegate: true},
		"planner",
		nil,
		store,
	)
	return tool.WithGateRunner(runner)
}

// reviewerEnginesWithProvider mirrors reviewerEnginesWithContext but
// returns the reviewer's mockProvider handle so a spec can inspect the
// request the member's stream captured — used to assert the gate
// directive is appended to a re-delegated member's prompt.
func reviewerEnginesWithProvider(swarmCtx *swarm.Context) (map[string]*engine.Engine, *mockProvider) {
	reviewerProv := reviewerProvider()
	leadEng := engine.New(engine.Config{
		ChatProvider: leadProvider(),
		Manifest: agent.Manifest{
			ID:                "planner",
			Name:              "Planner",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			ContextManagement: agent.DefaultContextManagement(),
		},
		SwarmContext: swarmCtx,
	})
	reviewerEng := engine.New(engine.Config{
		ChatProvider: reviewerProv,
		Manifest: agent.Manifest{
			ID:                "plan-reviewer",
			Name:              "Plan Reviewer",
			Instructions:      agent.Instructions{SystemPrompt: "review"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{
		"planner":       leadEng,
		"plan-reviewer": reviewerEng,
	}
	return engines, reviewerProv
}

// reviewerEnginesWithLeadAndProvider mirrors reviewerEnginesWithProvider
// but also returns the lead ("planner") engine so a spec can pin its
// resolved (provider, model) via SetModelPreference — the source the
// corrective-retry model override copies from. Used by the corrective-
// retry model-override specs that assert the retry routes the struggling
// member onto the lead's proven-reachable model.
func reviewerEnginesWithLeadAndProvider(swarmCtx *swarm.Context) (map[string]*engine.Engine, *engine.Engine, *mockProvider) {
	reviewerProv := reviewerProvider()
	leadEng := engine.New(engine.Config{
		ChatProvider: leadProvider(),
		Manifest: agent.Manifest{
			ID:                "planner",
			Name:              "Planner",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			ContextManagement: agent.DefaultContextManagement(),
		},
		SwarmContext: swarmCtx,
	})
	reviewerEng := engine.New(engine.Config{
		ChatProvider: reviewerProv,
		Manifest: agent.Manifest{
			ID:                "plan-reviewer",
			Name:              "Plan Reviewer",
			Instructions:      agent.Instructions{SystemPrompt: "review"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{
		"planner":       leadEng,
		"plan-reviewer": reviewerEng,
	}
	return engines, leadEng, reviewerProv
}

// newDelegateToolWithRunnerAndOwner mirrors newDelegateToolWithRunner but
// pins the owner (lead) engine via WithOwnerEngine so the corrective-retry
// model override can read the lead's resolved (provider, model). Production
// wiring (app.go) always installs the DelegateTool on the lead's engine via
// WithOwnerEngine; the bare newDelegateToolWithRunner helper omitted it
// because the earlier specs did not exercise the lead-engine lookup.
func newDelegateToolWithRunnerAndOwner(
	engines map[string]*engine.Engine,
	store coordination.Store,
	runner swarm.GateRunner,
	owner *engine.Engine,
) *engine.DelegateTool {
	tool := engine.NewDelegateToolWithBackground(
		engines,
		agent.Delegation{CanDelegate: true},
		"planner",
		nil,
		store,
	)
	return tool.WithGateRunner(runner).WithOwnerEngine(owner)
}

// flakyMemberGateRunner fails the first failFor post-member gate
// dispatches with the no-output GateError shape, then passes. It records
// the dispatch count so a spec can assert the exact number of member
// re-dispatches the retry loop attempted. failFor = math.MaxInt models a
// member that never writes (always fails).
type flakyMemberGateRunner struct {
	failFor    int
	calls      int
	gateName   string
	reason     string
	memberID   string
	swarmID    string
	lifecycles []string
}

func (r *flakyMemberGateRunner) Run(_ context.Context, gate swarm.GateSpec, args swarm.GateArgs) error {
	r.calls++
	r.lifecycles = append(r.lifecycles, gate.When)
	if r.calls <= r.failFor {
		reason := r.reason
		if reason == "" {
			reason = "no member output found at [planning/plan-reviewer/output]: " +
				"the member did not write its output — it likely narrated the write but emitted no tool call. " +
				"Re-delegate this member with an explicit instruction to perform the coordination_store write " +
				"(do not narrate it), naming the concrete chainID and target key."
		}
		return &swarm.GateError{
			GateName: gate.Name,
			GateKind: gate.Kind,
			When:     gate.When,
			SwarmID:  args.SwarmID,
			MemberID: args.MemberID,
			Reason:   reason,
		}
	}
	return nil
}

func validVerdictPayload() []byte {
	// The review-verdict gate now keys on the recognised verdict TOKEN the
	// approve/reject loop reads (coordination.ContainsRecognisedVerdict),
	// not a JSON `verdict` enum. A real reviewer emits "VERDICT: APPROVE".
	return []byte("VERDICT: APPROVE")
}

func reviewerEngines() (map[string]*engine.Engine, *engine.Engine) {
	leadEng := engine.New(engine.Config{
		ChatProvider: leadProvider(),
		Manifest: agent.Manifest{
			ID:                "planner",
			Name:              "Planner",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			ContextManagement: agent.DefaultContextManagement(),
		},
		SwarmContext: planningLoopSwarmContext(),
	})
	reviewerEng := engine.New(engine.Config{
		ChatProvider: reviewerProvider(),
		Manifest: agent.Manifest{
			ID:                "plan-reviewer",
			Name:              "Plan Reviewer",
			Instructions:      agent.Instructions{SystemPrompt: "review"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{
		"planner":       leadEng,
		"plan-reviewer": reviewerEng,
	}
	return engines, leadEng
}

func reviewerDelegateInput() tool.Input {
	return tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "plan-reviewer",
			"message":       "review the plan",
		},
	}
}

var _ = Describe("DelegateTool post-member gate dispatch (T-swarm-3)", func() {
	BeforeEach(func() {
		swarm.ClearSchemasForTest()
		Expect(swarm.SeedDefaultSchemas()).To(Succeed())
	})

	It("halts the delegation with a GateError when the reviewer writes no recognised verdict token", func() {
		store := coordination.NewMemoryStore()
		Expect(store.Set("planning/plan-reviewer/review", []byte(`{"reasoning":"missing verdict"}`))).To(Succeed())

		engines, _ := reviewerEngines()
		delegateTool := engine.NewDelegateToolWithBackground(
			engines,
			agent.Delegation{CanDelegate: true},
			"planner",
			nil,
			store,
		)
		multi := swarm.NewMultiRunner()
		multi.Register("builtin:result-schema", swarm.NewResultSchemaRunner())
		delegateTool = delegateTool.WithGateRunner(multi)

		_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

		Expect(err).To(HaveOccurred())
		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.GateName).To(Equal("post-member-plan-reviewer-result-schema"))
		Expect(gateErr.MemberID).To(Equal("plan-reviewer"))
		Expect(gateErr.SwarmID).To(Equal("planning-loop"))
		Expect(gateErr.Reason).To(MatchRegexp(`(?i)verdict`),
			"the failure must name the missing verdict token, the SAME signal the approve/reject loop reads")
	})

	It("returns the delegation result unchanged when the reviewer writes a recognised verdict token", func() {
		store := coordination.NewMemoryStore()
		Expect(store.Set("planning/plan-reviewer/review", []byte("VERDICT: APPROVE"))).To(Succeed())

		engines, _ := reviewerEngines()
		delegateTool := engine.NewDelegateToolWithBackground(
			engines,
			agent.Delegation{CanDelegate: true},
			"planner",
			nil,
			store,
		)
		multi := swarm.NewMultiRunner()
		multi.Register("builtin:result-schema", swarm.NewResultSchemaRunner())
		delegateTool = delegateTool.WithGateRunner(multi)

		result, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("review complete"))
	})

	It("skips gate dispatch when no swarm context is installed", func() {
		store := coordination.NewMemoryStore()

		engines, leadEng := reviewerEngines()
		leadEng.SetSwarmContext(nil)
		delegateTool := engine.NewDelegateToolWithBackground(
			engines,
			agent.Delegation{CanDelegate: true},
			"planner",
			nil,
			store,
		)
		multi := swarm.NewMultiRunner()
		multi.Register("builtin:result-schema", swarm.NewResultSchemaRunner())
		delegateTool = delegateTool.WithGateRunner(multi)

		_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

		Expect(err).NotTo(HaveOccurred())
	})

	It("fires pre-swarm gates exactly once before the first member runs (when=pre)", func() {
		store := coordination.NewMemoryStore()
		Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())

		runner := &recordingRunner{}
		gates := []swarm.GateSpec{
			{Name: "envelope-check", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, runner)

		_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
		Expect(err).NotTo(HaveOccurred())

		_, err = delegateTool.Execute(context.Background(), reviewerDelegateInput())
		Expect(err).NotTo(HaveOccurred())

		Expect(runner.calls).To(HaveLen(1))
		Expect(runner.calls[0].Name).To(Equal("envelope-check"))
	})

	It("halts the delegation when a pre-swarm gate fails before the member streams", func() {
		store := coordination.NewMemoryStore()
		runner := &recordingRunner{
			fail: map[string]error{
				"envelope-check": &swarm.GateError{
					GateName: "envelope-check",
					GateKind: "builtin:result-schema",
					When:     swarm.LifecyclePreSwarm,
					SwarmID:  "planning-loop",
					Reason:   "chain_prefix missing",
				},
			},
		}
		gates := []swarm.GateSpec{
			{Name: "envelope-check", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, runner)

		_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.GateName).To(Equal("envelope-check"))
		Expect(gateErr.When).To(Equal(swarm.LifecyclePreSwarm))
	})

	It("fires pre-member gates immediately before the targeted member's stream starts", func() {
		store := coordination.NewMemoryStore()
		Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())

		runner := &recordingRunner{}
		gates := []swarm.GateSpec{
			{Name: "pre-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePreMember, Target: "plan-reviewer"},
			{Name: "post-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, runner)

		_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
		Expect(err).NotTo(HaveOccurred())

		Expect(runner.calls).To(HaveLen(2))
		Expect(runner.calls[0].Name).To(Equal("pre-member-plan-reviewer"))
		Expect(runner.calls[1].Name).To(Equal("post-member-plan-reviewer"))
	})

	It("halts the delegation when a pre-member gate fails (skipping the stream and the post-member gate)", func() {
		store := coordination.NewMemoryStore()
		runner := &recordingRunner{
			fail: map[string]error{
				"pre-member-plan-reviewer": &swarm.GateError{
					GateName: "pre-member-plan-reviewer",
					GateKind: "builtin:result-schema",
					When:     swarm.LifecyclePreMember,
					Reason:   "missing prerequisite",
				},
			},
		}
		gates := []swarm.GateSpec{
			{Name: "pre-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePreMember, Target: "plan-reviewer"},
			{Name: "post-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, runner)

		_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

		var gateErr *swarm.GateError
		Expect(errors.As(err, &gateErr)).To(BeTrue())
		Expect(gateErr.GateName).To(Equal("pre-member-plan-reviewer"))
		Expect(runner.calls).To(HaveLen(1),
			"the post-member gate must NOT fire when the pre-member gate halts dispatch")
	})

	It("fires post-swarm gates from FlushSwarmLifecycle after the swarm ends (when=post)", func() {
		store := coordination.NewMemoryStore()
		Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())

		runner := &recordingRunner{}
		gates := []swarm.GateSpec{
			{Name: "post-swarm-aggregate", Kind: "builtin:result-schema", When: swarm.LifecyclePostSwarm},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, runner)

		_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
		Expect(err).NotTo(HaveOccurred())
		Expect(runner.calls).To(BeEmpty(),
			"post-swarm must NOT fire on member completion — it waits for FlushSwarmLifecycle")

		Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed())
		Expect(runner.calls).To(HaveLen(1))
		Expect(runner.calls[0].Name).To(Equal("post-swarm-aggregate"))
	})

	It("deterministically publishes the plan to the vault BEFORE the post-swarm gate fires", func() {
		// Regression guard for the "no plan in Obsidian" bug: the plan
		// sits only in the coord-store. FlushSwarmLifecycle must write a
		// real vault file AND the plan_publication record so the
		// downstream honesty gate can verify it — without an LLM emitting
		// a write tool call (Defect 2's synthesis-hang).
		outputDir := GinkgoT().TempDir()
		store := coordination.NewMemoryStore()
		Expect(store.Set("readyz-chain/plan",
			[]byte(`{"markdown":"# Readyz Plan\n\nbody","title":"Readyz Plan"}`))).To(Succeed())
		Expect(store.Set("readyz-chain/review", validVerdictPayload())).To(Succeed())

		// A runner that records, AT GATE-DISPATCH TIME, whether the
		// publish already happened: the publication record must exist
		// before the post-swarm gate runs.
		probe := &publishProbeRunner{store: store, key: "readyz-chain/plan_publication"}
		gates := []swarm.GateSpec{
			{Name: "post-swarm-plan-published", Kind: "builtin:artifact-published", When: swarm.LifecyclePostSwarm, OutputKey: "{chainID}/plan"},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, probe).
			WithPlanOutputDir(outputDir)

		Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed())

		Expect(probe.sawPublication).To(BeTrue(),
			"the publication record must exist BEFORE the post-swarm gate runs")

		vaultPath := filepath.Join(outputDir, "Readyz Plan.md")
		Expect(vaultPath).To(BeAnExistingFile(),
			"the plan must be written to a real file in the vault")
		body, readErr := os.ReadFile(vaultPath)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("# Readyz Plan"))
	})

	It("threads the lead-allocated chainID so the NAMED chain is published, not a stale one (Bug 1)", func() {
		// Multi-chain coord-store: a member is dispatched with an explicit
		// caller chainID. FlushSwarmLifecycle must publish THAT chain's
		// plan (captured at dispatch time) — not an arbitrary stale
		// "*/plan" key — and thread the same chainID into the post-swarm
		// gate's GateArgs.ChainID.
		outputDir := GinkgoT().TempDir()
		store := coordination.NewMemoryStore()
		Expect(store.Set("stale-chain/plan", []byte("# Stale Plan\n\nDo not publish."))).To(Succeed())
		Expect(store.Set("mhc-target/plan",
			[]byte(`{"markdown":"# Mental Health Companion\n\nThe wanted plan.","title":"Mental Health Companion"}`))).To(Succeed())
		Expect(store.Set("mhc-target/review", validVerdictPayload())).To(Succeed())

		argsProbe := &chainArgProbeRunner{}
		gates := []swarm.GateSpec{
			{Name: "post-swarm-plan-published", Kind: "builtin:artifact-published", When: swarm.LifecyclePostSwarm, OutputKey: "{chainID}/plan"},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, argsProbe).
			WithPlanOutputDir(outputDir)

		// Dispatch a member with an explicit caller chainID — this is what
		// the lead does when it free-forms the per-run chain.
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "plan-reviewer",
				"message":       "review the plan",
				"chainID":       "mhc-target",
			},
		}
		_, err := delegateTool.Execute(context.Background(), input)
		Expect(err).NotTo(HaveOccurred())

		Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed())

		// The NAMED chain's plan was published, NOT the stale one.
		wantedPath := filepath.Join(outputDir, "Mental Health Companion.md")
		Expect(wantedPath).To(BeAnExistingFile(),
			"the lead-allocated chain's plan reaches the vault")
		entries, _ := os.ReadDir(outputDir)
		Expect(entries).To(HaveLen(1), "only the named chain is published")

		recorded, ok := readPublicationRecord(store, "mhc-target")
		Expect(ok).To(BeTrue(), "publication recorded under the NAMED chain")
		Expect(recorded).To(Equal(wantedPath))

		// The post-swarm gate received the threaded chainID.
		Expect(argsProbe.lastChainID).To(Equal("mhc-target"),
			"the post-swarm gate's GateArgs.ChainID is the lead-allocated chain")
	})

	It("publishes and gates the ENGINE-ASSIGNED chain when no caller chainID is supplied (ADR forward decision)", func() {
		// The engine assigns the run's chainID at swarm start
		// (AssignRunChainID stamps swarmCtx.ChainPrefix) so the LLM cannot
		// invent an off-chain namespace. With NO caller chainID in the
		// delegate message, FlushSwarmLifecycle must resolve the
		// engine-assigned chain — publish THAT chain's plan and thread it
		// into the post-swarm gate — rather than falling back to a
		// member-invented prefix or a stale suffix-scan hit.
		outputDir := GinkgoT().TempDir()
		store := coordination.NewMemoryStore()

		swarmCtx := defaultPrefixSwarmContextWithGates([]swarm.GateSpec{
			{Name: "post-swarm-plan-published", Kind: "builtin:artifact-published", When: swarm.LifecyclePostSwarm, OutputKey: "{chainID}/plan"},
		})
		// Engine assigns the per-run chain at start; the LLM never sees a
		// chance to pick one.
		swarmCtx.AssignRunChainID("session-engine-owned")
		assignedChain := swarmCtx.ChainPrefix
		Expect(assignedChain).NotTo(Equal("planning-loop"),
			"precondition: the engine assigned a per-run chain, not the static swarm id")
		Expect(assignedChain).To(HavePrefix("planning-loop-"),
			"precondition: the engine-assigned chain is anchored under the swarm id")

		// A member invented an off-chain prefix — it must NOT be published.
		Expect(store.Set("member-invented-prefix/plan",
			[]byte("# Off-Chain Drift\n\nThe member invented this; do not publish."))).To(Succeed())
		// The real deliverable landed under the engine-assigned chain.
		Expect(store.Set(assignedChain+"/plan",
			[]byte(`{"markdown":"# Engine Owned Plan\n\nThe wanted plan.","title":"Engine Owned Plan"}`))).To(Succeed())

		argsProbe := &chainArgProbeRunner{}
		engines, _ := reviewerEnginesWithContext(swarmCtx)
		delegateTool := newDelegateToolWithRunner(engines, store, argsProbe).
			WithPlanOutputDir(outputDir)

		// No member dispatch supplies a chainID; the lifecycle must still
		// resolve the engine-assigned chain.
		Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed())

		wantedPath := filepath.Join(outputDir, "Engine Owned Plan.md")
		Expect(wantedPath).To(BeAnExistingFile(),
			"the engine-assigned chain's plan reaches the vault")
		entries, _ := os.ReadDir(outputDir)
		Expect(entries).To(HaveLen(1), "only the engine-assigned chain is published")

		recorded, ok := readPublicationRecord(store, assignedChain)
		Expect(ok).To(BeTrue(), "publication recorded under the ENGINE-ASSIGNED chain")
		Expect(recorded).To(Equal(wantedPath))

		Expect(argsProbe.lastChainID).To(Equal(assignedChain),
			"the post-swarm gate's GateArgs.ChainID is the engine-assigned chain")
	})

	It("OVERRIDES an LLM-supplied chainID with the engine-assigned one inside an engine-owned run (chainID-identity unification)", func() {
		// THE CORE REGRESSION FLIP. Pre-fix, an LLM free-forming a chainID in
		// its delegate prose WON over the engine-assigned namespace: the
		// member wrote + the publisher targeted the LLM value while the wave
		// validator resolved the engine value, so the loop doom-looped. The
		// new contract (ADR Engine-Owned Workflow Mechanics): when the engine
		// stamped a per-run chainID at swarm start, that value is
		// AUTHORITATIVE and the LLM-supplied chainID is IGNORED. Every site —
		// member preamble, validator, gate, publisher — converges on it.
		outputDir := GinkgoT().TempDir()
		store := coordination.NewMemoryStore()

		swarmCtx := defaultPrefixSwarmContextWithGates([]swarm.GateSpec{
			{Name: "post-swarm-plan-published", Kind: "builtin:artifact-published", When: swarm.LifecyclePostSwarm, OutputKey: "{chainID}/plan"},
		})
		swarmCtx.AssignRunChainID("session-engine-owned")
		assignedChain := swarmCtx.ChainPrefix

		// The engine-assigned chain holds the real deliverable. The LLM
		// free-formed a DIFFERENT chain in its delegate message — it must be
		// ignored, NOT published.
		Expect(store.Set(assignedChain+"/plan",
			[]byte(`{"markdown":"# Engine Owned Plan\n\nthe wanted plan","title":"Engine Owned Plan"}`))).To(Succeed())
		Expect(store.Set(assignedChain+"/review", validVerdictPayload())).To(Succeed())
		Expect(store.Set("llm-free-formed/plan",
			[]byte(`{"markdown":"# LLM Free-Formed\n\nshould lose","title":"LLM Free-Formed"}`))).To(Succeed())

		argsProbe := &chainArgProbeRunner{}
		engines, _ := reviewerEnginesWithContext(swarmCtx)
		delegateTool := newDelegateToolWithRunner(engines, store, argsProbe).
			WithPlanOutputDir(outputDir)

		// The LLM supplies its own chainID in the delegate call — the engine
		// must override it.
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "plan-reviewer",
				"message":       "review the plan",
				"chainID":       "llm-free-formed",
			},
		}
		_, err := delegateTool.Execute(context.Background(), input)
		Expect(err).NotTo(HaveOccurred())

		Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed())

		wantedPath := filepath.Join(outputDir, "Engine Owned Plan.md")
		Expect(wantedPath).To(BeAnExistingFile(),
			"the ENGINE-ASSIGNED chain's plan is published, not the LLM free-formed one")
		entries, _ := os.ReadDir(outputDir)
		Expect(entries).To(HaveLen(1),
			"only the engine-owned chain is published; the LLM free-formed one is ignored")
		Expect(argsProbe.lastChainID).To(Equal(assignedChain),
			"the post-swarm gate's GateArgs.ChainID is the engine-assigned chain, NOT the LLM-supplied one")
	})

	It("slugifies an LLM/caller chainID containing a slash so it can never break key parsing (the exact doom-loop repro)", func() {
		// The precise live-run repro: the planner free-formed
		// "planner/sme-sectional-plans" (a chainID WITH A SLASH). Members
		// wrote evidence under that three-segment key while the validator
		// split on the first "/". Here there is NO engine assignment (a
		// seeded/standalone run), so the caller value is honoured — but it
		// MUST be slugified to a key-safe value before it becomes a namespace.
		outputDir := GinkgoT().TempDir()
		store := coordination.NewMemoryStore()

		// Seeded (legacy) swarm: an explicit static chain_prefix means the
		// engine does NOT stamp a per-run id (ChainIDAssigned stays false),
		// so the caller-supplied chainID is the one honoured — after slugify.
		swarmCtx := swarmContextWithGates([]swarm.GateSpec{
			{Name: "post-swarm-plan-published", Kind: "builtin:artifact-published", When: swarm.LifecyclePostSwarm, OutputKey: "{chainID}/plan"},
		})

		// The deliverable lives under the SLUGIFIED form of the free-form
		// chainID — exactly where a slugifying member preamble would have
		// written it.
		const slugged = "planner-sme-sectional-plans"
		Expect(store.Set(slugged+"/plan",
			[]byte(`{"markdown":"# Sectional Plans\n\nthe wanted plan","title":"Sectional Plans"}`))).To(Succeed())
		Expect(store.Set(slugged+"/review", validVerdictPayload())).To(Succeed())

		argsProbe := &chainArgProbeRunner{}
		engines, _ := reviewerEnginesWithContext(swarmCtx)
		delegateTool := newDelegateToolWithRunner(engines, store, argsProbe).
			WithPlanOutputDir(outputDir)

		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "plan-reviewer",
				"message":       "review the plan",
				"chainID":       "planner/sme-sectional-plans",
			},
		}
		_, err := delegateTool.Execute(context.Background(), input)
		Expect(err).NotTo(HaveOccurred())

		Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed())

		Expect(argsProbe.lastChainID).To(Equal(slugged),
			"the slash in the caller chainID must be slugified before it becomes a coord-store namespace")
		Expect(argsProbe.lastChainID).NotTo(ContainSubstring("/"),
			"a chainID with a slash must never reach the gate verbatim — it fractures key parsing")

		wantedPath := filepath.Join(outputDir, "Sectional Plans.md")
		Expect(wantedPath).To(BeAnExistingFile(),
			"the slugified chain's plan is published under the key-safe namespace")
	})

	It("resolves ONE chainID across member-write, gate and publish for an engine-owned run (the core regression)", func() {
		// THE CORE REGRESSION. A planning swarm run must resolve the SAME
		// chainID at every load-bearing site or it doom-loops: the member
		// writes evidence under chain X, the validator/gate/publisher look
		// under chain Y, the wave never completes. This drives a real member
		// dispatch through the engine-owned ctx scope and asserts the chain
		// the member-write path captured == the chain the gate received ==
		// the chain the publisher wrote under == the engine-assigned slug
		// the wave validator resolves (swarm.SlugifyChainID(ChainPrefix)).
		outputDir := GinkgoT().TempDir()
		store := coordination.NewMemoryStore()

		swarmCtx := defaultPrefixSwarmContextWithGates([]swarm.GateSpec{
			{Name: "post-swarm-plan-published", Kind: "builtin:artifact-published", When: swarm.LifecyclePostSwarm, OutputKey: "{chainID}/plan"},
		})
		swarmCtx.AssignRunChainID("session-core-regression")
		engineChain := swarmCtx.ChainPrefix

		// What the wave validator resolves (same normalisation, same input).
		validatorChain := swarm.SlugifyChainID(engineChain)
		Expect(validatorChain).To(Equal(engineChain),
			"precondition: the engine form is already key-safe so the validator reads it unchanged")

		// The deliverable lands under the engine-assigned chain (where a
		// member following the slugged engine value would have written it).
		Expect(store.Set(engineChain+"/plan",
			[]byte(`{"markdown":"# Unified Plan\n\nthe wanted plan","title":"Unified Plan"}`))).To(Succeed())
		Expect(store.Set(engineChain+"/review", validVerdictPayload())).To(Succeed())

		argsProbe := &chainArgProbeRunner{}
		engines, _ := reviewerEnginesWithContext(swarmCtx)
		delegateTool := newDelegateToolWithRunner(engines, store, argsProbe).
			WithPlanOutputDir(outputDir)

		// Drive a real member dispatch through the engine-owned ctx scope.
		// The LLM free-forms its own chainID — the engine overrides it, so
		// the member-write capture lands on the engine chain.
		dispatchCtx := swarm.WithScope(context.Background(), swarmCtx)
		_, err := delegateTool.Execute(dispatchCtx, tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "plan-reviewer",
				"message":       "review the plan",
				"chainID":       "llm/free/formed/with/slashes",
			},
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(delegateTool.FlushSwarmLifecycle(dispatchCtx)).To(Succeed())

		// Site 1 — gate: the post-swarm gate received the engine chain.
		Expect(argsProbe.lastChainID).To(Equal(engineChain),
			"gate chain MUST equal the engine-assigned chain")
		// Site 2 — publish: the plan was published under the engine chain.
		recorded, ok := readPublicationRecord(store, engineChain)
		Expect(ok).To(BeTrue(), "publication recorded under the engine chain")
		Expect(recorded).To(Equal(filepath.Join(outputDir, "Unified Plan.md")))
		entries, _ := os.ReadDir(outputDir)
		Expect(entries).To(HaveLen(1),
			"only the single engine-owned chain is published — no divergent namespace")
		// Site 3 — validator: resolves the identical engine chain.
		Expect(argsProbe.lastChainID).To(Equal(validatorChain),
			"gate/publish chain MUST equal the wave-validator-resolved chain — they all converge on ONE value")
	})

	It("is a no-op when no plan_output_dir is wired (historical behaviour preserved)", func() {
		store := coordination.NewMemoryStore()
		Expect(store.Set("some-chain/plan", []byte("# A Plan\n\nbody"))).To(Succeed())

		runner := &recordingRunner{}
		gates := []swarm.GateSpec{
			{Name: "post-swarm-aggregate", Kind: "builtin:result-schema", When: swarm.LifecyclePostSwarm},
		}
		engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
		delegateTool := newDelegateToolWithRunner(engines, store, runner) // no WithPlanOutputDir

		Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed())

		exists, err := store.Exists("some-chain/plan_publication")
		Expect(err).NotTo(HaveOccurred())
		Expect(exists).To(BeFalse(),
			"with no output dir the publisher writes nothing and fabricates no record")
		Expect(runner.calls).To(HaveLen(1), "the post-swarm gate still fires")
	})

	Context("post-member gate retry (single-miss is not terminal)", func() {
		postMemberGate := func() []swarm.GateSpec {
			return []swarm.GateSpec{
				{
					Name:   "post-member-plan-reviewer-output",
					Kind:   "builtin:result-schema",
					When:   swarm.LifecyclePostMember,
					Target: "plan-reviewer",
				},
			}
		}

		It("re-delegates the member when the post-member gate fails on attempt 1 but passes on attempt 2", func() {
			// Regression guard: a member that narrates-without-writing on the
			// first attempt must NOT kill the run. The loop re-dispatches and
			// the gate passes on the retry, so Execute returns the result.
			store := coordination.NewMemoryStore()
			runner := &flakyMemberGateRunner{failFor: 1}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(postMemberGate()))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			result, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

			Expect(err).NotTo(HaveOccurred(),
				"a single post-member gate miss must be retried, not terminal")
			Expect(result.Output).To(ContainSubstring("review complete"))
			Expect(runner.calls).To(Equal(2),
				"the member is re-dispatched exactly once after the first miss")
		})

		It("fails terminally with the GateError after exhausting the retry budget", func() {
			// Honest-fail preserved: a member that never writes across every
			// attempt still surfaces the GateError — only AFTER the bounded
			// retries are exhausted.
			store := coordination.NewMemoryStore()
			runner := &flakyMemberGateRunner{failFor: math.MaxInt}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(postMemberGate()))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue(),
				"after exhausting retries the GateError becomes terminal")
			Expect(gateErr.MemberID).To(Equal("plan-reviewer"))
			Expect(runner.calls).To(Equal(engine.PostMemberGateMaxAttempts),
				"the member is re-dispatched up to the bounded attempt cap then fails loudly")
		})

		It("appends the gate directive to the re-delegated member's prompt", func() {
			// The directive that breaks the synthesis-hang loop ("perform the
			// coordination_store write, do not narrate it") must reach the
			// member on retry — passively re-running the same prompt would
			// just reproduce the miss.
			store := coordination.NewMemoryStore()
			directive := "perform the coordination_store write (do not narrate it)"
			runner := &flakyMemberGateRunner{
				failFor: 1,
				reason:  "no member output found at [planning/plan-reviewer/output]: " + directive,
			}
			engines, reviewerProv := reviewerEnginesWithProvider(swarmContextWithGates(postMemberGate()))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
			Expect(err).NotTo(HaveOccurred())

			Expect(reviewerProv.LastRequestContainsSubstring(directive)).To(BeTrue(),
				"the re-delegated member's prompt carries the gate's re-write directive")
		})

		It("does not retry when the post-member gate passes on the first attempt", func() {
			// Happy-path guard: a member that writes on attempt 1 dispatches
			// exactly once — the retry wrapper must not change clean-pass
			// behaviour.
			store := coordination.NewMemoryStore()
			runner := &flakyMemberGateRunner{failFor: 0}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(postMemberGate()))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			result, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("review complete"))
			Expect(runner.calls).To(Equal(1),
				"a clean first-attempt pass is dispatched exactly once — no retry")
		})

		Context("forced tool_choice on the corrective retry", func() {
			It("forces the coordination_store write on the re-delegated member turn", func() {
				// The synthesis-hang signature is a marginal model (zai/glm-4.5)
				// NARRATING the write instead of emitting the tool call. A prose
				// directive alone was ignored across all attempts in production.
				// The corrective re-delegation must FORCE the required tool call
				// via tool_choice so even a marginal model emits it. The forced
				// value names the coordination_store write the gate's output_key
				// is read from.
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{failFor: 1}
				engines, reviewerProv := reviewerEnginesWithProvider(swarmContextWithGates(postMemberGate()))
				delegateTool := newDelegateToolWithRunner(engines, store, runner)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.StreamCallCount()).To(BeNumerically(">=", 2),
					"the member is re-dispatched after the first narration-only miss")
				Expect(reviewerProv.ToolChoiceForAttempt(2)).To(Equal("tool:coordination_store"),
					"the corrective retry forces the coordination_store write, not a prose ask")
			})

			It("does NOT force tool_choice on the first attempt", func() {
				// Multi-step members (explorer/librarian) legitimately read or
				// search BEFORE writing. Forcing the write on the FIRST turn
				// would break that. Forcing is reserved for the corrective retry
				// after a narration-without-write miss.
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{failFor: 1}
				engines, reviewerProv := reviewerEnginesWithProvider(swarmContextWithGates(postMemberGate()))
				delegateTool := newDelegateToolWithRunner(engines, store, runner)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.ToolChoiceForAttempt(1)).To(BeEmpty(),
					"the first attempt stays unconstrained so multi-step members can read/search first")
			})

			It("does not force tool_choice when the gate passes on the first attempt", func() {
				// Clean-pass guard: a member that writes on attempt 1 is never
				// forced — no retry happens, so the single dispatch is
				// unconstrained.
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{failFor: 0}
				engines, reviewerProv := reviewerEnginesWithProvider(swarmContextWithGates(postMemberGate()))
				delegateTool := newDelegateToolWithRunner(engines, store, runner)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.StreamCallCount()).To(Equal(1))
				Expect(reviewerProv.ToolChoiceForAttempt(1)).To(BeEmpty(),
					"a clean first-attempt pass dispatches once, unconstrained")
			})
		})

		Context("model override on the corrective retry", func() {
			It("routes the corrective retry onto the lead's resolved model", func() {
				// Forcing the tool_choice on the retry made the marginal
				// member (zai/glm-4.5) emit the coordination_store write —
				// but glm-4.5 cannot reliably emit a single clean JSON object
				// for the bundle schema (it appends a second object / trailing
				// junk, surfacing as `invalid character ',' after top-level
				// value`). The corrective retry must ALSO route the struggling
				// member onto a tool-AND-structured-JSON-reliable model. The
				// lead engine resolved to a reachable, proven model in this
				// deployment (production: openai/gpt-5), so the retry copies
				// the lead's resolved (provider, model) rather than hardcoding
				// a bare string that may be unreachable here.
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{failFor: 1}
				engines, leadEng, reviewerProv := reviewerEnginesWithLeadAndProvider(
					swarmContextWithGates(postMemberGate()))
				leadEng.SetModelPreference("openai", "gpt-5")
				delegateTool := newDelegateToolWithRunnerAndOwner(engines, store, runner, leadEng)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.StreamCallCount()).To(BeNumerically(">=", 2),
					"the member is re-dispatched after the first miss")
				Expect(reviewerProv.ProviderForAttempt(2)).To(Equal("openai"),
					"the corrective retry routes onto the lead's resolved provider")
				Expect(reviewerProv.ModelForAttempt(2)).To(Equal("gpt-5"),
					"the corrective retry routes onto the lead's resolved model")
				Expect(reviewerProv.ToolChoiceForAttempt(2)).To(Equal("tool:coordination_store"),
					"the corrective retry still forces the coordination_store write")
			})

			It("does NOT override the model on the first attempt", func() {
				// The first attempt keeps the member's own manifest-resolved
				// model so multi-step members run on the model their manifest
				// declares. Only the corrective retry — after a miss — escalates
				// onto the lead's reliable model.
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{failFor: 1}
				engines, leadEng, reviewerProv := reviewerEnginesWithLeadAndProvider(
					swarmContextWithGates(postMemberGate()))
				leadEng.SetModelPreference("openai", "gpt-5")
				delegateTool := newDelegateToolWithRunnerAndOwner(engines, store, runner, leadEng)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.ModelForAttempt(1)).NotTo(Equal("gpt-5"),
					"the first attempt is not forced onto the lead's model")
				Expect(reviewerProv.ToolChoiceForAttempt(1)).To(BeEmpty(),
					"the first attempt stays unconstrained — neither tool nor model is forced")
			})

			It("does not override the model when the gate passes on the first attempt", func() {
				// Clean-pass guard: a member that writes on attempt 1 is never
				// re-routed — no retry happens, so the single dispatch keeps the
				// member's own model and stays unconstrained.
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{failFor: 0}
				engines, leadEng, reviewerProv := reviewerEnginesWithLeadAndProvider(
					swarmContextWithGates(postMemberGate()))
				leadEng.SetModelPreference("openai", "gpt-5")
				delegateTool := newDelegateToolWithRunnerAndOwner(engines, store, runner, leadEng)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.StreamCallCount()).To(Equal(1))
				Expect(reviewerProv.ModelForAttempt(1)).NotTo(Equal("gpt-5"),
					"a clean first-attempt pass keeps the member's own model")
			})
		})
	})
})

// publishProbeRunner is a GateRunner that records, at dispatch time,
// whether the deterministic publisher has already written the publication
// record — proving the publish runs BEFORE the post-swarm gate.
type publishProbeRunner struct {
	store          coordination.Store
	key            string
	sawPublication bool
}

func (p *publishProbeRunner) Run(_ context.Context, _ swarm.GateSpec, _ swarm.GateArgs) error {
	exists, _ := p.store.Exists(p.key)
	p.sawPublication = exists
	return nil
}

// chainArgProbeRunner records the GateArgs.ChainID it was dispatched with
// so a spec can assert the lead-allocated chain is threaded into the
// post-swarm gate (Bug 1).
type chainArgProbeRunner struct {
	lastChainID string
}

func (c *chainArgProbeRunner) Run(_ context.Context, _ swarm.GateSpec, args swarm.GateArgs) error {
	c.lastChainID = args.ChainID
	return nil
}

// readPublicationRecord decodes the "<chainID>/plan_publication" record the
// deterministic publisher writes so a spec can assert the recorded path.
func readPublicationRecord(store coordination.Store, chainID string) (string, bool) {
	raw, err := store.Get(chainID + "/plan_publication")
	if err != nil {
		return "", false
	}
	var rec struct {
		VaultPath string `json:"vault_path"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", false
	}
	return rec.VaultPath, true
}
