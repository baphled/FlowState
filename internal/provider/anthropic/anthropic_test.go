package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"time"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newTestAPIError(statusCode int) *anthropicAPI.Error {
	u, _ := url.Parse("https://api.anthropic.com/v1/messages")

	return &anthropicAPI.Error{
		StatusCode: statusCode,
		Request:    &http.Request{Method: "POST", URL: u},
		Response:   &http.Response{StatusCode: statusCode},
	}
}

// newTestAPIErrorWithHeaders builds an SDK error whose underlying
// http.Response carries the given headers. Used to exercise rate-limit
// header propagation without round-tripping through a live HTTP server.
func newTestAPIErrorWithHeaders(
	statusCode int, headers http.Header,
) *anthropicAPI.Error {
	apiErr := newTestAPIError(statusCode)
	apiErr.Response.Header = headers
	return apiErr
}

func newTestAPIErrorWithBody(
	statusCode int, body string,
) *anthropicAPI.Error {
	apiErr := newTestAPIError(statusCode)
	if body != "" {
		_ = apiErr.UnmarshalJSON([]byte(body))
		apiErr.StatusCode = statusCode
	}

	return apiErr
}

var _ = Describe("buildToolResultMessage", func() {
	It("returns a user message with tool result block (success)", func() {
		m := provider.Message{
			Role:      "tool",
			Content:   "42",
			ToolCalls: []provider.ToolCall{{ID: "toolu_01tool1"}},
		}
		msg := buildToolResultMessage(m)
		Expect(msg).NotTo(BeNil())
		Expect(string(msg.Role)).To(Equal("user"))
		Expect(msg.Content).To(HaveLen(1))
		block := msg.Content[0]
		Expect(block.OfToolResult).NotTo(BeNil())
		tr := block.OfToolResult
		Expect(tr.ToolUseID).To(Equal("toolu_01tool1"))
		Expect(tr.Content).To(HaveLen(1))
		Expect(tr.Content[0].OfText).NotTo(BeNil())
		Expect(tr.Content[0].OfText.Text).To(Equal("42"))
		Expect(tr.IsError.Valid()).To(BeTrue())
		Expect(tr.IsError.Value).To(BeFalse())
	})

	It("returns a user message with tool result block (error)", func() {
		m := provider.Message{
			Role:      "tool",
			Content:   "Error: failed to compute",
			IsError:   true,
			ToolCalls: []provider.ToolCall{{ID: "toolu_01tool2"}},
		}
		msg := buildToolResultMessage(m)
		Expect(msg).NotTo(BeNil())
		Expect(string(msg.Role)).To(Equal("user"))
		Expect(msg.Content).To(HaveLen(1))
		block := msg.Content[0]
		Expect(block.OfToolResult).NotTo(BeNil())
		tr := block.OfToolResult
		Expect(tr.ToolUseID).To(Equal("toolu_01tool2"))
		Expect(tr.Content).To(HaveLen(1))
		Expect(tr.Content[0].OfText).NotTo(BeNil())
		Expect(tr.Content[0].OfText.Text).To(Equal("Error: failed to compute"))
		Expect(tr.IsError.Value).To(BeTrue())
	})

	It("returns nil if no tool calls are present", func() {
		m := provider.Message{Role: "tool", Content: "foo"}
		msg := buildToolResultMessage(m)
		Expect(msg).To(BeNil())
	})

	// Bug M4 (May 2026): the prior implementation derived is_error via
	// strings.HasPrefix(m.Content, "Error:"), which mis-flagged two
	// real-world shapes. The seam fix lifts the truth onto
	// provider.Message.IsError, populated by the engine from
	// tool.Result.Error at the tool-result construction site. These
	// specs pin the new contract.
	Context("M4: is_error reads from Message.IsError, not Content prefix", func() {
		It("a success result whose Content starts with 'Error:' is NOT flagged as error", func() {
			// Tool legitimately returns "Error: 0 results found" as its
			// happy-path output (search miss). IsError on Message stays
			// false; the wire flag must follow.
			m := provider.Message{
				Role:      "tool",
				Content:   "Error: 0 results found",
				IsError:   false,
				ToolCalls: []provider.ToolCall{{ID: "toolu_01fp"}},
			}
			msg := buildToolResultMessage(m)
			Expect(msg).NotTo(BeNil())
			tr := msg.Content[0].OfToolResult
			Expect(tr).NotTo(BeNil())
			Expect(tr.IsError.Valid()).To(BeTrue())
			Expect(tr.IsError.Value).To(BeFalse(),
				"is_error must follow Message.IsError, not the 'Error:' content prefix")
			Expect(tr.Content[0].OfText.Text).To(Equal("Error: 0 results found"))
		})

		It("a failure result whose Content does NOT start with 'Error:' IS flagged as error", func() {
			// Tool failed with executor.Error != nil but the message
			// content the engine surfaced does not begin with the
			// "Error:" prefix (e.g. "failed: timeout exceeded",
			// "panic: runtime nil deref"). IsError on Message is true;
			// the wire flag must follow.
			m := provider.Message{
				Role:      "tool",
				Content:   "failed: timeout exceeded after 30s",
				IsError:   true,
				ToolCalls: []provider.ToolCall{{ID: "toolu_01fn"}},
			}
			msg := buildToolResultMessage(m)
			Expect(msg).NotTo(BeNil())
			tr := msg.Content[0].OfToolResult
			Expect(tr).NotTo(BeNil())
			Expect(tr.IsError.Valid()).To(BeTrue())
			Expect(tr.IsError.Value).To(BeTrue(),
				"is_error must follow Message.IsError, not the 'Error:' content prefix")
			Expect(tr.Content[0].OfText.Text).To(Equal("failed: timeout exceeded after 30s"))
		})
	})
})

var _ = Describe("sanitizeMessageSequence", func() {
	It("removes system messages and merges consecutive user messages", func() {
		msgs := []provider.Message{
			{Role: "system", Content: "ignore me"},
			{Role: "user", Content: "hello"},
			{Role: "user", Content: "world"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "foo"},
			{Role: "user", Content: "bar"},
		}
		result := sanitizeMessageSequence(msgs)
		Expect(result).To(HaveLen(3))
		Expect(result[0].Role).To(Equal("user"))
		Expect(result[0].Content).To(Equal("hello\n\nworld"))
		Expect(result[1].Role).To(Equal("assistant"))
		Expect(result[1].Content).To(Equal("hi"))
		Expect(result[2].Role).To(Equal("user"))
		Expect(result[2].Content).To(Equal("foo\n\nbar"))
	})

	It("returns empty slice if all are system", func() {
		msgs := []provider.Message{
			{Role: "system", Content: "ignore"},
			{Role: "system", Content: "ignore2"},
		}
		result := sanitizeMessageSequence(msgs)
		Expect(result).To(BeEmpty())
	})
})

var _ = Describe("buildMessages", func() {
	It("builds correct sequence for mixed user/assistant/tool", func() {
		msgs := []provider.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "tool", Content: "42", ToolCalls: []provider.ToolCall{{ID: "toolu_01tool1"}}},
			{Role: "assistant", Content: "done"},
		}
		result := buildMessages(msgs)
		Expect(result).To(HaveLen(4))
		Expect(string(result[0].Role)).To(Equal("user"))
		Expect(result[0].Content[0].OfText.Text).To(Equal("hello"))
		Expect(string(result[1].Role)).To(Equal("assistant"))
		Expect(result[1].Content[0].OfText.Text).To(Equal("hi"))
		Expect(string(result[2].Role)).To(Equal("user"))
		tr := result[2].Content[0].OfToolResult
		Expect(tr).NotTo(BeNil())
		Expect(tr.ToolUseID).To(Equal("toolu_01tool1"))
		Expect(tr.Content[0].OfText.Text).To(Equal("42"))
		Expect(tr.IsError.Valid()).To(BeTrue())
		Expect(tr.IsError.Value).To(BeFalse())
		Expect(string(result[3].Role)).To(Equal("assistant"))
		Expect(result[3].Content[0].OfText.Text).To(Equal("done"))
	})

	It("skips system and empty assistant messages", func() {
		msgs := []provider.Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "foo"},
			{Role: "assistant", Content: ""},
			{Role: "assistant", Content: "bar"},
		}
		result := buildMessages(msgs)
		Expect(result).To(HaveLen(2))
		Expect(string(result[0].Role)).To(Equal("user"))
		Expect(result[0].Content[0].OfText.Text).To(Equal("foo"))
		Expect(string(result[1].Role)).To(Equal("assistant"))
		Expect(result[1].Content[0].OfText.Text).To(Equal("bar"))
	})

	It("handles tool messages with multiple tool calls", func() {
		msgs := []provider.Message{
			{Role: "user", Content: "ask"},
			{Role: "tool", Content: "result1", ToolCalls: []provider.ToolCall{{ID: "toolu_01id1"}, {ID: "toolu_01id2"}}},
		}
		result := buildMessages(msgs)
		Expect(result).To(HaveLen(2))
		Expect(string(result[1].Role)).To(Equal("user"))
		tr1 := result[1].Content[0].OfToolResult
		tr2 := result[1].Content[1].OfToolResult
		Expect(tr1.ToolUseID).To(Equal("toolu_01id1"))
		Expect(tr2.ToolUseID).To(Equal("toolu_01id2"))
		Expect(tr1.Content[0].OfText.Text).To(Equal("result1"))
		Expect(tr2.Content[0].OfText.Text).To(Equal("result1"))
	})

	// M4-adjacent (May 2026): when the session manager reloads a persisted
	// "tool_error" row it canonicalises to Role:"tool" + IsError:true at
	// the projection seam. From buildMessages' perspective the input is the
	// canonical shape; the assertion here is that the canonicalised input
	// reaches the wire encoded with is_error:true. The companion projection
	// spec lives in `internal/session` (SendMessage context seeding).
	It("round-trips a canonicalised reloaded tool error onto a tool_result block with is_error:true", func() {
		// Shape produced by the manager's projection on reload of a
		// session.Message{Role:"tool_error"} row.
		msgs := []provider.Message{
			{Role: "user", Content: "Run the tool."},
			{
				Role:      "tool",
				Content:   "failed: timeout exceeded after 30s",
				IsError:   true,
				ToolCalls: []provider.ToolCall{{ID: "toolu_01reload"}},
			},
		}
		result := buildMessages(msgs)
		Expect(result).To(HaveLen(2),
			"a canonicalised tool error MUST survive the role switch — "+
				"buildMessages drops any role other than 'tool', so a raw "+
				"'tool_error' would have produced HaveLen(1) and the model "+
				"would never see the failure")
		Expect(string(result[1].Role)).To(Equal("user"))
		tr := result[1].Content[0].OfToolResult
		Expect(tr).NotTo(BeNil())
		Expect(tr.ToolUseID).To(Equal("toolu_01reload"))
		Expect(tr.Content[0].OfText.Text).To(Equal("failed: timeout exceeded after 30s"))
		Expect(tr.IsError.Valid()).To(BeTrue())
		Expect(tr.IsError.Value).To(BeTrue(),
			"is_error on the wire must follow the canonicalised "+
				"provider.Message.IsError stamped at the reload projection seam")
	})
})

