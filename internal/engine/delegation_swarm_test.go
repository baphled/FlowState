package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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

// newDelegateToolWithRunnerOwnerAndRegistry mirrors
// newDelegateToolWithRunnerAndOwner but also installs an agent registry
// so the corrective-retry model override can read the MEMBER's declared
// preferred_models chain (resolveChildModelChain). The corrective retry
// escalates the struggling member onto its OWN capable preferred tier
// (the chain head), not the lead's current model — so a member stalling
// on a weak fallback (zai/glm) is re-rolled on its capable tier when one
// is reachable, rather than re-pinned to the lead's (possibly-glm) model.
func newDelegateToolWithRunnerOwnerAndRegistry(
	engines map[string]*engine.Engine,
	store coordination.Store,
	runner swarm.GateRunner,
	owner *engine.Engine,
	reg *agent.Registry,
) *engine.DelegateTool {
	tool := engine.NewDelegateToolWithBackground(
		engines,
		agent.Delegation{CanDelegate: true},
		"planner",
		nil,
		store,
	)
	return tool.WithGateRunner(runner).WithOwnerEngine(owner).WithRegistry(reg)
}

// memberChainRegistry builds an agent registry whose plan-reviewer member
// declares the given preferred_models chain (capable head first). Used by
// the corrective-retry escalation specs to assert the retry targets the
// member's capable tier.
func memberChainRegistry(prefs ...agent.ModelPreference) *agent.Registry {
	reg := agent.NewRegistry()
	reg.Register(&agent.Manifest{
		ID:              "plan-reviewer",
		Name:            "Plan Reviewer",
		PreferredModels: prefs,
	})
	return reg
}

