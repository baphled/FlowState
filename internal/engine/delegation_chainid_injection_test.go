package engine_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	delegationpkg "github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// These specs pin the engine-level chainID auto-injection contract: the
// delegate tool accepts a top-level chainID parameter; when supplied
// (or surfaced via handoff.chain_id) the dispatched specialist's user
// message is auto-prefixed with a structured preamble carrying the
// chainID and, for the five known coord-store specialists, the
// canonical target key.
//
// Backwards compatibility is part of the contract: callers that omit
// chainID see no injection, and callers that already embed
// chainID=<value> in their free-form message get no duplication.
//
// The specialist's provider sees the composed user message via
// mockProvider.capturedRequest.Messages — verifying at that boundary
// proves the engine, not the prompt, owns the injection.
var _ = Describe("DelegateTool chainID auto-injection", func() {
	var (
		specialistProvider *mockProvider
		specialistManifest agent.Manifest
		specialistEngine   *engine.Engine
		engines            map[string]*engine.Engine
		delegateTool       *engine.DelegateTool
	)

	buildSpecialist := func(agentID string) {
		specialistProvider = &mockProvider{
			name: agentID + "-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "ok", Done: true},
			},
		}
		specialistManifest = agent.Manifest{
			ID:                agentID,
			Name:              agentID,
			Instructions:      agent.Instructions{SystemPrompt: "spec"},
			ContextManagement: agent.DefaultContextManagement(),
		}
		specialistEngine = engine.New(engine.Config{
			ChatProvider: specialistProvider,
			Manifest:     specialistManifest,
		})
		engines = map[string]*engine.Engine{agentID: specialistEngine}
		delegateTool = engine.NewDelegateTool(
			engines,
			agent.Delegation{CanDelegate: true},
			"planner",
		)
	}

	lastUserMessage := func() string {
		Expect(specialistProvider.capturedRequest).NotTo(BeNil(),
			"specialist provider must have received a request")
		for i := len(specialistProvider.capturedRequest.Messages) - 1; i >= 0; i-- {
			msg := specialistProvider.capturedRequest.Messages[i]
			if msg.Role == "user" {
				return msg.Content
			}
		}
		Fail("no user message captured by specialist provider")
		return ""
	}

	Describe("Schema", func() {
		It("declares the new chainID property", func() {
			delegateTool = engine.NewDelegateTool(nil, agent.Delegation{}, "source")
			schema := delegateTool.Schema()
			Expect(schema.Properties).To(HaveKey("chainID"),
				"the engine-level handoff requires a top-level chainID property "+
					"so callers do not have to thread it through free-form text")
		})
	})

	Describe("when the top-level chainID parameter is supplied", func() {
		It("auto-prefixes the specialist user message with a structured preamble", func() {
			buildSpecialist("explorer")

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"chainID":       "plan-auth-2026-04-23",
					"message":       "Explore the auth module.",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMessage()
			Expect(body).To(ContainSubstring("chainID=plan-auth-2026-04-23"),
				"the chainID preamble must surface verbatim in the specialist's user message")
			Expect(body).To(ContainSubstring("Explore the auth module."),
				"the original caller-supplied message must remain after the preamble")
		})

		DescribeTable("auto-injects the canonical coord_store key for known specialists",
			func(agentID, expectedSuffix string) {
				buildSpecialist(agentID)

				_, err := delegateTool.Execute(context.Background(), tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": agentID,
						"chainID":       "plan-x-2026",
						"message":       "Do the thing.",
					},
				})
				Expect(err).NotTo(HaveOccurred())

				body := lastUserMessage()
				expectedKey := "coordination_store key=plan-x-2026/" + expectedSuffix
				Expect(body).To(ContainSubstring(expectedKey),
					"the role-specific coord-store key must be present in the preamble for "+agentID)
			},
			Entry("explorer", "explorer", "codebase-findings"),
			Entry("librarian", "librarian", "external-refs"),
			Entry("analyst", "analyst", "analysis"),
			Entry("plan-writer", "plan-writer", "plan"),
			Entry("plan-reviewer", "plan-reviewer", "review"),
		)

		It("omits the coord_store key line for agents outside the known set", func() {
			buildSpecialist("custom-helper")

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "custom-helper",
					"chainID":       "plan-y-2026",
					"message":       "Do the thing.",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMessage()
			Expect(body).To(ContainSubstring("chainID=plan-y-2026"),
				"unknown agents still receive the chainID preamble")
			Expect(body).NotTo(ContainSubstring("coordination_store key="),
				"unknown agents must NOT receive a synthetic coord_store key — "+
					"custom delegations stay free-form")
		})

		It("is idempotent when the message already embeds chainID=<value>", func() {
			buildSpecialist("explorer")

			original := "chainID=plan-z-2026. Explore the network module."
			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"chainID":       "plan-z-2026",
					"message":       original,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMessage()
			Expect(strings.Count(body, "chainID=plan-z-2026")).To(Equal(1),
				"the injector must detect the existing chainID substring and leave the message unchanged")
		})

		It("rejects a non-string chainID value", func() {
			buildSpecialist("explorer")

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"chainID":       42,
					"message":       "Explore something.",
				},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("chainID"),
				"the type-error sentinel must mention the chainID parameter")
		})
	})

	Describe("when only handoff.chain_id is supplied", func() {
		It("auto-injects the same preamble as the top-level parameter", func() {
			buildSpecialist("plan-writer")

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "plan-writer",
					"message":       "Write the plan.",
					"handoff": map[string]interface{}{
						"chain_id": "plan-h-2026",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMessage()
			Expect(body).To(ContainSubstring("chainID=plan-h-2026"))
			Expect(body).To(ContainSubstring("coordination_store key=plan-h-2026/plan"))
		})
	})

	Describe("when no chainID is supplied (backwards compatibility)", func() {
		It("does not inject any preamble", func() {
			buildSpecialist("custom-helper")

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "custom-helper",
					"message":       "Plain task.",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMessage()
			Expect(body).NotTo(ContainSubstring("chainID="),
				"the auto-generated fallback chainID must stay internal — "+
					"never injected into the message when the caller did not supply one")
			Expect(body).To(Equal("Plain task."),
				"the message must be passed through verbatim")
		})
	})

	Describe("DelegationInfo propagation", func() {
		It("populates the chainID on the DelegationInfo with the caller-supplied value", func() {
			buildSpecialist("explorer")

			outChan := make(chan provider.StreamChunk, 16)
			ctx := engine.WithStreamOutput(context.Background(), outChan)

			_, err := delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"chainID":       "plan-info-2026",
					"message":       "Investigate.",
				},
			})
			Expect(err).NotTo(HaveOccurred())
			close(outChan)

			seen := false
			for chunk := range outChan {
				if chunk.DelegationInfo != nil && chunk.DelegationInfo.ChainID == "plan-info-2026" {
					seen = true
					break
				}
			}
			Expect(seen).To(BeTrue(),
				"DelegationInfo events must carry the caller-supplied chainID, "+
					"not a freshly generated fallback — downstream consumers (event "+
					"bus, RejectionTracker) need the planner's namespace to match the "+
					"coord-store prefix")
		})

		It("overwrites handoff.ChainID on the resolved target with the caller-supplied value", func() {
			// Setting both handoff.chain_id AND a top-level chainID; the
			// top-level value must win (precedence rule). We assert the
			// effect indirectly by observing the preamble — the injected
			// chainID must equal the top-level value, not the handoff value.
			buildSpecialist("explorer")

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"chainID":       "top-level-wins",
					"message":       "Investigate.",
					"handoff": map[string]interface{}{
						"chain_id": "handoff-loses",
					},
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMessage()
			Expect(body).To(ContainSubstring("chainID=top-level-wins"))
			Expect(body).NotTo(ContainSubstring("handoff-loses"))
		})
	})

	// Compile-time touch on the delegationpkg import so the spec block
	// stays self-documenting about the handoff package the helper reads.
	var _ = delegationpkg.Handoff{}
})

