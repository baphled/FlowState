package engine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	delegationpkg "github.com/baphled/flowstate/internal/delegation"
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

		It("returns correct schema with subagent_type and message as required parameters", func() {
			delegateTool := engine.NewDelegateTool(nil, agent.Delegation{}, "source")

			schema := delegateTool.Schema()

			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Properties).To(HaveKey("task_type"))
			Expect(schema.Properties).To(HaveKey("message"))
			Expect(schema.Required).To(ConsistOf("subagent_type", "message"))
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

		Describe("DelegateTool async mode", func() {
			var (
				qaProvider *mockProvider
				qaEngine   *engine.Engine
				engines    map[string]*engine.Engine
				bgManager  *engine.BackgroundTaskManager
				delegation agent.Delegation
			)

			BeforeEach(func() {
				bgManager = engine.NewBackgroundTaskManager()

				qaProvider = &mockProvider{
					name: "qa-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "async response", Done: true},
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
			})

			It("returns task ID immediately when run_in_background is true", func() {
				delegateTool := engine.NewDelegateToolWithBackground(
					engines, delegation, "orchestrator", bgManager, nil,
				)
				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type":         "testing",
						"message":           "Run tests async",
						"run_in_background": true,
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(MatchRegexp(`"task_id":\s*"task-qa-agent-\d+"`))
				Expect(result.Output).To(ContainSubstring(`"status": "running"`))
			})

			It("tracks background task in manager", func() {
				delegateTool := engine.NewDelegateToolWithBackground(
					engines, delegation, "orchestrator", bgManager, nil,
				)
				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type":         "testing",
						"message":           "Run tests async",
						"run_in_background": true,
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() int {
					return bgManager.ActiveCount()
				}).Should(BeNumerically(">=", 1))

				tasks := bgManager.List()
				Expect(tasks).To(HaveLen(1))
				Expect(tasks[0].AgentID).To(Equal("qa-agent"))

				Eventually(func() string {
					return bgManager.List()[0].Status.Load()
				}).Should(Equal("completed"))
			})

			It("updates task status to completed when done", func() {
				delegateTool := engine.NewDelegateToolWithBackground(
					engines, delegation, "orchestrator", bgManager, nil,
				)
				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type":         "testing",
						"message":           "Run tests async",
						"run_in_background": true,
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring(`"status": "running"`))

				time.Sleep(100 * time.Millisecond)

				tasks := bgManager.List()
				Expect(tasks).To(HaveLen(1))
				Expect(tasks[0].Status.Load()).To(Equal("completed"))
				Expect(tasks[0].Result).To(ContainSubstring("async response"))
			})

			It("returns error when background mode disabled", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type":         "testing",
						"message":           "Run tests async",
						"run_in_background": true,
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("background mode disabled"))
			})

			It("supports handoff parameter with ChainID", func() {
				delegateTool := engine.NewDelegateToolWithBackground(
					engines, delegation, "orchestrator", bgManager, nil,
				)
				outChan := make(chan provider.StreamChunk, 16)
				ctx := engine.WithStreamOutput(context.Background(), outChan)
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run tests with handoff",
						"handoff": map[string]interface{}{
							"source_agent": "orchestrator",
							"target_agent": "qa-agent",
							"chain_id":     "chain-test-123",
							"task_type":    "testing",
						},
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("async response"))

				close(outChan)
				var delegationChunks []provider.StreamChunk
				for chunk := range outChan {
					if chunk.DelegationInfo != nil {
						delegationChunks = append(delegationChunks, chunk)
					}
				}

				Expect(delegationChunks).To(HaveLen(2))
				started := delegationChunks[0].DelegationInfo
				Expect(started.ChainID).To(Equal("chain-test-123"))
				Expect(started.SourceAgent).To(Equal("orchestrator"))
				Expect(started.TargetAgent).To(Equal("qa-agent"))
			})
		})

		Describe("emitDelegationEvent non-blocking send", func() {
			It("background task completes even when outChan consumer is slow", func() {
				qaProvider := &mockProvider{
					name: "qa-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "nb response", Done: true},
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

				nbEngines := map[string]*engine.Engine{"qa-agent": qaEngine}
				nbDelegation := agent.Delegation{
					CanDelegate:     true,
					DelegationTable: map[string]string{"testing": "qa-agent"},
				}

				nbBgManager := engine.NewBackgroundTaskManager()
				delegateTool := engine.NewDelegateToolWithBackground(
					nbEngines, nbDelegation, "orchestrator", nbBgManager, nil,
				)

				fullChan := make(chan provider.StreamChunk, 1)
				ctx := engine.WithStreamOutput(context.Background(), fullChan)

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type":         "testing",
						"message":           "Run tests async",
						"run_in_background": true,
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() string {
					tasks := nbBgManager.List()
					if len(tasks) == 0 {
						return ""
					}
					return tasks[0].Status.Load()
				}, "3s", "50ms").Should(Equal("completed"))
			})
		})

		Describe("circuit breaker integration", func() {
			It("returns error when circuit is open after max failures", func() {
				failProvider := &mockProvider{
					name:      "fail-provider",
					streamErr: errors.New("always fails"),
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

				cbEngines := map[string]*engine.Engine{"fail-agent": failEngine}
				cbDelegation := agent.Delegation{
					CanDelegate:     true,
					DelegationTable: map[string]string{"testing": "fail-agent"},
				}

				delegateTool := engine.NewDelegateTool(cbEngines, cbDelegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "run tests",
					},
				}

				for range 3 {
					_, _ = delegateTool.Execute(ctx, input)
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("circuit breaker open"))
			})

			It("records success on completed delegation", func() {
				successProvider := &mockProvider{
					name: "success-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "success", Done: true},
					},
				}

				successManifest := agent.Manifest{
					ID:                "success-agent",
					Name:              "Success Agent",
					Instructions:      agent.Instructions{SystemPrompt: "succeed"},
					ContextManagement: agent.DefaultContextManagement(),
				}

				successEngine := engine.New(engine.Config{
					ChatProvider: successProvider,
					Manifest:     successManifest,
				})

				cbEngines := map[string]*engine.Engine{"success-agent": successEngine}
				cbDelegation := agent.Delegation{
					CanDelegate:     true,
					DelegationTable: map[string]string{"testing": "success-agent"},
				}

				delegateTool := engine.NewDelegateTool(cbEngines, cbDelegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "run tests",
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(delegateTool.CircuitBreaker().Failures()).To(Equal(0))
			})
		})

		Describe("spawn limits enforcement", func() {
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
						{Content: "delegated response", Done: true},
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
			})

			It("prevents delegation when depth limit is exceeded", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				limits := delegationpkg.DefaultSpawnLimits()
				limits.MaxDepth = 3
				delegateTool.WithSpawnLimits(limits)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run tests",
						"handoff": map[string]interface{}{
							"source_agent": "orchestrator",
							"target_agent": "qa-agent",
							"chain_id":     "chain-test",
							"metadata": map[string]string{
								"depth": "3",
							},
						},
					},
				}

				_, err := delegateTool.Execute(ctx, input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("depth limit exceeded"))
			})

			It("prevents delegation when budget limit is exceeded", func() {
				bgManager := engine.NewBackgroundTaskManager()

				delegateTool := engine.NewDelegateToolWithBackground(
					engines, delegation, "orchestrator", bgManager, nil,
				)
				limits := delegationpkg.DefaultSpawnLimits()
				limits.MaxTotalBudget = 2
				delegateTool.WithSpawnLimits(limits)

				for i := range 2 {
					bgManager.Launch(context.Background(), fmt.Sprintf("task-%d", i), "test-agent", "test", func(ctx context.Context) (string, error) {
						select {
						case <-ctx.Done():
							return "", ctx.Err()
						case <-time.After(10 * time.Second):
							return "done", nil
						}
					})
				}

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
				Expect(err.Error()).To(ContainSubstring("budget limit exceeded"))
			})

			It("allows delegation when within limits", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				limits := delegationpkg.DefaultSpawnLimits()
				delegateTool.WithSpawnLimits(limits)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run tests",
						"handoff": map[string]interface{}{
							"source_agent": "orchestrator",
							"target_agent": "qa-agent",
							"chain_id":     "chain-test",
							"metadata": map[string]string{
								"depth": "2",
							},
						},
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("delegated response"))
			})

			It("uses default limits when WithSpawnLimits not called", func() {
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("delegated response"))
			})
		})

		Describe("delegation model selection", func() {
			It("uses the target agent's model preferences, not the coordinator's", func() {
				coordinatorProvider := &mockProvider{
					name:         "anthropic",
					streamChunks: []provider.StreamChunk{{Content: "wrong", Done: true}},
					models:       []provider.Model{{ID: "claude-sonnet-4-6", Provider: "anthropic", ContextLength: 200000}},
				}
				targetProvider := &mockProvider{
					name:         "ollama",
					streamChunks: []provider.StreamChunk{{Content: "target response", Done: true}},
					models:       []provider.Model{{ID: "llama3.2", Provider: "ollama", ContextLength: 128000}},
				}

				provReg := provider.NewRegistry()
				provReg.Register(coordinatorProvider)
				provReg.Register(targetProvider)

				coordinatorManifest := agent.Manifest{
					ID: "coordinator",
					ModelPreferences: map[string][]agent.ModelPref{
						"anthropic": {{Provider: "anthropic", Model: "claude-sonnet-4-6"}},
					},
					Delegation: agent.Delegation{
						CanDelegate:     true,
						DelegationTable: map[string]string{"writing": "writer"},
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				writerManifest := agent.Manifest{
					ID: "writer",
					ModelPreferences: map[string][]agent.ModelPref{
						"ollama": {{Provider: "ollama", Model: "llama3.2"}},
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				writerEngine := engine.New(engine.Config{
					Registry: provReg,
					Manifest: writerManifest,
				})

				engines := map[string]*engine.Engine{"writer": writerEngine}
				delegation := coordinatorManifest.Delegation
				delegateTool := engine.NewDelegateTool(engines, delegation, "coordinator")

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "writing",
						"message":   "Write the plan",
					},
				}

				result, err := delegateTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("target response"))

				Expect(targetProvider.capturedRequest).NotTo(BeNil())
				Expect(targetProvider.capturedRequest.Model).To(Equal("llama3.2"))

				Expect(coordinatorProvider.capturedRequest).To(BeNil())

				Expect(writerEngine.LastModel()).To(Equal("llama3.2"))
				Expect(writerEngine.LastProvider()).To(Equal("ollama"))
			})
		})
	})

	Describe("SkillResolver", func() {
		Describe("FileSkillResolver", func() {
			It("resolves a skill from the filesystem", func() {
				tmpDir := GinkgoT().TempDir()
				skillDir := filepath.Join(tmpDir, "test-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath := filepath.Join(skillDir, "SKILL.md")
				skillContent := "# Test Skill\n\nTest content"
				Expect(os.WriteFile(skillPath, []byte(skillContent), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				content, err := resolver.Resolve("test-skill")

				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(Equal(skillContent))
			})

			It("returns ErrSkillNotFound for nonexistent skill", func() {
				tmpDir := GinkgoT().TempDir()
				resolver := engine.NewFileSkillResolver(tmpDir)
				_, err := resolver.Resolve("nonexistent")

				Expect(err).To(MatchError(engine.ErrSkillNotFound))
			})

			It("returns empty string for empty skill file", func() {
				tmpDir := GinkgoT().TempDir()
				skillDir := filepath.Join(tmpDir, "empty-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath := filepath.Join(skillDir, "SKILL.md")
				Expect(os.WriteFile(skillPath, []byte(""), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				content, err := resolver.Resolve("empty-skill")

				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(Equal(""))
			})
		})

		Describe("DelegateTool skill injection", func() {
			It("WithSkillResolver sets the resolver", func() {
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				tmpDir := GinkgoT().TempDir()
				resolver := engine.NewFileSkillResolver(tmpDir)

				result := delegateTool.WithSkillResolver(resolver)

				Expect(result).To(Equal(delegateTool))
			})

			It("injects skills into system prompt when loadSkills is provided", func() {
				tmpDir := GinkgoT().TempDir()
				skillDir := filepath.Join(tmpDir, "skill1")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath := filepath.Join(skillDir, "SKILL.md")
				skillContent := "Skill 1 content"
				Expect(os.WriteFile(skillPath, []byte(skillContent), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				delegateTool.WithSkillResolver(resolver)

				basePrompt := "base prompt"
				injected := delegateTool.InjectSkillsIfProvided([]string{"skill1"}, basePrompt)

				Expect(injected).NotTo(Equal(basePrompt))
				Expect(injected).To(ContainSubstring(skillContent))
				Expect(injected).To(ContainSubstring(basePrompt))
			})

			It("returns base prompt when no resolver is set", func() {
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				basePrompt := "base prompt"

				injected := delegateTool.InjectSkillsIfProvided([]string{"skill1"}, basePrompt)

				Expect(injected).To(Equal(basePrompt))
			})

			It("returns base prompt when loadSkills is empty", func() {
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				basePrompt := "base prompt"

				injected := delegateTool.InjectSkillsIfProvided([]string{}, basePrompt)

				Expect(injected).To(Equal(basePrompt))
			})

			It("prepends skills to child engine system prompt during delegation", func() {
				tmpDir := GinkgoT().TempDir()
				skillDir := filepath.Join(tmpDir, "test-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath := filepath.Join(skillDir, "SKILL.md")
				skillContent := "Test skill content"
				Expect(os.WriteFile(skillPath, []byte(skillContent), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)

				mockEngine := engine.New(engine.Config{})

				engines := map[string]*engine.Engine{
					"target-agent": mockEngine,
				}

				delegateTool := engine.NewDelegateTool(engines, agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"test": "target-agent",
					},
				}, "test-agent")
				delegateTool.WithSkillResolver(resolver)

				basePrompt := "base prompt"
				injected := delegateTool.InjectSkillsIfProvided([]string{"test-skill"}, basePrompt)

				Expect(injected).To(ContainSubstring(skillContent))
				Expect(injected).To(ContainSubstring(basePrompt))
			})
		})

		Describe("DelegateTool category routing", func() {
			It("resolves category to model config when CategoryResolver set", func() {
				chatProvider := &mockProvider{
					name: "test-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "delegated response", Done: true},
					},
				}

				targetManifest := agent.Manifest{
					ID:                "target-agent",
					Name:              "Target Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are target."},
					ContextManagement: agent.DefaultContextManagement(),
				}

				targetEngine := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     targetManifest,
				})

				engines := map[string]*engine.Engine{
					"target-agent": targetEngine,
				}

				delegation := agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"quick": "target-agent",
					},
				}

				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				categoryResolver := engine.NewCategoryResolver(nil)
				delegateTool.WithCategoryResolver(categoryResolver)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "quick",
						"category":  "quick",
						"message":   "Execute quick task",
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("delegated response"))
			})

			It("falls back to task_type routing when no category resolver configured", func() {
				chatProvider := &mockProvider{
					name: "test-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "task type routed", Done: true},
					},
				}

				targetManifest := agent.Manifest{
					ID:                "target-agent",
					Name:              "Target Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are target."},
					ContextManagement: agent.DefaultContextManagement(),
				}

				targetEngine := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     targetManifest,
				})

				engines := map[string]*engine.Engine{
					"target-agent": targetEngine,
				}

				delegation := agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"testing": "target-agent",
					},
				}

				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type": "testing",
						"message":   "Run tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("task type routed"))
			})

			It("injects skills when load_skills provided", func() {
				tmpDir := GinkgoT().TempDir()
				skillDir := filepath.Join(tmpDir, "injected-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath := filepath.Join(skillDir, "SKILL.md")
				skillContent := "Injected skill content"
				Expect(os.WriteFile(skillPath, []byte(skillContent), 0o600)).To(Succeed())

				chatProvider := &mockProvider{
					name: "test-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "skill injection done", Done: true},
					},
				}

				targetManifest := agent.Manifest{
					ID:   "target-agent",
					Name: "Target Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "Base system prompt",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				targetEngine := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     targetManifest,
				})

				engines := map[string]*engine.Engine{
					"target-agent": targetEngine,
				}

				delegation := agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"testing": "target-agent",
					},
				}

				skillResolver := engine.NewFileSkillResolver(tmpDir)
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				delegateTool.WithSkillResolver(skillResolver)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"task_type":   "testing",
						"message":     "Run tests with skills",
						"load_skills": []interface{}{"injected-skill"},
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("skill injection done"))

				updatedManifest := targetEngine.Manifest()
				Expect(updatedManifest.Instructions.SystemPrompt).To(ContainSubstring(skillContent))
			})
		})
	})

})