// flakyMemberGateRunner fails the first failFor post-member gate
// dispatches with the no-output GateError shape, then passes. It records
// the dispatch count so a spec can assert the exact number of member
// re-dispatches the retry loop attempted. failFor = math.MaxInt models a
// member that never writes (always fails).
type flakyMemberGateRunner struct {
	failFor    int
	calls      int
	reason     string
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
		//
		// The run must OWN its chain: the engine stamps a per-run chainID
		// at swarm start (AssignRunChainID) so the publisher targets the
		// owned namespace, never an empty-chain suffix-scan of a foreign
		// "*/plan" (the cross-chain-publish footgun).
		outputDir := GinkgoT().TempDir()
		store := coordination.NewMemoryStore()
		gates := []swarm.GateSpec{
			{Name: "post-swarm-plan-published", Kind: "builtin:artifact-published", When: swarm.LifecyclePostSwarm, OutputKey: "{chainID}/plan"},
		}
		swarmCtx := defaultPrefixSwarmContextWithGates(gates)
		swarmCtx.AssignRunChainID("session-readyz")
		assignedChain := swarmCtx.ChainPrefix

		Expect(store.Set(assignedChain+"/plan",
			[]byte(`{"markdown":"# Readyz Plan\n\nbody","title":"Readyz Plan"}`))).To(Succeed())
		Expect(store.Set(assignedChain+"/review", validVerdictPayload())).To(Succeed())

		// A runner that records, AT GATE-DISPATCH TIME, whether the
		// publish already happened: the publication record must exist
		// before the post-swarm gate runs.
		probe := &publishProbeRunner{store: store, key: assignedChain + "/plan_publication"}
		engines, _ := reviewerEnginesWithContext(swarmCtx)
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

	Context("a NON-planning swarm (no artifact-published gate) at post-swarm", func() {
		// The headline incident: `mental-health-swarm` pins chain_prefix
		// "mental-health" while its id is "mental-health-swarm", declares no
		// artifact-published gate, and dispatches no member that supplies a
		// chainID. Under the old code FlushSwarmLifecycle published
		// UNCONDITIONALLY: with an empty resolved chain the publisher
		// suffix-scanned a FOREIGN chain's "*/plan" (a stale "planner/plan"
		// failover-envelope JSON) and aborted the entire run with
		// "refusing to publish chain ... no markdown heading structure".
		//
		// A swarm that does not publish plans must NEVER touch the publish
		// path: the post-swarm flush is a clean no-op, never errors, and
		// never reads — let alone publishes — another chain's plan.

		// nonPlanningSwarmContext mirrors the incident's manifest shape: a
		// pinned prefix that differs from the swarm id, no per-run assignment,
		// and NO artifact-published gate.
		nonPlanningSwarmContext := func() *swarm.Context {
			return &swarm.Context{
				SwarmID:     "mental-health-swarm",
				LeadAgent:   "planner",
				Members:     []string{"plan-reviewer"},
				ChainPrefix: "mental-health",
				Gates:       nil,
			}
		}

		It("does NOT publish or error, and does NOT touch a foreign chain's plan", func() {
			outputDir := GinkgoT().TempDir()
			store := coordination.NewMemoryStore()
			// A stale FOREIGN chain's plan key sits in the store from an
			// earlier, unrelated planning run. It is exactly the kind of
			// non-plan blob the old suffix-scan grabbed and choked on.
			Expect(store.Set("planner/plan",
				[]byte(`{"role":"assistant","content":"failover envelope, not a plan"}`))).To(Succeed())

			runner := &recordingRunner{}
			engines, _ := reviewerEnginesWithContext(nonPlanningSwarmContext())
			delegateTool := newDelegateToolWithRunner(engines, store, runner).
				WithPlanOutputDir(outputDir)

			// The whole run must NOT abort.
			Expect(delegateTool.FlushSwarmLifecycle(context.Background())).To(Succeed(),
				"a non-planning swarm's post-swarm flush must never abort on a foreign plan key")

			// Nothing was published.
			entries, _ := os.ReadDir(outputDir)
			Expect(entries).To(BeEmpty(),
				"a swarm with no artifact-published gate writes no vault file")

			// The foreign chain was NOT consumed / recorded against.
			exists, err := store.Exists("planner/plan_publication")
			Expect(err).NotTo(HaveOccurred())
			Expect(exists).To(BeFalse(),
				"the foreign chain must never receive a publication record from another swarm's run")
		})
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

			foundDirective := false
			for _, req := range reviewerProv.capturedRequests {
				for _, msg := range req.Messages {
					if strings.Contains(msg.Content, directive) || strings.Contains(msg.Content, "You are in final delivery mode.") {
						foundDirective = true
						break
					}
				}
				if foundDirective {
					break
				}
			}
			Expect(foundDirective).To(BeTrue(),
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

			It("forces the coordination_store write when the gate fails with a schema validation error", func() {
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{
					failFor: 1,
					reason: "schema validation failed: validating root: required: " +
						`missing properties: ["summary" "findings"]`,
				}
				engines, reviewerProv := reviewerEnginesWithProvider(swarmContextWithGates(postMemberGate()))
				delegateTool := newDelegateToolWithRunner(engines, store, runner)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.StreamCallCount()).To(BeNumerically(">=", 2),
					"the member is re-dispatched after the schema-validation miss")
				Expect(reviewerProv.ToolChoiceForAttempt(2)).To(Equal("tool:coordination_store"),
					"the corrective retry forces the coordination_store write even on a schema validation failure")
			})
		})

		Context("model override on the corrective retry", func() {
			It("escalates the corrective retry onto the MEMBER's capable preferred tier, not the lead's model", func() {
				// Bug (May 2026): the corrective retry copied the LEAD's
				// current (provider, model). When the lead itself has failed
				// over onto a weak model (anthropic + openai unavailable →
				// everything cascades to zai/glm), the retry routed the
				// member glm → glm — a NO-OP, so a member STALLING on glm was
				// re-rolled on glm and stalled every time.
				//
				// Fix: the corrective retry escalates the struggling member
				// onto its OWN preferred_models chain head (the most-capable
				// tier the member declares), not the lead's current model. The
				// member here declares [anthropic/claude-sonnet-4-6 →
				// openai/gpt-4o → zai/glm-5.1]; the retry must target the
				// capable HEAD (anthropic/claude-sonnet-4-6). The engine's
				// failover then cascades down the member's chain to a reachable
				// tier if the head is down — so "only glm reachable" degrades
				// to today's behaviour with no regression, while a reachable
				// capable tier is genuinely re-attempted.
				//
				// The lead is pinned to the WEAK glm model to prove the retry
				// does NOT copy it.
				store := coordination.NewMemoryStore()
				runner := &flakyMemberGateRunner{failFor: 1}
				engines, leadEng, reviewerProv := reviewerEnginesWithLeadAndProvider(
					swarmContextWithGates(postMemberGate()))
				leadEng.SetModelPreference("zai", "glm-5.1")
				reg := memberChainRegistry(
					agent.ModelPreference{Provider: "anthropic", Model: "claude-sonnet-4-6"},
					agent.ModelPreference{Provider: "openai", Model: "gpt-4o"},
					agent.ModelPreference{Provider: "zai", Model: "glm-5.1"},
				)
				delegateTool := newDelegateToolWithRunnerOwnerAndRegistry(
					engines, store, runner, leadEng, reg)

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())

				Expect(reviewerProv.StreamCallCount()).To(BeNumerically(">=", 2),
					"the member is re-dispatched after the first miss")
				Expect(reviewerProv.ProviderForAttempt(2)).To(Equal("anthropic"),
					"the corrective retry escalates onto the member's capable preferred provider, not the lead's glm")
				Expect(reviewerProv.ModelForAttempt(2)).To(Equal("claude-sonnet-4-6"),
					"the corrective retry escalates onto the member's capable preferred model (chain head), not the lead's glm")
				Expect(reviewerProv.ToolChoiceForAttempt(2)).To(Equal("tool:coordination_store"),
					"the corrective retry still forces the coordination_store write")
			})

			It("falls back to the lead's resolved model when the member declares NO preferred_models", func() {
				// Fallback guard: a member with an empty preferred_models chain
				// (or a registry-less surface) has no capable tier to escalate
				// onto, so the corrective retry keeps the existing behaviour and
				// routes onto the lead's already-resolved (provider, model). The
				// override must NOT be lost entirely (which would leave the
				// member on its stalled tier). No registry is wired here, so
				// resolveChildModelChain returns nil and the lead-model fallback
				// fires.
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
					"with no member chain the retry falls back to the lead's resolved provider")
				Expect(reviewerProv.ModelForAttempt(2)).To(Equal("gpt-5"),
					"with no member chain the retry falls back to the lead's resolved model")
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

		Context("salvaging the member reply into its output_key", func() {
			// salvageGate is a single prose-tolerant post-member gate with an
			// EXPLICIT, non-templated output_key so the resolved coord-store
			// key is deterministic (planning/plan-reviewer/evidence) — no
			// random per-run chainID to thread through the assertion. The real
			// result-schema runner reads this key; the engine's salvage step
			// must write the member's reply into the SAME key.
			salvageGate := func() []swarm.GateSpec {
				return []swarm.GateSpec{
					{
						Name:      "post-member-plan-reviewer-evidence",
						Kind:      "builtin:result-schema",
						SchemaRef: swarm.EvidenceBundleV1Name,
						When:      swarm.LifecyclePostMember,
						Target:    "plan-reviewer",
						OutputKey: "evidence",
					},
				}
			}

			realRunner := func() swarm.GateRunner {
				multi := swarm.NewMultiRunner()
				multi.Register("builtin:result-schema", swarm.NewResultSchemaRunner())
				return multi
			}

			It("salvages the reply into the resolved output_key when the member produced content but never wrote it", func() {
				// gpt-4o reliably NARRATES the artefact in its reply but fails to
				// emit the coordination_store(set) tool call. The content exists
				// in the member's turn (result.response) — the engine must, on the
				// FINAL attempt, salvage that reply into the gate's output_key so
				// the post-member gate validates it and the member succeeds instead
				// of going terminal over a missing key.
				store := coordination.NewMemoryStore()
				engines, _ := reviewerEnginesWithContext(swarmContextWithGates(salvageGate()))
				delegateTool := newDelegateToolWithRunner(engines, store, realRunner())

				result, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

				Expect(err).NotTo(HaveOccurred(),
					"a member whose reply carries the artefact must be salvaged, not failed")
				Expect(result.Output).To(ContainSubstring("review complete"))

				val, getErr := store.Get("planning/plan-reviewer/evidence")
				Expect(getErr).NotTo(HaveOccurred(),
					"the salvage wrote the reply into the gate's resolved output_key")
				Expect(string(val)).To(Equal("review complete"),
					"the RAW reply is salvaged verbatim — no <task_result> wrapping that would pollute the prose")
			})

			It("does not salvage when the member already wrote its output_key — the explicit write wins", func() {
				// Clean-path guard: a member that performs the coordination_store
				// write on attempt 1 passes the gate immediately; the salvage floor
				// must not overwrite the member's own (richer) write with the bare
				// reply text.
				store := coordination.NewMemoryStore()
				Expect(store.Set("planning/plan-reviewer/evidence",
					[]byte("# Findings\nthe member's explicit write"))).To(Succeed())
				engines, _ := reviewerEnginesWithContext(swarmContextWithGates(salvageGate()))
				delegateTool := newDelegateToolWithRunner(engines, store, realRunner())

				result, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("review complete"))

				val, getErr := store.Get("planning/plan-reviewer/evidence")
				Expect(getErr).NotTo(HaveOccurred())
				Expect(string(val)).To(Equal("# Findings\nthe member's explicit write"),
					"the member's own write is preserved — salvage only fills an EMPTY key")
			})

			It("still fails terminally when the member's reply is empty — salvage rescues no content", func() {
				// Guardrail: the salvage is a content RECOVERY, not a pass-through.
				// A member that narrates NOTHING (empty reply) still goes terminal —
				// salvaging an empty body leaves the prose gate failing closed, so a
				// junk plan is NEVER published.
				store := coordination.NewMemoryStore()
				emptyReviewer := &mockProvider{
					name:         "empty-reviewer-provider",
					streamChunks: []provider.StreamChunk{{Content: "   ", Done: true}},
				}
				leadEng := engine.New(engine.Config{
					ChatProvider: leadProvider(),
					Manifest: agent.Manifest{
						ID:                "planner",
						Name:              "Planner",
						Instructions:      agent.Instructions{SystemPrompt: "lead"},
						ContextManagement: agent.DefaultContextManagement(),
					},
					SwarmContext: swarmContextWithGates(salvageGate()),
				})
				reviewerEng := engine.New(engine.Config{
					ChatProvider: emptyReviewer,
					Manifest: agent.Manifest{
						ID:                "plan-reviewer",
						Name:              "Plan Reviewer",
						Instructions:      agent.Instructions{SystemPrompt: "review"},
						ContextManagement: agent.DefaultContextManagement(),
					},
				})
				engines := map[string]*engine.Engine{"planner": leadEng, "plan-reviewer": reviewerEng}
				delegateTool := newDelegateToolWithRunner(engines, store, realRunner())

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

				var gateErr *swarm.GateError
				Expect(errors.As(err, &gateErr)).To(BeTrue(),
					"an empty reply is not salvageable — the gate stays terminal")
				Expect(gateErr.MemberID).To(Equal("plan-reviewer"))

				// The salvage may write the whitespace verbatim, but the prose
				// gate rejects empty/whitespace, so the GateError above is the
				// authoritative outcome — no RENDERABLE content was accepted. The
				// stored value (if any) carries no usable artefact.
				val, getErr := store.Get("planning/plan-reviewer/evidence")
				if getErr == nil {
					Expect(strings.TrimSpace(string(val))).To(BeEmpty(),
						"a whitespace-only reply carries no artefact — salvage never publishes junk")
				}
			})

			It("does NOT salvage a member with more than one result-schema output gate", func() {
				// Safety guard: salvage refuses to guess which key to fill for a
				// multi-output member. With two result-schema post-member gates
				// carrying output_keys, neither is salvaged — the member must write
				// explicitly, so the run goes terminal over the missing keys.
				store := coordination.NewMemoryStore()
				twoGates := []swarm.GateSpec{
					{
						Name:      "post-member-plan-reviewer-evidence",
						Kind:      "builtin:result-schema",
						SchemaRef: swarm.EvidenceBundleV1Name,
						When:      swarm.LifecyclePostMember,
						Target:    "plan-reviewer",
						OutputKey: "evidence",
					},
					{
						Name:      "post-member-plan-reviewer-refs",
						Kind:      "builtin:result-schema",
						SchemaRef: swarm.ExternalRefsV1Name,
						When:      swarm.LifecyclePostMember,
						Target:    "plan-reviewer",
						OutputKey: "refs",
					},
				}
				engines, _ := reviewerEnginesWithContext(swarmContextWithGates(twoGates))
				delegateTool := newDelegateToolWithRunner(engines, store, realRunner())

				_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())

				var gateErr *swarm.GateError
				Expect(errors.As(err, &gateErr)).To(BeTrue(),
					"a multi-output member is not salvaged — it must write explicitly")

				evExists, _ := store.Exists("planning/plan-reviewer/evidence")
				refExists, _ := store.Exists("planning/plan-reviewer/refs")
				Expect(evExists).To(BeFalse(), "no key is salvaged for a multi-output member")
				Expect(refExists).To(BeFalse(), "no key is salvaged for a multi-output member")
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

// handoffAtDepth builds a delegation.Handoff metadata-tagged with the
// requested depth so checkSpawnLimits sees the right value.
func handoffAtDepth(n int) *delegation.Handoff {
	return &delegation.Handoff{
		Metadata: map[string]string{"depth": strconv.Itoa(n)},
	}
}

// minimalDelegateTool returns a DelegateTool with default spawn
// limits and the given swarm registry installed.
func minimalDelegateTool(reg *swarm.Registry) *engine.DelegateTool {
	return engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{CanDelegate: true}, "lead").
		WithSwarmRegistry(reg)
}

// codegenManifest returns a swarm manifest with SwarmType=codegen so
// ResolveMaxDepth picks the addendum-A4 depth-16 default.
func codegenManifest() *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "codegen-swarm",
		Lead:          "lead",
		Members:       []string{"worker"},
		SwarmType:     swarm.SwarmTypeCodegen,
	}
}

// pinnedDepthManifest returns a manifest with an explicit MaxDepth.
func pinnedDepthManifest(depth int) *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "pinned-depth-swarm",
		Lead:          "lead",
		Members:       []string{"worker"},
		MaxDepth:      depth,
	}
}