// swarmPreambleFixture wires a DelegateTool inside an active swarm so that
// buildMemberSwarmPreamble can resolve the gate definitions from the registry.
func swarmPreambleFixture(agentID string, gates []swarm.GateSpec) (*engine.DelegateTool, *mockProvider) {
	mp := &mockProvider{
		name:         agentID + "-provider",
		streamChunks: []provider.StreamChunk{{Content: "ok", Done: true}},
	}
	manifest := agent.Manifest{
		ID:                agentID,
		Name:              agentID,
		Instructions:      agent.Instructions{SystemPrompt: "spec"},
		ContextManagement: agent.DefaultContextManagement(),
	}
	eng := engine.New(engine.Config{
		ChatProvider: mp,
		Manifest:     manifest,
	})

	swarmManifest := &swarm.Manifest{}
	swarmManifest.ID = "dev-feature"
	swarmManifest.Context.ChainPrefix = "dev-feature"
	swarmManifest.Harness.Gates = gates

	reg := swarm.NewRegistry()
	reg.Register(swarmManifest)

	ctx := swarm.NewContext("dev-feature", swarmManifest)
	eng.SetSwarmContext(&ctx)

	dt := engine.NewDelegateTool(
		map[string]*engine.Engine{agentID: eng},
		agent.Delegation{CanDelegate: true},
		"Tech-Lead",
	).WithOwnerEngine(eng).WithSwarmRegistry(reg)

	return dt, mp
}

