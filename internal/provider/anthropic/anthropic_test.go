package anthropic

import (
	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("buildToolResultMessage", func() {
	It("returns a user message with tool result block (success)", func() {
		m := provider.Message{
			Role:      "tool",
			Content:   "42",
			ToolCalls: []provider.ToolCall{{ID: "tool1"}},
		}
		msg := buildToolResultMessage(m)
		Expect(msg).NotTo(BeNil())
		Expect(string(msg.Role)).To(Equal("user"))
		Expect(msg.Content).To(HaveLen(1))
		block := msg.Content[0]
		Expect(block.OfToolResult).NotTo(BeNil())
		tr := block.OfToolResult
		Expect(tr.ToolUseID).To(Equal("tool1"))
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
			ToolCalls: []provider.ToolCall{{ID: "tool2"}},
		}
		msg := buildToolResultMessage(m)
		Expect(msg).NotTo(BeNil())
		Expect(string(msg.Role)).To(Equal("user"))
		Expect(msg.Content).To(HaveLen(1))
		block := msg.Content[0]
		Expect(block.OfToolResult).NotTo(BeNil())
		tr := block.OfToolResult
		Expect(tr.ToolUseID).To(Equal("tool2"))
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
			{Role: "tool", Content: "42", ToolCalls: []provider.ToolCall{{ID: "tool1"}}},
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
		Expect(tr.ToolUseID).To(Equal("tool1"))
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
			{Role: "tool", Content: "result1", ToolCalls: []provider.ToolCall{{ID: "id1"}, {ID: "id2"}}},
		}
		result := buildMessages(msgs)
		Expect(result).To(HaveLen(2))
		Expect(string(result[1].Role)).To(Equal("user"))
		tr1 := result[1].Content[0].OfToolResult
		tr2 := result[1].Content[1].OfToolResult
		Expect(tr1.ToolUseID).To(Equal("id1"))
		Expect(tr2.ToolUseID).To(Equal("id2"))
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