var _ = Describe("DelegateTool.ResolveByNameOrAlias", func() {
	var (
		reg          *agent.Registry
		delegateTool *engine.DelegateTool
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{
			ID:      "senior-engineer",
			Name:    "Senior Engineer",
			Aliases: []string{"lead-dev", "guru"},
		})
		reg.Register(&agent.Manifest{
			ID:      "qa-agent",
			Name:    "QA Agent",
			Aliases: []string{"tester"},
		})

		engines := map[string]*engine.Engine{}
		delegation := agent.Delegation{CanDelegate: true}
		delegateTool = engine.NewDelegateTool(engines, delegation, "orchestrator").WithRegistry(reg)
	})

	It("resolves by exact name", func() {
		id, err := delegateTool.ResolveByNameOrAlias("senior-engineer")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("senior-engineer"))
	})

	It("resolves by case-insensitive name", func() {
		id, err := delegateTool.ResolveByNameOrAlias("SENIOR-ENGINEER")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("senior-engineer"))
	})

	It("resolves by alias", func() {
		id, err := delegateTool.ResolveByNameOrAlias("guru")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("senior-engineer"))
	})

	It("returns error for unknown name", func() {
		_, err := delegateTool.ResolveByNameOrAlias("xyz-unknown")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("xyz-unknown"))
	})

	It("lists available agents alphabetically in error for unknown name", func() {
		_, err := delegateTool.ResolveByNameOrAlias("xyz-unknown")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("available agents: qa-agent, senior-engineer"))
	})
})