// installSwarmCtxOnLead wires a swarm.Context onto the lead engine so
// checkSpawnLimits's d.activeSwarmContext() lookup succeeds.
func installSwarmCtxOnLead(dt *engine.DelegateTool, m *swarm.Manifest) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	swarmCtx := swarm.NewContext(m.ID, m)
	lead.SetSwarmContext(&swarmCtx)
	engines := map[string]*engine.Engine{"lead": lead}
	dt.SetEnginesForTest(engines)
}

var _ = Describe("SwarmDepthResolution", func() {
	Context("with a codegen swarm context", func() {
		It("permits a depth-10 handoff that the historical default would have rejected", func() {
			reg := swarm.NewRegistry()
			manifest := codegenManifest()
			reg.Register(manifest)
			dt := minimalDelegateTool(reg)
			installSwarmCtxOnLead(dt, manifest)

			err := dt.CheckSpawnLimitsForTest(handoffAtDepth(10))

			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("with an explicit MaxDepth pinned on the manifest", func() {
		It("rejects a handoff at the pinned ceiling", func() {
			reg := swarm.NewRegistry()
			manifest := pinnedDepthManifest(7)
			reg.Register(manifest)
			dt := minimalDelegateTool(reg)
			installSwarmCtxOnLead(dt, manifest)

			err := dt.CheckSpawnLimitsForTest(handoffAtDepth(8))

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("depth limit"))
		})
	})

	Context("with no active swarm context", func() {
		It("falls back to the historical SpawnLimits.MaxDepth=5", func() {
			dt := minimalDelegateTool(nil)

			err := dt.CheckSpawnLimitsForTest(handoffAtDepth(5))

			Expect(err).To(HaveOccurred())
		})
	})
})

// extGateSwarmContext builds a swarm.Context whose Gates slice
// includes a single ext: post-member gate targeting the named member.
func extGateSwarmContext(swarmID, gateKind, target string) swarm.Context {
	return swarm.Context{
		SwarmID:   swarmID,
		LeadAgent: "lead",
		Members:   []string{target},
		Gates: []swarm.GateSpec{{
			Name:   "g1",
			Kind:   gateKind,
			When:   swarm.LifecyclePostMember,
			Target: target,
		}},
		ChainPrefix: swarmID,
	}
}

var _ = Describe("SwarmExtGateRouting", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
	})

	Context("when the manifest names an ext gate registered via RegisterExtGateFunc", func() {
		It("dispatches through the public RunGate path and observes a pass", func() {
			var calls atomic.Int32
			err := swarm.RegisterExtGateFunc("echo-pass", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				calls.Add(1)
				return swarm.ExtGateResponse{Pass: true}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			runner := swarm.NewMultiRunner()
			gateErr := runner.Run(context.Background(), swarm.GateSpec{
				Name: "g1",
				Kind: "ext:echo-pass",
				When: swarm.LifecyclePostMember,
			}, swarm.GateArgs{SwarmID: "s", MemberID: "m"})

			Expect(gateErr).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
		})

		It("surfaces *swarm.GateError when the registered ext gate returns Pass:false", func() {
			err := swarm.RegisterExtGateFunc("echo-fail", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				return swarm.ExtGateResponse{Pass: false, Reason: "no go"}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			runner := swarm.NewMultiRunner()
			gateErr := runner.Run(context.Background(), swarm.GateSpec{
				Name: "g2",
				Kind: "ext:echo-fail",
				When: swarm.LifecyclePostMember,
			}, swarm.GateArgs{SwarmID: "s", MemberID: "m"})

			Expect(gateErr).To(HaveOccurred())
			var ge *swarm.GateError
			Expect(errors.As(gateErr, &ge)).To(BeTrue())
			Expect(ge.GateKind).To(Equal("ext:echo-fail"))
		})

		It("times out the gate when Timeout is set and the func sleeps past it", func() {
			err := swarm.RegisterExtGateFunc("slow-gate", func(ctx context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				select {
				case <-time.After(500 * time.Millisecond):
					return swarm.ExtGateResponse{Pass: true}, nil
				case <-ctx.Done():
					return swarm.ExtGateResponse{}, ctx.Err()
				}
			})
			Expect(err).NotTo(HaveOccurred())

			report := swarm.Dispatch(context.Background(), swarm.NewMultiRunner(), []swarm.GateSpec{{
				Name:    "g3",
				Kind:    "ext:slow-gate",
				When:    swarm.LifecyclePostMember,
				Timeout: 50 * time.Millisecond,
			}}, swarm.GateArgs{SwarmID: "s", MemberID: "m"})

			Expect(report.Halted).To(BeTrue())
			var ge *swarm.GateError
			Expect(errors.As(report.Err, &ge)).To(BeTrue())
			Expect(errors.Is(report.Err, context.DeadlineExceeded)).To(BeTrue())
		})
	})

	Context("when an ext gate fires through the engine's post-member dispatcher", func() {
		It("routes via runner.Run for the matching member and the func runs once", func() {
			var calls atomic.Int32
			err := swarm.RegisterExtGateFunc("engine-pass", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				calls.Add(1)
				return swarm.ExtGateResponse{Pass: true}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			lead := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "lead"},
				Manifest: agent.Manifest{
					ID:                "lead",
					Name:              "Lead",
					Instructions:      agent.Instructions{SystemPrompt: "lead"},
					Delegation:        agent.Delegation{CanDelegate: true},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			engines := map[string]*engine.Engine{"lead": lead}
			swarmCtx := extGateSwarmContext("ext-route-swarm", "ext:engine-pass", "qa-agent")
			lead.SetSwarmContext(&swarmCtx)

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithGateRunner(swarm.NewMultiRunner())

			gateErr := delegateTool.DispatchPostMemberGatesForTest(context.Background(), "qa-agent", "")

			Expect(gateErr).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
		})
	})
})

// concurrencyProbeForEngine mirrors internal/swarm/dispatch_test.go's
// probe — same primitive, exposed so the engine-side tests don't
// reinvent the helper. Tracks max concurrent enter()/leave() pairs.
type concurrencyProbeForEngine struct {
	mu        sync.Mutex
	active    int
	maxActive int
	total     int
}

func newProbe() *concurrencyProbeForEngine {
	return &concurrencyProbeForEngine{}
}

func (p *concurrencyProbeForEngine) enter() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active++
	p.total++
	if p.active > p.maxActive {
		p.maxActive = p.active
	}
}

func (p *concurrencyProbeForEngine) leave() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.active--
}

func (p *concurrencyProbeForEngine) snapshotMax() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxActive
}

