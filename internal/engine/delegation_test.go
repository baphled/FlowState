package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Delegation", func() {
	var (
		chatProvider *mockProvider
		manifest     agent.AgentManifest
	)

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-chat-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Delegated response", Done: true},
			},
		}

		manifest = agent.AgentManifest{
			ID:   "orchestrator-agent",
			Name: "Orchestrator Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are an orchestrator.",
			},
			Delegation: agent.Delegation{
				CanDelegate: true,
				DelegationTable: map[string]string{
					"testing": "qa-agent",
					"coding":  "senior-engineer",
				},
			},
			ContextManagement: agent.DefaultContextManagement(),
		}
	})

	Describe("DelegateToAgent", func() {
		Context("when delegation is enabled", func() {
			It("routes to correct agent based on task type", func() {
				qaProvider := &mockProvider{
					name: "qa-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "QA response", Done: true},
					},
				}

				qaManifest := agent.AgentManifest{
					ID:                "qa-agent",
					Name:              "QA Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
					ContextManagement: agent.DefaultContextManagement(),
				}

				qaEngine := engine.New(engine.Config{
					ChatProvider: qaProvider,
					Manifest:     qaManifest,
				})

				engines := map[string]*engine.Engine{
					"qa-agent": qaEngine,
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.Background()
				chunks, err := eng.DelegateToAgent(ctx, engines, "testing", "Run the tests")

				Expect(err).NotTo(HaveOccurred())
				Expect(chunks).NotTo(BeNil())

				var received []provider.StreamChunk
				for chunk := range chunks {
					received = append(received, chunk)
				}

				Expect(received).To(HaveLen(1))
				Expect(received[0].Content).To(Equal("QA response"))
			})
		})

		Context("when CanDelegate is false", func() {
			It("returns an error", func() {
				manifest.Delegation.CanDelegate = false

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.Background()
				_, err := eng.DelegateToAgent(ctx, nil, "testing", "Run the tests")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("delegation not allowed"))
			})
		})

		Context("when task_type not in delegation table", func() {
			It("returns an error", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.Background()
				_, err := eng.DelegateToAgent(ctx, nil, "unknown-task", "Do something")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no agent configured for task type"))
			})
		})

		Context("when target agent engine not available", func() {
			It("returns an error", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				engines := map[string]*engine.Engine{}

				ctx := context.Background()
				_, err := eng.DelegateToAgent(ctx, engines, "testing", "Run the tests")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("target agent engine not available"))
			})
		})
	})

	Describe("DelegateTool", func() {
		It("implements Tool interface", func() {
			delegateTool := engine.NewDelegateTool(nil, agent.Delegation{})

			var _ tool.Tool = delegateTool
			Expect(delegateTool.Name()).To(Equal("delegate"))
			Expect(delegateTool.Description()).NotTo(BeEmpty())
		})

		It("returns correct schema with task_type and message parameters", func() {
			delegateTool := engine.NewDelegateTool(nil, agent.Delegation{})

			schema := delegateTool.Schema()

			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Properties).To(HaveKey("task_type"))
			Expect(schema.Properties).To(HaveKey("message"))
			Expect(schema.Required).To(ConsistOf("task_type", "message"))
		})

		Context("when executing", func() {
			It("dispatches via delegation table", func() {
				qaProvider := &mockProvider{
					name: "qa-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "Tool delegated response", Done: true},
					},
				}

				qaManifest := agent.AgentManifest{
					ID:                "qa-agent",
					Name:              "QA Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
					ContextManagement: agent.DefaultContextManagement(),
				}

				qaEngine := engine.New(engine.Config{
					ChatProvider: qaProvider,
					Manifest:     qaManifest,
				})

				engines := map[string]*engine.Engine{
					"qa-agent": qaEngine,
				}

				delegation := agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"testing": "qa-agent",
					},
				}

				delegateTool := engine.NewDelegateTool(engines, delegation)

				ctx := context.Background()
				input := tool.ToolInput{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run the unit tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("Tool delegated response"))
				Expect(result.Error).ToNot(HaveOccurred())
			})

			It("returns error when delegation not allowed", func() {
				delegation := agent.Delegation{
					CanDelegate: false,
				}

				delegateTool := engine.NewDelegateTool(nil, delegation)

				ctx := context.Background()
				input := tool.ToolInput{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run tests",
					},
				}

				_, err := delegateTool.Execute(ctx, input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("delegation not allowed"))
			})

			It("returns error when task_type not in table", func() {
				delegation := agent.Delegation{
					CanDelegate:     true,
					DelegationTable: map[string]string{},
				}

				delegateTool := engine.NewDelegateTool(nil, delegation)

				ctx := context.Background()
				input := tool.ToolInput{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "unknown",
						"message":   "Do something",
					},
				}

				_, err := delegateTool.Execute(ctx, input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no agent configured for task type"))
			})

			It("returns error when target engine not available", func() {
				engines := map[string]*engine.Engine{}

				delegation := agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"testing": "qa-agent",
					},
				}

				delegateTool := engine.NewDelegateTool(engines, delegation)

				ctx := context.Background()
				input := tool.ToolInput{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run tests",
					},
				}

				_, err := delegateTool.Execute(ctx, input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("target agent engine not available"))
			})
		})
	})
})
