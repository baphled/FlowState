package anthropic

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"time"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
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
					Type: "unknown_future_event",
				}

				_, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeFalse())
			})
		})

		Context("when receiving a message_start event with usage data", func() {
			It("captures input/output tokens and cache stats into Usage", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_start",
					Message: anthropicAPI.Message{
						ID:    "msg_01ABCXYZ",
						Model: "claude-opus-4-7-20251201",
						Usage: anthropicAPI.Usage{
							InputTokens:              42,
							OutputTokens:             1,
							CacheCreationInputTokens: 1024,
							CacheReadInputTokens:     2048,
						},
					},
				}

				chunk, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.EventType).To(Equal("usage"))
				Expect(chunk.Usage).NotTo(BeNil())
				Expect(chunk.Usage.InputTokens).To(Equal(int64(42)))
				Expect(chunk.Usage.OutputTokens).To(Equal(int64(1)))
				Expect(chunk.Usage.CacheCreationInputTokens).To(Equal(int64(1024)))
				Expect(chunk.Usage.CacheReadInputTokens).To(Equal(int64(2048)),
					"cache stats arrive on message_start, not message_delta — dropping "+
						"the event under-reports cache hits in token accounting")
				Expect(chunk.Usage.RequestID).To(Equal("msg_01ABCXYZ"))
				Expect(chunk.Usage.Model).To(Equal("claude-opus-4-7-20251201"))
			})

			It("does not emit a chunk for an empty message_start", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_start",
				}

				_, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeFalse())
			})
		})

		Context("when receiving a message_delta event", func() {
			It("captures stop_reason end_turn", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						StopReason: anthropicAPI.StopReasonEndTurn,
					},
				}

				chunk, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.EventType).To(Equal("stop_reason"))
				Expect(chunk.StopReason).To(Equal("end_turn"))
			})

			It("captures stop_reason tool_use", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						StopReason: anthropicAPI.StopReasonToolUse,
					},
				}

				chunk, _ := handler.handleEvent(event)

				Expect(chunk.StopReason).To(Equal("tool_use"))
			})

			It("captures stop_reason max_tokens", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						StopReason: anthropicAPI.StopReasonMaxTokens,
					},
				}

				chunk, _ := handler.handleEvent(event)

				Expect(chunk.StopReason).To(Equal("max_tokens"))
			})

			It("captures stop_reason refusal (Claude 4+)", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						StopReason: anthropicAPI.StopReasonRefusal,
					},
				}

				chunk, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.StopReason).To(Equal("refusal"),
					"refusal must surface so the engine can distinguish a model "+
						"refusal from a normal end_turn")
			})

			It("captures stop_reason pause_turn", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						StopReason: anthropicAPI.StopReasonPauseTurn,
					},
				}

				chunk, _ := handler.handleEvent(event)

				Expect(chunk.StopReason).To(Equal("pause_turn"))
			})

			It("captures stop_sequence with the matched sequence", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						StopReason:   anthropicAPI.StopReasonStopSequence,
						StopSequence: "</halt>",
					},
				}

				chunk, _ := handler.handleEvent(event)

				Expect(chunk.StopReason).To(Equal("stop_sequence"))
				Expect(chunk.StopSequence).To(Equal("</halt>"))
			})

			It("captures cumulative output tokens via Usage", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						StopReason: anthropicAPI.StopReasonEndTurn,
					},
					Usage: anthropicAPI.MessageDeltaUsage{
						OutputTokens: 256,
					},
				}

				chunk, _ := handler.handleEvent(event)

				Expect(chunk.Usage).NotTo(BeNil())
				Expect(chunk.Usage.OutputTokens).To(Equal(int64(256)))
			})

			It("does not emit a chunk for an empty message_delta", func() {
				event := anthropicAPI.MessageStreamEventUnion{
					Type: "message_delta",
				}

				_, shouldSend := handler.handleEvent(event)

				Expect(shouldSend).To(BeFalse())
			})
		})

		Context("when receiving signature_delta events for thinking", func() {
			It("accumulates the signature and emits it with the thinking chunk", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "thinking",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:     "thinking_delta",
						Thinking: "weighing the request",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:      "signature_delta",
						Signature: "sig-part-1",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:      "signature_delta",
						Signature: "sig-part-2",
					},
				})

				chunk, shouldSend := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				})

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.Thinking).To(Equal("weighing the request"))
				Expect(chunk.Signature).To(Equal("sig-part-1sig-part-2"),
					"signature_delta accumulates across multiple deltas; without round-"+
						"tripping the full signature, Anthropic silently disables thinking "+
						"continuity on the next turn")
			})

			It("attributes signatures to the correct block when multiple thinking blocks interleave", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "thinking",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 1,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "thinking",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:     "thinking_delta",
						Thinking: "first",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 0,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:      "signature_delta",
						Signature: "sig-A",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 1,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:     "thinking_delta",
						Thinking: "second",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 1,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:      "signature_delta",
						Signature: "sig-B",
					},
				})

				chunk0, _ := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				})
				chunk1, _ := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 1,
				})

				Expect(chunk0.Thinking).To(Equal("first"))
				Expect(chunk0.Signature).To(Equal("sig-A"))
				Expect(chunk1.Thinking).To(Equal("second"))
				Expect(chunk1.Signature).To(Equal("sig-B"))
			})
		})

		Context("when receiving a redacted_thinking content block", func() {
			It("captures the encrypted data and emits it on content_block_stop", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "redacted_thinking",
						Data: "encrypted-payload-xyz",
					},
				})

				chunk, shouldSend := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				})

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.RedactedThinking).To(Equal("encrypted-payload-xyz"),
					"redacted_thinking blocks must be replayed verbatim — Anthropic "+
						"requires the encrypted payload on the next turn even though it "+
						"is opaque to the client")
				Expect(chunk.Thinking).To(BeEmpty())
				Expect(chunk.Signature).To(BeEmpty())
			})

			It("does not interfere with regular signed thinking on a different index", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "redacted_thinking",
						Data: "redacted-data",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 1,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "thinking",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 1,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:     "thinking_delta",
						Thinking: "visible",
					},
				})
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_delta",
					Index: 1,
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type:      "signature_delta",
						Signature: "sig-1",
					},
				})

				redactedChunk, _ := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				})
				signedChunk, _ := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 1,
				})

				Expect(redactedChunk.RedactedThinking).To(Equal("redacted-data"))
				Expect(redactedChunk.Thinking).To(BeEmpty())
				Expect(signedChunk.Thinking).To(Equal("visible"))
				Expect(signedChunk.Signature).To(Equal("sig-1"))
				Expect(signedChunk.RedactedThinking).To(BeEmpty())
			})
		})

		Context("P2 T1: ToolCallID propagation from Anthropic tool_use blocks", func() {
			It("populates StreamChunk.ToolCallID from the Anthropic tool_use block ID", func() {
				handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: anthropicAPI.ContentBlockStartEventContentBlockUnion{
						Type: "tool_use",
						ID:   "toolu_01PROPAGATE",
						Name: "bash",
					},
				})

				chunk, shouldSend := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type:  "content_block_stop",
					Index: 0,
				})

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.EventType).To(Equal("tool_call"))
				Expect(chunk.ToolCallID).To(Equal("toolu_01PROPAGATE"),
					"tool_call StreamChunk must carry the upstream tool_use block ID so the "+
						"intent layer can correlate start/result events")
			})

			It("leaves ToolCallID empty for non-tool chunks", func() {
				chunk, shouldSend := handler.handleEvent(anthropicAPI.MessageStreamEventUnion{
					Type: "content_block_delta",
					Delta: anthropicAPI.MessageStreamEventUnionDelta{
						Type: "text_delta",
						Text: "hello",
					},
				})

				Expect(shouldSend).To(BeTrue())
				Expect(chunk.ToolCallID).To(Equal(""))
			})
		})
	})

	Context("when receiving a ping event", func() {
		It("returns a marker chunk with EventType=ping", func() {
			event := anthropicAPI.MessageStreamEventUnion{
				Type: "ping",
			}

			chunk, shouldSend := handler.handleEvent(event)

			Expect(shouldSend).To(BeTrue())
			Expect(chunk.EventType).To(Equal("ping"))
			Expect(chunk.Done).To(BeFalse())
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

// Bug: the Anthropic SDK's stream.Next() parks on a hung HTTP body
// read with no read deadline configured. The May 2026 mid-thinking-
// halt incident captured this in a 50s live SSE probe: 103 thinking
// chunks then HALT with no [DONE]. The load-bearing fix is the
// engine's idle-stream watchdog (engineStreamIdleTimeout = 60s);
// this provider-level wall-clock cap is the defence-in-depth backstop
// that lets stream.Next() return an error rather than block forever.
//
// The SDK threads option.WithRequestTimeout through to
// context.WithTimeout on the per-attempt ctx, which propagates into
// the body read. We pin that wiring by running Stream against a
// httptest server that begins an SSE handshake then blocks forever
// on the body, with the timeout overridden to a fast 100ms.
var _ = Describe("Anthropic Stream wall-clock timeout", func() {
	It("returns a Done chunk with an error when the body read parks past the timeout", func() {
		// hangServer accepts the request, writes 200 + SSE headers,
		// flushes them so the SDK considers the handshake complete,
		// then blocks on a channel that never closes during the
		// test's lifetime. The handler returns when the client
		// disconnects (ctx cancel from WithRequestTimeout).
		hangServer := httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.WriteHeader(http.StatusOK)
				flusher, ok := w.(http.Flusher)
				if ok {
					// Send a minimal message_start so the SDK
					// commits to streaming-body mode. Without this
					// some SDK versions retry on a zero-byte body.
					fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-3-5-sonnet-20241022\",\"content\":[],\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
					flusher.Flush()
				}
				// Park until the client disconnects (the SDK's
				// WithRequestTimeout will cancel the request ctx,
				// which disconnects the body read and unblocks
				// r.Context().Done() here).
				<-r.Context().Done()
			},
		))
		defer hangServer.Close()

		p, err := NewWithOptions(
			"sk-ant-test-key",
			option.WithBaseURL(hangServer.URL),
			option.WithMaxRetries(0),
		)
		Expect(err).NotTo(HaveOccurred())
		p.SetStreamRequestTimeoutForTest(100 * time.Millisecond)

		req := provider.ChatRequest{
			Model: "claude-3-5-sonnet-20241022",
			Messages: []provider.Message{
				{Role: "user", Content: "hello"},
			},
		}

		ch, err := p.Stream(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())

		// The wall-clock cap should unblock the parked body read
		// within ~100ms; allow generous slack for goroutine
		// scheduling. Channel drain semantics: streamMessages
		// closes ch on any terminal condition (Done chunk, error,
		// or ctx cancel), so we accept either a Done chunk or a
		// clean close as proof the body read unparked.
		var sawTerminal bool
		Eventually(func() bool {
			select {
			case chunk, ok := <-ch:
				if !ok {
					sawTerminal = true
					return true
				}
				if chunk.Done || chunk.Error != nil {
					sawTerminal = true
					return true
				}
				return false
			default:
				return false
			}
		}, "3s", "10ms").Should(BeTrue(),
			"expected the SDK's per-attempt wall-clock cap to unblock the parked body read within the override timeout + scheduling slack")
		Expect(sawTerminal).To(BeTrue())
	})

	It("uses a sane production default", func() {
		// Pin the production default — long enough that legitimate
		// Anthropic extended-thinking responses don't trip it,
		// short enough that a truly stuck connection surfaces
		// within a single user-perceptible failure window.
		Expect(DefaultStreamRequestTimeoutForTest()).To(Equal(10 * time.Minute),
			"defaultStreamRequestTimeout pins the production wall-clock cap; "+
				"changing it requires reviewing the trade-off between "+
				"legitimate long-thinking allowances and stuck-connection "+
				"detection windows")
	})
})