var _ = Describe("DelegateTool Schema subagent_type enum", func() {
	It("populates subagent_type enum from registry agent IDs", func() {
		reg := agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "explorer"})
		reg.Register(&agent.Manifest{ID: "planner"})
		reg.Register(&agent.Manifest{ID: "senior-engineer"})
		reg.Register(&agent.Manifest{ID: "qa-agent"})
		reg.Register(&agent.Manifest{ID: "analyst"})
		reg.Register(&agent.Manifest{ID: "librarian"})
		reg.Register(&agent.Manifest{ID: "plan-reviewer"})

		engines := map[string]*engine.Engine{}
		del := agent.Delegation{CanDelegate: true}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		schema := delegateTool.Schema()
		subagentProp := schema.Properties["subagent_type"]

		Expect(subagentProp.Enum).To(ConsistOf(
			"analyst", "explorer", "librarian", "plan-reviewer",
			"planner", "qa-agent", "senior-engineer",
		))
	})

	It("leaves subagent_type enum nil when registry is nil", func() {
		engines := map[string]*engine.Engine{}
		del := agent.Delegation{CanDelegate: true}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator")

		schema := delegateTool.Schema()
		subagentProp := schema.Properties["subagent_type"]

		Expect(subagentProp.Enum).To(BeNil())
	})

	It("does not change category enum", func() {
		reg := agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "explorer"})

		engines := map[string]*engine.Engine{}
		del := agent.Delegation{CanDelegate: true}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		schema := delegateTool.Schema()
		categoryProp := schema.Properties["category"]

		Expect(categoryProp.Enum).NotTo(ContainElement("explorer"))
	})
})

