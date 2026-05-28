package engine_test

import (
	"context"
	"errors"
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

func validVerdictPayload() []byte {
	return []byte(`{"verdict":"approve"}`)
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

	It("halts the delegation with a GateError when the reviewer writes a malformed verdict", func() {
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
		Expect(gateErr.Reason).To(ContainSubstring("schema validation failed"))
	})

	It("returns the delegation result unchanged when the reviewer writes a valid verdict", func() {
		store := coordination.NewMemoryStore()
		Expect(store.Set("planning/plan-reviewer/review", []byte(`{"verdict":"approve"}`))).To(Succeed())

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

		vaultPath := filepath.Join(outputDir, "readyz-plan.md")
		Expect(vaultPath).To(BeAnExistingFile(),
			"the plan must be written to a real file in the vault")
		body, readErr := os.ReadFile(vaultPath)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("# Readyz Plan"))
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
