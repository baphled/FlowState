package anthropic

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
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
			Expect(defs.betaHeaders(true, true)).To(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 4.5 omits the header when thinking is on but tools are empty", func() {
			defs := resolveModelDefaults("claude-sonnet-4-5-20251020")
			Expect(defs.betaHeaders(true, false)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 4.5 omits the header when thinking is off but tools are present", func() {
			defs := resolveModelDefaults("claude-sonnet-4-5-20251020")
			Expect(defs.betaHeaders(false, true)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Opus 4.7 omits the header even with thinking on AND tools present (auto-enabled server-side)", func() {
			defs := resolveModelDefaults("claude-opus-4-7-20251201")
			Expect(defs.betaHeaders(true, true)).NotTo(ContainElement(interleavedThinkingBetaHeader),
				"4.6+ family auto-enables interleaving; explicit header is rejected on Bedrock/Vertex")
		})

		It("Opus 4.6 omits the header (auto-enabled server-side)", func() {
			defs := resolveModelDefaults("claude-opus-4-6-20251020")
			Expect(defs.betaHeaders(true, true)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 4.6 omits the header (auto-enabled server-side)", func() {
			defs := resolveModelDefaults("claude-sonnet-4-6-20251020")
			Expect(defs.betaHeaders(true, true)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Sonnet 3.7 omits the header (interleaving not supported)", func() {
			defs := resolveModelDefaults("claude-3-7-sonnet-20250219")
			Expect(defs.betaHeaders(true, true)).NotTo(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Opus 4.1 emits the header when thinking is on AND tools are present", func() {
			defs := resolveModelDefaults("claude-opus-4-1-20250805")
			Expect(defs.betaHeaders(true, true)).To(ContainElement(interleavedThinkingBetaHeader))
		})

		It("Haiku 4.5 emits the header when thinking is on AND tools are present", func() {
			defs := resolveModelDefaults("claude-haiku-4-5-20251020")
			Expect(defs.betaHeaders(true, true)).To(ContainElement(interleavedThinkingBetaHeader))
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

		It("Sonnet 3.7 + enabled + tools → no beta opt", func() {
			req := provider.ChatRequest{
				Model:        "claude-3-7-sonnet-20250219",
				Messages:     []provider.Message{{Role: "user", Content: "hi"}},
				Tools:        []provider.Tool{tool},
				ThinkingMode: "enabled:4096",
				MaxTokens:    16000,
			}
			_, opts, err := p.buildRequestParams(req)
			Expect(err).NotTo(HaveOccurred())
			Expect(opts).To(BeEmpty(),
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