var _ = Describe("resolveTargetWithOptions subagent_type wiring", func() {
	var (
		reg            *agent.Registry
		explorerEngine *engine.Engine
		qaEngine       *engine.Engine
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{
			ID:      "explorer",
			Name:    "Explorer",
			Aliases: []string{"scout"},
		})
		reg.Register(&agent.Manifest{
			ID:   "qa-agent",
			Name: "QA Agent",
		})

		explorerProvider := &mockProvider{
			name: "explorer-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Explorer response", Done: true},
			},
		}
		explorerManifest := agent.Manifest{
			ID:                "explorer",
			Name:              "Explorer",
			Instructions:      agent.Instructions{SystemPrompt: "You are an explorer."},
			ContextManagement: agent.DefaultContextManagement(),
		}
		explorerEngine = engine.New(engine.Config{
			ChatProvider: explorerProvider,
			Manifest:     explorerManifest,
		})

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
		qaEngine = engine.New(engine.Config{
			ChatProvider: qaProvider,
			Manifest:     qaManifest,
		})
	})

	It("resolves subagent_type via registry lookup", func() {
		engines := map[string]*engine.Engine{
			"explorer": explorerEngine,
		}
		del := agent.Delegation{
			CanDelegate:     true,
			DelegationTable: map[string]string{},
		}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "explorer",
				"message":       "Investigate the codebase",
			},
		}

		result, err := delegateTool.Execute(ctx, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("Explorer response"))
	})

	It("resolves subagent_type case-insensitively via registry", func() {
		engines := map[string]*engine.Engine{
			"explorer": explorerEngine,
		}
		del := agent.Delegation{
			CanDelegate:     true,
			DelegationTable: map[string]string{},
		}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "EXPLORER",
				"message":       "Investigate the codebase",
			},
		}

		result, err := delegateTool.Execute(ctx, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("Explorer response"))
	})

	It("resolves task_type via delegation table for backward compatibility", func() {
		engines := map[string]*engine.Engine{
			"qa-agent": qaEngine,
		}
		del := agent.Delegation{
			CanDelegate: true,
			DelegationTable: map[string]string{
				"testing": "qa-agent",
			},
		}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

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
		Expect(result.Output).To(ContainSubstring("QA response"))
	})
})

