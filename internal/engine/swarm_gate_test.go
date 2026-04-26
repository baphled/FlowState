package engine_test

import (
	"context"
	"errors"

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
})
