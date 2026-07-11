package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

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
							Arguments: map[string]interface{}{"key": "value"},
						},
					},
					{Done: true, StopReason: "tool_use"},
				},
				{{Content: "Done after tool execution", Done: true, StopReason: "end_turn"}},
			}
		})

		It("injects a corrective message and retries with a tool call", func() {
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

			Expect(chatProvider.callIndex).To(Equal(3),
				"provider called three times: initial narration + retry with tool call + final response")
			Expect(retryCount).To(BeNumerically(">=", 1),
				"at least one retry event must fire when the delivery tool was not called")
			Expect(coordinationStore.execCalled).To(BeTrue(),
				"delivery tool must be executed after the retry corrective message")
			Expect(collectedContent).To(ContainSubstring("Done after tool execution"))
		})
	})

	Context("completes when delivery tool is called", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_1",
							Name:      "coordination_store",
							Arguments: map[string]interface{}{"key": "value"},
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
			Expect(coordinationStore.execCalled).To(BeTrue(),
				"delivery tool must execute")
			Expect(collectedContent).To(ContainSubstring("Done after tool execution"))
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
