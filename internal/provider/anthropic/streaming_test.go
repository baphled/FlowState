package anthropic

import (
	"context"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/baphled/flowstate/internal/provider"
	shared "github.com/baphled/flowstate/internal/provider/shared"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func createTestContext() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

var _ = Describe("streamEventHandler", func() {
	var handler *streamEventHandler

	BeforeEach(func() {
		handler = newStreamEventHandler()
	})

	Describe("handleEvent", func() {
		Context("when receiving a text_delta event", func() {
			It("returns a chunk with text content", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "content_block_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type: "text_delta",
						Text: "Hello world",
					},
				}

				chunk, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.Content).To(Equal("Hello world"))
				Expect(chunk.Done).To(BeFalse())
			})
		})

		Context("when receiving thinking block events", func() {
			It("accumulates thinking deltas and emits them on stop", func() {
				startEvent := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 1,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "thinking",
					},
				}
				handler.handleEvent(startEvent)

				deltaEvent := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 1,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:     "thinking_delta",
						Thinking: "first part",
					},
				}
				_, shouldSend := handler.handleEvent(deltaEvent)

				Expect(shouldSend).To(BeFalse())

				stopEvent := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 1,
				}

				chunk, shouldSend := handler.handleEvent(stopEvent)

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.Thinking).To(Equal("first part"))
			})
		})

		Context("when receiving a message_stop event", func() {
			It("returns a done chunk", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_stop",
				}

				chunk, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.Done).To(BeTrue())
			})
		})

		Context("when receiving a tool_use content_block_start", func() {
			It("does not emit a chunk yet", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "tool_use",
						ID:   "toolu_01ABC",
						Name: "skill_load",
					},
				}

				_, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeFalse())
			})
		})

		Context("when receiving input_json_delta events", func() {
			It("does not emit a chunk during accumulation", func() {
				startEvent := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "tool_use",
						ID:   "toolu_01ABC",
						Name: "skill_load",
					},
				}
				handler.handleEvent(startEvent)

				deltaEvent := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:        "input_json_delta",
						PartialJSON: `{"name":`,
					},
				}

				_, shouldSend := handler.handleEvent(deltaEvent)

				Expect(shouldSend).To(BeFalse())
			})
		})

		Context("when receiving a complete tool call sequence", func() {
			It("emits a tool call chunk on content_block_stop", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "tool_use",
						ID:   "toolu_01ABC",
						Name: "skill_load",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:        "input_json_delta",
						PartialJSON: `{"name":`,
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:        "input_json_delta",
						PartialJSON: ` "pre-action"}`,
					},
				})

				stopEvent := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				}

				chunk, shouldSend := handler.handleEvent(stopEvent)

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.EventType).To(Equal("tool_call"))
				Expect(chunk.ToolCall).NotTo(BeNil())
				Expect(chunk.ToolCall.ID).To(Equal("toolu_01ABC"))
				Expect(chunk.ToolCall.Name).To(Equal("skill_load"))
				Expect(chunk.ToolCall.Arguments).To(HaveKeyWithValue("name", "pre-action"))
			})
		})

		Context("when receiving multiple tool calls on different indices", func() {
			It("tracks each tool call independently", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "tool_use",
						ID:   "toolu_01ABC",
						Name: "skill_load",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 2,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "tool_use",
						ID:   "toolu_02DEF",
						Name: "memory_search",
					},
				})

				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:        "input_json_delta",
						PartialJSON: `{"name": "golang"}`,
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 2,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:        "input_json_delta",
						PartialJSON: `{"query": "streaming"}`,
					},
				})

				chunk0, send0 := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				})
				chunk2, send2 := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 2,
				})

				Expect(send0).To(BeTrue())
				Expect(chunk0.ToolCall.ID).To(Equal("toolu_01ABC"))
				Expect(chunk0.ToolCall.Name).To(Equal("skill_load"))
				Expect(chunk0.ToolCall.Arguments).To(HaveKeyWithValue("name", "golang"))

				Expect(send2).To(BeTrue())
				Expect(chunk2.ToolCall.ID).To(Equal("toolu_02DEF"))
				Expect(chunk2.ToolCall.Name).To(Equal("memory_search"))
				Expect(chunk2.ToolCall.Arguments).To(HaveKeyWithValue("query", "streaming"))
			})
		})

		Context("when receiving content_block_stop for a non-tool block", func() {
			It("does not emit a chunk", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 5,
				}

				_, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeFalse())
			})
		})

		Context("when input_json_delta arrives without matching content_block_start", func() {
			It("does not emit a chunk and does not panic", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 99,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:        "input_json_delta",
						PartialJSON: `{"orphan": true}`,
					},
				}

				_, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeFalse())
			})
		})

		Context("when tool call has empty arguments", func() {
			It("emits a tool call with empty arguments map", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "tool_use",
						ID:   "toolu_01EMPTY",
						Name: "no_args_tool",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:        "input_json_delta",
						PartialJSON: `{}`,
					},
				})

				chunk, shouldSend := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				})

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.ToolCall.ID).To(Equal("toolu_01EMPTY"))
				Expect(chunk.ToolCall.Arguments).To(BeEmpty())
			})
		})

		Context("when receiving an unrecognised event type", func() {
			It("does not emit a chunk", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_start",
				}

				_, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeFalse())
			})
		})
	})

	Describe("sendChunk helper", func() {
		It("sends a chunk to the channel", func() {
			ch := make(chan provider.StreamChunk, 1)
			ctx, cancel := createTestContext()
			defer cancel()

			sent := shared.SendChunk(ctx, ch, provider.StreamChunk{Content: "hello"})

			Expect(sent).To(BeTrue())
			Expect(<-ch).To(Equal(provider.StreamChunk{Content: "hello"}))
		})
	})
})
