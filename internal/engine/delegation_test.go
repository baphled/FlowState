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
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Delegation", func() {
	var (
		chatProvider *mockProvider
		manifest     agent.Manifest
	)

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-chat-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Delegated response", Done: true},
			},
		}

		manifest = agent.Manifest{
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

				qaManifest := agent.Manifest{
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
			delegateTool := engine.NewDelegateTool(nil, agent.Delegation{}, "source")

			var _ tool.Tool = delegateTool
			Expect(delegateTool.Name()).To(Equal("delegate"))
			Expect(delegateTool.Description()).NotTo(BeEmpty())
		})

		It("returns correct schema with task_type and message parameters", func() {
			delegateTool := engine.NewDelegateTool(nil, agent.Delegation{}, "source")

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

				qaManifest := agent.Manifest{
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

				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
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

				delegateTool := engine.NewDelegateTool(nil, delegation, "source")

				ctx := context.Background()
				input := tool.Input{
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

				delegateTool := engine.NewDelegateTool(nil, delegation, "source")

				ctx := context.Background()
				input := tool.Input{
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

				delegateTool := engine.NewDelegateTool(engines, delegation, "source")

				ctx := context.Background()
				input := tool.Input{
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

		Context("when emitting delegation events", func() {
			var (
				qaEngine   *engine.Engine
				engines    map[string]*engine.Engine
				delegation agent.Delegation
				outChan    chan provider.StreamChunk
			)

			BeforeEach(func() {
				qaProvider := &mockProvider{
					name: "qa-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "delegated output", Done: true},
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
					DelegationTable: map[string]string{
						"testing": "qa-agent",
					},
				}

				outChan = make(chan provider.StreamChunk, 16)
			})

			It("emits started and completed DelegationInfo chunks", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				ctx := engine.WithStreamOutput(context.Background(), outChan)
				startedBefore := time.Now().UTC()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run the tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				completedAfter := time.Now().UTC()
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("delegated output"))

				close(outChan)
				var delegationChunks []provider.StreamChunk
				for chunk := range outChan {
					if chunk.DelegationInfo != nil {
						delegationChunks = append(delegationChunks, chunk)
					}
				}

				Expect(delegationChunks).To(HaveLen(2))

				started := delegationChunks[0].DelegationInfo
				Expect(started.Status).To(Equal("started"))
				Expect(started.SourceAgent).To(Equal("orchestrator"))
				Expect(started.TargetAgent).To(Equal("qa-agent"))
				Expect(started.Description).To(Equal("Run the tests"))
				Expect(started.ChainID).NotTo(BeEmpty())
				Expect(started.ToolCalls).To(Equal(0))
				Expect(started.LastTool).To(BeEmpty())
				Expect(started.StartedAt).NotTo(BeNil())
				Expect(*started.StartedAt).To(BeTemporally(">=", startedBefore))
				Expect(*started.StartedAt).To(BeTemporally("<=", completedAfter))
				Expect(started.CompletedAt).To(BeNil())

				completed := delegationChunks[1].DelegationInfo
				Expect(completed.Status).To(Equal("completed"))
				Expect(completed.SourceAgent).To(Equal("orchestrator"))
				Expect(completed.TargetAgent).To(Equal("qa-agent"))
				Expect(completed.ChainID).To(Equal(started.ChainID))
				Expect(completed.ToolCalls).To(Equal(1))
				Expect(completed.LastTool).To(BeEmpty())
				Expect(completed.StartedAt).NotTo(BeNil())
				Expect(completed.CompletedAt).NotTo(BeNil())
				Expect(*completed.CompletedAt).To(BeTemporally(">=", *started.StartedAt))
			})

			It("emits failed DelegationInfo when target engine stream errors", func() {
				failProvider := &mockProvider{
					name:      "fail-provider",
					streamErr: errors.New("stream broke"),
				}

				failManifest := agent.Manifest{
					ID:                "fail-agent",
					Name:              "Fail Agent",
					Instructions:      agent.Instructions{SystemPrompt: "fail"},
					ContextManagement: agent.DefaultContextManagement(),
				}

				failEngine := engine.New(engine.Config{
					ChatProvider: failProvider,
					Manifest:     failManifest,
				})

				failEngines := map[string]*engine.Engine{
					"qa-agent": failEngine,
				}

				delegateTool := engine.NewDelegateTool(failEngines, delegation, "orchestrator")
				ctx := engine.WithStreamOutput(context.Background(), outChan)
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run the tests",
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())

				close(outChan)
				var delegationChunks []provider.StreamChunk
				for chunk := range outChan {
					if chunk.DelegationInfo != nil {
						delegationChunks = append(delegationChunks, chunk)
					}
				}

				Expect(delegationChunks).To(HaveLen(2))

				started := delegationChunks[0].DelegationInfo
				Expect(started.Status).To(Equal("started"))
				Expect(started.ChainID).NotTo(BeEmpty())
				Expect(started.StartedAt).NotTo(BeNil())
				Expect(started.CompletedAt).To(BeNil())

				failed := delegationChunks[1].DelegationInfo
				Expect(failed.Status).To(Equal("failed"))
				Expect(failed.SourceAgent).To(Equal("orchestrator"))
				Expect(failed.TargetAgent).To(Equal("qa-agent"))
				Expect(failed.ChainID).To(Equal(started.ChainID))
				Expect(failed.ToolCalls).To(Equal(0))
				Expect(failed.LastTool).To(BeEmpty())
				Expect(failed.StartedAt).NotTo(BeNil())
				Expect(failed.CompletedAt).NotTo(BeNil())
			})

			It("works without context output channel", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run the tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("delegated output"))
			})
		})
	})
})