var _ = Describe("Phase 3 multi-turn thinking round-trip", func() {
	// End-to-end pin for the behaviour 266aec8 promised but didn't deliver
	// on turn 2: a stream that emits signed-thinking + content must produce
	// an assistant message whose thinking blocks survive into the next
	// turn's request payload, with the signature UNCHANGED.
	//
	// The provider's StreamChunk shape is the seam between the streaming
	// layer (which captures Signature alongside Thinking) and the message
	// layer (which feeds buildAssistantMessage on subsequent turns). We
	// drive both ends from the public types here to keep the test
	// provider-agnostic at the seam — anyone porting the same accumulator
	// to a different provider repeats the same shape.
	It("turn 1 stream → assembled provider.Message → turn 2 request preserves thinking", func() {
		// Turn 1: simulate the chunks the streaming layer would produce
		// from a turn that ended with end_turn and contained one signed
		// thinking block plus visible content.
		turn1Chunks := []provider.StreamChunk{
			{Thinking: "weighing the request", Signature: "sig-encrypted-xyz"},
			{Content: "the answer is 42"},
			{EventType: "stop_reason", StopReason: "end_turn"},
			{Done: true},
		}

		// Reconstruct what the accumulator would persist on the assistant
		// message: a provider.Message carrying the visible content, the
		// stop reason, and the thinking blocks (with signatures).
		// Construct equivalent provider.Message inline so this test
		// remains in the anthropic package boundary; the equivalent
		// accumulator behaviour is already covered by the session tests.
		var thinking, signature, content, stopReason string
		for _, c := range turn1Chunks {
			if c.Thinking != "" {
				thinking = c.Thinking
				signature = c.Signature
			}
			if c.Content != "" {
				content = c.Content
			}
			if c.EventType == "stop_reason" {
				stopReason = c.StopReason
			}
		}
		Expect(stopReason).To(Equal("end_turn"))

		assistantMsg := provider.Message{
			Role:       "assistant",
			Content:    content,
			StopReason: stopReason,
			ThinkingBlocks: []provider.ThinkingBlock{
				{Thinking: thinking, Signature: signature},
			},
		}

		// Turn 2: caller sends back history (user, assistant, user). The
		// assistant message reconstructed above must serialise into the
		// turn-2 request with the thinking block intact and signature
		// UNCHANGED — this is what Anthropic uses to verify thinking
		// continuity. Without it, the API silently disables thinking.
		turn2Messages := []provider.Message{
			{Role: "user", Content: "what is the answer?"},
			assistantMsg,
			{Role: "user", Content: "and the question?"},
		}

		built := buildMessages(turn2Messages)
		Expect(built).To(HaveLen(3))

		// The assistant slot must carry the thinking block first, then text.
		assistant := built[1]
		Expect(string(assistant.Role)).To(Equal("assistant"))
		Expect(assistant.Content).To(HaveLen(2))
		Expect(assistant.Content[0].OfThinking).NotTo(BeNil(),
			"the persisted thinking must round-trip into the turn-2 request — "+
				"this is the bug 266aec8 left open: per-model thinking opt-in "+
				"works on turn 1 only, because the streaming layer was dropping "+
				"signature_delta and the round-trip path was dropping the block")
		Expect(assistant.Content[0].OfThinking.Thinking).To(Equal("weighing the request"))
		Expect(assistant.Content[0].OfThinking.Signature).To(Equal("sig-encrypted-xyz"),
			"signature must be byte-identical to what the API returned — Anthropic "+
				"verifies this before honouring thinking continuity")
		Expect(assistant.Content[1].OfText).NotTo(BeNil())
		Expect(assistant.Content[1].OfText.Text).To(Equal("the answer is 42"))
	})

	It("redacted thinking from turn 1 round-trips into turn 2 request", func() {
		assistantMsg := provider.Message{
			Role:    "assistant",
			Content: "redacted answer",
			ThinkingBlocks: []provider.ThinkingBlock{
				{Redacted: true, Data: "encrypted-payload-from-turn-1"},
			},
		}
		built := buildMessages([]provider.Message{
			{Role: "user", Content: "ask"},
			assistantMsg,
			{Role: "user", Content: "follow up"},
		})

		Expect(built).To(HaveLen(3))
		assistant := built[1]
		Expect(assistant.Content[0].OfRedactedThinking).NotTo(BeNil())
		Expect(assistant.Content[0].OfRedactedThinking.Data).To(Equal("encrypted-payload-from-turn-1"),
			"redacted thinking is opaque encrypted data — must replay verbatim "+
				"or Anthropic disables thinking continuity")
	})
})

var _ = Describe("buildAssistantMessage thinking round-trip", func() {
	It("emits a single text block when no thinking blocks are present (back-compat)", func() {
		m := provider.Message{Role: "assistant", Content: "hello"}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(1))
		Expect(msg.Content[0].OfText).NotTo(BeNil())
		Expect(msg.Content[0].OfText.Text).To(Equal("hello"))
	})

	It("prepends a signed thinking block before the text content", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "the answer is 42",
			ThinkingBlocks: []provider.ThinkingBlock{
				{Thinking: "weighing the request", Signature: "sig-encrypted-xyz"},
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(2),
			"thinking block must precede the text block on a replayed turn")
		Expect(msg.Content[0].OfThinking).NotTo(BeNil(),
			"the first content block must be the thinking block — Anthropic "+
				"rejects (or silently drops) thinking that comes after text")
		Expect(msg.Content[0].OfThinking.Thinking).To(Equal("weighing the request"))
		Expect(msg.Content[0].OfThinking.Signature).To(Equal("sig-encrypted-xyz"),
			"the signature must round-trip UNCHANGED — without it, the server "+
				"silently disables extended thinking on the next turn (266aec8 "+
				"per-model thinking opt-in becomes a no-op past turn 1)")
		Expect(msg.Content[1].OfText).NotTo(BeNil())
		Expect(msg.Content[1].OfText.Text).To(Equal("the answer is 42"))
	})

	It("prepends a redacted thinking block carrying the encrypted data", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "redacted answer",
			ThinkingBlocks: []provider.ThinkingBlock{
				{Redacted: true, Data: "encrypted-blob-xyz"},
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(2))
		Expect(msg.Content[0].OfRedactedThinking).NotTo(BeNil())
		Expect(msg.Content[0].OfRedactedThinking.Data).To(Equal("encrypted-blob-xyz"))
	})

	It("preserves block ordering when both signed and redacted thinking are present", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "final",
			ThinkingBlocks: []provider.ThinkingBlock{
				{Thinking: "visible", Signature: "sig-A"},
				{Redacted: true, Data: "data-B"},
				{Thinking: "more visible", Signature: "sig-C"},
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg.Content).To(HaveLen(4))
		Expect(msg.Content[0].OfThinking).NotTo(BeNil())
		Expect(msg.Content[0].OfThinking.Signature).To(Equal("sig-A"))
		Expect(msg.Content[1].OfRedactedThinking).NotTo(BeNil())
		Expect(msg.Content[1].OfRedactedThinking.Data).To(Equal("data-B"))
		Expect(msg.Content[2].OfThinking).NotTo(BeNil())
		Expect(msg.Content[2].OfThinking.Signature).To(Equal("sig-C"))
		Expect(msg.Content[3].OfText).NotTo(BeNil())
		Expect(msg.Content[3].OfText.Text).To(Equal("final"))
	})

	It("places thinking blocks before tool_use blocks (Anthropic ordering rule)", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "running tool",
			ToolCalls: []provider.ToolCall{
				{ID: "toolu_01call", Name: "bash", Arguments: map[string]any{"cmd": "ls"}},
			},
			ThinkingBlocks: []provider.ThinkingBlock{
				{Thinking: "deciding to call bash", Signature: "sig-tool"},
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(3))
		Expect(msg.Content[0].OfThinking).NotTo(BeNil(),
			"on a tool-use turn the thinking block must come BEFORE both text and "+
				"tool_use — Anthropic rejects/drops thinking otherwise")
		Expect(msg.Content[0].OfThinking.Signature).To(Equal("sig-tool"))
		Expect(msg.Content[1].OfText).NotTo(BeNil())
		Expect(msg.Content[2].OfToolUse).NotTo(BeNil())
		Expect(msg.Content[2].OfToolUse.Name).To(Equal("bash"))
	})

	It("skips empty thinking-block records so a fresh first turn is unchanged", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "hello",
			ThinkingBlocks: []provider.ThinkingBlock{
				{}, // entirely empty record — should be skipped
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(1),
			"empty thinking records must not synthesise blocks — back-compat for "+
				"providers and turns that produced no thinking")
		Expect(msg.Content[0].OfText).NotTo(BeNil())
	})

	// Production bug 2026-05-11 (req_011Cavnk52Fbsfes8zWumAcm):
	// Anthropic rejects any thinking block whose text is empty or
	// whitespace-only with HTTP 400 invalid_request_error
	// "messages.N.content.0.thinking: each thinking block must contain
	// non-whitespace thinking". The serialisation layer must filter these
	// records out before the request is built — even if a valid signature
	// is present, a signature without thinking is meaningless and the
	// API rejects it.
	It("skips a signature-only thinking-block record (empty thinking, non-empty signature)", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "hello",
			ThinkingBlocks: []provider.ThinkingBlock{
				{Thinking: "", Signature: "sig-orphan-xyz"},
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(1),
			"signature-without-thinking records must not synthesise blocks — "+
				"Anthropic rejects them with HTTP 400 invalid_request_error "+
				"(production bug req_011Cavnk52Fbsfes8zWumAcm)")
		Expect(msg.Content[0].OfText).NotTo(BeNil())
		Expect(msg.Content[0].OfText.Text).To(Equal("hello"))
	})

	It("skips a whitespace-only thinking-block record (signature valid, thinking is whitespace)", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "hello",
			ThinkingBlocks: []provider.ThinkingBlock{
				{Thinking: "   \n\t  ", Signature: "sig-whitespace-xyz"},
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(1),
			"whitespace-only thinking must not synthesise blocks — Anthropic "+
				"explicitly rejects them: 'each thinking block must contain "+
				"non-whitespace thinking' (production bug "+
				"req_011Cavnk52Fbsfes8zWumAcm)")
		Expect(msg.Content[0].OfText).NotTo(BeNil())
	})

	It("preserves a valid thinking record adjacent to an empty one (filter is per-record, not all-or-nothing)", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "hello",
			ThinkingBlocks: []provider.ThinkingBlock{
				{Thinking: "", Signature: "sig-orphan"},
				{Thinking: "real thinking", Signature: "sig-real"},
			},
		}

		msg := buildAssistantMessage(m)

		Expect(msg).NotTo(BeNil())
		Expect(msg.Content).To(HaveLen(2),
			"the valid thinking block must survive; only the empty one is filtered")
		Expect(msg.Content[0].OfThinking).NotTo(BeNil())
		Expect(msg.Content[0].OfThinking.Thinking).To(Equal("real thinking"))
		Expect(msg.Content[0].OfThinking.Signature).To(Equal("sig-real"))
		Expect(msg.Content[1].OfText).NotTo(BeNil())
	})
})

var _ = Describe("mergeConsecutiveUserMessages", func() {
	It("merges content with double newline", func() {
		last := &provider.Message{Role: "user", Content: "foo"}
		m := provider.Message{Role: "user", Content: "bar"}
		mergeConsecutiveUserMessages(last, m)
		Expect(last.Content).To(Equal("foo\n\nbar"))
	})

	It("takes second's content if first is empty", func() {
		last := &provider.Message{Role: "user", Content: ""}
		m := provider.Message{Role: "user", Content: "bar"}
		mergeConsecutiveUserMessages(last, m)
		Expect(last.Content).To(Equal("bar"))
	})

	It("joins both if both have content", func() {
		last := &provider.Message{Role: "user", Content: "foo"}
		m := provider.Message{Role: "user", Content: "baz"}
		mergeConsecutiveUserMessages(last, m)
		Expect(last.Content).To(Equal("foo\n\nbaz"))
	})

	It("no change if second is empty", func() {
		last := &provider.Message{Role: "user", Content: "foo"}
		m := provider.Message{Role: "user", Content: ""}
		mergeConsecutiveUserMessages(last, m)
		Expect(last.Content).To(Equal("foo"))
	})
})

