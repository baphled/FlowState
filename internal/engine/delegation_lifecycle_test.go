package engine_test

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
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
		Describe("UnwrapTaskResult", func() {
			It("strips the canonical wrapper that formatDelegationOutput emits", func() {
				wrapped := engine.FormatDelegationOutput("inner agent text")
				Expect(engine.UnwrapTaskResult(wrapped)).To(Equal("inner agent text"))
			})

			It("returns input unchanged when no wrapper is present (defensive — never partial-strips)", func() {
				Expect(engine.UnwrapTaskResult("plain content")).To(Equal("plain content"))
				Expect(engine.UnwrapTaskResult("<task_result>opening only")).To(Equal("<task_result>opening only"))
				Expect(engine.UnwrapTaskResult("closing only</task_result>")).To(Equal("closing only</task_result>"))
			})

			It("preserves multi-line inner content verbatim", func() {
				inner := "first line\n\nsecond line\nthird line"
				Expect(engine.UnwrapTaskResult(engine.FormatDelegationOutput(inner))).To(Equal(inner))
			})
		})

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

// Plans/Delegation Bus Bridge — Engine to SSE (May 2026) §"Test
// Strategy" §"Engine seam". The DelegateTool publishes
// `delegation.{started,completed,failed}` onto the engine's
// `*eventbus.EventBus` at the six lifecycle sites identified by the
// plan. These specs pin those sites end-to-end.
var _ = Describe("DelegateTool delegation lifecycle bus publication", func() {
	var (
		qaProvider *mockProvider
		qaEngine   *engine.Engine
		engines    map[string]*engine.Engine
		delegation agent.Delegation
		mgr        *session.Manager
		bus        *eventbus.EventBus
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

		mgr = session.NewManager(nil)
		bus = eventbus.NewEventBus()
	})

	Context("executeSync success path", func() {
		It("publishes delegation.started post-resolve with the populated child session id and parent session id", func() {
			captured := newDelegationCapture(bus)

			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
				WithSessionManager(mgr).
				WithEventBus(bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run all the tests",
				},
			}

			_, err = delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			started := captured.startedEvents()
			Expect(started).To(HaveLen(1),
				"executeSync must publish exactly one delegation.started on the success path")
			Expect(started[0].Data.ChildSessionID).NotTo(BeEmpty(),
				"delegation.started must carry a populated ChildSessionID — the load-bearing field for the SSE click-through")
			Expect(started[0].Data.ParentSessionID).To(Equal(parent.ID))
			Expect(started[0].Data.TargetAgent).To(Equal("qa-agent"))
			Expect(started[0].Data.SourceAgent).To(Equal("orchestrator"))
		})

		It("publishes delegation.completed with model, provider and tool metadata after the stream drains cleanly", func() {
			captured := newDelegationCapture(bus)

			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
				WithSessionManager(mgr).
				WithEventBus(bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Do the thing",
				},
			}

			result, err := delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			completed := captured.completedEvents()
			Expect(completed).To(HaveLen(1),
				"executeSync must publish exactly one delegation.completed on the success path")
			Expect(completed[0].Data.ChildSessionID).To(Equal(result.Metadata["sessionId"]),
				"completed event must carry the same child session id the result metadata reports")
			Expect(completed[0].Data.ParentSessionID).To(Equal(parent.ID))
			Expect(completed[0].Data.CompletedAt).NotTo(BeNil(),
				"delegation.completed must populate CompletedAt at the success terminator")
			Expect(captured.failedEvents()).To(BeEmpty(),
				"delegation.failed must not fire on the success path")
		})
	})

	Context("executeSync no-op when the bus is not wired", func() {
		It("does not panic and behaves identically to today when WithEventBus is unset", func() {
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator").
				WithSessionManager(mgr)

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "no bus path",
				},
			}

			result, err := delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Metadata).To(HaveKey("sessionId"))
		})
	})

	Context("executeSync stream failure path", func() {
		It("publishes exactly one delegation.failed and no delegation.completed when the streamer returns an error", func() {
			failingProvider := &mockProvider{
				name:      "failing-provider",
				streamErr: errors.New("stream init failed"),
			}
			failingManifest := agent.Manifest{
				ID:                "failing-agent",
				Name:              "Failing Agent",
				Instructions:      agent.Instructions{SystemPrompt: "fail"},
				ContextManagement: agent.DefaultContextManagement(),
			}
			failingEngine := engine.New(engine.Config{
				ChatProvider: failingProvider,
				Manifest:     failingManifest,
			})
			failingEngines := map[string]*engine.Engine{
				"failing-agent": failingEngine,
			}

			captured := newDelegationCapture(bus)

			delegateTool := engine.NewDelegateTool(failingEngines, delegation, "orchestrator").
				WithSessionManager(mgr).
				WithEventBus(bus)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "failing-agent",
					"message":       "this will fail",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)
			Expect(err).To(HaveOccurred())

			Expect(captured.failedEvents()).To(HaveLen(1),
				"a stream error must produce exactly one delegation.failed")
			Expect(captured.failedEvents()[0].Data.Error).NotTo(BeEmpty(),
				"delegation.failed must carry the failing path's error message")
			Expect(captured.completedEvents()).To(BeEmpty(),
				"delegation.completed must not fire on the failure path")
		})
	})

	Context("executeSync delegation-not-allowed path (pre-resolve failure)", func() {
		It("does not publish a started or failed event when the canDelegate gate refuses up-front", func() {
			captured := newDelegationCapture(bus)

			disallowed := agent.Delegation{CanDelegate: false}
			delegateTool := engine.NewDelegateTool(engines, disallowed, "orchestrator").
				WithSessionManager(mgr).
				WithEventBus(bus)

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "rejected",
				},
			}

			_, err := delegateTool.Execute(context.Background(), input)
			Expect(err).To(HaveOccurred())

			// CanDelegate refusal happens in resolveTargetWithOptions
			// before executeSync is reached, so the bus sees nothing.
			// This pin guards against future refactors that move the
			// gate into executeSync without updating the publish sites.
			Expect(captured.startedEvents()).To(BeEmpty())
			Expect(captured.failedEvents()).To(BeEmpty())
			Expect(captured.completedEvents()).To(BeEmpty())
		})
	})

	Context("executeAsync background mode", func() {
		It("publishes delegation.started carrying the task id as the child session id before launching the goroutine", func() {
			captured := newDelegationCapture(bus)

			bgManager := engine.NewBackgroundTaskManager()
			parent, err := mgr.CreateSession("orchestrator")
			Expect(err).NotTo(HaveOccurred())

			delegateTool := engine.NewDelegateToolWithBackground(
				engines, delegation, "orchestrator", bgManager, nil,
			).
				WithSessionManager(mgr).
				WithEventBus(bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, parent.ID)
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

			taskID, ok := result.Metadata["sessionId"].(string)
			Expect(ok).To(BeTrue())
			Expect(taskID).NotTo(BeEmpty())

			started := captured.startedEvents()
			Expect(started).To(HaveLen(1),
				"executeAsync must publish exactly one delegation.started immediately after createChildSession")
			Expect(started[0].Data.ChildSessionID).To(Equal(taskID),
				"async delegation.started's ChildSessionID must equal the task id surfaced to the caller")
			Expect(started[0].Data.ParentSessionID).To(Equal(parent.ID))
		})
	})
})

