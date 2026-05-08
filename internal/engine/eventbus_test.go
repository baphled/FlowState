package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("EventBus Integration", func() {
	var (
		chatProvider *mockProvider
		manifest     agent.Manifest
	)

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-chat-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Hello", Done: true},
			},
		}
		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}
	})

	Describe("EventBus accessor", func() {
		It("returns a non-nil EventBus created at construction", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			Expect(eng.EventBus()).NotTo(BeNil())
		})
		It("returns the same EventBus instance on repeated calls", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			Expect(eng.EventBus()).To(BeIdenticalTo(eng.EventBus()))
		})
	})

	Describe("Config.EventBus injection", func() {
		Context("when Config.EventBus is provided", func() {
			It("uses the provided EventBus instead of creating a new one", func() {
				sharedBus := eventbus.NewEventBus()
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					EventBus:     sharedBus,
				})
				Expect(eng.EventBus()).To(BeIdenticalTo(sharedBus))
			})

			It("delivers events to subscribers on the provided bus", func() {
				sharedBus := eventbus.NewEventBus()
				var mu sync.Mutex
				var received []events.SessionEventData
				sharedBus.Subscribe("session.created", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					EventBus:     sharedBus,
				})
				store := newTempFileContextStore()
				DeferCleanup(func() { cleanupStore(store) })
				eng.SetContextStore(store, "shared-session")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(HaveLen(1))
				Expect(received[0].SessionID).To(Equal("shared-session"))
			})
		})

		Context("when Config.EventBus is nil", func() {
			It("creates a new EventBus as fallback", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})
				Expect(eng.EventBus()).NotTo(BeNil())
			})
		})
	})

	Describe("session events", func() {
		Context("when SetContextStore is called with a non-nil store", func() {
			It("publishes a session.created event", func() {
				eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
				var mu sync.Mutex
				var received []events.SessionEventData
				eng.EventBus().Subscribe("session.created", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				store := newTempFileContextStore()
				DeferCleanup(func() { cleanupStore(store) })
				eng.SetContextStore(store, "test-session")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(HaveLen(1))
				Expect(received[0].Action).To(Equal("created"))
			})
		})
		Context("when SetContextStore is called with nil", func() {
			It("publishes a session.ended event", func() {
				eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
				var mu sync.Mutex
				var received []events.SessionEventData
				eng.EventBus().Subscribe("session.ended", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				store := newTempFileContextStore()
				DeferCleanup(func() { cleanupStore(store) })
				eng.SetContextStore(store, "test-session")
				eng.SetContextStore(nil, "")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(HaveLen(1))
				Expect(received[0].Action).To(Equal("ended"))
			})
		})
		Context("when SetContextStore is called with nil but no previous store", func() {
			It("does not publish a session.ended event", func() {
				eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
				var mu sync.Mutex
				var received []events.SessionEventData
				eng.EventBus().Subscribe("session.ended", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				eng.SetContextStore(nil, "")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(BeEmpty())
			})
		})
	})

	Describe("tool events", func() {
		var (
			testTool     *executableMockTool
			seqProvider  *streamSequenceProvider
			toolManifest agent.Manifest
		)
		BeforeEach(func() {
			testTool = &executableMockTool{name: "test_tool", description: "A test tool", execResult: tool.Result{Output: "tool output"}}
			seqProvider = &streamSequenceProvider{
				name: "test-provider",
				sequences: [][]provider.StreamChunk{
					{{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_evt_test", Name: "test_tool", Arguments: map[string]interface{}{"key": "value"}}}},
					{{Content: "Done.", Done: true}},
				},
			}
			toolManifest = agent.Manifest{ID: "test-agent", Name: "Test Agent", Instructions: agent.Instructions{SystemPrompt: "You are a helpful assistant."}, ContextManagement: agent.DefaultContextManagement()}
		})

		It("publishes tool.execute.before before tool execution", func() {
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var beforeEvents []*events.ToolEvent
			eng.EventBus().Subscribe("tool.execute.before", func(event any) {
				if te, ok := event.(*events.ToolEvent); ok {
					mu.Lock()
					beforeEvents = append(beforeEvents, te)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(beforeEvents).To(HaveLen(1))
			Expect(beforeEvents[0].Data.ToolName).To(Equal("test_tool"))
			Expect(beforeEvents[0].Data.Args).To(HaveKeyWithValue("key", "value"))
		})

		It("publishes tool.execute.after after tool execution", func() {
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var afterEvents []*events.ToolEvent
			eng.EventBus().Subscribe("tool.execute.after", func(event any) {
				if te, ok := event.(*events.ToolEvent); ok {
					mu.Lock()
					afterEvents = append(afterEvents, te)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(afterEvents).To(HaveLen(1))
			Expect(afterEvents[0].Data.ToolName).To(Equal("test_tool"))
			Expect(afterEvents[0].Data.Result).To(Equal("tool output"))
		})

		It("publishes tool.execute.after with error when tool fails", func() {
			testTool.execErr = errors.New("tool failed")
			seqProvider.sequences = [][]provider.StreamChunk{
				{{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_fail", Name: "test_tool", Arguments: map[string]interface{}{}}}},
				{{Content: "Tool failed.", Done: true}},
			}
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var afterEvents []*events.ToolEvent
			eng.EventBus().Subscribe("tool.execute.after", func(event any) {
				if te, ok := event.(*events.ToolEvent); ok {
					mu.Lock()
					afterEvents = append(afterEvents, te)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(afterEvents).To(HaveLen(1))
			Expect(afterEvents[0].Data.Error).To(MatchError("tool failed"))
		})

		It("publishes before event prior to after event", func() {
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var order []string
			eng.EventBus().Subscribe("tool.execute.before", func(_ any) { mu.Lock(); order = append(order, "before"); mu.Unlock() })
			eng.EventBus().Subscribe("tool.execute.after", func(_ any) { mu.Lock(); order = append(order, "after"); mu.Unlock() })
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(order).To(Equal([]string{"before", "after"}))
		})

		// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026) §"Engine wiring".
		// The publishToolBeforeEvent / publishToolAfterEvent seams must
		// stamp the upstream provider-scoped ToolCallID (P14b) and the
		// FlowState session-scoped InternalToolCallID (P14) on every
		// tool.execute.* event the engine publishes. Without these, the
		// SSE projector cannot key its SwarmEvents on a stable id and
		// downstream coalesce drops.
		Context("with correlation IDs", func() {
			It("stamps ToolCallID and InternalToolCallID on tool.execute.before", func() {
				eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
				var mu sync.Mutex
				var beforeEvents []*events.ToolEvent
				eng.EventBus().Subscribe("tool.execute.before", func(event any) {
					if te, ok := event.(*events.ToolEvent); ok {
						mu.Lock()
						beforeEvents = append(beforeEvents, te)
						mu.Unlock()
					}
				})
				chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain channel
				}
				mu.Lock()
				defer mu.Unlock()
				Expect(beforeEvents).To(HaveLen(1))
				Expect(beforeEvents[0].Data.ToolCallID).To(Equal("call_evt_test"),
					"the upstream provider tool-use id (P14b) must propagate from the chunk to the bus event")
				Expect(beforeEvents[0].Data.InternalToolCallID).NotTo(BeEmpty(),
					"the FlowState-internal correlation id (P14) must be stamped at publication; "+
						"downstream consumers key on this for failover-stable SwarmEvent IDs")
			})

			It("stamps the same correlation IDs on tool.execute.after and tool.execute.result (success path)", func() {
				eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
				var mu sync.Mutex
				var afterEvents []*events.ToolEvent
				var resultEvents []*events.ToolExecuteResultEvent
				eng.EventBus().Subscribe("tool.execute.after", func(event any) {
					if te, ok := event.(*events.ToolEvent); ok {
						mu.Lock()
						afterEvents = append(afterEvents, te)
						mu.Unlock()
					}
				})
				eng.EventBus().Subscribe("tool.execute.result", func(event any) {
					if re, ok := event.(*events.ToolExecuteResultEvent); ok {
						mu.Lock()
						resultEvents = append(resultEvents, re)
						mu.Unlock()
					}
				})
				chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain channel
				}
				mu.Lock()
				defer mu.Unlock()
				Expect(afterEvents).To(HaveLen(1))
				Expect(afterEvents[0].Data.ToolCallID).To(Equal("call_evt_test"))
				Expect(afterEvents[0].Data.InternalToolCallID).NotTo(BeEmpty())

				Expect(resultEvents).To(HaveLen(1),
					"on the success path the engine publishes tool.execute.result alongside tool.execute.after")
				Expect(resultEvents[0].Data.ToolCallID).To(Equal("call_evt_test"))
				Expect(resultEvents[0].Data.InternalToolCallID).NotTo(BeEmpty())
				Expect(resultEvents[0].Data.InternalToolCallID).To(Equal(afterEvents[0].Data.InternalToolCallID),
					"after and result must share the same InternalToolCallID — both fire from the same call site")
			})

			It("stamps correlation IDs on tool.execute.error (failure path)", func() {
				testTool.execErr = errors.New("tool failed")
				seqProvider.sequences = [][]provider.StreamChunk{
					{{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_fail_id", Name: "test_tool", Arguments: map[string]interface{}{}}}},
					{{Content: "Tool failed.", Done: true}},
				}
				eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
				var mu sync.Mutex
				var errorEvents []*events.ToolExecuteErrorEvent
				eng.EventBus().Subscribe("tool.execute.error", func(event any) {
					if ee, ok := event.(*events.ToolExecuteErrorEvent); ok {
						mu.Lock()
						errorEvents = append(errorEvents, ee)
						mu.Unlock()
					}
				})
				chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain channel
				}
				mu.Lock()
				defer mu.Unlock()
				Expect(errorEvents).To(HaveLen(1),
					"the failure path publishes tool.execute.error alongside tool.execute.after")
				Expect(errorEvents[0].Data.ToolCallID).To(Equal("call_fail_id"))
				Expect(errorEvents[0].Data.InternalToolCallID).NotTo(BeEmpty())
			})
		})
	})

	Describe("provider error events", func() {
		It("publishes provider.error when provider stream fails", func() {
			chatProvider.streamErr = errors.New("provider unavailable")
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			var mu sync.Mutex
			var providerErrors []*events.ProviderErrorEvent
			eng.EventBus().Subscribe("provider.error", func(event any) {
				if pe, ok := event.(*events.ProviderErrorEvent); ok {
					mu.Lock()
					providerErrors = append(providerErrors, pe)
					mu.Unlock()
				}
			})
			_, err := eng.Stream(context.Background(), "test-agent", "Hello")
			Expect(err).To(HaveOccurred())
			mu.Lock()
			defer mu.Unlock()
			Expect(providerErrors).To(HaveLen(1))
			Expect(providerErrors[0].Data.ProviderName).To(Equal("test-chat-provider"))
			Expect(providerErrors[0].Data.Error).To(MatchError("provider unavailable"))
		})

		It("publishes provider.error when provider fails during tool loop", func() {
			fp := &failOnSecondCallProvider{
				name: "fail-on-second",
				firstChunks: []provider.StreamChunk{
					{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_err", Name: "test_tool", Arguments: map[string]interface{}{}}},
				},
				secondErr: errors.New("provider down on retry"),
			}
			tt := &executableMockTool{name: "test_tool", description: "A test tool", execResult: tool.Result{Output: "ok"}}
			eng := engine.New(engine.Config{ChatProvider: fp, Manifest: manifest, Tools: []tool.Tool{tt}})
			var mu sync.Mutex
			var providerErrors []*events.ProviderErrorEvent
			eng.EventBus().Subscribe("provider.error", func(event any) {
				if pe, ok := event.(*events.ProviderErrorEvent); ok {
					mu.Lock()
					providerErrors = append(providerErrors, pe)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(providerErrors).To(HaveLen(1))
			Expect(providerErrors[0].Data.Error).To(MatchError("provider down on retry"))
		})
	})

	Describe("provider request events", func() {
		It("publishes provider.request before the initial provider stream call", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			var mu sync.Mutex
			var requestEvents []*events.ProviderRequestEvent
			eng.EventBus().Subscribe("provider.request", func(event any) {
				if pe, ok := event.(*events.ProviderRequestEvent); ok {
					mu.Lock()
					requestEvents = append(requestEvents, pe)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(requestEvents).To(HaveLen(1))
			Expect(requestEvents[0].EventType()).To(Equal("provider.request"))
			Expect(requestEvents[0].Data.AgentID).To(Equal("test-agent"))
			Expect(requestEvents[0].Data.ProviderName).To(Equal("test-chat-provider"))
			Expect(requestEvents[0].Data.Request.Messages).NotTo(BeEmpty())
			Expect(requestEvents[0].Data.Request.Provider).To(Equal("test-chat-provider"))
		})

		It("publishes provider.request before each tool loop retry", func() {
			testTool := &executableMockTool{name: "test_tool", description: "A test tool", execResult: tool.Result{Output: "tool output"}}
			seqProvider := &streamSequenceProvider{
				name: "test-provider",
				sequences: [][]provider.StreamChunk{
					{{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_req_test", Name: "test_tool", Arguments: map[string]interface{}{"key": "value"}}}},
					{{Content: "Done.", Done: true}},
				},
			}
			toolManifest := agent.Manifest{ID: "test-agent", Name: "Test Agent", Instructions: agent.Instructions{SystemPrompt: "You are a helpful assistant."}, ContextManagement: agent.DefaultContextManagement()}
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var requestEvents []*events.ProviderRequestEvent
			eng.EventBus().Subscribe("provider.request", func(event any) {
				if pe, ok := event.(*events.ProviderRequestEvent); ok {
					mu.Lock()
					requestEvents = append(requestEvents, pe)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(requestEvents).To(HaveLen(2))
			Expect(requestEvents[0].Data.AgentID).To(Equal("test-agent"))
			Expect(requestEvents[1].Data.AgentID).To(Equal("test-agent"))
			Expect(requestEvents[1].Data.Request.Messages).NotTo(BeEmpty())
		})
	})

	Describe("agent.switched events", func() {
		Context("when Stream triggers a manifest switch", func() {
			It("includes the session ID from context in the agent.switched event", func() {
				initialManifest := agent.Manifest{
					ID:   "initial-agent",
					Name: "Initial Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the initial agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				switchedManifest := agent.Manifest{
					ID:   "switched-agent",
					Name: "Switched Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the switched agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				registry := agent.NewRegistry()
				registry.Register(&switchedManifest)

				eng := engine.New(engine.Config{
					ChatProvider:  chatProvider,
					Manifest:      initialManifest,
					AgentRegistry: registry,
				})

				var mu sync.Mutex
				var switchedEvents []*events.AgentSwitchedEvent
				eng.EventBus().Subscribe(events.EventAgentSwitched, func(event any) {
					if se, ok := event.(*events.AgentSwitchedEvent); ok {
						mu.Lock()
						switchedEvents = append(switchedEvents, se)
						mu.Unlock()
					}
				})

				ctx := context.WithValue(context.Background(), session.IDKey{}, "test-session-id")
				chunks, err := eng.Stream(ctx, "switched-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain channel
				}

				mu.Lock()
				defer mu.Unlock()
				Expect(switchedEvents).To(HaveLen(1))
				Expect(switchedEvents[0].Data.SessionID).To(Equal("test-session-id"))
				Expect(switchedEvents[0].Data.FromAgent).To(Equal("initial-agent"))
				Expect(switchedEvents[0].Data.ToAgent).To(Equal("switched-agent"))
			})
		})

		Context("when SetManifest is called after Stream has established a session", func() {
			It("includes the last known session ID in the agent.switched event", func() {
				initialManifest := agent.Manifest{
					ID:   "base-agent",
					Name: "Base Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the base agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				newManifest := agent.Manifest{
					ID:   "new-agent",
					Name: "New Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the new agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     initialManifest,
				})

				var mu sync.Mutex
				var switchedEvents []*events.AgentSwitchedEvent
				eng.EventBus().Subscribe(events.EventAgentSwitched, func(event any) {
					if se, ok := event.(*events.AgentSwitchedEvent); ok {
						mu.Lock()
						switchedEvents = append(switchedEvents, se)
						mu.Unlock()
					}
				})

				ctx := context.WithValue(context.Background(), session.IDKey{}, "established-session-id")
				chunks, err := eng.Stream(ctx, "", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain channel
				}

				eng.SetManifest(newManifest)

				mu.Lock()
				defer mu.Unlock()
				Expect(switchedEvents).To(HaveLen(1))
				Expect(switchedEvents[0].Data.SessionID).To(Equal("established-session-id"))
				Expect(switchedEvents[0].Data.FromAgent).To(Equal("base-agent"))
				Expect(switchedEvents[0].Data.ToAgent).To(Equal("new-agent"))
			})
		})
	})

	Describe("EventBus does not affect existing hook chain", func() {
		It("does not interfere with normal stream operation", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			Expect(eng.EventBus()).NotTo(BeNil())
			chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())
			var received []provider.StreamChunk
			for chunk := range chunks {
				received = append(received, chunk)
			}
			Expect(received).To(HaveLen(1))
			Expect(received[0].Content).To(Equal("Hello"))
		})
	})

	// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026) §"Engine wiring".
	// runSwarmGates and dispatchMemberGates wrap their existing
	// `swarm.Dispatch` call sites with bus publication so the silent
	// swallow at orchestrator.go:275 / server.go:582-585 stops being the
	// only diagnostic path. Pass-event policy: halt-class only on
	// `gate.failed`; batch-level `gate.evaluating` and `gate.passed`
	// fire once per dispatch, NOT per gate; continue-class and
	// warn-class failures stay log-only.
	Describe("publishes gate lifecycle events from runSwarmGates", func() {
		It("publishes gate.evaluating and gate.passed for a clean pre-swarm batch", func() {
			store := coordination.NewMemoryStore()
			Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())

			runner := &recordingRunner{}
			gates := []swarm.GateSpec{
				{Name: "envelope-check", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
				{Name: "shape-check", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
			}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			bus := eventbus.NewEventBus()
			delegateTool.WithEventBus(bus)

			capture := newGateCapture(bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess-1")
			_, err := delegateTool.Execute(ctx, reviewerDelegateInput())
			Expect(err).NotTo(HaveOccurred())

			evaluatingPre := capture.evaluatingFor(swarm.LifecyclePreSwarm)
			Expect(evaluatingPre).To(HaveLen(1),
				"runSwarmGates must publish exactly one gate.evaluating per non-empty pre-swarm batch")
			Expect(evaluatingPre[0].Data.GateCount).To(Equal(2),
				"GateCount carries the batch size so the activity pane can render 'evaluating N gates…'")
			Expect(evaluatingPre[0].Data.SwarmID).To(Equal("planning-loop"))
			Expect(evaluatingPre[0].Data.SessionID).To(Equal("parent-sess-1"),
				"the parent session id propagates through the publish helper so subscribers can filter")
			Expect(evaluatingPre[0].Data.MemberID).To(BeEmpty(),
				"swarm-level gate.evaluating carries no MemberID")

			passedPre := capture.passedFor(swarm.LifecyclePreSwarm)
			Expect(passedPre).To(HaveLen(1),
				"runSwarmGates must publish exactly one gate.passed when the batch completes without halt")
			Expect(passedPre[0].Data.GateCount).To(Equal(2))
			Expect(capture.failed()).To(BeEmpty(),
				"a clean batch must NOT publish gate.failed")
		})

		It("publishes gate.failed (and not gate.passed) for a halting pre-swarm batch with the typed *swarm.GateError fields populated", func() {
			store := coordination.NewMemoryStore()
			runner := &recordingRunner{
				fail: map[string]error{
					"envelope-check": &swarm.GateError{
						GateName: "envelope-check",
						GateKind: "builtin:result-schema",
						When:     swarm.LifecyclePreSwarm,
						SwarmID:  "planning-loop",
						Reason:   "chain_prefix missing",
					},
				},
			}
			gates := []swarm.GateSpec{
				{Name: "envelope-check", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
			}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			bus := eventbus.NewEventBus()
			delegateTool.WithEventBus(bus)

			capture := newGateCapture(bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess-fail")
			_, err := delegateTool.Execute(ctx, reviewerDelegateInput())
			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())

			failed := capture.failed()
			Expect(failed).To(HaveLen(1),
				"runSwarmGates must publish exactly one gate.failed per halting gate; halt-class only")
			Expect(failed[0].Data.GateName).To(Equal("envelope-check"))
			Expect(failed[0].Data.GateKind).To(Equal("builtin:result-schema"))
			Expect(failed[0].Data.Lifecycle).To(Equal(swarm.LifecyclePreSwarm))
			Expect(failed[0].Data.SwarmID).To(Equal("planning-loop"))
			Expect(failed[0].Data.SessionID).To(Equal("parent-sess-fail"),
				"SessionID propagates so the API SSE bridge and TUI subscriber can filter on the active session")
			Expect(failed[0].Data.MemberID).To(BeEmpty(),
				"pre-swarm failures have no member context — MemberID is empty")
			Expect(failed[0].Data.Reason).To(ContainSubstring("chain_prefix missing"))

			Expect(capture.passedFor(swarm.LifecyclePreSwarm)).To(BeEmpty(),
				"a halting batch must NOT publish gate.passed alongside gate.failed")
		})
	})

	Describe("publishes member-scoped gate events from dispatchMemberGates", func() {
		It("publishes gate.evaluating, gate.passed and gate.failed with MemberID populated for member-pre / member-post lifecycle points", func() {
			store := coordination.NewMemoryStore()
			Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())

			runner := &recordingRunner{}
			gates := []swarm.GateSpec{
				{Name: "pre-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePreMember, Target: "plan-reviewer"},
				{Name: "post-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
			}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			bus := eventbus.NewEventBus()
			delegateTool.WithEventBus(bus)

			capture := newGateCapture(bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess-mem")
			_, err := delegateTool.Execute(ctx, reviewerDelegateInput())
			Expect(err).NotTo(HaveOccurred())

			Expect(capture.evaluatingFor(swarm.LifecyclePreMember)).To(HaveLen(1),
				"dispatchMemberGates must publish gate.evaluating once per member-pre batch")
			Expect(capture.evaluatingFor(swarm.LifecyclePreMember)[0].Data.MemberID).To(Equal("plan-reviewer"))
			Expect(capture.evaluatingFor(swarm.LifecyclePreMember)[0].Data.SessionID).To(Equal("parent-sess-mem"))

			Expect(capture.passedFor(swarm.LifecyclePreMember)).To(HaveLen(1))
			Expect(capture.passedFor(swarm.LifecyclePreMember)[0].Data.MemberID).To(Equal("plan-reviewer"))

			Expect(capture.evaluatingFor(swarm.LifecyclePostMember)).To(HaveLen(1))
			Expect(capture.passedFor(swarm.LifecyclePostMember)).To(HaveLen(1))
			Expect(capture.failed()).To(BeEmpty())
		})

		It("publishes gate.failed with MemberID populated when a member-post gate halts", func() {
			store := coordination.NewMemoryStore()
			Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())
			runner := &recordingRunner{
				fail: map[string]error{
					"post-member-plan-reviewer": &swarm.GateError{
						GateName: "post-member-plan-reviewer",
						GateKind: "builtin:result-schema",
						When:     swarm.LifecyclePostMember,
						MemberID: "plan-reviewer",
						SwarmID:  "planning-loop",
						Reason:   "schema validation failed",
					},
				},
			}
			gates := []swarm.GateSpec{
				{Name: "post-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
			}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			bus := eventbus.NewEventBus()
			delegateTool.WithEventBus(bus)

			capture := newGateCapture(bus)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess-mem-fail")
			_, err := delegateTool.Execute(ctx, reviewerDelegateInput())
			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())

			failed := capture.failed()
			Expect(failed).To(HaveLen(1))
			Expect(failed[0].Data.GateName).To(Equal("post-member-plan-reviewer"))
			Expect(failed[0].Data.MemberID).To(Equal("plan-reviewer"),
				"member-post failures carry the failing member's id so the surface can attribute the halt")
			Expect(failed[0].Data.Lifecycle).To(Equal(swarm.LifecyclePostMember))
			Expect(failed[0].Data.SessionID).To(Equal("parent-sess-mem-fail"))
		})
	})

	Describe("gate.failed payload parity across SSE bridge and TUI subscriber", func() {
		// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026)
		// §"Cross-cutting parity assertion". Both consumers — the API
		// SSE bridge (newGateFailedHandler in internal/api/event_bridge.go)
		// and the TUI chat-intent subscriber (subscribeToFailoverEvents
		// in internal/tui/intents/chat/intent.go) — bind to the same
		// `events.GateFailedEvent` shape on the same bus. The parity
		// guarantee is that a single publisher emits one event whose
		// `Data events.GateEventData` carries every field both surfaces
		// project: GateName, GateKind, Lifecycle, MemberID, SwarmID,
		// SessionID, Reason, Cause, CoordStoreKeys.
		//
		// This test pins the parity by attaching two real subscribers
		// to the same bus, driving a halting member-post gate, and
		// asserting both subscribers receive the SAME pointer (the
		// fire-and-forget bus dispatches the same event instance to
		// every handler) with all per-surface-rendered fields populated.

		It("delivers the same *GateFailedEvent instance to both the SSE-bridge-shaped subscriber and the TUI-shaped subscriber", func() {
			store := coordination.NewMemoryStore()
			Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())

			runner := &recordingRunner{
				fail: map[string]error{
					"post-member-plan-reviewer": &swarm.GateError{
						GateName: "post-member-plan-reviewer",
						GateKind: "builtin:result-schema",
						When:     swarm.LifecyclePostMember,
						MemberID: "plan-reviewer",
						SwarmID:  "planning-loop",
						Reason:   "schema validation failed: required property missing",
					},
				},
			}
			gates := []swarm.GateSpec{
				{Name: "post-member-plan-reviewer", Kind: "builtin:result-schema", When: swarm.LifecyclePostMember, Target: "plan-reviewer"},
			}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)

			bus := eventbus.NewEventBus()
			delegateTool.WithEventBus(bus)

			// Subscriber A: shaped like the API SSE bridge handler —
			// extracts the typed fields the wire payload carries
			// (gate_name / lifecycle / member_id / swarm_id / reason
			// / cause / coord_store_keys / session_id) into a flat
			// projection.
			var sseProjection map[string]any
			var sseMu sync.Mutex
			bus.Subscribe(events.EventGateFailed, func(msg any) {
				ge, ok := msg.(*events.GateFailedEvent)
				if !ok {
					return
				}
				sseMu.Lock()
				sseProjection = map[string]any{
					"gate_name":  ge.Data.GateName,
					"gate_kind":  ge.Data.GateKind,
					"lifecycle":  ge.Data.Lifecycle,
					"member_id":  ge.Data.MemberID,
					"swarm_id":   ge.Data.SwarmID,
					"session_id": ge.Data.SessionID,
					"reason":     ge.Data.Reason,
					"cause":      ge.Data.Cause,
				}
				sseMu.Unlock()
			})

			// Subscriber B: shaped like the TUI subscriber — extracts
			// the same fields via the same struct, no provider-side
			// translation. Both subscribers consume the same in-process
			// payload; the fire-and-forget bus delivers the same event
			// pointer to every handler.
			var tuiProjection map[string]any
			var tuiMu sync.Mutex
			bus.Subscribe(events.EventGateFailed, func(msg any) {
				ge, ok := msg.(*events.GateFailedEvent)
				if !ok {
					return
				}
				tuiMu.Lock()
				tuiProjection = map[string]any{
					"gate_name":  ge.Data.GateName,
					"gate_kind":  ge.Data.GateKind,
					"lifecycle":  ge.Data.Lifecycle,
					"member_id":  ge.Data.MemberID,
					"swarm_id":   ge.Data.SwarmID,
					"session_id": ge.Data.SessionID,
					"reason":     ge.Data.Reason,
					"cause":      ge.Data.Cause,
				}
				tuiMu.Unlock()
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parity-sess")
			_, err := delegateTool.Execute(ctx, reviewerDelegateInput())
			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())

			sseMu.Lock()
			tuiMu.Lock()
			defer sseMu.Unlock()
			defer tuiMu.Unlock()

			Expect(sseProjection).NotTo(BeNil(),
				"SSE-bridge-shaped subscriber must receive the gate.failed event")
			Expect(tuiProjection).NotTo(BeNil(),
				"TUI-shaped subscriber must receive the gate.failed event")

			// Parity: both subscribers project the same fields from
			// the same bus payload. The user-facing assertion the whole
			// initiative was meant to establish.
			Expect(sseProjection).To(Equal(tuiProjection),
				"the SSE bridge and the TUI subscriber must extract identical fields from the same gate.failed bus event")
			Expect(sseProjection["gate_name"]).To(Equal("post-member-plan-reviewer"))
			Expect(sseProjection["lifecycle"]).To(Equal(swarm.LifecyclePostMember))
			Expect(sseProjection["member_id"]).To(Equal("plan-reviewer"))
			Expect(sseProjection["session_id"]).To(Equal("parity-sess"))
		})
	})

	Describe("gate publication is a no-op when the bus is not wired", func() {
		It("does not panic and behaves identically when WithEventBus is unset", func() {
			store := coordination.NewMemoryStore()
			Expect(store.Set("planning/plan-reviewer/review", validVerdictPayload())).To(Succeed())

			runner := &recordingRunner{}
			gates := []swarm.GateSpec{
				{Name: "envelope-check", Kind: "builtin:result-schema", When: swarm.LifecyclePreSwarm},
			}
			engines, _ := reviewerEnginesWithContext(swarmContextWithGates(gates))
			delegateTool := newDelegateToolWithRunner(engines, store, runner)
			// No WithEventBus call — historical pre-bridge behaviour.

			_, err := delegateTool.Execute(context.Background(), reviewerDelegateInput())
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// gateCapture is a thread-safe sink for gate lifecycle events
// published onto an `*eventbus.EventBus`. Mirrors delegationCapture
// (delegation_lifecycle_test.go) — same shape, three-status triplet.
type gateCapture struct {
	mu          sync.Mutex
	evaluating  []*events.GateEvaluatingEvent
	passed      []*events.GatePassedEvent
	failedSlice []*events.GateFailedEvent
}

func newGateCapture(bus *eventbus.EventBus) *gateCapture {
	c := &gateCapture{}
	bus.Subscribe(events.EventGateEvaluating, func(ev any) {
		if e, ok := ev.(*events.GateEvaluatingEvent); ok {
			c.mu.Lock()
			c.evaluating = append(c.evaluating, e)
			c.mu.Unlock()
		}
	})
	bus.Subscribe(events.EventGatePassed, func(ev any) {
		if e, ok := ev.(*events.GatePassedEvent); ok {
			c.mu.Lock()
			c.passed = append(c.passed, e)
			c.mu.Unlock()
		}
	})
	bus.Subscribe(events.EventGateFailed, func(ev any) {
		if e, ok := ev.(*events.GateFailedEvent); ok {
			c.mu.Lock()
			c.failedSlice = append(c.failedSlice, e)
			c.mu.Unlock()
		}
	})
	return c
}

func (c *gateCapture) evaluatingFor(lifecycle string) []*events.GateEvaluatingEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*events.GateEvaluatingEvent, 0, len(c.evaluating))
	for _, e := range c.evaluating {
		if e.Data.Lifecycle == lifecycle {
			out = append(out, e)
		}
	}
	return out
}

func (c *gateCapture) passedFor(lifecycle string) []*events.GatePassedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*events.GatePassedEvent, 0, len(c.passed))
	for _, e := range c.passed {
		if e.Data.Lifecycle == lifecycle {
			out = append(out, e)
		}
	}
	return out
}

func (c *gateCapture) failed() []*events.GateFailedEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*events.GateFailedEvent, len(c.failedSlice))
	copy(out, c.failedSlice)
	return out
}

func newTempFileContextStore() *recall.FileContextStore {
	tmpDir, err := os.MkdirTemp("", "engine-eventbus-test-*")
	Expect(err).NotTo(HaveOccurred())
	storePath := filepath.Join(tmpDir, "context.json")
	store, err := recall.NewFileContextStore(storePath, "test-model")
	Expect(err).NotTo(HaveOccurred())
	return store
}

func cleanupStore(_ *recall.FileContextStore) {}

type failOnSecondCallProvider struct {
	name        string
	firstChunks []provider.StreamChunk
	secondErr   error
	callCount   int
}

func (p *failOnSecondCallProvider) Name() string { return p.name }
func (p *failOnSecondCallProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.callCount++
	if p.callCount > 1 {
		return nil, p.secondErr
	}
	ch := make(chan provider.StreamChunk, len(p.firstChunks))
	go func() {
		defer close(ch)
		for i := range p.firstChunks {
			ch <- p.firstChunks[i]
		}
	}()
	return ch, nil
}
func (p *failOnSecondCallProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *failOnSecondCallProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (p *failOnSecondCallProvider) Models() ([]provider.Model, error) { return nil, nil }
