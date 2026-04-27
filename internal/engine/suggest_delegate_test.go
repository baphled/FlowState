package engine_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// Phase 12 — suggest_delegate is a read-only escape hatch offered to agents
// with can_delegate:false. When the user's prompt references @<agent> and the
// current agent cannot delegate directly, the model can call suggest_delegate
// to return a structured payload the UI renders as a "switch agent?" prompt.
// The tool never performs delegation itself.
var _ = Describe("SuggestDelegateTool", func() {
	var (
		registry *agent.Registry
		tl       *engine.SuggestDelegateTool
		ctx      context.Context
	)

	BeforeEach(func() {
		registry = agent.NewRegistry()
		// A delegating agent — the suggested switch target.
		registry.Register(&agent.Manifest{
			ID:   "router",
			Name: "Router",
			Delegation: agent.Delegation{
				CanDelegate: true,
			},
		})
		// A specialist target the user asked for.
		registry.Register(&agent.Manifest{
			ID:   "team-lead",
			Name: "Team Lead",
			Delegation: agent.Delegation{
				CanDelegate: true,
			},
		})
		// The current non-delegating agent issuing the suggestion.
		registry.Register(&agent.Manifest{
			ID:   "executor",
			Name: "Executor",
			Delegation: agent.Delegation{
				CanDelegate: false,
			},
		})

		tl = engine.NewSuggestDelegateTool(registry, "executor")
		ctx = context.Background()
	})

	Describe("Name", func() {
		It("returns 'suggest_delegate'", func() {
			Expect(tl.Name()).To(Equal("suggest_delegate"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description that mentions switching", func() {
			Expect(tl.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("requires target_agent and reason", func() {
			schema := tl.Schema()
			Expect(schema.Required).To(ContainElement("target_agent"))
			Expect(schema.Required).To(ContainElement("reason"))
		})

		It("exposes target_agent as a string property", func() {
			schema := tl.Schema()
			Expect(schema.Properties).To(HaveKey("target_agent"))
			Expect(schema.Properties["target_agent"].Type).To(Equal("string"))
		})

		It("exposes reason as a string property", func() {
			schema := tl.Schema()
			Expect(schema.Properties).To(HaveKey("reason"))
			Expect(schema.Properties["reason"].Type).To(Equal("string"))
		})
	})

	Describe("Execute", func() {
		Context("when the target agent exists and a delegating agent is configured", func() {
			It("returns a structured switch-agent payload without delegating", func() {
				result, err := tl.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "team-lead",
						"reason":       "needs sprint planning expertise",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())

				var payload map[string]interface{}
				Expect(json.Unmarshal([]byte(result.Output), &payload)).To(Succeed())

				Expect(payload["suggestion"]).To(Equal("switch_agent"))
				Expect(payload["from_agent"]).To(Equal("executor"))
				Expect(payload["to_agent"]).To(Equal("router"))
				Expect(payload["target_agent"]).To(Equal("team-lead"))
				Expect(payload["reason"]).To(Equal("needs sprint planning expertise"))
				Expect(payload["user_prompt"]).To(BeAssignableToTypeOf(""))
				Expect(payload["user_prompt"].(string)).To(ContainSubstring("router"))
				Expect(payload["user_prompt"].(string)).To(ContainSubstring("team-lead"))
			})

			It("resolves the target by alias when no direct ID match exists", func() {
				registry.Register(&agent.Manifest{
					ID:      "planner",
					Name:    "Planner",
					Aliases: []string{"plan-writer"},
					Delegation: agent.Delegation{
						CanDelegate: false,
					},
				})

				result, err := tl.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "plan-writer",
						"reason":       "requires planning",
					},
				})
				Expect(err).NotTo(HaveOccurred())

				var payload map[string]interface{}
				Expect(json.Unmarshal([]byte(result.Output), &payload)).To(Succeed())
				Expect(payload["target_agent"]).To(Equal("planner"))
			})
		})

		Context("when arguments are invalid", func() {
			It("errors when target_agent is missing", func() {
				_, err := tl.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"reason": "needs planning",
					},
				})
				Expect(err).To(HaveOccurred())
			})

			It("errors when reason is missing", func() {
				_, err := tl.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "team-lead",
					},
				})
				Expect(err).To(HaveOccurred())
			})

			It("errors when target_agent is not a string", func() {
				_, err := tl.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": 42,
						"reason":       "foo",
					},
				})
				Expect(err).To(HaveOccurred())
			})
		})

		Context("when the target agent does not exist", func() {
			It("returns an error identifying the unknown target", func() {
				_, err := tl.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "ghost",
						"reason":       "wants planning",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ghost"))
			})
		})

		Context("when no delegating agent is registered", func() {
			It("returns an error explaining delegation is not configured", func() {
				emptyReg := agent.NewRegistry()
				emptyReg.Register(&agent.Manifest{
					ID:   "executor",
					Name: "Executor",
					Delegation: agent.Delegation{
						CanDelegate: false,
					},
				})
				emptyReg.Register(&agent.Manifest{
					ID:   "team-lead",
					Name: "Team Lead",
					Delegation: agent.Delegation{
						CanDelegate: false, // deliberately false — no routers present
					},
				})
				emptyTool := engine.NewSuggestDelegateTool(emptyReg, "executor")

				_, err := emptyTool.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "team-lead",
						"reason":       "needs planning",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("delegat"))
			})
		})

		Context("when multiple delegating agents are configured", func() {
			It("prefers an agent named 'router' or 'orchestrator' when present", func() {
				reg := agent.NewRegistry()
				reg.Register(&agent.Manifest{
					ID:         "alpha",
					Name:       "Alpha",
					Delegation: agent.Delegation{CanDelegate: true},
				})
				reg.Register(&agent.Manifest{
					ID:         "orchestrator",
					Name:       "Orchestrator",
					Delegation: agent.Delegation{CanDelegate: true},
				})
				reg.Register(&agent.Manifest{
					ID:         "zulu",
					Name:       "Zulu",
					Delegation: agent.Delegation{CanDelegate: true},
				})
				reg.Register(&agent.Manifest{
					ID:         "executor",
					Name:       "Executor",
					Delegation: agent.Delegation{CanDelegate: false},
				})
				reg.Register(&agent.Manifest{
					ID:         "team-lead",
					Name:       "Team Lead",
					Delegation: agent.Delegation{CanDelegate: true},
				})

				preferTool := engine.NewSuggestDelegateTool(reg, "executor")
				result, err := preferTool.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "team-lead",
						"reason":       "planning",
					},
				})
				Expect(err).NotTo(HaveOccurred())

				var payload map[string]interface{}
				Expect(json.Unmarshal([]byte(result.Output), &payload)).To(Succeed())
				Expect(payload["to_agent"]).To(Equal("orchestrator"))
			})
		})
	})

	Describe("swarm-id targets", func() {
		var swarmReg *swarm.Registry

		BeforeEach(func() {
			swarmReg = newSwarmRegistryWithBugHunt(registry)
			tl = engine.NewSuggestDelegateToolWithSwarms(registry, swarmReg, "executor")
		})

		It("falls through to the swarm registry when target is a swarm id", func() {
			result, err := tl.Execute(ctx, tool.Input{
				Arguments: map[string]interface{}{
					"target_agent": "bug-hunt",
					"reason":       "user mentioned bug-hunt swarm",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			payload := decodeSuggestPayload(result)
			Expect(payload["target_swarm"]).To(Equal("bug-hunt"))
			Expect(payload["target_kind"]).To(Equal("swarm"))
			Expect(payload["target_lead"]).To(Equal("router"))
			Expect(payload["user_prompt"]).To(ContainSubstring("@bug-hunt"))
		})

		It("returns target_kind=agent for plain agent targets", func() {
			result, err := tl.Execute(ctx, tool.Input{
				Arguments: map[string]interface{}{
					"target_agent": "team-lead",
					"reason":       "specialist needed",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			payload := decodeSuggestPayload(result)
			Expect(payload["target_kind"]).To(Equal("agent"))
			Expect(payload).NotTo(HaveKey("target_swarm"))
		})

		It("errors when target matches neither registry", func() {
			_, err := tl.Execute(ctx, tool.Input{
				Arguments: map[string]interface{}{
					"target_agent": "no-such-thing",
					"reason":       "anything",
				},
			})

			Expect(err).To(MatchError(ContainSubstring(`"no-such-thing"`)))
			Expect(err.Error()).To(SatisfyAny(
				ContainSubstring("agent or swarm not found"),
				ContainSubstring("not found"),
			))
		})

		Context("when the calling agent is the lead of the target swarm", func() {
			It("errors with a self-dispatch refusal so the model does not echo a confirmation prompt to the user", func() {
				leadTool := engine.NewSuggestDelegateToolWithSwarms(registry, swarmReg, "router")

				_, err := leadTool.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "bug-hunt",
						"reason":       "double-checking the dispatch",
					},
				})

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("already leading this swarm"))
			})

			It("honours SetSourceAgentID so an engine-level manifest swap propagates the lead-self check at runtime", func() {
				tl.SetSourceAgentID("router")

				_, err := tl.Execute(ctx, tool.Input{
					Arguments: map[string]interface{}{
						"target_agent": "bug-hunt",
						"reason":       "post-switch sanity check",
					},
				})

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("already leading this swarm"))
			})
		})
	})
})

func newSwarmRegistryWithBugHunt(agentReg *agent.Registry) *swarm.Registry {
	_ = agentReg
	reg := swarm.NewRegistry()
	reg.Register(&swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "bug-hunt",
		Lead:          "router",
		Members:       []string{"team-lead"},
	})
	return reg
}

func decodeSuggestPayload(result tool.Result) map[string]interface{} {
	var payload map[string]interface{}
	Expect(json.Unmarshal([]byte(result.Output), &payload)).To(Succeed())
	return payload
}
