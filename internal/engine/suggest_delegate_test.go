package engine_test

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
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
})