// sharedBarrier holds the gate state every streamer in a fan-out
// shares. enterAndWait increments the arrival counter and releases
// once releaseAt arrivals have accumulated.
type sharedBarrier struct {
	gate    chan struct{}
	arrived int32
	target  int32
}

func newSharedBarrier(releaseAt int) *sharedBarrier {
	return &sharedBarrier{gate: make(chan struct{}), target: int32(releaseAt)}
}

func (b *sharedBarrier) enterAndWait(ctx context.Context) {
	if atomic.AddInt32(&b.arrived, 1) >= b.target {
		select {
		case <-b.gate:
		default:
			close(b.gate)
		}
	}
	select {
	case <-b.gate:
	case <-ctx.Done():
	}
}

// barrierStreamer returns a streaming.Streamer that blocks every
// member until the shared barrier hits releaseAt arrivals.
func barrierStreamer(probe *concurrencyProbeForEngine, barrier *sharedBarrier) streaming.Streamer {
	return streamerFunc(func(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		probe.enter()
		barrier.enterAndWait(ctx)
		probe.leave()
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// boundedHoldStreamer marks enter/leave around a bounded sleep so
// MaxParallel clamping can be observed without depending on a fixed
// barrier-arrival count.
func boundedHoldStreamer(probe *concurrencyProbeForEngine, hold time.Duration) streaming.Streamer {
	return streamerFunc(func(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		probe.enter()
		select {
		case <-time.After(hold):
		case <-ctx.Done():
		}
		probe.leave()
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// orderRecorderForEngine mirrors the swarm-package recorder; tracks
// the start:/end: ordering of member streams.
type orderRecorderForEngine struct {
	mu     sync.Mutex
	events []string
}

func newRecorder() *orderRecorderForEngine {
	return &orderRecorderForEngine{}
}

func (o *orderRecorderForEngine) record(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, s)
}

func (o *orderRecorderForEngine) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]string, len(o.events))
	copy(out, o.events)
	return out
}

// recordingStreamer marks start: and end: events around a fixed wait
// so the test can confirm sequential mode respects roster order.
func recordingStreamer(rec *orderRecorderForEngine, member string) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		rec.record("start:" + member)
		time.Sleep(2 * time.Millisecond)
		rec.record("end:" + member)
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// parallelManifest builds a swarm manifest pinned to parallel
// dispatch with cap, used to drive DispatchSwarmMembers.
func parallelManifest(id string, members []string, parallel bool, maxParallel int) *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            id,
		Lead:          "lead",
		Members:       members,
		SwarmType:     swarm.SwarmTypeAnalysis,
		Harness: swarm.HarnessConfig{
			Parallel:    parallel,
			MaxParallel: maxParallel,
		},
	}
}

// stallingStreamer returns a streamer that opens a channel but never
// emits anything (and never closes it) until the per-call ctx is
// cancelled. Models a delegate child that goes silent mid-stream — the
// exact symptom the per-member timeout guards against. The returned
// signal channel closes once ctx.Done() fires so the test can prove
// the cancellation observed by the streamer was deadline-driven.
func stallingStreamer() (streaming.Streamer, <-chan context.Context) {
	cancelled := make(chan context.Context, 4)
	s := streamerFunc(func(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		ch := make(chan provider.StreamChunk)
		go func() {
			<-ctx.Done()
			select {
			case cancelled <- ctx:
			default:
			}
			close(ch)
		}()
		return ch, nil
	})
	return s, cancelled
}

// buildLeadAndMemberEngines creates a lead plus N member engines and
// returns the engines map ready for DelegateTool wiring.
func buildLeadAndMemberEngines(memberIDs []string) (*engine.Engine, map[string]*engine.Engine) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{"lead": lead}
	for _, id := range memberIDs {
		engines[id] = engine.New(engine.Config{
			ChatProvider: &mockProvider{name: id},
			Manifest: agent.Manifest{
				ID:                id,
				Name:              id,
				Instructions:      agent.Instructions{SystemPrompt: id},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})
	}
	return lead, engines
}