var _ = Describe("parseAnthropicError", func() {
	Context("when error is an Anthropic API error", func() {
		It("maps 529 to Overload (Anthropic-specific)", func() {
			apiErr := newTestAPIError(529)
			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(529))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeOverload))
			Expect(result.Provider).To(Equal("anthropic"))
			Expect(result.IsRetriable).To(BeTrue())
			Expect(result.RawError).To(Equal(apiErr))
		})

		It("maps 429 to RateLimit", func() {
			apiErr := newTestAPIError(429)
			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(429))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
			Expect(result.IsRetriable).To(BeTrue())
		})

		It("maps 401 to AuthFailure", func() {
			apiErr := newTestAPIError(401)
			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(401))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeAuthFailure))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("maps 503 to Overload (Anthropic uses 503 for overload)", func() {
			apiErr := newTestAPIError(503)
			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(503))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeOverload))
			Expect(result.IsRetriable).To(BeTrue())
		})

		It("maps other 5xx to ServerError", func() {
			for _, code := range []int{500, 502, 504} {
				result := parseAnthropicError(newTestAPIError(code))
				Expect(result).To(HaveOccurred())
				Expect(result.HTTPStatus).To(Equal(code))
				Expect(result.ErrorType).To(Equal(provider.ErrorTypeServerError))
				Expect(result.IsRetriable).To(BeTrue())
			}
		})

		It("maps 400 with billing context to Billing", func() {
			apiErr := newTestAPIErrorWithBody(
				400,
				`{"message":"Your credit balance is too low"}`,
			)
			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(400))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeBilling))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("maps 400 without billing context to Unknown", func() {
			apiErr := newTestAPIError(400)
			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(400))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeUnknown))
		})

		// H7 — Anthropic 400 with a context-window-exceeded message must
		// classify as ErrorTypeContextWindowExceeded, not Unknown. The
		// previous mapping fell through to Unknown which the failover
		// hook then treated as a per-provider problem and blackballed
		// every candidate in the chain for 5 minutes — even though an
		// oversized prompt cannot fit anywhere in the chain. The fix
		// makes the classification request-level so the gating in
		// markProviderHealth can skip the unhealthy mark.
		Describe("H7 — context-window-exceeded classification on 400", func() {
			DescribeTable(
				"classifies known context-overflow phrasings as ContextWindowExceeded (non-retriable)",
				func(body string) {
					apiErr := newTestAPIErrorWithBody(400, body)
					result := parseAnthropicError(apiErr)
					Expect(result).To(HaveOccurred())
					Expect(result.HTTPStatus).To(Equal(400))
					Expect(result.ErrorType).To(
						Equal(provider.ErrorTypeContextWindowExceeded),
						"oversized-prompt 400s must be request-level, "+
							"not per-provider Unknown",
					)
					Expect(result.IsRetriable).To(BeFalse(),
						"context-window-exceeded is request-level — "+
							"retrying anywhere will fail the same way")
				},
				Entry(
					"OpenAI-style code keyword",
					`{"message":"This model's maximum context length is 200000 tokens. context_length_exceeded"}`,
				),
				Entry(
					"explicit 'context window' phrasing",
					`{"message":"prompt is too long for this model's context window"}`,
				),
				Entry(
					"'context length' phrasing",
					`{"message":"input exceeds maximum context length of 200000 tokens"}`,
				),
				Entry(
					"'prompt is too long' phrasing — Anthropic's documented wording",
					`{"message":"prompt is too long: 250000 tokens > 200000 maximum"}`,
				),
			)

			It("does NOT misclassify unrelated 400s as ContextWindowExceeded", func() {
				apiErr := newTestAPIErrorWithBody(
					400,
					`{"message":"invalid request: unsupported parameter foo"}`,
				)
				result := parseAnthropicError(apiErr)
				Expect(result).To(HaveOccurred())
				Expect(result.HTTPStatus).To(Equal(400))
				Expect(result.ErrorType).To(Equal(provider.ErrorTypeUnknown),
					"non-overflow 400s must still surface as Unknown — "+
						"the keyword detector must not over-match")
			})

			It("billing keywords still win over context keywords on 400", func() {
				// A billing-driven 400 happens to mention 'credit' which is
				// the Billing keyword — even if the message also referenced
				// context, billing is the actionable category for the user.
				apiErr := newTestAPIErrorWithBody(
					400,
					`{"message":"Your credit balance is too low to fulfill the request"}`,
				)
				result := parseAnthropicError(apiErr)
				Expect(result).To(HaveOccurred())
				Expect(result.ErrorType).To(Equal(provider.ErrorTypeBilling))
			})
		})
	})

	Context("when error is nil", func() {
		It("returns nil", func() {
			result := parseAnthropicError(nil)
			Expect(result).ToNot(HaveOccurred())
		})
	})

	Context("when error is not an Anthropic API error", func() {
		It("returns nil", func() {
			result := parseAnthropicError(errors.New("random error"))
			Expect(result).ToNot(HaveOccurred())
		})
	})

	Context("when error is wrapped", func() {
		It("extracts the Anthropic error from the chain", func() {
			apiErr := newTestAPIError(429)
			wrapped := fmt.Errorf("anthropic chat failed: %w", apiErr)
			result := parseAnthropicError(wrapped)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
		})
	})

	// Phase 3 #3 — Anthropic exposes per-window rate-limit metadata as
	// response headers on every response (success and error). On the
	// error path, capturing them on provider.Error lets the failover
	// hook honour the carrier-issued back-off (`retry-after`) instead
	// of guessing a generic per-error-type cooldown. The four windows
	// (input tokens, output tokens, requests, combined tokens) each
	// have limit / remaining / reset triples; reset is RFC 3339.
	Context("when the error response carries rate-limit headers", func() {
		It("populates RateLimit.RetryAfter from the retry-after header (seconds form)", func() {
			headers := http.Header{}
			headers.Set("retry-after", "30")
			apiErr := newTestAPIErrorWithHeaders(429, headers)

			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.RateLimit).NotTo(BeNil(),
				"a 429 with retry-after must carry the parsed metadata so failover can back off precisely")
			Expect(result.RateLimit.RetryAfter).To(Equal(30 * time.Second))
		})

		It("captures the request-id header for support correlation", func() {
			headers := http.Header{}
			headers.Set("request-id", "req_abc123")
			apiErr := newTestAPIErrorWithHeaders(429, headers)

			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.RateLimit).NotTo(BeNil())
			Expect(result.RateLimit.RequestID).To(Equal("req_abc123"))
		})

		It("captures all four window remaining/reset triples", func() {
			resetTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			headers := http.Header{}
			headers.Set("anthropic-ratelimit-input-tokens-limit", "20000")
			headers.Set("anthropic-ratelimit-input-tokens-remaining", "1000")
			headers.Set("anthropic-ratelimit-input-tokens-reset", resetTime.Format(time.RFC3339))
			headers.Set("anthropic-ratelimit-output-tokens-limit", "8000")
			headers.Set("anthropic-ratelimit-output-tokens-remaining", "500")
			headers.Set("anthropic-ratelimit-output-tokens-reset", resetTime.Format(time.RFC3339))
			headers.Set("anthropic-ratelimit-requests-limit", "1000")
			headers.Set("anthropic-ratelimit-requests-remaining", "0")
			headers.Set("anthropic-ratelimit-requests-reset", resetTime.Format(time.RFC3339))
			headers.Set("anthropic-ratelimit-tokens-limit", "28000")
			headers.Set("anthropic-ratelimit-tokens-remaining", "1500")
			headers.Set("anthropic-ratelimit-tokens-reset", resetTime.Format(time.RFC3339))
			apiErr := newTestAPIErrorWithHeaders(429, headers)

			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.RateLimit).NotTo(BeNil())
			rl := result.RateLimit
			Expect(rl.InputTokensLimit).To(Equal(20000))
			Expect(rl.InputTokensRemaining).To(Equal(1000))
			Expect(rl.InputTokensReset).To(BeTemporally("==", resetTime))
			Expect(rl.OutputTokensLimit).To(Equal(8000))
			Expect(rl.OutputTokensRemaining).To(Equal(500))
			Expect(rl.OutputTokensReset).To(BeTemporally("==", resetTime))
			Expect(rl.RequestsLimit).To(Equal(1000))
			Expect(rl.RequestsRemaining).To(Equal(0))
			Expect(rl.RequestsReset).To(BeTemporally("==", resetTime))
			Expect(rl.TokensLimit).To(Equal(28000))
			Expect(rl.TokensRemaining).To(Equal(1500))
			Expect(rl.TokensReset).To(BeTemporally("==", resetTime))
		})

		It("uses -1 sentinels for windows that were not reported", func() {
			// A response that only carries retry-after and request-id
			// (e.g. an early 429 before the per-window headers are
			// populated) must not pretend the budgets are zero. -1
			// disambiguates "not provided" from "0 remaining".
			headers := http.Header{}
			headers.Set("retry-after", "5")
			headers.Set("request-id", "req_partial")
			apiErr := newTestAPIErrorWithHeaders(429, headers)

			result := parseAnthropicError(apiErr)
			Expect(result.RateLimit).NotTo(BeNil())
			Expect(result.RateLimit.InputTokensRemaining).To(Equal(-1))
			Expect(result.RateLimit.OutputTokensRemaining).To(Equal(-1))
			Expect(result.RateLimit.RequestsRemaining).To(Equal(-1))
			Expect(result.RateLimit.TokensRemaining).To(Equal(-1))
			Expect(result.RateLimit.InputTokensReset.IsZero()).To(BeTrue())
		})
	})

	Context("when the error response carries no rate-limit headers", func() {
		It("leaves RateLimit nil for a 500 server error", func() {
			// A vanilla 500 has no rate-limit metadata; the failover
			// hook must fall back to the per-error-type cooldown.
			apiErr := newTestAPIErrorWithHeaders(500, http.Header{})

			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeServerError))
			Expect(result.RateLimit).To(BeNil(),
				"absent rate-limit headers must surface as nil so the failover hook falls back to the default cooldown")
		})

		It("leaves RateLimit nil when retry-after is unparseable", func() {
			headers := http.Header{}
			headers.Set("retry-after", "not-a-number")
			apiErr := newTestAPIErrorWithHeaders(429, headers)

			result := parseAnthropicError(apiErr)
			Expect(result).To(HaveOccurred())
			Expect(result.RateLimit).To(BeNil(),
				"a malformed header must not synthesise a phantom 0s back-off; the default cooldown wins")
		})
	})
})