var _ = Describe("DelegateTool pre-flight candidate check", func() {
	var delegation agent.Delegation

	BeforeEach(func() {
		delegation = agent.Delegation{CanDelegate: true}
	})

	Context("when all failover candidates are rate-limited", func() {
		It("returns an error before opening a stream", func() {
			sentinel := &mockProvider{
				name: "anthropic",
				streamChunks: []provider.StreamChunk{
					{Content: "must not be streamed", Done: true},
				},
			}

			health := failover.NewHealthManager()
			registry := provider.NewRegistry()
			registry.Register(sentinel)
			manager := failover.NewManager(registry, health, 5*time.Minute)
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			})
			health.MarkRateLimited("anthropic", "claude-sonnet-4-6", time.Now().Add(1*time.Hour))

			targetEngine := engine.New(engine.Config{
				FailoverManager: manager,
				Manifest: agent.Manifest{
					ID:                "restricted-agent",
					Name:              "Restricted Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are restricted."},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"restricted-agent": targetEngine},
				delegation, "orchestrator",
			)

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "restricted-agent",
					"message":       "Do something",
				},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("no available model candidates"))
			Expect(sentinel.capturedRequest).To(BeNil(),
				"Stream must never be opened when all candidates are rate-limited")
		})
	})

	Context("when at least one candidate is healthy", func() {
		It("proceeds to stream without error", func() {
			sentinel := &mockProvider{
				name: "anthropic",
				streamChunks: []provider.StreamChunk{
					{Content: "response", Done: true},
				},
			}

			health := failover.NewHealthManager()
			registry := provider.NewRegistry()
			registry.Register(sentinel)
			manager := failover.NewManager(registry, health, 5*time.Minute)
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			})

			targetEngine := engine.New(engine.Config{
				ChatProvider:    sentinel,
				FailoverManager: manager,
				Manifest: agent.Manifest{
					ID:                "healthy-agent",
					Name:              "Healthy Agent",
					Instructions:      agent.Instructions{SystemPrompt: "You are healthy."},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"healthy-agent": targetEngine},
				delegation, "orchestrator",
			)

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "healthy-agent",
					"message":       "Do something",
				},
			})

			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("DelegateTool parent model/provider override isolation", func() {
	// Regression: when the parent session selects a specific provider/model
	// (e.g. github-copilot), that selection must NOT propagate into child
	// delegate engines via context. The delegate must use its own configured
	// failover preferences.
	It("does not forward the parent session's ProviderOverrideKey to the child engine", func() {
		childProvider := &mockProvider{
			name: "anthropic",
			streamChunks: []provider.StreamChunk{
				{Content: "delegate response", Done: true},
			},
		}

		health := failover.NewHealthManager()
		registry := provider.NewRegistry()
		registry.Register(childProvider)
		manager := failover.NewManager(registry, health, 5*time.Minute)
		manager.SetBasePreferences([]provider.ModelPreference{
			{Provider: "anthropic", Model: "claude-sonnet-4-6"},
		})

		targetEngine := engine.New(engine.Config{
			ChatProvider:    childProvider,
			FailoverManager: manager,
			Manifest: agent.Manifest{
				ID:                "explorer",
				Name:              "Codebase Explorer",
				Instructions:      agent.Instructions{SystemPrompt: "You explore code."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})

		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"explorer": targetEngine},
			agent.Delegation{CanDelegate: true}, "orchestrator",
		)

		// Simulate a parent session that has github-copilot selected.
		parentCtx := context.WithValue(context.Background(), session.ProviderOverrideKey{}, "github")
		parentCtx = context.WithValue(parentCtx, session.ModelOverrideKey{}, "copilot-gpt-4o")

		_, err := delegateTool.Execute(parentCtx, tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "explorer",
				"message":       "Find patterns",
			},
		})

		Expect(err).NotTo(HaveOccurred())
		Expect(childProvider.capturedRequest).NotTo(BeNil())
		Expect(childProvider.capturedRequest.Provider).NotTo(Equal("github"),
			"delegate must not inherit the parent's ProviderOverrideKey")
		Expect(childProvider.capturedRequest.Model).NotTo(Equal("copilot-gpt-4o"),
			"delegate must not inherit the parent's ModelOverrideKey")
	})
})