var _ = Describe("SwarmParallelDispatch", func() {
	Context("with Parallel=true and MaxParallel=2 over three members", func() {
		It("never lets more than MaxParallel members run concurrently", func() {
			members := []string{"alpha", "bravo", "charlie"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("parallel-swarm", members, true, 2)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			probe := newProbe()
			barrier := newSharedBarrier(2)
			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = barrierStreamer(probe, barrier)
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(BeNumerically("==", 2))
		})
	})

	Context("with Parallel=false (the default)", func() {
		It("runs members one at a time in roster order", func() {
			members := []string{"alpha", "bravo", "charlie"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("seq-swarm", members, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			rec := newRecorder()
			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = recordingStreamer(rec, m)
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(rec.snapshot()).To(Equal([]string{
				"start:alpha", "end:alpha",
				"start:bravo", "end:bravo",
				"start:charlie", "end:charlie",
			}))
		})
	})

	Context("when the manifest's MaxParallel exceeds MaxTotalBudget", func() {
		It("clamps the fan-out at the spawn-limits budget", func() {
			members := []string{"alpha", "bravo", "charlie", "delta"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("budget-swarm", members, true, 4)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			probe := newProbe()
			streamers := map[string]streaming.Streamer{}
			for _, m := range members {
				streamers[m] = boundedHoldStreamer(probe, 25*time.Millisecond)
			}

			limits := delegation.DefaultSpawnLimits()
			limits.MaxTotalBudget = 2
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSpawnLimits(limits)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(probe.snapshotMax()).To(BeNumerically("<=", 2))
		})
	})

	// Per-member timeout guards the parent against a stalled child
	// hanging the coordinator forever. Symptom: session 3255e2ee — a
	// coordinator dispatched researcher + executor in parallel; the
	// executor went silent mid-stream and the parent's
	// collectWithProgress await loop had no time.After branch, so the
	// parent session stayed active indefinitely. Fix: HarnessConfig
	// gains MemberTimeout; the per-member dispatch ctx is wrapped with
	// WithTimeout (zero = no deadline preserves current behaviour).
	Context("with Harness.MemberTimeout set and a stalled member stream", func() {
		It("returns the deadline error and cancels the sibling member", func() {
			members := []string{"stalls", "sibling"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("timeout-swarm", members, true, 2)
			manifest.Harness.MemberTimeout = 100 * time.Millisecond
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			stallStreamer, stallCancels := stallingStreamer()
			siblingStreamer, siblingCancels := stallingStreamer()
			streamers := map[string]streaming.Streamer{
				"stalls":  stallStreamer,
				"sibling": siblingStreamer,
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			start := time.Now()
			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")
			elapsed := time.Since(start)

			Expect(err).To(HaveOccurred(), "stalled member must surface as an error, not a forever-hang")
			Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue(),
				"expected DeadlineExceeded in the error chain; got %v", err)
			Expect(elapsed).To(BeNumerically("<", 5*time.Second),
				"with MemberTimeout=100ms the dispatch must unwind well before any default; got %s", elapsed)

			// Both streamer goroutines must observe their per-call ctx
			// firing — the stalled member from its own deadline, the
			// sibling from dispatchParallel's first-error cancel cascade.
			Eventually(stallCancels, "1s").Should(Receive())
			Eventually(siblingCancels, "1s").Should(Receive())
		})

		It("does not fire when MemberTimeout is zero (the default) and the streamer completes", func() {
			members := []string{"alpha"}
			lead, engines := buildLeadAndMemberEngines(members)
			manifest := parallelManifest("no-timeout-swarm", members, true, 1)
			// MemberTimeout left at zero — backwards-compatible default.
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			probe := newProbe()
			streamers := map[string]streaming.Streamer{
				"alpha": boundedHoldStreamer(probe, 25*time.Millisecond),
			}
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, members, "go")

			Expect(err).NotTo(HaveOccurred(),
				"zero MemberTimeout must preserve the no-deadline contract")
		})
	})

	// Bug C — Swarm-target dispatch case-fold. Forensic evidence:
	// session 7dfdb197-ce21-45a2-b5da-f2fa62dd293b at 17:57:05.214
	//
	//   swarm-target dispatch "dev-swarm" failed:
	//     no engine for swarm member "tech-Lead"
	//
	// The orchestrator delegated to a swarm whose Members[] referenced
	// `tech-Lead`, but the per-agent engines map was keyed under the
	// canonical `Tech-Lead`. The first lookup (in-swarm gate at
	// resolveTargetWithOptions, fixed by Bug 2 / 576b8156 via
	// containsAgent's strings.EqualFold) passed; the SECOND lookup
	// (d.engines[memberID] at delegation.go:2983 inside buildMemberRunner)
	// is a raw map access that's case-sensitive — so the dispatch
	// surfaces "no engine for swarm member" despite the member being
	// registered under a case-variant of the same id.
	//
	// Pin the case-fold contract end-to-end:
	//   - canonical-registered Tech-Lead resolves under member id
	//     `tech-Lead`, `TECH-LEAD`, and `tech-lead`.
	//   - canonical-registered Senior-Engineer resolves under
	//     `senior-engineer` and `Senior-engineer`.
	//   - completely unknown ids still error with the existing
	//     "no engine for swarm member" sentinel.
	Context("Bug C — case-insensitive engine resolution under swarm-target dispatch", func() {
		It("resolves Tech-Lead engine when the swarm member id case-differs (tech-Lead)", func() {
			// Engines registered under canonical case.
			lead, engines := buildLeadAndMemberEngines([]string{"Tech-Lead"})

			// Manifest Members[] uses the case-variant the orchestrator
			// actually typed in the forensic session.
			memberAsCalled := "tech-Lead"
			manifest := parallelManifest("dev-swarm", []string{memberAsCalled}, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{
				"Tech-Lead": trivialStreamer(nil),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(
				context.Background(), &swarmCtx, []string{memberAsCalled}, "go")

			Expect(err).NotTo(HaveOccurred(),
				"buildMemberRunner must case-fold d.engines lookup so a case-variant member id ("+memberAsCalled+") resolves to the canonical engine (Tech-Lead) — mirrors Bug 2 / containsAgent's strings.EqualFold contract")
		})

		It("resolves the canonical engine across multiple case variants", func() {
			// Multiple canonical-cased engines registered.
			lead, engines := buildLeadAndMemberEngines([]string{"Tech-Lead", "Senior-Engineer"})

			cases := []struct {
				name          string
				memberAsTyped string
			}{
				{"upper-case member id", "TECH-LEAD"},
				{"all-lower member id", "tech-lead"},
				{"mixed-case Senior-Engineer", "Senior-engineer"},
				{"all-lower Senior-Engineer", "senior-engineer"},
			}

			for _, tc := range cases {
				By(tc.name)
				manifest := parallelManifest("dev-swarm-"+tc.memberAsTyped, []string{tc.memberAsTyped}, false, 0)
				reg := swarm.NewRegistry()
				reg.Register(manifest)
				swarmCtx := swarm.NewContext(manifest.ID, manifest)
				lead.SetSwarmContext(&swarmCtx)

				streamers := map[string]streaming.Streamer{
					"Tech-Lead":       trivialStreamer(nil),
					"Senior-Engineer": trivialStreamer(nil),
				}

				delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
					WithStreamers(streamers).
					WithSwarmRegistry(reg)

				err := delegateTool.DispatchSwarmMembers(
					context.Background(), &swarmCtx, []string{tc.memberAsTyped}, "go")

				Expect(err).NotTo(HaveOccurred(),
					"case variant "+tc.memberAsTyped+" must resolve to the canonical engine")
			}
		})

		It("still surfaces 'no engine for swarm member' when the target is genuinely unknown", func() {
			// Negative case — case-folding must NOT swallow real misses.
			// A completely unknown member id (no matching engine under
			// any case) must keep producing the existing sentinel error
			// so honest typos still surface to the model.
			lead, engines := buildLeadAndMemberEngines([]string{"Tech-Lead"})

			unknown := "Completely-Unknown-Agent"
			manifest := parallelManifest("dev-swarm-unknown", []string{unknown}, false, 0)
			reg := swarm.NewRegistry()
			reg.Register(manifest)
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(
				context.Background(), &swarmCtx, []string{unknown}, "go")

			Expect(err).To(HaveOccurred(),
				"a member id with no canonical match under any case must still error — case-fold must not swallow real misses")
			Expect(err.Error()).To(ContainSubstring("no engine for swarm member"),
				"the sentinel error message stays unchanged so existing log/transcript filters keep working")
			Expect(err.Error()).To(ContainSubstring(unknown),
				"the rejected member id appears verbatim in the error so the model sees what it actually typed")
		})
	})
})

// retryStreamerWith returns a streaming.Streamer that fails the first
// failures attempts with err and then drains a single Done chunk.
func retryStreamerWith(failures int, err error, calls *atomic.Int32) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		n := calls.Add(1)
		if int(n) <= failures {
			return nil, err
		}
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// panicStreamer returns a streaming.Streamer that panics inside Stream.
// Used to assert the runner maps panics to CategoryTerminal.
func panicStreamer(calls *atomic.Int32) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		calls.Add(1)
		panic("streamer boom")
	})
}

// streamerFunc adapts a func to streaming.Streamer for tests.
type streamerFunc func(ctx context.Context, agentID string, msg string) (<-chan provider.StreamChunk, error)

func (s streamerFunc) Stream(ctx context.Context, agentID string, msg string) (<-chan provider.StreamChunk, error) {
	return s(ctx, agentID, msg)
}

// retryableSwarmErr returns a CategorisedError tagged retryable so the
// swarm runner's retry policy fires.
func retryableSwarmErr() error {
	return &swarm.CategorisedError{Category: swarm.CategoryRetryable, Cause: errors.New("transient")}
}

// terminalSwarmErr returns a CategorisedError tagged terminal so the
// swarm runner short-circuits without retrying.
func terminalSwarmErr() error {
	return &swarm.CategorisedError{Category: swarm.CategoryTerminal, Cause: errors.New("permanent")}
}

// noJitterRetryManifest builds a swarm manifest pinned to fast retries
// so tests don't pay wall-clock backoff.
func noJitterRetryManifest(id string) *swarm.Manifest {
	return &swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            id,
		Lead:          "lead",
		Members:       []string{"qa-agent"},
		SwarmType:     swarm.SwarmTypeAnalysis,
		Retry: &swarm.RetryPolicy{
			MaxAttempts:    3,
			InitialBackoff: 1 * time.Millisecond,
			MaxBackoff:     1 * time.Millisecond,
			Multiplier:     1.0,
			Jitter:         false,
		},
	}
}

// installSwarmCtxOnEngine wires a swarm.Context into the engine and a
// matching manifest into a registry the DelegateTool can look up.
func installSwarmCtxOnEngine(eng *engine.Engine, m *swarm.Manifest) *swarm.Registry {
	reg := swarm.NewRegistry()
	reg.Register(m)
	swarmCtx := swarm.NewContext(m.ID, m)
	eng.SetSwarmContext(&swarmCtx)
	return reg
}

// buildLeadAndTargetEngines constructs a minimal lead + target engine
// pair the DelegateTool can route between.
func buildLeadAndTargetEngines() (*engine.Engine, *engine.Engine, map[string]*engine.Engine) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	target := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "qa"},
		Manifest: agent.Manifest{
			ID:                "qa-agent",
			Name:              "QA",
			Instructions:      agent.Instructions{SystemPrompt: "qa"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{"lead": lead, "qa-agent": target}
	return lead, target, engines
}

var _ = Describe("SwarmRunnerWiring", func() {
	Context("when the streamer returns a CategoryRetryable error", func() {
		It("retries up to the manifest's MaxAttempts and succeeds", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("retry-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(2, retryableSwarmErr(), calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)

			Expect(err).NotTo(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(3)))
		})
	})

	Context("when the streamer returns a CategoryTerminal error", func() {
		It("short-circuits at the first attempt and surfaces *swarm.CategorisedError", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("terminal-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(99, terminalSwarmErr(), calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)

			Expect(err).To(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
			var ce *swarm.CategorisedError
			Expect(errors.As(err, &ce)).To(BeTrue())
			Expect(ce.Category).To(Equal(swarm.CategoryTerminal))
		})
	})

	Context("when the streamer panics", func() {
		It("maps the panic to CategoryTerminal without retrying", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("panic-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": panicStreamer(calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)

			Expect(err).To(HaveOccurred())
			Expect(calls.Load()).To(Equal(int32(1)))
			var ce *swarm.CategorisedError
			Expect(errors.As(err, &ce)).To(BeTrue())
			Expect(ce.Category).To(Equal(swarm.CategoryTerminal))
		})
	})

	Context("when the same swarm context drives multiple delegations", func() {
		It("reuses a single Runner so breaker state accumulates across calls", func() {
			lead, _, engines := buildLeadAndTargetEngines()
			manifest := noJitterRetryManifest("cache-swarm")
			reg := installSwarmCtxOnEngine(lead, manifest)

			calls := &atomic.Int32{}
			streamers := map[string]streaming.Streamer{
				"qa-agent": retryStreamerWith(0, nil, calls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "hello",
				},
			}

			_, err1 := delegateTool.Execute(context.Background(), input)
			_, err2 := delegateTool.Execute(context.Background(), input)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(delegateTool.RunnerForSwarmIDForTest("cache-swarm")).
				To(BeIdenticalTo(delegateTool.RunnerForSwarmIDForTest("cache-swarm")))
		})
	})
})

func newSwarmLeadEngine(leadID string, registry *agent.Registry) *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "swarm-lead-test"},
		Manifest: agent.Manifest{
			ID:   leadID,
			Name: "Senior Engineer",
			Instructions: agent.Instructions{
				SystemPrompt: "You are the senior engineer.",
			},
		},
		AgentRegistry: registry,
	})
}