// Cross-provider failover: when session history contains tool-call IDs
// emitted by a non-Anthropic provider (e.g. openai-style "call_..."),
// the Anthropic request builder must translate them to toolu_-prefixed IDs
// so the Messages API accepts them. Bug #1: tool_use_id mismatch after failover.
var _ = Describe("cross-provider failover id translation (Anthropic target)", func() {
	It("rewrites a foreign call_-style id to a toolu_-prefixed id in tool_result", func() {
		m := provider.Message{
			Role:      "tool",
			Content:   "result from previously-openai tool call",
			ToolCalls: []provider.ToolCall{{ID: "call_foreign_xyz"}},
		}
		msg := buildToolResultMessage(m)
		Expect(msg).NotTo(BeNil())
		tr := msg.Content[0].OfToolResult
		Expect(tr).NotTo(BeNil())
		Expect(tr.ToolUseID).To(HavePrefix("toolu_"))
		Expect(tr.ToolUseID).NotTo(Equal("call_foreign_xyz"))
	})

	It("rewrites a foreign call_-style id to a toolu_-prefixed id in assistant tool_use", func() {
		m := provider.Message{
			Role:    "assistant",
			Content: "Calling weather",
			ToolCalls: []provider.ToolCall{{
				ID:        "call_foreign_xyz",
				Name:      "get_weather",
				Arguments: map[string]any{"city": "London"},
			}},
		}
		msg := buildAssistantMessage(m)
		Expect(msg).NotTo(BeNil())
		// Content[0] is the text block; Content[1] is the tool_use block.
		var toolUseID string
		for _, block := range msg.Content {
			if block.OfToolUse != nil {
				toolUseID = block.OfToolUse.ID
				break
			}
		}
		Expect(toolUseID).To(HavePrefix("toolu_"))
		Expect(toolUseID).NotTo(Equal("call_foreign_xyz"))
	})

	It("preserves pairing: assistant tool_use id matches subsequent tool_result id after translation", func() {
		foreign := "call_original_from_openai"
		msgs := []provider.Message{
			{Role: "user", Content: "what is the weather"},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{
				ID: foreign, Name: "get_weather", Arguments: map[string]any{"city": "London"},
			}}},
			{Role: "tool", Content: "15c", ToolCalls: []provider.ToolCall{{ID: foreign}}},
		}
		result := buildMessages(msgs)
		Expect(result).To(HaveLen(3))
		// Assistant tool_use block
		var assistantToolUseID string
		for _, block := range result[1].Content {
			if block.OfToolUse != nil {
				assistantToolUseID = block.OfToolUse.ID
				break
			}
		}
		Expect(assistantToolUseID).To(HavePrefix("toolu_"))
		// Tool result block
		trID := result[2].Content[0].OfToolResult.ToolUseID
		Expect(trID).To(HavePrefix("toolu_"))
		// Load-bearing contract: they must match, otherwise the API returns 400 tool_use_id mismatch.
		Expect(trID).To(Equal(assistantToolUseID))
	})

	It("leaves native toolu_-prefixed ids unchanged", func() {
		m := provider.Message{
			Role:      "tool",
			Content:   "ok",
			ToolCalls: []provider.ToolCall{{ID: "toolu_01NATIVEabc"}},
		}
		msg := buildToolResultMessage(m)
		Expect(msg).NotTo(BeNil())
		tr := msg.Content[0].OfToolResult
		Expect(tr.ToolUseID).To(Equal("toolu_01NATIVEabc"))
	})
})

