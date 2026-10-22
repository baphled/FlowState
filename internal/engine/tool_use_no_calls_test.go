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

var _ = Describe("ToolUseNoCalls", func() {
	var (
		chatProvider *streamSequenceProvider
		manifest     agent.Manifest
		testTool     *executableMockTool
		registry     *tool.Registry
	)

	BeforeEach(func() {
		chatProvider = &streamSequenceProvider{
			name:      "test-chat-provider",
			sequences: [][]provider.StreamChunk{},
		}

		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
			Capabilities:      agent.Capabilities{Tools: []string{"test_tool"}},
		}

		testTool = &executableMockTool{
			name:        "test_tool",
			description: "A test tool",
			execResult:  tool.Result{Output: "tool executed successfully"},
		}

		registry = tool.NewRegistry()
		registry.Register(testTool)
		registry.SetPermission("test_tool", tool.Allow)
	})

	Context("retry succeeds on second attempt", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{{Done: true, StopReason: "tool_use"}},
				{{Content: "Success!", Done: true, StopReason: "end_turn"}},
			}
		})

		It("retries once and delivers the successful response", func() {
			bus := eventbus.NewEventBus()
			retryEvents := make(chan *events.ProviderRequestRetryEvent, 4)
			bus.Subscribe(events.EventProviderRequestRetry, func(msg any) {
				if ev, ok := msg.(*events.ProviderRequestRetryEvent); ok {
					retryEvents <- ev
				}
			})

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     bus,
				Manifest:     manifest,
				Tools:        []tool.Tool{testTool},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			var doneChunks []provider.StreamChunk
			for chunk := range chunks {
				if chunk.Done {
					doneChunks = append(doneChunks, chunk)
				}
				collectedContent += chunk.Content
			}

			Expect(chatProvider.callIndex).To(Equal(2),
				"provider must be called twice: initial tool_use_no_calls + one retry")

			Expect(doneChunks).To(HaveLen(2),
				"consumer observes the original tool_use Done then the retry end_turn Done")
			Expect(doneChunks[0].StopReason).To(Equal("tool_use"))
			Expect(doneChunks[1].StopReason).To(Equal("end_turn"))
			Expect(collectedContent).To(ContainSubstring("Success!"))

			var retryEvt *events.ProviderRequestRetryEvent
			Eventually(retryEvents).Should(Receive(&retryEvt))
			Expect(retryEvt.Data.Reason).To(Equal("tool_use_no_calls"))
			Expect(retryEvt.Data.Attempt).To(Equal(1))
		})
	})

	Context("retries exhausted", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{{Done: true, StopReason: "tool_use"}},
				{{Done: true, StopReason: "tool_use"}},
				{{Done: true, StopReason: "tool_use"}},
				{{Done: true, StopReason: "tool_use"}},
			}
		})

		It("falls through to completeResponse after max retries", func() {
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
				Tools:        []tool.Tool{testTool},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			for range chunks {
			}

			Expect(retryCount).To(Equal(3),
				"must retry exactly 3 times (maxToolUseNoCallsRetries)")
			Expect(chatProvider.callIndex).To(Equal(4),
				"provider called 4 times: initial + 3 retries")
		})
	})

	Context("normal end_turn with zero tool calls", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{{Content: "Normal response", Done: true, StopReason: "end_turn"}},
			}
		})

		It("does not retry and delivers the response", func() {
			bus := eventbus.NewEventBus()
			var retryCount int
			bus.Subscribe(events.EventProviderRequestRetry, func(msg any) {
				defer GinkgoRecover()
				if _, ok := msg.(*events.ProviderRequestRetryEvent); ok {
					retryCount++
				}
			})

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     bus,
				Manifest:     manifest,
				Tools:        []tool.Tool{testTool},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			var doneChunks []provider.StreamChunk
			for chunk := range chunks {
				if chunk.Done {
					doneChunks = append(doneChunks, chunk)
				}
				collectedContent += chunk.Content
			}

			Expect(chatProvider.callIndex).To(Equal(1),
				"provider must be called exactly once")
			Expect(retryCount).To(BeZero(),
				"no retry events must fire for normal end_turn")
			Expect(doneChunks).To(HaveLen(1))
			Expect(doneChunks[0].StopReason).To(Equal("end_turn"))
			Expect(collectedContent).To(ContainSubstring("Normal response"))
		})
	})

	Context("tool_use with actual tool calls", func() {
		BeforeEach(func() {
			chatProvider.sequences = [][]provider.StreamChunk{
				{
					{
						EventType: "tool_call",
						ToolCall: &provider.ToolCall{
							ID:        "call_1",
							Name:      "test_tool",
							Arguments: map[string]interface{}{"key": "value"},
						},
					},
					{Done: true, StopReason: "tool_use"},
				},
				{
					{Content: "After tool execution", Done: true, StopReason: "end_turn"},
				},
			}
		})

		It("executes the tool without retrying", func() {
			bus := eventbus.NewEventBus()
			var noCallsRetries int
			bus.Subscribe(events.EventProviderRequestRetry, func(msg any) {
				defer GinkgoRecover()
				if ev, ok := msg.(*events.ProviderRequestRetryEvent); ok {
					if ev.Data.Reason == "tool_use_no_calls" {
						noCallsRetries++
					}
				}
			})

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				EventBus:     bus,
				Manifest:     manifest,
				Tools:        []tool.Tool{testTool},
			})

			ctx := context.Background()
			chunks, err := eng.Stream(ctx, "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())

			var collectedContent string
			var doneChunks []provider.StreamChunk
			for chunk := range chunks {
				if chunk.Done {
					doneChunks = append(doneChunks, chunk)
				}
				collectedContent += chunk.Content
			}

			Expect(testTool.execCalled).To(BeTrue(),
				"tool must execute for a legitimate tool_use with actual tool calls")
			Expect(doneChunks).To(HaveLen(1))
			Expect(doneChunks[0].StopReason).To(Equal("end_turn"))
			Expect(collectedContent).To(ContainSubstring("After tool execution"))
			Expect(noCallsRetries).To(BeZero(),
				"tool_use_no_calls retry must not fire when tool calls are present")
		})
	})
})