func newSwarmLeadEngineWithSwarmRegistry(leadID string, registry *agent.Registry, swarmReg *swarm.Registry) *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "swarm-lead-test"},
		Manifest: agent.Manifest{
			ID:   leadID,
			Name: "Senior Engineer",
			Instructions: agent.Instructions{
				SystemPrompt: "You are the senior engineer.",
			},
		},
		AgentRegistry: registry,
		SwarmRegistry: swarmReg,
	})
}

// newCoordinatorEngineWithSwarmRegistry builds the engine used by the
// meta-swarm specs. The coordinator manifest carries `id: coordinator`
// so the swarm-lead block fires when swarmCtx.LeadAgent == "coordinator",
// and a populated swarm registry lets the lead-block renderer fall
// through to swarm-id lookup for members that aren't agents.
func newCoordinatorEngineWithSwarmRegistry(swarmReg *swarm.Registry) *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "meta-swarm-lead-test"},
		Manifest: agent.Manifest{
			ID:   "coordinator",
			Name: "Coordinator",
			Instructions: agent.Instructions{
				SystemPrompt: "You are the coordinator.",
			},
		},
		AgentRegistry: agent.NewRegistry(), // empty — sub-swarm ids must NOT resolve as agents
		SwarmRegistry: swarmReg,
	})
}

func newSwarmTestRegistry() *agent.Registry {
	registry := agent.NewRegistry()
	registry.Register(&agent.Manifest{
		ID:       "senior-engineer",
		Name:     "Senior Engineer",
		Metadata: agent.Metadata{Role: "Lead Engineer"},
	})
	registry.Register(&agent.Manifest{
		ID:       "explorer",
		Name:     "Explorer",
		Metadata: agent.Metadata{Role: "Codebase Explorer"},
	})
	registry.Register(&agent.Manifest{
		ID:       "Code-Reviewer",
		Name:     "Code Reviewer",
		Metadata: agent.Metadata{Role: "Quality Gate"},
	})
	return registry
}

func newBugHuntContext() swarm.Context {
	return swarm.Context{
		SwarmID:     "bug-hunt",
		LeadAgent:   "senior-engineer",
		Members:     []string{"explorer", "Code-Reviewer"},
		ChainPrefix: "bug-hunt",
		Depth:       1,
	}
}