// delegationCapture is a thread-safe sink for delegation lifecycle events
// published onto an `*eventbus.EventBus`. It subscribes at construction so
// every event published after `newDelegationCapture` returns lands in the
// per-status slices. The mutex keeps reads safe against the publisher
// goroutine (the bus invokes handlers synchronously on the publisher
// goroutine, but tests may probe state from another goroutine).
type delegationCapture struct {
	mu        sync.Mutex
	started   []*events.DelegationStartedEvent
	completed []*events.DelegationCompletedEvent
	failed    []*events.DelegationFailedEvent
}

func newDelegationCapture(bus *eventbus.EventBus) *delegationCapture {
	c := &delegationCapture{}
	bus.Subscribe(events.EventDelegationStarted, func(ev any) {
		if e, ok := ev.(*events.DelegationStartedEvent); ok {
			c.mu.Lock()
			c.started = append(c.started, e)
			c.mu.Unlock()
		}
	})
	bus.Subscribe(events.EventDelegationCompleted, func(ev any) {
		if e, ok := ev.(*events.DelegationCompletedEvent); ok {
			c.mu.Lock()
			c.completed = append(c.completed, e)
			c.mu.Unlock()
		}
	})
	bus.Subscribe(events.EventDelegationFailed, func(ev any) {
		if e, ok := ev.(*events.DelegationFailedEvent); ok {
			c.mu.Lock()
			c.failed = append(c.failed, e)
			c.mu.Unlock()
		}
	})
	return c
}

func (c *delegationCapture) startedEvents() []*events.DelegationStartedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*events.DelegationStartedEvent, len(c.started))
	copy(out, c.started)
	return out
}

func (c *delegationCapture) completedEvents() []*events.DelegationCompletedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*events.DelegationCompletedEvent, len(c.completed))
	copy(out, c.completed)
	return out
}

func (c *delegationCapture) failedEvents() []*events.DelegationFailedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*events.DelegationFailedEvent, len(c.failed))
	copy(out, c.failed)
	return out
}