var _ = Describe("DelegateTool swarm-aware preamble injection", func() {
	lastUserMsg := func(mp *mockProvider) string {
		Expect(mp.capturedRequest).NotTo(BeNil())
		for i := len(mp.capturedRequest.Messages) - 1; i >= 0; i-- {
			msg := mp.capturedRequest.Messages[i]
			if msg.Role == "user" {
				return msg.Content
			}
		}
		Fail("no user message captured")
		return ""
	}

	Describe("when a swarm is active and the member has a post-member schema gate", func() {
		It("injects the swarm ID, coord-store key, and schema name into the delegation message", func() {
			gates := []swarm.GateSpec{
				{
					Name:      "post-explorer-codebase",
					Kind:      "builtin:result-schema",
					When:      swarm.LifecyclePostMember,
					Target:    "explorer",
					OutputKey: "codebase-findings",
					SchemaRef: "evidence-bundle-v1",
				},
			}
			dt, mp := swarmPreambleFixture("explorer", gates)

			_, err := dt.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"chainID":       "dev-feature",
					"message":       "Survey the codebase.",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMsg(mp)
			Expect(body).To(ContainSubstring("**dev-feature** swarm"),
				"member must know which swarm it belongs to")
			Expect(body).To(ContainSubstring("dev-feature/explorer/codebase-findings"),
				"member must know the exact coord-store key it must write")
			Expect(body).To(ContainSubstring("evidence-bundle-v1"),
				"member must know the schema it must conform to")
			Expect(body).To(ContainSubstring("Survey the codebase."),
				"original message must be preserved after the preamble")
		})

		It("uses the swarm chain_prefix in the coord-store key, not the swarm ID", func() {
			gates := []swarm.GateSpec{
				{
					Name:      "post-analyst-requirements",
					Kind:      "builtin:result-schema",
					When:      swarm.LifecyclePostMember,
					Target:    "analyst",
					OutputKey: "requirements",
					SchemaRef: "feature-requirements-v1",
				},
			}
			dt, mp := swarmPreambleFixture("analyst", gates)

			_, err := dt.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "analyst",
					"chainID":       "dev-feature",
					"message":       "Analyse the feature.",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMsg(mp)
			// chain_prefix is "dev-feature" so the key must be dev-feature/analyst/requirements
			Expect(body).To(ContainSubstring("dev-feature/analyst/requirements"))
		})

		It("is idempotent when the message already contains chainID=", func() {
			gates := []swarm.GateSpec{
				{
					Name:      "post-explorer-codebase",
					Kind:      "builtin:result-schema",
					When:      swarm.LifecyclePostMember,
					Target:    "explorer",
					OutputKey: "codebase-findings",
					SchemaRef: "evidence-bundle-v1",
				},
			}
			dt, mp := swarmPreambleFixture("explorer", gates)

			_, err := dt.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"chainID":       "dev-feature",
					"message":       "chainID=dev-feature. Survey the repo.",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMsg(mp)
			Expect(strings.Count(body, "chainID=dev-feature")).To(Equal(1),
				"preamble must not be injected when message already contains the marker")
		})
	})

	Describe("when the member has no post-member schema gate", func() {
		It("falls back to the basic chainID preamble (no swarm-specific content)", func() {
			// No gates defined for this member.
			dt, mp := swarmPreambleFixture("custom-helper", []swarm.GateSpec{})

			_, err := dt.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "custom-helper",
					"chainID":       "dev-feature",
					"message":       "Do something custom.",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			body := lastUserMsg(mp)
			Expect(body).To(ContainSubstring("chainID=dev-feature"),
				"basic chainID preamble must still be injected as fallback")
			Expect(body).NotTo(ContainSubstring("swarm"),
				"no swarm-specific language when the member has no gate")
		})
	})
})
