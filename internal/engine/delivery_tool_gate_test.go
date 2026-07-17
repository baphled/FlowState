package engine_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/todo"
)

type erroringStreamSequenceProvider struct {
	streamSequenceProvider
	failOnCall map[int]error
}

func (p *erroringStreamSequenceProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.capturedRequests = append(p.capturedRequests, req)
	if err, ok := p.failOnCall[p.callIndex]; ok {
		p.callIndex++
		return nil, err
	}
	if p.callIndex >= len(p.sequences) {
		ch := make(chan provider.StreamChunk, 16)
		close(ch)
		return ch, nil
	}
	chunks := p.sequences[p.callIndex]
	p.callIndex++
	ch := make(chan provider.StreamChunk, len(chunks))
	go func() {
		defer close(ch)
		for i := range chunks {
			ch <- chunks[i]
		}
	}()
	return ch, nil
}

var _ = Describe("DeliveryToolGate", func() {
	var (
		chatProvider      *streamSequenceProvider
		manifest          agent.Manifest
		coordinationStore *executableMockTool
	)

	BeforeEach(func() {
		chatProvider = &streamSequenceProvider{
			name:      "test-delivery-provider",
			sequences: [][]provider.StreamChunk{},
		}

		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
			Capabilities: agent.Capabilities{
				Tools:         []string{"coordination_store"},
				DeliveryTools: []string{"coordination_store"},
			},
		}

		coordinationStore = &executableMockTool{
			name:        "coordination_store",
			description: "Coordination store tool",
			execResult:  tool.Result{Output: "stored successfully"},
		}
	})

	Context("completes normally when agent has no delivery_tools", func() {
		BeforeEach(func() {
			manifest.Capabilities.DeliveryTools = nil
			chatProvider.sequences = [][]provider.StreamChunk{
				{{Content: "Normal response with no tool calls", Done: true, StopReason: "end_turn"}},
			}
		})

		It("completes without retrying", func() {
			bus := eventbus.NewEventBus()
			retryCount := 0
			bus.Subscribe(events.EventProviderRequestRetry, func(msg any) {
				if _, ok := msg.(*events.ProviderRequestRetryEvent); ok {
					retryCount++
				}
			})

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     bus,
				Manifest:     manifest,
				Tools:        []tool.Tool{coordinationStore},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			for chunk := range chunks {
				collectedContent += chunk.Content
			}

			Expect(chatProvider.callIndex).To(Equal(1),
				"provider must be called exactly once — no retry when no delivery tools are declared")
			Expect(retryCount).To(BeZero(),
				"no retry events must fire when no delivery tools are declared")
			Expect(collectedContent).To(ContainSubstring("Normal response"))
			Expect(coordinationStore.execCalled).To(BeFalse(),
				"tool must not execute when the agent never calls it")
		})
	})

	Context("retries when delivery tool not called", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{{Content: "Now writing to the coordination store.", Done: true, StopReason: "end_turn"}},
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_1",
							Name:      "coordination_store",
							Arguments: map[string]interface{}{"operation": "get", "key": "value"},
						},
					},
					{Done: true, StopReason: "tool_use"},
				},
				{{Content: "Still narrating without tools", Done: true, StopReason: "end_turn"}},
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_2",
							Name:      "coordination_store",
							Arguments: map[string]interface{}{"operation": "set", "key": "output", "value": "report"},
						},
					},
					{Done: true, StopReason: "tool_use"},
				},
				{{Content: "Done after tool execution", Done: true, StopReason: "end_turn"}},
			}
		})

		It("injects corrective messages and retries until delivery tool is called with a write operation", func() {
			bus := eventbus.NewEventBus()
			retryCount := 0
			bus.Subscribe(events.EventProviderRequestRetry, func(msg any) {
				if _, ok := msg.(*events.ProviderRequestRetryEvent); ok {
					retryCount++
				}
			})

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     bus,
				Manifest:     manifest,
				Tools:        []tool.Tool{coordinationStore},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			for chunk := range chunks {
				collectedContent += chunk.Content
			}

			Expect(chatProvider.callIndex).To(Equal(5),
				"provider called five times: initial narration + retry with get (not counted as delivery) + retry narration + retry with set (counted) + final response")
			Expect(chatProvider.ToolNamesForAttempt(1)).To(ContainElement("coordination_store"),
				"the initial work phase may advertise the full declared toolset")
			Expect(chatProvider.ToolNamesForAttempt(2)).To(Equal([]string{"coordination_store"}),
				"the first corrective delivery retry must narrow the advertised tools to delivery tools only")
			Expect(chatProvider.ToolNamesForAttempt(4)).To(Equal([]string{"coordination_store"}),
				"subsequent corrective delivery retries must stay in delivery-only mode")
			Expect(retryCount).To(Equal(4),
				"four retry events fire: delivery-gate retry (seq 0), tool-loop continuation after get (seq 1), delivery-gate retry (seq 2), tool-loop continuation after set (seq 3) — set operation finally counts as delivery in seq 4")
			Expect(coordinationStore.execCalled).To(BeTrue(),
				"delivery tool must be executed after the corrective messages")
			Expect(collectedContent).To(ContainSubstring("Done after tool execution"))
		})
	})

	Context("when todos are incomplete and delivery is still missing", func() {
		It("forces delivery retry before todo continuation", func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{{Content: "I have finished the report and will write it now.", Done: true, StopReason: "end_turn"}},
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_1",
							Name:      "coordination_store",
							Arguments: map[string]interface{}{"operation": "set", "key": "output", "value": "report"},
						},
					},
					{Done: true, StopReason: "tool_use"},
				},
				{{Content: "Done after tool execution", Done: true, StopReason: "end_turn"}},
			}

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     eventbus.NewEventBus(),
				Manifest:     manifest,
				Tools:        []tool.Tool{coordinationStore},
			})

			todoStore := todo.NewMemoryStore()
			sessionID := "delivery-before-todo-session"
			todoStore.Set(sessionID, []todo.Item{{Content: "write final output", Status: "pending", Priority: "high"}})
			eng.SetTodoStoreForTest(todoStore)

			ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)
			chunks, err := eng.Stream(ctx, sessionID, "Hello")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			for chunk := range chunks {
				collectedContent += chunk.Content
			}

			Expect(chatProvider.callIndex).To(BeNumerically(">=", 3),
				"provider should force delivery before any todo continuation; additional turns after a successful write are acceptable")
			Expect(len(chatProvider.capturedRequests)).To(BeNumerically(">=", 3))
			Expect(chatProvider.capturedRequests[1].Messages[len(chatProvider.capturedRequests[1].Messages)-1].Content).
				To(ContainSubstring("You MUST call one of the delivery tools now to persist your results"))
			Expect(chatProvider.capturedRequests[1].Messages[len(chatProvider.capturedRequests[1].Messages)-1].Content).
				NotTo(ContainSubstring("You have an active task"))
			Expect(coordinationStore.execCalled).To(BeTrue())
			Expect(collectedContent).To(ContainSubstring("Done after tool execution"))
		})
	})

	Context("completes when delivery tool is called with a write operation", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_1",
							Name:      "coordination_store",
							Arguments: map[string]interface{}{"operation": "set", "key": "output", "value": "report"},
						},
					},
					{Done: true, StopReason: "tool_use"},
				},
				{{Content: "Done after tool execution", Done: true, StopReason: "end_turn"}},
			}
		})

		It("passes the gate when the delivery tool was called", func() {
			bus := eventbus.NewEventBus()

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     bus,
				Manifest:     manifest,
				Tools:        []tool.Tool{coordinationStore},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			for chunk := range chunks {
				collectedContent += chunk.Content
			}

			Expect(chatProvider.callIndex).To(Equal(2),
				"provider called twice: tool_use turn + end_turn turn — no delivery retry needed")
			Expect(coordinationStore.execCount).To(Equal(1),
				"engine fallback must not write when the model already completed the delivery tool")
			Expect(coordinationStore.execCalled).To(BeTrue(),
				"delivery tool must execute")
			Expect(collectedContent).To(ContainSubstring("Done after tool execution"))
		})
	})

	Context("when all providers fail during the corrective delivery retry", func() {
		It("persists an engine-owned fallback envelope to a reserved coordination key", func() {
			failingProvider := &erroringStreamSequenceProvider{
				streamSequenceProvider: streamSequenceProvider{
					name: "test-delivery-provider",
					sequences: [][]provider.StreamChunk{
						{{Content: "I will write this now.", Done: true, StopReason: "end_turn"}},
					},
				},
				failOnCall: map[int]error{
					1: errors.New("all providers failed: provider openai error [billing/insufficient_quota HTTP 429]"),
				},
			}

			coordinationStore.execCalled = false
			coordinationStore.execCount = 0
			coordinationStore.execResult = tool.Result{Output: "stored successfully"}

			eng := engine.New(engine.Config{
				ChatProvider: failingProvider,
				EventBus:     eventbus.NewEventBus(),
				Manifest:     manifest,
				Tools:        []tool.Tool{coordinationStore},
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "fallback-session")
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())
			for range chunks {
			}

			Expect(coordinationStore.execCalled).To(BeTrue())
			Expect(coordinationStore.execCount).To(Equal(1),
				"only the engine-owned fallback write should run in this scenario")
			Expect(coordinationStore.lastInput.Arguments["operation"]).To(Equal("set"))
			Expect(coordinationStore.lastInput.Arguments["key"]).To(Equal("fallback-session/_engine_fallback/test-agent/delivery_failure"))
			value, ok := coordinationStore.lastInput.Arguments["value"].(string)
			Expect(ok).To(BeTrue())
			Expect(value).To(ContainSubstring(`"status":"delivery_failed_engine_fallback"`))
			Expect(value).To(ContainSubstring(`"failure_summary":"all providers failed:`))
		})
	})

	Context("completes after max retries exhausted", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{{Content: "Narrating without calling tools", Done: true, StopReason: "end_turn"}},
				{{Content: "Still narrating", Done: true, StopReason: "end_turn"}},
				{{Content: "More narration", Done: true, StopReason: "end_turn"}},
				{{Content: "Final narration", Done: true, StopReason: "end_turn"}},
			}
		})

		It("completes after maxDeliveryRetries without calling the delivery tool", func() {
			bus := eventbus.NewEventBus()
			retryCount := 0
			bus.Subscribe(events.EventProviderRequestRetry, func(msg any) {
				if _, ok := msg.(*events.ProviderRequestRetryEvent); ok {
					retryCount++
				}
			})

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     bus,
				Manifest:     manifest,
				Tools:        []tool.Tool{coordinationStore},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			for chunk := range chunks {
				collectedContent += chunk.Content
			}

			Expect(chatProvider.callIndex).To(Equal(4),
				"provider called 4 times: initial + 3 delivery retries")
			Expect(retryCount).To(Equal(3),
				"exactly 3 delivery retries must fire before falling through")
			Expect(coordinationStore.execCalled).To(BeFalse(),
				"delivery tool was never called")
			Expect(collectedContent).To(ContainSubstring("Final narration"))
		})
	})
})