// Per-model payload contract — each model id family has a different
// "what is allowed" matrix. The provider's buildRequestParams fan out
// to applyModelConstraints; these specs pin the matrix in code so a
// silent regression on any branch surfaces here.
var _ = Describe("buildRequestParams per-model contract", func() {
	var p *Provider

	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	baseReq := func(model string) provider.ChatRequest {
		return provider.ChatRequest{
			Model:    model,
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		}
	}

	Context("Opus 4.7 (claude-opus-4-7*)", func() {
		It("defaults max_tokens to 128000 when caller does not specify", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-opus-4-7-20251201"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(128000)))
		})

		It("strips temperature even when caller sets it", func() {
			req := baseReq("claude-opus-4-7-20251201")
			t := 0.5
			req.Temperature = &t
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Temperature.Valid()).To(BeFalse(),
				"Opus 4.7 must omit temperature so the API uses its server-side default")
		})

		It("strips top_p and top_k when caller sets them", func() {
			req := baseReq("claude-opus-4-7-20251201")
			tp := 0.9
			tk := 40
			req.TopP = &tp
			req.TopK = &tk
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.TopP.Valid()).To(BeFalse())
			Expect(params.TopK.Valid()).To(BeFalse())
		})

		It("rejects manual thinking: enabled with errThinkingEnabledRejected", func() {
			req := baseReq("claude-opus-4-7-20251201")
			req.ThinkingMode = "enabled"
			_, _, err := p.buildRequestParams(req)
			Expect(err).To(MatchError(errThinkingEnabledRejected))
		})

		It("rejects manual thinking: enabled:N with errThinkingEnabledRejected", func() {
			req := baseReq("claude-opus-4-7-20251201")
			req.ThinkingMode = "enabled:8000"
			_, _, err := p.buildRequestParams(req)
			Expect(err).To(MatchError(errThinkingEnabledRejected))
		})

		It("accepts thinking: adaptive and writes the adaptive variant", func() {
			req := baseReq("claude-opus-4-7-20251201")
			req.ThinkingMode = "adaptive"
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Thinking.OfAdaptive).NotTo(BeNil())
			Expect(params.Thinking.OfEnabled).To(BeNil())
		})
	})

	Context("Opus 4.6 (claude-opus-4-6*)", func() {
		It("defaults max_tokens to 128000", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-opus-4-6-20251020"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(128000)))
		})

		It("threads caller temperature through", func() {
			req := baseReq("claude-opus-4-6-20251020")
			t := 0.7
			req.Temperature = &t
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Temperature.Valid()).To(BeTrue())
			Expect(params.Temperature.Value).To(BeNumerically("~", 0.7, 1e-9))
		})

		It("accepts manual thinking: enabled (deprecated but allowed)", func() {
			req := baseReq("claude-opus-4-6-20251020")
			req.ThinkingMode = "enabled:8000"
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Thinking.OfEnabled).NotTo(BeNil())
			Expect(params.Thinking.OfEnabled.BudgetTokens).To(Equal(int64(8000)))
		})
	})

	Context("Sonnet 4.6 (claude-sonnet-4-6*)", func() {
		It("defaults max_tokens to 64000", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-sonnet-4-6-20251020"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(64000)))
		})

		It("accepts thinking: adaptive", func() {
			req := baseReq("claude-sonnet-4-6-20251020")
			req.ThinkingMode = "adaptive"
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Thinking.OfAdaptive).NotTo(BeNil())
		})
	})

	Context("Sonnet 4.5 / Haiku 4.5 (claude-sonnet-4-5*, claude-haiku-4-5*)", func() {
		It("Sonnet 4.5 defaults max_tokens to 64000", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-sonnet-4-5-20251020"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(64000)))
		})

		It("Haiku 4.5 defaults max_tokens to 64000", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-haiku-4-5-20251020"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(64000)))
		})

		It("allows manual thinking: enabled with explicit budget", func() {
			req := baseReq("claude-sonnet-4-5-20251020")
			req.ThinkingMode = "enabled:4096"
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Thinking.OfEnabled).NotTo(BeNil())
			Expect(params.Thinking.OfEnabled.BudgetTokens).To(Equal(int64(4096)))
		})
	})

	Context("Opus 4 / 4.1 / 4.5 (claude-opus-4*)", func() {
		It("defaults max_tokens to 32000", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-opus-4-20250514"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(32000)))
		})

		It("threads sampling fields through verbatim", func() {
			req := baseReq("claude-opus-4-20250514")
			t := 0.4
			tp := 0.95
			tk := 100
			req.Temperature = &t
			req.TopP = &tp
			req.TopK = &tk
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Temperature.Value).To(BeNumerically("~", 0.4, 1e-9))
			Expect(params.TopP.Value).To(BeNumerically("~", 0.95, 1e-9))
			Expect(params.TopK.Value).To(Equal(int64(100)))
		})
	})

	Context("Sonnet 3.7 (claude-3-7-sonnet*)", func() {
		It("keeps the historical 4096 default when caller does not specify", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-3-7-sonnet-20250219"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(4096)))
		})

		It("allows the caller to opt into >64k output via MaxTokens", func() {
			req := baseReq("claude-3-7-sonnet-20250219")
			req.MaxTokens = 100000
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(100000)))
		})

		It("supports manual thinking: enabled", func() {
			req := baseReq("claude-3-7-sonnet-20250219")
			req.ThinkingMode = "enabled:8000"
			req.MaxTokens = 16000
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Thinking.OfEnabled).NotTo(BeNil())
		})
	})

	Context("legacy 3.5 / 3.0 models", func() {
		It("silently drops thinking — older models do not support it", func() {
			req := baseReq("claude-3-5-sonnet-20241022")
			req.ThinkingMode = "enabled:8000"
			params, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(params.Thinking.OfEnabled).To(BeNil())
			Expect(params.Thinking.OfAdaptive).To(BeNil())
			Expect(params.Thinking.OfDisabled).To(BeNil())
		})

		It("keeps the historical 4096 default", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-3-5-haiku-latest"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(4096)))
		})
	})

	Context("backwards compatibility — caller supplies nothing", func() {
		It("unknown model gets max_tokens=4096 / temperature=0", func() {
			params, _, err := p.buildRequestParams(baseReq("claude-unknown-model-vXXX"))
			Expect(err).NotTo(HaveOccurred())
			Expect(params.MaxTokens).To(Equal(int64(4096)))
			Expect(params.Temperature.Valid()).To(BeTrue())
			Expect(params.Temperature.Value).To(BeNumerically("~", 0.0, 1e-9))
		})

		It("zero ChatRequest fields are accepted on every Claude family", func() {
			for _, id := range []string{
				"claude-opus-4-7-20251201",
				"claude-opus-4-6-20251020",
				"claude-sonnet-4-6-20251020",
				"claude-sonnet-4-5-20251020",
				"claude-opus-4-20250514",
				"claude-3-7-sonnet-20250219",
				"claude-3-5-haiku-latest",
			} {
				_, _, err := p.buildRequestParams(baseReq(id))
				Expect(err).NotTo(HaveOccurred(), "model %s rejected zero ChatRequest", id)
			}
		})
	})

	// Assistant prefill — when the request's last message has role
	// "assistant", the server is asked to continue from that prefill.
	// Models in the 4.6+ family (Opus 4.6, Sonnet 4.6, Opus 4.7) reject
	// this with HTTP 400 "Prefilling assistant messages is not supported
	// for this model." We catch this client-side so callers see a clear
	// error before the request is sent.
	Context("assistant prefill rejection (4.6+ family)", func() {
		prefillReq := func(model string) provider.ChatRequest {
			return provider.ChatRequest{
				Model: model,
				Messages: []provider.Message{
					{Role: "user", Content: "hi"},
					{Role: "assistant", Content: "{"},
				},
			}
		}

		It("Opus 4.7 rejects an assistant-role last message", func() {
			_, _, err := p.buildRequestParams(prefillReq("claude-opus-4-7-20251201"))
			Expect(err).To(MatchError(errAssistantPrefillRejected))
			Expect(err.Error()).To(ContainSubstring("claude-opus-4-7-20251201"))
			Expect(err.Error()).To(ContainSubstring("assistant prefill"))
		})

		It("Opus 4.6 rejects an assistant-role last message", func() {
			_, _, err := p.buildRequestParams(prefillReq("claude-opus-4-6-20251020"))
			Expect(err).To(MatchError(errAssistantPrefillRejected))
			Expect(err.Error()).To(ContainSubstring("claude-opus-4-6-20251020"))
		})

		It("Sonnet 4.6 rejects an assistant-role last message", func() {
			_, _, err := p.buildRequestParams(prefillReq("claude-sonnet-4-6-20251020"))
			Expect(err).To(MatchError(errAssistantPrefillRejected))
			Expect(err.Error()).To(ContainSubstring("claude-sonnet-4-6-20251020"))
		})

		It("Sonnet 4.5 accepts an assistant-role last message (prefill allowed)", func() {
			_, _, err := p.buildRequestParams(prefillReq("claude-sonnet-4-5-20251020"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("Sonnet 3.7 accepts an assistant-role last message (prefill allowed)", func() {
			_, _, err := p.buildRequestParams(prefillReq("claude-3-7-sonnet-20250219"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("Opus 4.7 with last message role=user is unaffected (no prefill)", func() {
			req := provider.ChatRequest{
				Model: "claude-opus-4-7-20251201",
				Messages: []provider.Message{
					{Role: "assistant", Content: "earlier turn"},
					{Role: "user", Content: "follow-up"},
				},
			}
			_, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Opus 4.7 with empty messages is unaffected (no prefill present)", func() {
			req := provider.ChatRequest{
				Model:    "claude-opus-4-7-20251201",
				Messages: nil,
			}
			_, _, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// Thinking-parameter validation operates on the assembled
// MessageNewParams so the matrix is exercised directly without going
// through the per-model branches.
var _ = Describe("thinking constraint validation", func() {
	var p *Provider
	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	It("rejects budget_tokens < 1024 with errThinkingBudgetTooLow", func() {
		req := provider.ChatRequest{
			Model:        "claude-sonnet-4-5-20251020",
			Messages:     []provider.Message{{Role: "user", Content: "hi"}},
			ThinkingMode: "enabled:512",
		}
		_, _, err := p.buildRequestParams(req)
		Expect(err).To(MatchError(errThinkingBudgetTooLow))
	})

	It("rejects budget_tokens >= max_tokens with errThinkingBudgetExceedsMax", func() {
		req := provider.ChatRequest{
			Model:        "claude-sonnet-4-5-20251020",
			Messages:     []provider.Message{{Role: "user", Content: "hi"}},
			MaxTokens:    8000,
			ThinkingMode: "enabled:8000",
		}
		_, _, err := p.buildRequestParams(req)
		Expect(err).To(MatchError(errThinkingBudgetExceedsMax))
	})

	It("returns an error for invalid enabled:N parse", func() {
		req := provider.ChatRequest{
			Model:        "claude-sonnet-4-5-20251020",
			Messages:     []provider.Message{{Role: "user", Content: "hi"}},
			ThinkingMode: "enabled:not-a-number",
		}
		_, _, err := p.buildRequestParams(req)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("invalid thinking mode"))
	})

	It("explicit thinking: disabled writes the disabled variant", func() {
		req := provider.ChatRequest{
			Model:        "claude-opus-4-7-20251201",
			Messages:     []provider.Message{{Role: "user", Content: "hi"}},
			ThinkingMode: "disabled",
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.Thinking.OfDisabled).NotTo(BeNil())
		Expect(params.Thinking.OfAdaptive).To(BeNil())
		Expect(params.Thinking.OfEnabled).To(BeNil())
	})
})

// Tool choice mapping — the SDK union accepts auto/any/tool/none. The
// {auto, none}-only contract applies only when thinking is on; without
// thinking every variant is allowed.
var _ = Describe("tool_choice mapping", func() {
	var p *Provider
	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	makeReq := func(choice, thinking string) provider.ChatRequest {
		return provider.ChatRequest{
			Model:        "claude-sonnet-4-5-20251020",
			Messages:     []provider.Message{{Role: "user", Content: "hi"}},
			MaxTokens:    16000,
			ThinkingMode: thinking,
			ToolChoice:   choice,
		}
	}

	It("maps auto", func() {
		params, _, err := p.buildRequestParams(makeReq("auto", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(params.ToolChoice.OfAuto).NotTo(BeNil())
	})

	It("maps any", func() {
		params, _, err := p.buildRequestParams(makeReq("any", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(params.ToolChoice.OfAny).NotTo(BeNil())
	})

	It("maps none", func() {
		params, _, err := p.buildRequestParams(makeReq("none", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(params.ToolChoice.OfNone).NotTo(BeNil())
	})

	It("maps tool:NAME", func() {
		params, _, err := p.buildRequestParams(makeReq("tool:get_weather", ""))
		Expect(err).NotTo(HaveOccurred())
		Expect(params.ToolChoice.OfTool).NotTo(BeNil())
		Expect(params.ToolChoice.OfTool.Name).To(Equal("get_weather"))
	})

	It("rejects 'any' when thinking is on", func() {
		_, _, err := p.buildRequestParams(makeReq("any", "enabled:4096"))
		Expect(err).To(MatchError(errThinkingToolChoiceInvalid))
	})

	It("rejects 'tool:X' when thinking is on", func() {
		_, _, err := p.buildRequestParams(makeReq("tool:foo", "enabled:4096"))
		Expect(err).To(MatchError(errThinkingToolChoiceInvalid))
	})

	It("allows 'auto' when thinking is on", func() {
		params, _, err := p.buildRequestParams(makeReq("auto", "enabled:4096"))
		Expect(err).NotTo(HaveOccurred())
		Expect(params.ToolChoice.OfAuto).NotTo(BeNil())
	})

	It("allows 'none' when thinking is on", func() {
		params, _, err := p.buildRequestParams(makeReq("none", "enabled:4096"))
		Expect(err).NotTo(HaveOccurred())
		Expect(params.ToolChoice.OfNone).NotTo(BeNil())
	})

	It("rejects an unrecognised tool_choice with a clear error", func() {
		_, _, err := p.buildRequestParams(makeReq("garbage", ""))
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("unrecognised tool_choice"))
	})
})

// ChatRequest threading — the optional fields on provider.ChatRequest
// (MaxTokens, Temperature, TopP, TopK) reach the assembled
// MessageNewParams when the model permits them.
var _ = Describe("ChatRequest field threading", func() {
	var p *Provider
	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	It("MaxTokens overrides per-model default when set", func() {
		req := provider.ChatRequest{
			Model:     "claude-opus-4-7-20251201", // default would be 128k
			Messages:  []provider.Message{{Role: "user", Content: "hi"}},
			MaxTokens: 8192,
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.MaxTokens).To(Equal(int64(8192)))
	})

	It("Temperature pointer threads through on permissive models", func() {
		req := provider.ChatRequest{
			Model:    "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		}
		t := 0.3
		req.Temperature = &t
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.Temperature.Value).To(BeNumerically("~", 0.3, 1e-9))
	})

	It("nil Temperature falls back to historical 0", func() {
		req := provider.ChatRequest{
			Model:    "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.Temperature.Valid()).To(BeTrue())
		Expect(params.Temperature.Value).To(BeNumerically("~", 0.0, 1e-9))
	})
})

// Beta-header injection — interleaved-thinking-2025-05-14.
//
// The header gates whether Claude is allowed to interleave thinking
// blocks with tool_use blocks within a single turn. Without it on
// Claude 4 / 4.1 / 4.5 / Sonnet 4 / 4.5 / Haiku 4.5, thinking happens
// once at the top of the turn and tools fire without further thinking
// — degraded multi-step reasoning. On Claude 4.6+ (Opus 4.6/4.7,
// Sonnet 4.6) interleaving is auto-enabled server-side; sending the
// header is harmless on the direct API but REJECTED on Bedrock and
// Vertex passthroughs, so it must NOT be sent for those models.
//
// The header is only useful when BOTH thinking is on AND tools are
// present, so the classifier gates on both. This pins the matrix in
// code so a silent regression on any branch surfaces here.
var _ = Describe("interleaved-thinking beta header", func() {
	var p *Provider

	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	tool := provider.Tool{
		Name:        "echo",
		Description: "echo input",
	}

	reqWith := func(model, mode string, tools []provider.Tool) provider.ChatRequest {
		return provider.ChatRequest{
			Model:        model,
			Messages:     []provider.Message{{Role: "user", Content: "hi"}},
			Tools:        tools,
			ThinkingMode: mode,
		}
	}

	Describe("classifier — modelDefaults.betaHeaders", func() {
		It("Sonnet 4.5 emits the header when thinking is on AND tools are present", func() {
			defs := resolveModelDefaults("claude-sonnet-4-5-20251020")
			Expect(defs.betaHeaders(true, true, 4096)).To(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 4.5 omits the header when thinking is on but tools are empty", func() {
			defs := resolveModelDefaults("claude-sonnet-4-5-20251020")
			Expect(defs.betaHeaders(true, false, 4096)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 4.5 omits the header when thinking is off but tools are present", func() {
			defs := resolveModelDefaults("claude-sonnet-4-5-20251020")
			Expect(defs.betaHeaders(false, true, 4096)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Opus 4.7 omits the header even with thinking on AND tools present (auto-enabled server-side)", func() {
			defs := resolveModelDefaults("claude-opus-4-7-20251201")
			Expect(defs.betaHeaders(true, true, 4096)).NotTo(ContainElement(interleavedThinkingBetaHeader),
				"4.6+ family auto-enables interleaving; explicit header is rejected on Bedrock/Vertex")
		})

		It("Opus 4.6 omits the header (auto-enabled server-side)", func() {
			defs := resolveModelDefaults("claude-opus-4-6-20251020")
			Expect(defs.betaHeaders(true, true, 4096)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 4.6 omits the header (auto-enabled server-side)", func() {
			defs := resolveModelDefaults("claude-sonnet-4-6-20251020")
			Expect(defs.betaHeaders(true, true, 4096)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 3.7 omits the interleaved-thinking header (interleaving not supported)", func() {
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			Expect(defs.betaHeaders(true, true, 4096)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Opus 4.1 emits the header when thinking is on AND tools are present", func() {
			defs := resolveModelDefaults("claude-opus-4-1-20250805")
			Expect(defs.betaHeaders(true, true, 4096)).To(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Haiku 4.5 emits the header when thinking is on AND tools are present", func() {
			defs := resolveModelDefaults("claude-haiku-4-5-20251020")
			Expect(defs.betaHeaders(true, true, 4096)).To(ContainElement(interleavedThinkingBetaHeader))
		})
	})

	Describe("wiring — buildRequestParams threads the option through", func() {
		// The classifier specs above pin the per-model matrix on
		// pure data. These specs pin that buildRequestParams plumbs
		// the same matrix through to the per-call opts slice, by
		// asserting the slice length matches what the classifier
		// said. Length 1 is unambiguous because the only families
		// that emit a static beta (`betas []string` field) are not
		// exercised here — we never see the slice grow past 1.

		It("Sonnet 4.5 + adaptive + tools → exactly one beta opt", func() {
			req := reqWith("claude-sonnet-4-5-20251020", "adaptive", []provider.Tool{tool})
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(HaveLen(1),
				"Sonnet 4.5 with thinking on AND tools must emit exactly the "+
					"interleaved-thinking beta header")
		})

		It("Sonnet 4.5 + enabled:N + tools → exactly one beta opt", func() {
			req := provider.ChatRequest{
				Model:        "claude-sonnet-4-5-20251020",
				Messages:     []provider.Message{{Role: "user", Content: "hi"}},
				Tools:        []provider.Tool{tool},
				ThinkingMode: "enabled:4096",
				MaxTokens:    16000,
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(HaveLen(1))
		})

		It("Opus 4.7 + adaptive + tools → no beta opt", func() {
			req := reqWith("claude-opus-4-7-20251201", "adaptive", []provider.Tool{tool})
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"Opus 4.7 auto-enables interleaving; explicit header fails on Bedrock/Vertex")
		})

		It("Sonnet 3.7 + enabled + tools → opts must NOT include interleaved-thinking", func() {
			// Sonnet 3.7 does not support interleaving, so the
			// interleaved-thinking header must never appear. The
			// request DOES however get the Sonnet 3.7
			// token-efficient-tools beta because tools are present —
			// pinned in the dedicated Sonnet 3.7 spec block below.
			// Here we only assert the interleaved-thinking gate.
			req := provider.ChatRequest{
				Model:        "claude-3-7-sonnet-20250219",
				Messages:     []provider.Message{{Role: "user", Content: "hi"}},
				Tools:        []provider.Tool{tool},
				ThinkingMode: "enabled:4096",
				MaxTokens:    16000,
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(HaveLen(1),
				"Sonnet 3.7 with tools earns the token-efficient-tools "+
					"beta but never the interleaved-thinking beta")
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			Expect(defs.betaHeaders(true, true, 16000)).
				NotTo(ContainElement(interleavedThinkingBetaHeader),
					"Sonnet 3.7 does not support interleaving — header must not be sent")
		})

		It("Sonnet 4.5 + thinking on but tools EMPTY → no beta opt", func() {
			req := reqWith("claude-sonnet-4-5-20251020", "adaptive", nil)
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"interleaved-thinking is meaningless without tools — must not be sent")
		})

		It("Sonnet 4.5 + thinking OFF + tools present → no beta opt", func() {
			req := reqWith("claude-sonnet-4-5-20251020", "", []provider.Tool{tool})
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"interleaved-thinking requires thinking to actually be on")
		})

		It("Sonnet 4.5 + thinking: disabled + tools present → no beta opt", func() {
			req := reqWith("claude-sonnet-4-5-20251020", "disabled", []provider.Tool{tool})
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"explicit thinking: disabled means thinking is OFF — interleaved header must not be sent")
		})

		It("back-compat: no thinking, no tools → opts are empty (identical to today's request)", func() {
			req := reqWith("claude-sonnet-4-5-20251020", "", nil)
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"requests without thinking or without tools must produce identical headers as today")
		})

		It("unknown model → opts are empty", func() {
			req := reqWith("claude-unknown-model-vXXX", "adaptive", []provider.Tool{tool})
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty())
		})
	})

	// buildBetaHeaderOptions is the wire-transformer between the
	// string slice the classifier returns and the SDK's
	// option.RequestOption type. It must produce one option per
	// header value (the SDK's WithHeaderAdd appends each onto the
	// same `anthropic-beta` HTTP header, comma-joining on the wire),
	// and must return nil on empty input so the call site stays
	// allocation-free for the steady-state path.
	Describe("buildBetaHeaderOptions transformer", func() {
		It("returns nil for an empty input", func() {
			Expect(buildBetaHeaderOptions(nil)).To(BeNil())
			Expect(buildBetaHeaderOptions([]string{})).To(BeNil())
		})

		It("emits one option per beta value", func() {
			opts := buildBetaHeaderOptions([]string{"a", "b", "c"})
			Expect(opts).To(HaveLen(3))
		})
	})
})

// constantWireValue pins the on-the-wire spelling of the
// interleaved-thinking beta header. The constant in production must
// match exactly — Anthropic checks for this literal string.
var _ = Describe("interleavedThinkingBetaHeader wire spelling", func() {
	It("matches the published Anthropic beta name", func() {
		Expect(interleavedThinkingBetaHeader).To(Equal("interleaved-thinking-2025-05-14"))
	})
})

// Beta-header injection — Sonnet 3.7 (token-efficient-tools and
// output-128k).
//
// Sonnet 3.7 ships two opt-in betas that callers should not have to
// manage manually:
//
//   - `token-efficient-tools-2025-02-19` reduces output tokens on
//     tool-using turns by ~14-70%. Harmless when the model does not
//     emit a tool_use block, so the gate is "tools are declared".
//   - `output-128k-2025-02-19` lifts the default 64k max_tokens cap to
//     128k. Triggered by caller intent — when MaxTokens > 64000 we
//     auto-attach the header so the upstream request is accepted.
//
// On Claude 4+ both headers are silently ignored on the direct API but
// we strip them for request cleanliness. On Sonnet 3.7 they coexist
// happily with one another (and with no other beta — Sonnet 3.7 does
// not get the interleaved-thinking header).
//
// The classifier specs below pin the per-model matrix on pure data;
// the wiring specs below assert that buildRequestParams plumbs the
// matrix through to the per-call opts slice end-to-end.
var _ = Describe("Sonnet 3.7 beta headers", func() {
	var p *Provider

	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	tool := provider.Tool{Name: "echo", Description: "echo input"}

	Describe("classifier — modelDefaults.betaHeaders", func() {
		It("Sonnet 3.7 + tools → token-efficient-tools header is present", func() {
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			Expect(defs.betaHeaders(false, true, 4096)).
				To(ContainElement(tokenEfficientToolsBetaHeader))
		})

		It("Sonnet 3.7 + max_tokens=128000 → output-128k header is present", func() {
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			Expect(defs.betaHeaders(false, false, 128000)).
				To(ContainElement(output128kBetaHeader))
		})

		It("Sonnet 3.7 + tools + max_tokens=100000 → BOTH headers are present", func() {
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			betas := defs.betaHeaders(false, true, 100000)
			Expect(betas).To(ContainElement(tokenEfficientToolsBetaHeader))
			Expect(betas).To(ContainElement(output128kBetaHeader))
		})

		It("Sonnet 3.7 + no tools + max_tokens=4096 → NEITHER header is present", func() {
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			betas := defs.betaHeaders(false, false, 4096)
			Expect(betas).NotTo(ContainElement(tokenEfficientToolsBetaHeader))
			Expect(betas).NotTo(ContainElement(output128kBetaHeader))
		})

		It("Sonnet 3.7 + max_tokens exactly at threshold (64000) → output-128k header NOT present", func() {
			// The threshold is strictly greater-than: 64000 is the
			// post-beta cap that's already legal without the header,
			// so we must not opt in until the caller actually exceeds
			// it. This pins the boundary so an off-by-one regression
			// on the inequality surfaces here.
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			Expect(defs.betaHeaders(false, false, 64000)).
				NotTo(ContainElement(output128kBetaHeader))
		})

		It("Opus 4.7 + tools + max_tokens=128000 → NEITHER Sonnet 3.7 beta is present", func() {
			defs := resolveModelDefaults("claude-opus-4-7-20251201")
			betas := defs.betaHeaders(false, true, 128000)
			Expect(betas).NotTo(ContainElement(tokenEfficientToolsBetaHeader),
				"only Sonnet 3.7 honours token-efficient-tools — strip on 4.x")
			Expect(betas).NotTo(ContainElement(output128kBetaHeader),
				"only Sonnet 3.7 honours output-128k — strip on 4.x")
		})

		It("Sonnet 4.5 + tools → NEITHER Sonnet 3.7 beta is present", func() {
			defs := resolveModelDefaults("claude-sonnet-4-5-20251020")
			betas := defs.betaHeaders(false, true, 64000)
			Expect(betas).NotTo(ContainElement(tokenEfficientToolsBetaHeader))
			Expect(betas).NotTo(ContainElement(output128kBetaHeader))
		})

		It("Sonnet 3.7 + tools + thinking on → does NOT get interleaved-thinking", func() {
			// Pins the coexistence contract: Sonnet 3.7 can earn the
			// 3.7 betas but never the 4.x interleaved-thinking header.
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			betas := defs.betaHeaders(true, true, 100000)
			Expect(betas).To(ContainElement(tokenEfficientToolsBetaHeader))
			Expect(betas).To(ContainElement(output128kBetaHeader))
			Expect(betas).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})
	})

	Describe("wiring — buildRequestParams threads the options through", func() {
		It("Sonnet 3.7 + tools → opts include the token-efficient-tools beta", func() {
			req := provider.ChatRequest{
				Model:    "claude-3-7-sonnet-20250219",
				Messages: []provider.Message{{Role: "user", Content: "hi"}},
				Tools:    []provider.Tool{tool},
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(HaveLen(1),
				"tools-only request on Sonnet 3.7 emits exactly the "+
					"token-efficient-tools beta")
		})

		It("Sonnet 3.7 + max_tokens=128000 → opts include the output-128k beta", func() {
			req := provider.ChatRequest{
				Model:     "claude-3-7-sonnet-20250219",
				Messages:  []provider.Message{{Role: "user", Content: "hi"}},
				MaxTokens: 128000,
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(HaveLen(1),
				"max_tokens above the 64k threshold on Sonnet 3.7 "+
					"emits exactly the output-128k beta")
		})

		It("Sonnet 3.7 + tools + max_tokens=100000 → opts include BOTH betas", func() {
			req := provider.ChatRequest{
				Model:     "claude-3-7-sonnet-20250219",
				Messages:  []provider.Message{{Role: "user", Content: "hi"}},
				Tools:     []provider.Tool{tool},
				MaxTokens: 100000,
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(HaveLen(2),
				"both betas must be wired through when both gates fire")
		})

		It("Sonnet 3.7 + no tools + max_tokens=4096 → opts are empty", func() {
			req := provider.ChatRequest{
				Model:     "claude-3-7-sonnet-20250219",
				Messages:  []provider.Message{{Role: "user", Content: "hi"}},
				MaxTokens: 4096,
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"steady-state Sonnet 3.7 request must remain identical to today")
		})

		It("Opus 4.7 + tools + max_tokens=200000 (clamped to 128k) → opts are empty", func() {
			// Opus 4.7's per-model max_tokens ceiling is 128k; we
			// pass 128000 directly because applyMaxTokens does not
			// clamp, but the spirit of the brief is "Opus 4.7 with a
			// large budget gets neither 3.7 beta". Use exactly 128000
			// to match the model's documented cap and assert no 3.7
			// betas appear regardless of how big the request is.
			req := provider.ChatRequest{
				Model:     "claude-opus-4-7-20251201",
				Messages:  []provider.Message{{Role: "user", Content: "hi"}},
				Tools:     []provider.Tool{tool},
				MaxTokens: 128000,
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"Claude 4.x must not emit Sonnet 3.7 betas — request must stay clean")
		})

		It("Sonnet 4.5 + tools → opts must NOT include any Sonnet 3.7 beta", func() {
			// Sonnet 4.5 does emit interleaved-thinking when thinking
			// is on; here we leave thinking off so the only gate that
			// could fire is the 3.7-only ones, and they must not.
			req := provider.ChatRequest{
				Model:    "claude-sonnet-4-5-20251020",
				Messages: []provider.Message{{Role: "user", Content: "hi"}},
				Tools:    []provider.Tool{tool},
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
				"Sonnet 4.5 with no thinking must produce zero beta opts — "+
					"only the 3.7 family gets these betas")
		})
	})

	// Wire-spelling pins for the two new beta header constants. The
	// strings must match the published Anthropic names exactly —
	// Anthropic checks for these literals.
	Describe("wire spelling", func() {
		It("tokenEfficientToolsBetaHeader matches the published name", func() {
			Expect(tokenEfficientToolsBetaHeader).To(Equal("token-efficient-tools-2025-02-19"))
		})

		It("output128kBetaHeader matches the published name", func() {
			Expect(output128kBetaHeader).To(Equal("output-128k-2025-02-19"))
		})
	})
})

// OAuth wire headers — wire-level assertions.
//
// The Claude CLI mimics four headers on every request to route OAuth
// traffic to the Claude Code billing pool:
//   - `x-anthropic-billing-header` — billing-routing metadata
//     (cc_version, cc_entrypoint, cch).
//   - `anthropic-beta` — must include `oauth-2025-04-20`. Anthropic's
//     edge uses this beta value to recognise OAuth-issued requests.
//     Other beta values (e.g. `interleaved-thinking-2025-05-14`) can
//     comma-join onto the same header on a per-call basis, so OAuth
//     coverage is asserted as a substring.
//   - `user-agent` — exact `claude-cli/2.1.2 (external, cli)`.
//   - `x-app` — exact `cli`.
//
// All four are equally load-bearing: a silent regression on any one
// of them re-classifies the user's traffic out of the Claude Code
// pool without surfacing an error. An earlier (incorrect)
// implementation injected the billing header as a synthetic
// `TextBlockParam` at the front of the system prompt, which (a)
// wasted ~30 input tokens per request, (b) leaked routing metadata
// into the model's view of the system prompt, and (c) sat in front
// of the caller's first cache_control breakpoint as an uncached
// prefix, invalidating the cache key.
//
// These specs pin the wire shape end-to-end:
//   - OAuth requests carry all four headers on the wire.
//   - OAuth requests' system prompt is exactly what the caller sent
//     (no synthetic prefix, caller's cache_control intact on the
//     first system block).
//   - API-key requests do NOT carry the OAuth-only headers (they
//     were never meaningful on the API-key path).
//
// We use a httptest server with WithBaseURL to capture the
// SDK-issued HTTP request. No live API calls.
var _ = Describe("OAuth wire headers", func() {
	type captured struct {
		headers http.Header
		body    map[string]any
	}

	// minimalMessageResponse returns the smallest body the
	// anthropic-sdk-go decoder accepts as a successful Message.
	// Just enough to let p.Chat unwind without erroring so the
	// captured request is the focus of the assertion.
	minimalMessageResponse := func() string {
		return `{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"model": "claude-3-5-sonnet-20241022",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"usage": {"input_tokens": 1, "output_tokens": 1}
		}`
	}

	captureServer := func(c *captured) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				c.headers = r.Header.Clone()
				_ = json.NewDecoder(r.Body).Decode(&c.body)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, minimalMessageResponse())
			},
		))
	}

	chatReq := func() provider.ChatRequest {
		return provider.ChatRequest{
			Model: "claude-3-5-sonnet-20241022",
			Messages: []provider.Message{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "hello"},
			},
		}
	}

	Context("OAuth path", func() {
		var (
			server *httptest.Server
			cap    captured
			p      *Provider
		)

		BeforeEach(func() {
			cap = captured{}
			server = captureServer(&cap)
			p = &Provider{
				client: newOAuthClient(
					"sk-ant-oat01-test",
					option.WithBaseURL(server.URL),
				),
				isOAuth:      true,
				tokenManager: NewDirectTokenManager("sk-ant-oat01-test"),
				currentToken: "sk-ant-oat01-test",
			}
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("sends x-anthropic-billing-header as a real HTTP header", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			Expect(cap.headers.Get("X-Anthropic-Billing-Header")).
				To(Equal("cc_version=2.1.80.a46; cc_entrypoint=sdk-cli; cch=00000;"),
					"OAuth requests must carry the billing-routing "+
						"metadata on the wire, byte-for-byte matching "+
						"the Claude CLI")
		})

		It("sends anthropic-beta containing oauth-2025-04-20", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			// Substring-match: the SDK appends per-call betas
			// (e.g. interleaved-thinking-2025-05-14) onto the same
			// `anthropic-beta` HTTP header by comma-joining values.
			// We only need to pin that the OAuth marker is present;
			// other betas may legitimately appear alongside it.
			Expect(cap.headers.Get("Anthropic-Beta")).
				To(ContainSubstring(oauthBetaHeader),
					"OAuth requests must carry oauth-2025-04-20 on "+
						"anthropic-beta — Anthropic's edge uses this "+
						"value to recognise OAuth-issued requests")
		})

		It("sends user-agent matching the Claude CLI exactly", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			Expect(cap.headers.Get("User-Agent")).
				To(Equal(oauthUserAgent),
					"OAuth requests must spoof the Claude CLI user-agent "+
						"byte-for-byte; any drift re-classifies traffic "+
						"out of the Claude Code billing pool")
		})

		It("sends x-app: cli", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			Expect(cap.headers.Get("X-App")).
				To(Equal(oauthAppHeader),
					"OAuth requests must declare x-app: cli to match "+
						"the Claude CLI's edge classification")
		})

		It("does NOT inject the billing header as a synthetic system-prompt block", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			sys, ok := cap.body["system"].([]any)
			Expect(ok).To(BeTrue(), "system field must be present and an array")
			for i, blk := range sys {
				bm, ok := blk.(map[string]any)
				Expect(ok).To(BeTrue(), "system[%d] must be an object", i)
				text, _ := bm["text"].(string)
				Expect(text).NotTo(ContainSubstring("x-anthropic-billing-header"),
					"system[%d] must not leak the billing header as text", i)
				Expect(text).NotTo(ContainSubstring("cc_version="),
					"system[%d] must not leak billing-routing metadata as text", i)
				Expect(text).NotTo(ContainSubstring("cc_entrypoint="),
					"system[%d] must not leak billing-routing metadata as text", i)
			}
		})

		It("preserves the caller's cache_control on the first system block (no synthetic prefix)", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			sys, ok := cap.body["system"].([]any)
			Expect(ok).To(BeTrue())
			Expect(sys).To(HaveLen(1),
				"OAuth must NOT prepend a synthetic block — the only "+
					"block on the wire is the caller's system message")
			first, _ := sys[0].(map[string]any)
			Expect(first["text"]).To(Equal("You are a helpful assistant."),
				"first system block must be the caller's content verbatim")
			cc, hasCC := first["cache_control"].(map[string]any)
			Expect(hasCC).To(BeTrue(),
				"first system block must carry the caller's "+
					"cache_control — a synthetic uncached prefix would "+
					"break the cache key by sitting in front of it")
			Expect(cc["type"]).To(Equal("ephemeral"))
		})

		It("constants match the wire spelling expected by Anthropic", func() {
			Expect(oauthBillingHeaderName).To(Equal("x-anthropic-billing-header"))
			Expect(oauthBillingHeaderValue).To(Equal(
				"cc_version=2.1.80.a46; cc_entrypoint=sdk-cli; cch=00000;"))
			Expect(oauthBetaHeader).To(Equal("oauth-2025-04-20"))
			Expect(oauthUserAgent).To(Equal("claude-cli/2.1.2 (external, cli)"))
			Expect(oauthAppHeader).To(Equal("cli"))
		})
	})

	Context("API-key path", func() {
		var (
			server *httptest.Server
			cap    captured
			p      *Provider
		)

		BeforeEach(func() {
			cap = captured{}
			server = captureServer(&cap)
			var err error
			p, err = NewWithOptions(
				"test-api-key",
				option.WithBaseURL(server.URL),
			)
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		It("does NOT send x-anthropic-billing-header (only OAuth carries it)", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			Expect(cap.headers.Get("X-Anthropic-Billing-Header")).To(BeEmpty(),
				"API-key path must remain header-clean — the billing "+
					"header is OAuth-only routing metadata")
		})

		It("does NOT carry the OAuth anthropic-beta marker", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			// API-key with no tools / no thinking on a stable model
			// (claude-3-5-sonnet-20241022) emits no per-call betas;
			// the OAuth marker in particular is OAuth-only and must
			// never appear here. Asserted as "no oauth-2025-04-20
			// substring anywhere" rather than empty so this stays
			// robust if a future model adds an unrelated beta.
			Expect(cap.headers.Get("Anthropic-Beta")).
				NotTo(ContainSubstring(oauthBetaHeader),
					"API-key path must not carry oauth-2025-04-20 — "+
						"the OAuth beta marker is what tells "+
						"Anthropic's edge the request came from an "+
						"OAuth-issued client")
		})

		It("does NOT spoof the Claude CLI user-agent", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			Expect(cap.headers.Get("User-Agent")).
				NotTo(Equal(oauthUserAgent),
					"API-key path must not mimic the Claude CLI; "+
						"sending claude-cli/2.1.2 from an API-key "+
						"client would mis-classify traffic at "+
						"Anthropic's edge")
		})

		It("does NOT send x-app: cli", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			Expect(cap.headers.Get("X-App")).To(BeEmpty(),
				"API-key path must not declare x-app — the header "+
					"is part of the CLI-mimicking set and only "+
					"belongs on OAuth requests")
		})

		It("does NOT leak billing metadata into the system prompt", func() {
			_, err := p.Chat(context.Background(), chatReq())
			Expect(err).NotTo(HaveOccurred())
			sys, ok := cap.body["system"].([]any)
			Expect(ok).To(BeTrue())
			Expect(sys).To(HaveLen(1))
			first, _ := sys[0].(map[string]any)
			text, _ := first["text"].(string)
			Expect(text).To(Equal("You are a helpful assistant."))
		})
	})
})

// Cache breakpoint placement strategy.
//
// Anthropic accepts a maximum of 4 cache_control breakpoints per
// request. A breakpoint marks the END of a cacheable prefix; everything
// from the start of the request (or the previous breakpoint) up to and
// including that block becomes the cache key. Marking every block of a
// homogeneous prefix is wasteful — only the LAST block of the prefix
// needs the breakpoint. The earlier blocks are covered automatically.
//
// FlowState's chosen strategy for multi-turn agent conversations:
//
//  1. End of system prefix       — one breakpoint on the LAST system block
//  2. End of tool definitions    — one breakpoint on the LAST tool
//  3. End of conversation prefix — one breakpoint on the LAST assistant
//     message's last content block
//  4. Reserved                   — left unused so we never exceed 4
//
// Only the LAST block of each homogeneous prefix carries the breakpoint;
// earlier blocks ride inside the prefix the breakpoint anchors. This
// keeps us at most 3 breakpoints, well under the hard cap of 4 — leaving
// room for caller-driven overrides without a 400 from the API.
//
// TODO(min-cacheable-length): Anthropic silently drops cache attempts
// shorter than the per-model minimum (~4096 tokens for Opus 4.7, ~2048
// for Sonnet 4.6, ~1024 for older). We do not gate on length yet — the
// bigger correctness win was breakpoint COUNT, not length. Add a
// per-model `minCacheableTokens` table and a token-count gate before
// shipping prompt-cache hit-rate stats (Phase 3 #4).
var _ = Describe("extractSystemPrompt cache breakpoint placement", func() {
	var p *Provider
	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	It("places a breakpoint on ONLY the last block when there are 3 system blocks", func() {
		msgs := []provider.Message{
			{Role: "system", Content: "system block 1"},
			{Role: "system", Content: "system block 2"},
			{Role: "system", Content: "system block 3"},
			{Role: "user", Content: "hi"},
		}
		blocks := p.extractSystemPrompt(msgs)
		Expect(blocks).To(HaveLen(3))

		Expect(string(blocks[0].CacheControl.Type)).To(BeEmpty(),
			"earliest system block must NOT carry a breakpoint — it is "+
				"already inside the cacheable prefix the final breakpoint "+
				"anchors; marking it wastes one of Anthropic's 4 slots")
		Expect(string(blocks[1].CacheControl.Type)).To(BeEmpty(),
			"middle system block must NOT carry a breakpoint")
		Expect(string(blocks[2].CacheControl.Type)).To(Equal("ephemeral"),
			"the LAST system block anchors the entire system prefix — "+
				"this is the breakpoint Anthropic uses as the cache key")
	})

	It("places a breakpoint on the only block when there is 1 system block", func() {
		msgs := []provider.Message{
			{Role: "system", Content: "single system message"},
			{Role: "user", Content: "hi"},
		}
		blocks := p.extractSystemPrompt(msgs)
		Expect(blocks).To(HaveLen(1))
		Expect(string(blocks[0].CacheControl.Type)).To(Equal("ephemeral"),
			"a single system block is its own end-of-prefix — must carry "+
				"the breakpoint so the system prompt is cached")
	})

	It("returns no blocks (and no breakpoint) when there is no system prompt", func() {
		msgs := []provider.Message{
			{Role: "user", Content: "hi"},
		}
		blocks := p.extractSystemPrompt(msgs)
		Expect(blocks).To(BeEmpty(),
			"no system messages → no system blocks → zero breakpoints in "+
				"this slot; this is the back-compat case for callers who "+
				"do not set a system prompt")
	})

	It("ignores empty-content system messages so they do not consume a slot", func() {
		msgs := []provider.Message{
			{Role: "system", Content: ""},
			{Role: "system", Content: "real content"},
			{Role: "system", Content: ""},
			{Role: "user", Content: "hi"},
		}
		blocks := p.extractSystemPrompt(msgs)
		Expect(blocks).To(HaveLen(1),
			"empty system messages are skipped at extraction time — "+
				"they would not be part of a useful cache prefix")
		Expect(string(blocks[0].CacheControl.Type)).To(Equal("ephemeral"))
	})
})

// Cache breakpoint placement on the conversation prefix.
//
// In a multi-turn agent loop the most reusable prefix is the
// conversation history through the model's most recent turn. Pinning a
// breakpoint at the END of the LAST assistant message means every
// follow-up turn re-uses that prefix as the cache key. Earlier
// assistants ride inside the prefix that breakpoint anchors and do NOT
// need their own breakpoints.
var _ = Describe("buildRequestParams conversation cache breakpoint", func() {
	var p *Provider
	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	It("places a breakpoint on the last content block of the last assistant message", func() {
		req := provider.ChatRequest{
			Model: "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{
				{Role: "user", Content: "first user turn"},
				{Role: "assistant", Content: "first assistant turn"},
				{Role: "user", Content: "second user turn"},
				{Role: "assistant", Content: "second assistant turn"},
				{Role: "user", Content: "third user turn"},
			},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.Messages).To(HaveLen(5))

		// Earlier assistant must NOT carry a breakpoint.
		earlierAssistant := params.Messages[1]
		Expect(string(earlierAssistant.Role)).To(Equal("assistant"))
		Expect(earlierAssistant.Content).To(HaveLen(1))
		Expect(string(earlierAssistant.Content[0].OfText.CacheControl.Type)).
			To(BeEmpty(),
				"earlier assistant turn must NOT carry a breakpoint — it "+
					"sits inside the conversation prefix anchored by the "+
					"breakpoint on the LAST assistant turn")

		// Last assistant must carry a breakpoint on its final block.
		lastAssistant := params.Messages[3]
		Expect(string(lastAssistant.Role)).To(Equal("assistant"))
		Expect(lastAssistant.Content).To(HaveLen(1))
		Expect(string(lastAssistant.Content[0].OfText.CacheControl.Type)).
			To(Equal("ephemeral"),
				"the LAST assistant message anchors the conversation "+
					"prefix — every follow-up turn re-uses this as the "+
					"cache key")

		// User messages never carry breakpoints under this strategy.
		for i, m := range params.Messages {
			if string(m.Role) != "user" {
				continue
			}
			for j, blk := range m.Content {
				if blk.OfText == nil {
					continue
				}
				Expect(string(blk.OfText.CacheControl.Type)).To(BeEmpty(),
					"user message %d block %d must not carry a "+
						"breakpoint under the chosen strategy", i, j)
			}
		}
	})

	It("does not add a conversation breakpoint when there is no assistant message", func() {
		req := provider.ChatRequest{
			Model: "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{
				{Role: "user", Content: "single user turn"},
			},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.Messages).To(HaveLen(1))
		Expect(string(params.Messages[0].Role)).To(Equal("user"))
		// The user block must not carry a cache breakpoint — there is
		// no conversation prefix worth anchoring on a single-turn fresh
		// request.
		Expect(string(params.Messages[0].Content[0].OfText.CacheControl.Type)).
			To(BeEmpty())
	})

	It("places the breakpoint on the LAST content block of an assistant tool-use turn", func() {
		req := provider.ChatRequest{
			Model: "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{
				{Role: "user", Content: "ask"},
				{
					Role:    "assistant",
					Content: "calling tool",
					ToolCalls: []provider.ToolCall{
						{ID: "toolu_01abc", Name: "search", Arguments: map[string]any{"q": "x"}},
					},
				},
				{Role: "tool", Content: "result", ToolCalls: []provider.ToolCall{{ID: "toolu_01abc"}}},
				{Role: "user", Content: "follow up"},
			},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())

		// params.Messages[1] is the assistant turn (text + tool_use).
		assistant := params.Messages[1]
		Expect(string(assistant.Role)).To(Equal("assistant"))
		Expect(len(assistant.Content)).To(BeNumerically(">=", 2),
			"assistant turn must contain text and tool_use blocks")

		last := assistant.Content[len(assistant.Content)-1]
		Expect(last.OfToolUse).NotTo(BeNil(),
			"the final block of an assistant tool-use turn is the "+
				"tool_use block — that is where the breakpoint must "+
				"sit so the entire turn is anchored")
		Expect(string(last.OfToolUse.CacheControl.Type)).To(Equal("ephemeral"))

		// Earlier blocks of the same assistant turn must NOT carry a
		// duplicate breakpoint.
		for i := 0; i < len(assistant.Content)-1; i++ {
			blk := assistant.Content[i]
			if blk.OfText != nil {
				Expect(string(blk.OfText.CacheControl.Type)).To(BeEmpty(),
					"earlier content block in the LAST assistant turn "+
						"must not duplicate the breakpoint")
			}
			if blk.OfToolUse != nil {
				Expect(string(blk.OfToolUse.CacheControl.Type)).To(BeEmpty())
			}
		}
	})
})

// Total breakpoint budget across the request.
//
// Anthropic enforces a hard cap of 4 cache_control breakpoints per
// request. The provider's strategy uses at most 3 (system + tools +
// last-assistant) so a fully-loaded request leaves headroom. This spec
// pins the integration: nobody slips a fourth breakpoint in by
// accident, and the configured strategy never exceeds the cap.
var _ = Describe("buildRequestParams cache breakpoint budget", func() {
	var p *Provider
	BeforeEach(func() {
		var err error
		p, err = New("test-key")
		Expect(err).NotTo(HaveOccurred())
	})

	countBreakpoints := func(params anthropicAPI.MessageNewParams) int {
		count := 0
		for _, sb := range params.System {
			if string(sb.CacheControl.Type) != "" {
				count++
			}
		}
		for _, t := range params.Tools {
			if t.OfTool != nil && string(t.OfTool.CacheControl.Type) != "" {
				count++
			}
		}
		for _, m := range params.Messages {
			for _, blk := range m.Content {
				switch {
				case blk.OfText != nil && string(blk.OfText.CacheControl.Type) != "":
					count++
				case blk.OfToolUse != nil && string(blk.OfToolUse.CacheControl.Type) != "":
					count++
				case blk.OfToolResult != nil && string(blk.OfToolResult.CacheControl.Type) != "":
					count++
				}
			}
		}
		return count
	}

	It("never exceeds 4 breakpoints across system + tools + messages on a fully-loaded request", func() {
		req := provider.ChatRequest{
			Model: "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{
				{Role: "system", Content: "sys 1"},
				{Role: "system", Content: "sys 2"},
				{Role: "system", Content: "sys 3"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
				{Role: "user", Content: "u2"},
				{Role: "assistant", Content: "a2"},
				{Role: "user", Content: "u3"},
			},
			Tools: []provider.Tool{
				{Name: "t1", Description: "tool 1", Schema: provider.ToolSchema{Type: "object"}},
				{Name: "t2", Description: "tool 2", Schema: provider.ToolSchema{Type: "object"}},
				{Name: "t3", Description: "tool 3", Schema: provider.ToolSchema{Type: "object"}},
			},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(countBreakpoints(params)).To(BeNumerically("<=", 4),
			"Anthropic rejects requests with more than 4 cache_control "+
				"breakpoints — the strategy must never exceed the cap "+
				"on its own")
	})

	It("uses exactly 3 breakpoints on a fully-loaded request (system + tools + last assistant)", func() {
		req := provider.ChatRequest{
			Model: "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{
				{Role: "system", Content: "sys 1"},
				{Role: "system", Content: "sys 2"},
				{Role: "user", Content: "u1"},
				{Role: "assistant", Content: "a1"},
				{Role: "user", Content: "u2"},
			},
			Tools: []provider.Tool{
				{Name: "t1", Description: "tool 1", Schema: provider.ToolSchema{Type: "object"}},
				{Name: "t2", Description: "tool 2", Schema: provider.ToolSchema{Type: "object"}},
			},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(countBreakpoints(params)).To(Equal(3),
			"system prefix + tool prefix + conversation prefix = 3 "+
				"breakpoints on a typical multi-turn agent request; "+
				"slot 4 is reserved")
	})

	It("uses zero breakpoints on a request with no system prompt and no tools and no assistant turn", func() {
		req := provider.ChatRequest{
			Model: "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{
				{Role: "user", Content: "hi"},
			},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(countBreakpoints(params)).To(Equal(0),
			"a request with no system, no tools, and a single user turn "+
				"has no cacheable prefix worth anchoring — back-compat "+
				"with callers that do not opt in")
	})

	It("preserves the breakpoint on the LAST tool definition (existing behaviour)", func() {
		req := provider.ChatRequest{
			Model: "claude-sonnet-4-5-20251020",
			Messages: []provider.Message{
				{Role: "user", Content: "hi"},
			},
			Tools: []provider.Tool{
				{Name: "t1", Description: "tool 1", Schema: provider.ToolSchema{Type: "object"}},
				{Name: "t2", Description: "tool 2", Schema: provider.ToolSchema{Type: "object"}},
				{Name: "t3", Description: "tool 3", Schema: provider.ToolSchema{Type: "object"}},
			},
		}
		params, _, err := p.buildRequestParams(req)
		Expect(err).NotTo(HaveOccurred())
		Expect(params.Tools).To(HaveLen(3))
		Expect(string(params.Tools[0].OfTool.CacheControl.Type)).To(BeEmpty())
		Expect(string(params.Tools[1].OfTool.CacheControl.Type)).To(BeEmpty())
		Expect(string(params.Tools[2].OfTool.CacheControl.Type)).To(Equal("ephemeral"),
			"the LAST tool keeps its breakpoint — this anchors the tool-"+
				"definitions prefix and is the existing correct behaviour")
	})
})