var _ = Describe("Engine swarm-lead system prompt", func() {
	Describe("when no swarm context is attached", func() {
		It("does not include swarm-lead text in the prompt", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())

			prompt := eng.BuildSystemPrompt()

			Expect(strings.ToLower(prompt)).NotTo(ContainSubstring("leading swarm"))
			Expect(prompt).NotTo(ContainSubstring("Swarm Leadership"))
		})
	})

	Describe("when a swarm context is attached to the lead", func() {
		It("emits swarm id, member ids, and delegation guidance", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("bug-hunt"))
			Expect(prompt).To(ContainSubstring("explorer"))
			Expect(prompt).To(ContainSubstring("Code-Reviewer"))
			Expect(strings.ToLower(prompt)).To(ContainSubstring("delegate"))
			Expect(prompt).To(ContainSubstring("bug-hunt/senior-engineer"))
		})

		// Part 1 of the chainID-identity unification: when the engine has
		// stamped a per-run chainID (ChainIDAssigned), the lead's prompt MUST
		// surface that exact value and tell the lead to use it verbatim. The
		// recurring doom-loop was the planner free-forming its own chainID; the
		// fix is to give it the engine value and forbid invention. A swarm
		// WITHOUT an assigned chainID must NOT carry the directive (it would be
		// false — no engine value exists to reference).
		It("surfaces the engine-assigned chainID and forbids inventing one when the engine owns it", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()
			ctx.AssignRunChainID("session-prompt-test")
			engineChain := ctx.ChainPrefix
			Expect(engineChain).To(HavePrefix("bug-hunt-"),
				"precondition: the engine assigned a per-run chain anchored under the swarm id")

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring(engineChain),
				"the lead prompt must surface the exact engine-assigned chainID so the model references it")
			Expect(strings.ToLower(prompt)).To(ContainSubstring("engine-assigned"),
				"the lead prompt must mark the chainID as engine-assigned")
			Expect(strings.ToLower(prompt)).To(
				SatisfyAny(
					ContainSubstring("do not invent"),
					ContainSubstring("is ignored"),
				),
				"the lead prompt must forbid the model inventing its own chainID for an engine-owned run",
			)
		})

		It("does NOT emit the engine-assigned directive when no per-run chainID was stamped", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext() // static prefix, ChainIDAssigned == false

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(strings.ToLower(prompt)).NotTo(ContainSubstring("engine-assigned"),
				"a swarm without a stamped per-run chainID must not claim an engine-assigned value exists")
		})

		// Parallel dispatch: the lead must be instructed to emit independent
		// member delegate calls in a single assistant message so the engine's
		// concurrent dispatch path fires. Without this instruction the model
		// defaults to sequential one-at-a-time dispatch, burning 3–5× more
		// wall-clock time and tokens on wait overhead between members.
		// Wave-dependent members (e.g. reviewers that read explorer output)
		// must still be dispatched after their upstream members complete.
		It("instructs the lead to dispatch independent members in a single parallel message", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			lowerPrompt := strings.ToLower(prompt)
			Expect(lowerPrompt).To(
				SatisfyAny(
					ContainSubstring("single message"),
					ContainSubstring("simultaneously"),
					ContainSubstring("parallel"),
					ContainSubstring("at once"),
				),
				"swarm lead prompt must instruct the model to dispatch all members "+
					"in one message with multiple tool calls; without this the model "+
					"dispatches sequentially and blocks on each result before starting the next",
			)
		})

		It("resolves member names and roles from the agent registry", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("Explorer"))
			Expect(prompt).To(ContainSubstring("Codebase Explorer"))
			Expect(prompt).To(ContainSubstring("Quality Gate"))
		})

		It("appends swarm-owned lead prompt text from the swarm manifest", func() {
			swarmReg := swarm.NewRegistry()
			swarmReg.Register(&swarm.Manifest{
				ID:      "bug-hunt",
				Lead:    "senior-engineer",
				Members: []string{"explorer", "Code-Reviewer"},
				Context: swarm.ContextConfig{ChainPrefix: "bug-hunt"},
				Prompt:  swarm.PromptConfig{LeadAppend: "Lead-specific workflow instructions."},
			})
			eng := newSwarmLeadEngineWithSwarmRegistry("senior-engineer", newSwarmTestRegistry(), swarmReg)
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("Swarm Prompt Injection"))
			Expect(prompt).To(ContainSubstring("Lead-specific workflow instructions."))
		})
	})

	Describe("cache invalidation", func() {
		It("removes swarm-lead text after SetSwarmContext(nil)", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)
			withSwarm := eng.BuildSystemPrompt()
			Expect(strings.ToLower(withSwarm)).To(ContainSubstring("leading swarm"))

			eng.SetSwarmContext(nil)
			withoutSwarm := eng.BuildSystemPrompt()

			Expect(strings.ToLower(withoutSwarm)).NotTo(ContainSubstring("leading swarm"))
			Expect(withoutSwarm).NotTo(ContainSubstring("Swarm Leadership"))
		})

		It("returns identical prompts on repeated calls with the same context", func() {
			eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
			ctx := newBugHuntContext()
			eng.SetSwarmContext(&ctx)

			first := eng.BuildSystemPrompt()
			second := eng.BuildSystemPrompt()

			Expect(first).To(Equal(second))
		})
	})

	Describe("non-leading agent", func() {
		It("does not emit the leadership section even when swarm context is attached", func() {
			eng := newSwarmLeadEngine("explorer", newSwarmTestRegistry())
			ctx := newBugHuntContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(strings.ToLower(prompt)).NotTo(ContainSubstring("leading swarm"))
			Expect(prompt).NotTo(ContainSubstring("Swarm Leadership"))
		})

		It("appends swarm-owned member prompt text when the swarm scope targets that member", func() {
			swarmReg := swarm.NewRegistry()
			swarmReg.Register(&swarm.Manifest{
				ID:      "bug-hunt",
				Lead:    "senior-engineer",
				Members: []string{"explorer", "Code-Reviewer"},
				Prompt: swarm.PromptConfig{MemberAppends: map[string]swarm.PromptAppendConfig{
					"explorer": {Append: "Member-specific workflow instructions."},
				}},
			})
			eng := newSwarmLeadEngineWithSwarmRegistry("explorer", newSwarmTestRegistry(), swarmReg)
			ctx := newBugHuntContext()

			prompt := eng.BuildSystemPromptCtx(swarm.WithScope(context.Background(), &ctx))

			Expect(prompt).NotTo(ContainSubstring("Swarm Leadership"))
			Expect(prompt).To(ContainSubstring("Swarm Prompt Injection"))
			Expect(prompt).To(ContainSubstring("Member-specific workflow instructions."))
		})

		It("loads member prompt text from a swarm-owned file", func() {
			dir := GinkgoT().TempDir()
			appendPath := filepath.Join(dir, "explorer.md")
			Expect(os.WriteFile(appendPath, []byte("File-backed member instructions."), 0o600)).To(Succeed())

			swarmReg := swarm.NewRegistry()
			swarmReg.Register(&swarm.Manifest{
				ID:        "bug-hunt",
				Lead:      "senior-engineer",
				Members:   []string{"explorer", "Code-Reviewer"},
				SourceDir: dir,
				Prompt: swarm.PromptConfig{MemberAppends: map[string]swarm.PromptAppendConfig{
					"explorer": {File: "explorer.md"},
				}},
			})
			eng := newSwarmLeadEngineWithSwarmRegistry("explorer", newSwarmTestRegistry(), swarmReg)
			ctx := newBugHuntContext()

			prompt := eng.BuildSystemPromptCtx(swarm.WithScope(context.Background(), &ctx))

			Expect(prompt).To(ContainSubstring("File-backed member instructions."))
		})
	})

	// Meta-Swarm Coordinator Architecture (May 2026) — Phase 2.
	//
	// When the coordinator leads meta-swarm whose members ARE OTHER
	// SWARMS, the lead-block renderer (engine.go::appendSwarmLeadSectionFor)
	// must resolve each swarm-id member through the swarm registry and
	// render its description with a `(swarm)` suffix so the model can
	// tell at a glance that delegating to one of these members
	// dispatches a whole sub-swarm rather than a single agent.
	//
	// Without this, the renderer falls back to the bare id (since the
	// agent registry has no `a-team` / `dev-swarm` / etc. agents), the
	// model sees no description text, and the routing decision becomes
	// guesswork. The regression-pin tests below assert: each swarm-id
	// member appears in the prompt, each renders with its swarm
	// description, and the `(swarm)` marker is present so the kind is
	// disambiguated for the model.
	Describe("when the lead's swarm members are themselves swarms (meta-swarm)", func() {
		newMetaSwarmRegistry := func() *swarm.Registry {
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				ID:          "meta-swarm",
				Lead:        "coordinator",
				Members:     []string{"a-team", "dev-swarm", "planning-loop", "board-room"},
				Description: "Top-level orchestrator. Coordinator picks the right sub-swarm.",
			})
			reg.Register(&swarm.Manifest{
				ID:          "a-team",
				Lead:        "coordinator",
				Description: "A-Team Swarm (research → strategy → critique → writing → execution)",
			})
			reg.Register(&swarm.Manifest{
				ID:          "dev-swarm",
				Lead:        "Team-Lead",
				Description: "Dev Swarm (full-lifecycle implementation through Team-Lead)",
			})
			reg.Register(&swarm.Manifest{
				ID:          "planning-loop",
				Lead:        "planner",
				Description: "Planning Loop (requirements → research → plan)",
			})
			reg.Register(&swarm.Manifest{
				ID:          "board-room",
				Lead:        "chair",
				Description: "Board Room (financial/strategic analysis)",
			})
			return reg
		}

		newMetaSwarmContext := func() swarm.Context {
			return swarm.Context{
				SwarmID:     "meta-swarm",
				LeadAgent:   "coordinator",
				Members:     []string{"a-team", "dev-swarm", "planning-loop", "board-room"},
				ChainPrefix: "meta",
				Depth:       0,
			}
		}

		It("renders every sub-swarm member with its description and a (swarm) kind marker", func() {
			eng := newCoordinatorEngineWithSwarmRegistry(newMetaSwarmRegistry())
			ctx := newMetaSwarmContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			// All four sub-swarms appear as members in the lead block.
			Expect(prompt).To(ContainSubstring("a-team"))
			Expect(prompt).To(ContainSubstring("dev-swarm"))
			Expect(prompt).To(ContainSubstring("planning-loop"))
			Expect(prompt).To(ContainSubstring("board-room"))

			// Each sub-swarm's description is rendered so the model can
			// match user intent against a meaningful blurb.
			Expect(prompt).To(ContainSubstring("A-Team Swarm"))
			Expect(prompt).To(ContainSubstring("Dev Swarm"))
			Expect(prompt).To(ContainSubstring("Planning Loop"))
			Expect(prompt).To(ContainSubstring("Board Room"))

			// `(swarm)` marker disambiguates kind. Without this, the
			// model can't tell whether `a-team` is an agent or a
			// nested swarm and may invoke the wrong tool semantics.
			Expect(strings.Count(prompt, "(swarm)")).To(BeNumerically(">=", 4),
				"each sub-swarm member must render with `(swarm)` so the model "+
					"knows delegate('a-team', ...) dispatches a whole swarm, not an agent")
		})

		It("renders the coordination namespace under the meta-swarm chain prefix", func() {
			eng := newCoordinatorEngineWithSwarmRegistry(newMetaSwarmRegistry())
			ctx := newMetaSwarmContext()

			eng.SetSwarmContext(&ctx)

			prompt := eng.BuildSystemPrompt()

			Expect(prompt).To(ContainSubstring("meta/coordinator"),
				"meta-swarm uses chain_prefix `meta`; the coord-store namespace "+
					"must reflect that so sub-swarm runs don't collide with a-team / planning-loop / board-room / dev-swarm namespaces")
		})
	})
})

