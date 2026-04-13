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

		It("maps 5xx to ServerError", func() {
			for _, code := range []int{500, 502, 503, 504} {
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
