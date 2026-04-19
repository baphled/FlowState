package engine_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	delegationpkg "github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
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
				chunks, err := eng.DelegateToAgent(ctx, engines, "qa-agent", "Run the tests")

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

		Context("when target agent not in engines", func() {
			It("returns an error", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})

				ctx := context.Background()
				_, err := eng.DelegateToAgent(ctx, nil, "unknown-task", "Do something")

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("target agent engine not available"))
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
				_, err := eng.DelegateToAgent(ctx, engines, "qa-agent", "Run the tests")

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

		Describe("SetDelegation", func() {
			It("updates the delegation configuration", func() {
				delegateTool := engine.NewDelegateTool(nil, agent.Delegation{CanDelegate: false}, "source")

				Expect(delegateTool.Delegation().CanDelegate).To(BeFalse())

				delegateTool.SetDelegation(agent.Delegation{CanDelegate: true})

				Expect(delegateTool.Delegation().CanDelegate).To(BeTrue())
			})

			It("allows delegation after switching from false to true", func() {
				delegateTool := engine.NewDelegateTool(nil, agent.Delegation{CanDelegate: false}, "source")

				Expect(delegateTool.Delegation().CanDelegate).To(BeFalse())

				delegateTool.SetDelegation(agent.Delegation{CanDelegate: true})

				Expect(delegateTool.Delegation().CanDelegate).To(BeTrue())
			})
		})

		It("returns correct schema with subagent_type and message as required parameters", func() {
			delegateTool := engine.NewDelegateTool(nil, agent.Delegation{}, "source")

			schema := delegateTool.Schema()

			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Properties).NotTo(HaveKey("task_type"))
			Expect(schema.Properties).To(HaveKey("message"))
			Expect(schema.Required).To(ConsistOf("subagent_type", "message"))
		})

		Context("when executing", func() {
			It("dispatches via direct engine key match", func() {
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
				}

				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "qa-agent",
						"message":       "Run the unit tests",
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
						"subagent_type": "qa-agent",
						"message":       "Run tests",
					},
				}

				_, err := delegateTool.Execute(ctx, input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("delegation not allowed"))
			})

			It("returns error when subagent_type has no matching engine", func() {
				delegation := agent.Delegation{
					CanDelegate: true,
				}

				delegateTool := engine.NewDelegateTool(nil, delegation, "source")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "unknown",
						"message":       "Do something",
					},
				}

				_, err := delegateTool.Execute(ctx, input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no agent configured for task type"))
			})

			It("returns error when no engine matches subagent_type", func() {
				engines := map[string]*engine.Engine{}

				delegation := agent.Delegation{
					CanDelegate: true,
				}

				delegateTool := engine.NewDelegateTool(engines, delegation, "source")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "nonexistent-agent",
						"message":       "Run tests",
					},
				}

				_, err := delegateTool.Execute(ctx, input)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no agent configured for task type"))
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
						"subagent_type": "qa-agent",
						"message":       "Run the tests",
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
						"subagent_type": "qa-agent",
						"message":       "Run the tests",
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
						"subagent_type": "qa-agent",
						"message":       "Run the tests",
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
						"subagent_type":     "qa-agent",
						"message":           "Run tests async",
						"run_in_background": true,
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(MatchRegexp(`"task_id":\s*"delegate-qa-agent-\d+"`))
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
						"subagent_type":     "qa-agent",
						"message":           "Run tests async",
						"run_in_background": true,
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				tasks := bgManager.List()
				Expect(tasks).To(HaveLen(1))
				Expect(tasks[0].AgentID).To(Equal("qa-agent"))

				Eventually(func() string {
					tasks := bgManager.List()
					if len(tasks) == 0 {
						return ""
					}
					return tasks[0].Status.Load()
				}).Should(Equal("completed"))

				Eventually(func() int {
					return bgManager.ActiveCount()
				}).Should(Equal(0))
			})

			It("updates task status to completed when done", func() {
				delegateTool := engine.NewDelegateToolWithBackground(
					engines, delegation, "orchestrator", bgManager, nil,
				)
				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type":     "qa-agent",
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
						"subagent_type":     "qa-agent",
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
						"subagent_type": "qa-agent",
						"message":       "Run tests with handoff",
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
					CanDelegate: true,
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
						"subagent_type":     "qa-agent",
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
					CanDelegate: true,
				}

				delegateTool := engine.NewDelegateTool(cbEngines, cbDelegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "fail-agent",
						"message":       "run tests",
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
					CanDelegate: true,
				}

				delegateTool := engine.NewDelegateTool(cbEngines, cbDelegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "success-agent",
						"message":       "run tests",
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
						"subagent_type": "qa-agent",
						"message":       "Run tests",
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
						"subagent_type": "qa-agent",
						"message":       "Run tests",
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
						"subagent_type": "qa-agent",
						"message":       "Run tests",
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
						"subagent_type": "qa-agent",
						"message":       "Run tests",
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
					Delegation: agent.Delegation{
						CanDelegate: true,
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				writerManifest := agent.Manifest{
					ID:                "writer",
					ContextManagement: agent.DefaultContextManagement(),
				}
				writerHealth := failover.NewHealthManager()
				writerMgr := failover.NewManager(provReg, writerHealth, 5*time.Minute)
				writerMgr.SetBasePreferences([]provider.ModelPreference{
					{Provider: "ollama", Model: "llama3.2"},
				})

				writerEngine := engine.New(engine.Config{
					Registry:        provReg,
					Manifest:        writerManifest,
					FailoverManager: writerMgr,
				})

				engines := map[string]*engine.Engine{"writer": writerEngine}
				delegation := coordinatorManifest.Delegation
				delegateTool := engine.NewDelegateTool(engines, delegation, "coordinator")

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "writer",
						"message":       "Write the plan",
					},
				}

				result, err := delegateTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("target response"))

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
				}, "test-agent")
				delegateTool.WithSkillResolver(resolver)

				basePrompt := "base prompt"
				injected := delegateTool.InjectSkillsIfProvided([]string{"test-skill"}, basePrompt)

				Expect(injected).To(ContainSubstring(skillContent))
				Expect(injected).To(ContainSubstring(basePrompt))
			})

			It("skips skills already present in the base prompt", func() {
				tmpDir := GinkgoT().TempDir()
				skillDir := filepath.Join(tmpDir, "pre-action")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillContent := "# Skill: pre-action\n\nPre-action skill content."
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				delegateTool.WithSkillResolver(resolver)

				basePrompt := "# Skill: pre-action\n\nPre-action skill content.\n\nYou are an agent."
				injected := delegateTool.InjectSkillsIfProvided([]string{"pre-action"}, basePrompt)

				Expect(injected).To(Equal(basePrompt))
			})

			It("injects only skills not already present in the base prompt", func() {
				tmpDir := GinkgoT().TempDir()
				for _, name := range []string{"existing-skill", "new-skill"} {
					skillDir := filepath.Join(tmpDir, name)
					Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
					content := "# Skill: " + name + "\n\nContent for " + name + "."
					Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600)).To(Succeed())
				}

				resolver := engine.NewFileSkillResolver(tmpDir)
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				delegateTool.WithSkillResolver(resolver)

				basePrompt := "# Skill: existing-skill\n\nContent for existing-skill.\n\nYou are an agent."
				injected := delegateTool.InjectSkillsIfProvided([]string{"existing-skill", "new-skill"}, basePrompt)

				Expect(injected).To(ContainSubstring("# Skill: new-skill"))
				Expect(injected).To(ContainSubstring(basePrompt))
			})

			It("does not deduplicate skills without a heading marker", func() {
				tmpDir := GinkgoT().TempDir()
				skillDir := filepath.Join(tmpDir, "plain-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillContent := "Plain content without heading."
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				delegateTool.WithSkillResolver(resolver)

				basePrompt := "Plain content without heading.\n\nYou are an agent."
				injected := delegateTool.InjectSkillsIfProvided([]string{"plain-skill"}, basePrompt)

				Expect(injected).NotTo(Equal(basePrompt))
				Expect(injected).To(ContainSubstring(skillContent))
			})

			It("does not false-positive suppress a skill whose name is a prefix of a present skill", func() {
				tmpDir := GinkgoT().TempDir()
				for _, name := range []string{"golang", "golang-testing"} {
					skillDir := filepath.Join(tmpDir, name)
					Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
					content := "# Skill: " + name + "\n\nContent for " + name + "."
					Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600)).To(Succeed())
				}

				resolver := engine.NewFileSkillResolver(tmpDir)
				delegateTool := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "test-agent")
				delegateTool.WithSkillResolver(resolver)

				basePrompt := "# Skill: golang-testing\n\nContent for golang-testing.\n\nYou are an agent."
				injected := delegateTool.InjectSkillsIfProvided([]string{"golang"}, basePrompt)

				Expect(injected).To(ContainSubstring("# Skill: golang\n"))
			})
		})

		Describe("DelegateTool category routing", func() {
			It("resolves category to model config when CategoryResolver set and subagent_type provided", func() {
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
				}

				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				categoryResolver := engine.NewCategoryResolver(nil)
				delegateTool.WithCategoryResolver(categoryResolver)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "target-agent",
						"category":      "quick",
						"message":       "Execute quick task",
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("delegated response"))
			})

			It("routes via subagent_type direct engine key match", func() {
				chatProvider := &mockProvider{
					name: "test-provider",
					streamChunks: []provider.StreamChunk{
						{Content: "subagent routed", Done: true},
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
				}

				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "target-agent",
						"message":       "Run tests",
					},
				}

				result, err := delegateTool.Execute(ctx, input)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("subagent routed"))
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
				}

				skillResolver := engine.NewFileSkillResolver(tmpDir)
				delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
				delegateTool.WithSkillResolver(skillResolver)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "target-agent",
						"message":       "Run tests with skills",
						"load_skills":   []interface{}{"injected-skill"},
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

	It("returns error when registry is nil", func() {
		engines := map[string]*engine.Engine{}
		delegation := agent.Delegation{CanDelegate: true}
		nilRegDelegateTool := engine.NewDelegateTool(engines, delegation, "source-agent")
		_, err := nilRegDelegateTool.ResolveByNameOrAlias("any-agent")
		Expect(err).To(HaveOccurred())
	})

	It("lists empty available agents when registry has no agents", func() {
		emptyReg := agent.NewRegistry()
		engines := map[string]*engine.Engine{}
		delegation := agent.Delegation{CanDelegate: true}
		emptyRegDelegateTool := engine.NewDelegateTool(engines, delegation, "source-agent").WithRegistry(emptyReg)
		_, err := emptyRegDelegateTool.ResolveByNameOrAlias("xyz")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("xyz"))
	})

	It("resolves by uppercase alias", func() {
		id, err := delegateTool.ResolveByNameOrAlias("GURU")
		Expect(err).NotTo(HaveOccurred())
		Expect(id).To(Equal("senior-engineer"))
	})

	It("returns error for empty name", func() {
		_, err := delegateTool.ResolveByNameOrAlias("")
		Expect(err).To(HaveOccurred())
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
			CanDelegate: true,
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
			CanDelegate: true,
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

	It("resolves subagent_type via direct engine key match", func() {
		engines := map[string]*engine.Engine{
			"qa-agent": qaEngine,
		}
		del := agent.Delegation{
			CanDelegate: true,
		}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "qa-agent",
				"message":       "Run the unit tests",
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

	It("returns error when only category is provided without subagent_type", func() {
		engines := map[string]*engine.Engine{
			"explorer": explorerEngine,
		}
		del := agent.Delegation{
			CanDelegate: true,
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
		Expect(err.Error()).To(ContainSubstring("subagent_type"))
	})

	It("resolves agent from subagent_type when both category and subagent_type are provided", func() {
		engines := map[string]*engine.Engine{
			"explorer": explorerEngine,
		}
		del := agent.Delegation{
			CanDelegate: true,
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

	It("returns helpful error with available agents when subagent_type is unknown", func() {
		engines := map[string]*engine.Engine{
			"explorer": explorerEngine,
		}
		del := agent.Delegation{
			CanDelegate: true,
		}
		delegateTool := engine.NewDelegateTool(engines, del, "orchestrator").WithRegistry(reg)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "xyz-unknown",
				"message":       "do something",
			},
		}

		_, err := delegateTool.Execute(ctx, input)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("available agents:"))
	})

	It("requires subagent_type and message in Schema", func() {
		del := agent.Delegation{CanDelegate: true}
		delegateTool := engine.NewDelegateTool(nil, del, "orchestrator")
		schema := delegateTool.Schema()
		Expect(schema.Required).To(ConsistOf("subagent_type", "message"))
	})

	Describe("agent files skipping for delegated engines", func() {
		It("excludes agent files from child system prompt for fresh delegations", func() {
			tempDir, err := os.MkdirTemp("", "delegation-skip-agents-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Project Instructions\n\nExpensive project rules that waste tokens."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			childProvider := &mockProvider{
				name: "child-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "Child response", Done: true},
				},
			}

			childManifest := agent.Manifest{
				ID:                "child-agent",
				Name:              "Child Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are a child agent."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			childEngine := engine.New(engine.Config{
				ChatProvider:     childProvider,
				Manifest:         childManifest,
				AgentsFileLoader: loader,
			})

			engines := map[string]*engine.Engine{
				"child-agent": childEngine,
			}

			delegation := agent.Delegation{CanDelegate: true}
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "child-agent",
					"message":       "Do the task",
				},
			}

			_, err = delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())
			Expect(childProvider.capturedRequest).NotTo(BeNil())
			Expect(childProvider.capturedRequest.Messages[0].Content).NotTo(
				ContainSubstring("Expensive project rules that waste tokens."),
			)
		})

		It("resets skip flag between fresh and session-continuation delegations to the same engine", func() {
			tempDir, err := os.MkdirTemp("", "delegation-reset-skip-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Project Instructions\n\nShould appear on session continuation."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			childProvider := &mockProvider{
				name: "child-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "First response", Done: true},
				},
			}

			childManifest := agent.Manifest{
				ID:                "shared-child",
				Name:              "Shared Child",
				Instructions:      agent.Instructions{SystemPrompt: "You are a shared child agent."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			childEngine := engine.New(engine.Config{
				ChatProvider:     childProvider,
				Manifest:         childManifest,
				AgentsFileLoader: loader,
			})

			engines := map[string]*engine.Engine{
				"shared-child": childEngine,
			}

			delegation := agent.Delegation{CanDelegate: true}
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			ctx := context.Background()
			freshInput := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "shared-child",
					"message":       "Fresh delegation without session",
				},
			}
			_, err = delegateTool.Execute(ctx, freshInput)
			Expect(err).NotTo(HaveOccurred())

			childProvider.streamChunks = []provider.StreamChunk{
				{Content: "Session response", Done: true},
			}
			sessionInput := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "shared-child",
					"message":       "Continue the session",
					"session_id":    "existing-session-456",
				},
			}
			_, err = delegateTool.Execute(ctx, sessionInput)
			Expect(err).NotTo(HaveOccurred())
			Expect(childProvider.capturedRequest).NotTo(BeNil())
			Expect(childProvider.capturedRequest.Messages[0].Content).To(
				ContainSubstring("Should appear on session continuation."),
			)
		})

		It("includes agent files in child system prompt for session continuations", func() {
			tempDir, err := os.MkdirTemp("", "delegation-session-agents-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			agentsContent := "# Project Instructions\n\nImportant context for session continuation."
			Expect(os.WriteFile(filepath.Join(tempDir, "AGENTS.md"), []byte(agentsContent), 0o600)).To(Succeed())

			loader := agent.NewAgentsFileLoader(tempDir, "")

			childProvider := &mockProvider{
				name: "child-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "Session response", Done: true},
				},
			}

			childManifest := agent.Manifest{
				ID:                "child-agent",
				Name:              "Child Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are a child agent."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			childEngine := engine.New(engine.Config{
				ChatProvider:     childProvider,
				Manifest:         childManifest,
				AgentsFileLoader: loader,
			})

			engines := map[string]*engine.Engine{
				"child-agent": childEngine,
			}

			delegation := agent.Delegation{CanDelegate: true}
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "child-agent",
					"message":       "Continue the session",
					"session_id":    "existing-session-123",
				},
			}

			_, err = delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())
			Expect(childProvider.capturedRequest).NotTo(BeNil())
			Expect(childProvider.capturedRequest.Messages[0].Content).To(
				ContainSubstring("Important context for session continuation."),
			)
		})
	})

	Describe("DelegateTool delegation allowlist", func() {
		It("allows any agent when allowlist is empty", func() {
			targetProvider := &mockProvider{
				name: "target-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "Target response", Done: true},
				},
			}

			targetManifest := agent.Manifest{
				ID:                "target-agent",
				Name:              "Target Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are a target."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			targetEngine := engine.New(engine.Config{
				ChatProvider: targetProvider,
				Manifest:     targetManifest,
			})

			engines := map[string]*engine.Engine{
				"target-agent": targetEngine,
			}

			delegation := agent.Delegation{
				CanDelegate:         true,
				DelegationAllowlist: []string{},
			}

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "target-agent",
					"message":       "Do the work",
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("Target response"))
		})

		It("allows agents present in a non-empty allowlist", func() {
			targetProvider := &mockProvider{
				name: "target-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "Allowlisted response", Done: true},
				},
			}

			targetManifest := agent.Manifest{
				ID:                "explorer",
				Name:              "Explorer",
				Instructions:      agent.Instructions{SystemPrompt: "You explore."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			targetEngine := engine.New(engine.Config{
				ChatProvider: targetProvider,
				Manifest:     targetManifest,
			})

			engines := map[string]*engine.Engine{
				"explorer": targetEngine,
			}

			delegation := agent.Delegation{
				CanDelegate:         true,
				DelegationAllowlist: []string{"explorer", "librarian", "analyst"},
			}

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "explorer",
					"message":       "Explore the codebase",
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("Allowlisted response"))
		})

		It("rejects agents not present in a non-empty allowlist", func() {
			targetProvider := &mockProvider{
				name: "target-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "Should not reach", Done: true},
				},
			}

			targetManifest := agent.Manifest{
				ID:                "message",
				Name:              "Message Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You handle messages."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			targetEngine := engine.New(engine.Config{
				ChatProvider: targetProvider,
				Manifest:     targetManifest,
			})

			engines := map[string]*engine.Engine{
				"message": targetEngine,
			}

			delegation := agent.Delegation{
				CanDelegate:         true,
				DelegationAllowlist: []string{"explorer", "librarian", "analyst"},
			}

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "message",
					"message":       "Send a message",
				},
			}

			_, err := delegateTool.Execute(ctx, input)

			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ContainSubstring("agent not in delegation allowlist")))
			Expect(err.Error()).To(ContainSubstring("message"))
			Expect(err.Error()).To(ContainSubstring("explorer"))
		})
	})
})