var _ = Describe("Engine swarm-lead Final Output contract", func() {
	It("keeps the main thread and scopes the coord-store write to the publisher handoff", func() {
		eng := newSwarmLeadEngine("senior-engineer", newSwarmTestRegistry())
		ctx := newBugHuntContext()

		eng.SetSwarmContext(&ctx)

		prompt := eng.BuildSystemPrompt()

		Expect(prompt).To(ContainSubstring("bug-hunt/senior-engineer/output"))
		Expect(prompt).To(ContainSubstring("inter-agent handoff for the publisher"))
		Expect(prompt).To(ContainSubstring("keep reporting progress in this thread"))
	})
})

// e2eStreamer drains a single Done chunk and counts invocations.
func e2eStreamer(calls *atomic.Int32) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		if calls != nil {
			calls.Add(1)
		}
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// buildSmokeWorkerEngine constructs a single worker-engine the smoke
// test fans out to.
func buildSmokeWorkerEngine() *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "worker"},
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "worker"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
}

// buildSmokeLeadEngine constructs the lead engine the DelegateTool
// uses as its source-agent.
func buildSmokeLeadEngine() *engine.Engine {
	return engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead",
			Name:              "Lead",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
}

// smokeManifest builds a swarm manifest the e2e test drives end-to-
// end via NewManifestBuilder. Pinned at fast-no-jitter retry so the
// test does not pay wall-clock backoff on the panic / retryable
// edges in adjacent suites.
func smokeManifest(name, gateKind string) *swarm.Manifest {
	m := swarm.NewManifestBuilder("smoke-swarm").
		WithLead("lead").
		WithMember("worker").
		WithGate(name, gateKind, swarm.LifecyclePostMember, "worker").
		Build()
	return &m
}

var _ = Describe("SwarmEndToEndSmoke", func() {
	BeforeEach(func() {
		swarm.ResetExtGateRegistryForTest()
	})

	Context("with an ext:always-pass gate", func() {
		It("dispatches the worker once and the gate runs once on a passing run", func() {
			var gateCalls atomic.Int32
			err := swarm.RegisterExtGateFunc("always-pass", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				gateCalls.Add(1)
				return swarm.ExtGateResponse{Pass: true}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			manifest := smokeManifest("g1", "ext:always-pass")
			reg := swarm.NewRegistry()
			reg.Register(manifest)

			lead := buildSmokeLeadEngine()
			worker := buildSmokeWorkerEngine()
			engines := map[string]*engine.Engine{"lead": lead, "worker": worker}
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			var workerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"worker": e2eStreamer(&workerCalls),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithGateRunner(swarm.NewMultiRunner())

			err = delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, []string{"worker"}, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(workerCalls.Load()).To(Equal(int32(1)))
			Expect(gateCalls.Load()).To(Equal(int32(1)))
		})
	})

	Context("with an ext:always-fail gate", func() {
		It("surfaces *swarm.GateError carrying GateKind ext:always-fail", func() {
			err := swarm.RegisterExtGateFunc("always-fail", func(_ context.Context, _ swarm.ExtGateRequest) (swarm.ExtGateResponse, error) {
				return swarm.ExtGateResponse{Pass: false, Reason: "no go"}, nil
			})
			Expect(err).NotTo(HaveOccurred())

			manifest := smokeManifest("g1", "ext:always-fail")
			reg := swarm.NewRegistry()
			reg.Register(manifest)

			lead := buildSmokeLeadEngine()
			worker := buildSmokeWorkerEngine()
			engines := map[string]*engine.Engine{"lead": lead, "worker": worker}
			swarmCtx := swarm.NewContext(manifest.ID, manifest)
			lead.SetSwarmContext(&swarmCtx)

			streamers := map[string]streaming.Streamer{"worker": e2eStreamer(nil)}
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithGateRunner(swarm.NewMultiRunner())

			dispatchErr := delegateTool.DispatchSwarmMembers(context.Background(), &swarmCtx, []string{"worker"}, "go")

			Expect(dispatchErr).To(HaveOccurred())
			var ge *swarm.GateError
			Expect(errors.As(dispatchErr, &ge)).To(BeTrue())
			Expect(ge.GateKind).To(Equal("ext:always-fail"))
		})
	})
})