var _ = Describe("resolveAgentID category decoupling", func() {
	var (
		reg            *agent.Registry
		explorerEngine *engine.Engine
	)

	BeforeEach(func() {
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{
			ID:   "explorer",
			Name: "Explorer",
		})

		explorerProvider := &mockProvider{
			name: "explorer-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Explorer response", Done: true},
			},
		}
		explorerManifest := agent.Manifest{
			ID:                "explorer",
			Name:              "Explorer",
			Instructions:      agent.Instructions{SystemPrompt: "You are an explorer."},
			ContextManagement: agent.DefaultContextManagement(),
		}
		explorerEngine = engine.New(engine.Config{
			ChatProvider: explorerProvider,
			Manifest:     explorerManifest,
		})
	})

	It("returns error when only category is provided without subagent_type or task_type", func() {
		engines := map[string]*engine.Engine{
			"explorer": explorerEngine,
		}
		del := agent.Delegation{
			CanDelegate:     true,
			DelegationTable: map[string]string{"quick": "explorer"},
		}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"category": "quick",
				"message":  "do something",
			},
		}

		_, err := delegateTool.Execute(ctx, input)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("task_type"))
	})

	It("resolves agent from subagent_type when both category and subagent_type are provided", func() {
		engines := map[string]*engine.Engine{
			"explorer": explorerEngine,
		}
		del := agent.Delegation{
			CanDelegate:     true,
			DelegationTable: map[string]string{},
		}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"category":      "quick",
				"subagent_type": "explorer",
				"message":       "investigate",
			},
		}

		result, err := delegateTool.Execute(ctx, input)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("Explorer response"))
	})

	It("requires subagent_type and message in Schema", func() {
		del := agent.Delegation{CanDelegate: true}
		delegateTool := engine.NewDelegateTool(nil, del, "orchestrator")
		schema := delegateTool.Schema()
		Expect(schema.Required).To(ConsistOf("subagent_type", "message"))
	})
})