var _ = Describe("DelegateTool session metadata persistence", func() {
	var (
		sessionsDir  string
		targetEngine *engine.Engine
		engines      map[string]*engine.Engine
		delegCfg     agent.Delegation
		mgr          *session.Manager
	)

	BeforeEach(func() {
		var err error
		sessionsDir, err = os.MkdirTemp("", "delegate-persist-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(sessionsDir) })

		targetEngine = engine.New(engine.Config{
			ChatProvider: &mockProvider{
				name:         "target-provider",
				streamChunks: []provider.StreamChunk{{Content: "ok", Done: true}},
			},
			Manifest: agent.Manifest{
				ID:                "target-agent",
				Name:              "Target Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are a target."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})

		engines = map[string]*engine.Engine{"target-agent": targetEngine}
		delegCfg = agent.Delegation{CanDelegate: true}
		mgr = session.NewManager(nil)
	})

	Describe("WithSessionsDir", func() {
		Context("when sessionsDir is set and a child session is created", func() {
			It("writes a .meta.json file for the child session", func() {
				parentSessionID := "persist-parent"
				mgr.RegisterSession(parentSessionID, "parent-agent")

				delegateTool := engine.NewDelegateTool(engines, delegCfg, "orchestrator")
				delegateTool.WithSessionManager(mgr)
				delegateTool.WithSessionCreator(mgr)
				delegateTool.WithSessionsDir(sessionsDir)

				ctx := context.WithValue(context.Background(), session.IDKey{}, parentSessionID)
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "target-agent",
						"message":       "Persist this",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())

				metaFiles, err := filepath.Glob(filepath.Join(sessionsDir, "*.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(metaFiles).To(HaveLen(1))
			})
		})

		Context("when sessionsDir is empty", func() {
			It("does not fail and writes no files", func() {
				parentSessionID := "no-persist-parent"
				mgr.RegisterSession(parentSessionID, "parent-agent")

				delegateTool := engine.NewDelegateTool(engines, delegCfg, "orchestrator")
				delegateTool.WithSessionManager(mgr)
				delegateTool.WithSessionCreator(mgr)

				ctx := context.WithValue(context.Background(), session.IDKey{}, parentSessionID)
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "target-agent",
						"message":       "No persistence",
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).NotTo(BeEmpty())
			})
		})
	})
})

// sessionCapture holds a captured context for session ID verification.
type sessionCapture struct {
	ctx context.Context
}

var _ = Describe("Delegate session isolation", func() {
	var (
		capture       *sessionCapture
		targetEngine  *engine.Engine
		engines       map[string]*engine.Engine
		delegationCfg agent.Delegation
	)

	BeforeEach(func() {
		capture = &sessionCapture{}

		capturingProvider := &contextCapturingProvider{
			name: "ctx-capture-provider",
			chunks: []provider.StreamChunk{
				{Content: "delegate response", Done: true},
			},
			captureFn: func(ctx context.Context) {
				capture.ctx = ctx
			},
		}

		targetManifest := agent.Manifest{
			ID:                "target-agent",
			Name:              "Target Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are a target."},
			ContextManagement: agent.DefaultContextManagement(),
		}

		targetEngine = engine.New(engine.Config{
			ChatProvider: capturingProvider,
			Manifest:     targetManifest,
		})

		engines = map[string]*engine.Engine{
			"target-agent": targetEngine,
		}

		delegationCfg = agent.Delegation{CanDelegate: true}
	})

	Describe("synchronous delegation", func() {
		It("uses a delegate-specific session ID different from the parent", func() {
			parentSessionID := "parent-session-abc"
			ctx := context.WithValue(context.Background(), session.IDKey{}, parentSessionID)
			delegateTool := engine.NewDelegateTool(engines, delegationCfg, "orchestrator")
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "target-agent",
					"message":       "Do work",
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("delegate response"))
			Expect(capture.ctx).NotTo(BeNil())

			delegateSessionID, ok := capture.ctx.Value(session.IDKey{}).(string)
			Expect(ok).To(BeTrue())
			Expect(delegateSessionID).NotTo(BeEmpty())
			Expect(delegateSessionID).NotTo(Equal(parentSessionID))
			Expect(delegateSessionID).To(HavePrefix("delegate-target-agent-"))
		})
	})

	Describe("sanitiseDelegationMessage", func() {
		It("truncates messages exceeding the maximum length", func() {
			longMsg := strings.Repeat("a", 10001)
			result := engine.SanitiseDelegationMessageForTest(longMsg)
			Expect(result).To(HaveLen(10000))
		})

		It("strips control characters but preserves newlines and tabs", func() {
			msg := "hello\tworld\nfoo\x00bar\x07baz"
			result := engine.SanitiseDelegationMessageForTest(msg)
			Expect(result).To(Equal("hello\tworld\nfoobarbaz"))
		})

		It("preserves normal markdown and punctuation", func() {
			msg := "## Task\n\n- Item **bold** `code` [link](url)\n> quote\n\n1. Numbered"
			result := engine.SanitiseDelegationMessageForTest(msg)
			Expect(result).To(Equal(msg))
		})
	})

	Describe("cycle detection", func() {
		var (
			delegateTool *engine.DelegateTool
			targetEngine *engine.Engine
		)

		BeforeEach(func() {
			targetProvider := &mockProvider{
				name: "target-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "response", Done: true},
				},
			}
			targetManifest := agent.Manifest{
				ID:                "target-agent",
				Name:              "Target Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are a target."},
				ContextManagement: agent.DefaultContextManagement(),
			}
			targetEngine = engine.New(engine.Config{
				ChatProvider: targetProvider,
				Manifest:     targetManifest,
			})

			engines := map[string]*engine.Engine{
				"target-agent": targetEngine,
			}

			delegationCfg := agent.Delegation{
				CanDelegate: true,
			}

			delegateTool = engine.NewDelegateTool(engines, delegationCfg, "source-agent")
		})

		It("rejects delegation when target is already in visited_agents", func() {
			ctx := context.Background()
			handoff := &delegationpkg.Handoff{
				Metadata: map[string]string{
					"visited_agents": "agent-a,target-agent,agent-b",
				},
			}
			params := engine.DelegationParamsForTest{
				SubagentType: "target-agent",
				Message:      "do work",
				Handoff:      handoff,
			}

			_, err := delegateTool.ResolveTargetWithOptionsForTest(ctx, params)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("delegation cycle detected"))
			Expect(err.Error()).To(ContainSubstring("target-agent"))
		})

		It("allows delegation when visited_agents does not contain the target", func() {
			ctx := context.Background()
			handoff := &delegationpkg.Handoff{
				Metadata: map[string]string{
					"visited_agents": "agent-a,agent-b",
				},
			}
			params := engine.DelegationParamsForTest{
				SubagentType: "target-agent",
				Message:      "do work",
				Handoff:      handoff,
			}

			agentID, err := delegateTool.ResolveTargetWithOptionsForTest(ctx, params)
			Expect(err).NotTo(HaveOccurred())
			Expect(agentID).To(Equal("target-agent"))
		})

		It("allows delegation when no handoff metadata is present", func() {
			ctx := context.Background()
			params := engine.DelegationParamsForTest{
				SubagentType: "target-agent",
				Message:      "do work",
			}

			agentID, err := delegateTool.ResolveTargetWithOptionsForTest(ctx, params)
			Expect(err).NotTo(HaveOccurred())
			Expect(agentID).To(Equal("target-agent"))
		})
	})

	Describe("self-delegation", func() {
		It("rejects delegation when source and target are the same agent", func() {
			selfProvider := &mockProvider{
				name: "self-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "response", Done: true},
				},
			}
			selfManifest := agent.Manifest{
				ID:                "same-agent",
				Name:              "Same Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are the same."},
				ContextManagement: agent.DefaultContextManagement(),
			}
			selfEngine := engine.New(engine.Config{
				ChatProvider: selfProvider,
				Manifest:     selfManifest,
			})

			engines := map[string]*engine.Engine{
				"same-agent": selfEngine,
			}

			delegationCfg := agent.Delegation{
				CanDelegate: true,
			}

			delegateTool := engine.NewDelegateTool(engines, delegationCfg, "same-agent")

			ctx := context.Background()
			params := engine.DelegationParamsForTest{
				SubagentType: "same-agent",
				Message:      "delegate to myself",
			}

			_, err := delegateTool.ResolveTargetWithOptionsForTest(ctx, params)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("self-delegation not allowed"))
			Expect(err.Error()).To(ContainSubstring("same-agent"))
		})
	})

	Describe("asynchronous delegation", func() {
		It("uses the task ID as the delegate session ID", func() {
			parentSessionID := "parent-session-xyz"
			ctx := context.WithValue(context.Background(), session.IDKey{}, parentSessionID)
			bgManager := engine.NewBackgroundTaskManager()

			delegateTool := engine.NewDelegateToolWithBackground(
				engines, delegationCfg, "orchestrator", bgManager, nil,
			)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type":     "target-agent",
					"message":           "Do async work",
					"run_in_background": true,
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("task_id"))

			Eventually(func() string {
				tasks := bgManager.List()
				if len(tasks) == 0 {
					return ""
				}
				return tasks[0].Status.Load()
			}, "3s", "50ms").Should(Equal("completed"))

			Expect(capture.ctx).NotTo(BeNil())

			delegateSessionID, ok := capture.ctx.Value(session.IDKey{}).(string)
			Expect(ok).To(BeTrue())
			Expect(delegateSessionID).NotTo(BeEmpty())
			Expect(delegateSessionID).NotTo(Equal(parentSessionID))
			Expect(delegateSessionID).To(HavePrefix("delegate-target-agent-"))
		})
	})
})
