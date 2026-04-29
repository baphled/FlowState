package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("DelegateTool lifecycle", func() {
	var (
		qaProvider *mockProvider
		qaEngine   *engine.Engine
		engines    map[string]*engine.Engine
		delegation agent.Delegation
	)

	BeforeEach(func() {
		qaProvider = &mockProvider{
			name: "qa-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "lifecycle response", Done: true},
			},
		}

		qaManifest := agent.Manifest{
			ID:                "qa-agent",
			Name:              "QA Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
			ContextManagement: agent.DefaultContextManagement(),
		}

		qaEngine = engine.New(engine.Config{
			ChatProvider: qaProvider,
			Manifest:     qaManifest,
		})

		engines = map[string]*engine.Engine{
			"qa-agent": qaEngine,
		}

		delegation = agent.Delegation{
			CanDelegate: true,
		}
	})

	Describe("Gap 2: resolveOrCreateSession", func() {
		var mgr *session.Manager

		BeforeEach(func() {
			mgr = session.NewManager(nil)
		})

		Context("when session_id refers to an existing session", func() {
			It("reuses the existing session ID rather than creating a new one", func() {
				existing, err := mgr.CreateSession("qa-agent")
				Expect(err).NotTo(HaveOccurred())

				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
					WithSessionManager(mgr)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "qa-agent",
						"message":       "Resume the task",
						"session_id":    existing.ID,
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				Expect(result.Metadata).NotTo(BeNil())
				Expect(result.Metadata["sessionId"]).To(Equal(existing.ID))
			})
		})

		Context("when session_id is empty", func() {
			It("creates a new session and returns its ID in metadata", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
					WithSessionManager(mgr)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "qa-agent",
						"message":       "New task",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				Expect(result.Metadata).NotTo(BeNil())
				sessionID, ok := result.Metadata["sessionId"].(string)
				Expect(ok).To(BeTrue())
				Expect(sessionID).NotTo(BeEmpty())
			})
		})

		Context("when session_id refers to a non-existent session", func() {
			It("falls back to creating a new session", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
					WithSessionManager(mgr)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "qa-agent",
						"message":       "Task with stale session ID",
						"session_id":    "non-existent-session-id",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				Expect(result.Metadata).NotTo(BeNil())
				sessionID, ok := result.Metadata["sessionId"].(string)
				Expect(ok).To(BeTrue())
				Expect(sessionID).NotTo(Equal("non-existent-session-id"))
				Expect(sessionID).NotTo(BeEmpty())
			})
		})
	})

	Describe("Gap 3: agentHasToolPermission", func() {
		Context("when registry is not set", func() {
			It("allows all tools (permissive default)", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				Expect(delegateTool.AgentHasToolPermission("qa-agent", "delegate")).To(BeTrue())
				Expect(delegateTool.AgentHasToolPermission("qa-agent", "todowrite")).To(BeTrue())
			})
		})

		Context("when registry is set", func() {
			var reg *agent.Registry

			BeforeEach(func() {
				reg = agent.NewRegistry()
			})

			Context("when the agent has an empty tools list", func() {
				It("denies all tools (fail-closed)", func() {
					reg.Register(&agent.Manifest{
						ID:   "qa-agent",
						Name: "QA Agent",
						Capabilities: agent.Capabilities{
							Tools: []string{},
						},
					})

					delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
						WithRegistry(reg)

					Expect(delegateTool.AgentHasToolPermission("qa-agent", "delegate")).To(BeFalse())
					Expect(delegateTool.AgentHasToolPermission("qa-agent", "bash")).To(BeFalse())
				})
			})

			Context("when the agent lists specific tools", func() {
				It("returns true for a listed tool", func() {
					reg.Register(&agent.Manifest{
						ID:   "qa-agent",
						Name: "QA Agent",
						Capabilities: agent.Capabilities{
							Tools: []string{"bash", "delegate"},
						},
					})

					delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
						WithRegistry(reg)

					Expect(delegateTool.AgentHasToolPermission("qa-agent", "delegate")).To(BeTrue())
				})

				It("returns false for a tool not in the list", func() {
					reg.Register(&agent.Manifest{
						ID:   "qa-agent",
						Name: "QA Agent",
						Capabilities: agent.Capabilities{
							Tools: []string{"bash"},
						},
					})

					delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
						WithRegistry(reg)

					Expect(delegateTool.AgentHasToolPermission("qa-agent", "delegate")).To(BeFalse())
				})
			})

			Context("when the agent is not found in the registry", func() {
				It("allows all tools (permissive default for unknown agents)", func() {
					delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
						WithRegistry(reg)

					Expect(delegateTool.AgentHasToolPermission("unknown-agent", "delegate")).To(BeTrue())
				})
			})
		})
	})

	Describe("Gap 4: formatDelegationOutput and enriched Result", func() {
		Describe("formatDelegationOutput", func() {
			It("wraps the response in a task_result block", func() {
				output := engine.FormatDelegationOutput("the agent response")

				Expect(output).To(ContainSubstring("<task_result>"))
				Expect(output).To(ContainSubstring("the agent response"))
				Expect(output).To(ContainSubstring("</task_result>"))
			})

			It("does not include the misleading task_id header that the lead used to misread as a background-task id", func() {
				output := engine.FormatDelegationOutput("response text")

				Expect(output).NotTo(ContainSubstring("task_id:"),
					"sync delegate output must not advertise a task_id; the lead conflated it with background_output's id namespace and produced 'task not found' cascades")
				Expect(output).NotTo(ContainSubstring("for resuming to continue this task if needed"))
			})

			It("emits exactly the task_result block and nothing else", func() {
				output := engine.FormatDelegationOutput("my response")

				Expect(output).To(Equal("<task_result>\nmy response\n</task_result>"))
			})
		})

		Describe("executeSync returns enriched Result", func() {
			It("returns Title set to the delegation message", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "qa-agent",
						"message":       "Run all the tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Title).To(Equal("Run all the tests"))
			})

			It("returns Metadata with sessionId, model, and provider keys", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "qa-agent",
						"message":       "Run all the tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Metadata).NotTo(BeNil())
				Expect(result.Metadata).To(HaveKey("sessionId"))
				Expect(result.Metadata).To(HaveKey("model"))
				Expect(result.Metadata).To(HaveKey("provider"))
			})

			It("wraps the output in a task_result block without a misleading task_id header", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "qa-agent",
						"message":       "Run all the tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(ContainSubstring("task_id:"),
					"sync delegate output must not include task_id (lead misread it as a background-task id)")
				Expect(result.Output).To(ContainSubstring("<task_result>"))
				Expect(result.Output).To(ContainSubstring("lifecycle response"))
				Expect(result.Output).To(ContainSubstring("</task_result>"))
			})
		})

		Describe("executeAsync returns enriched Result", func() {
			It("returns Title and Metadata with sessionId for background tasks", func() {
				bgManager := engine.NewBackgroundTaskManager()
				delegateTool := engine.NewDelegateToolWithBackground(
					engines, delegation, "orchestrator", bgManager, nil,
				)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type":     "qa-agent",
						"message":           "Background task",
						"run_in_background": true,
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Title).To(Equal("Background task"))
				Expect(result.Metadata).NotTo(BeNil())
				Expect(result.Metadata).To(HaveKey("sessionId"))
			})
		})
	})
})
