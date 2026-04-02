package shared_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/shared"
)

var _ = Describe("ConvertMessagesToRolePairs", func() {
	Context("when given an empty message slice", func() {
		It("returns an empty slice", func() {
			result := shared.ConvertMessagesToRolePairs([]provider.Message{})
			Expect(result).To(BeEmpty())
		})
	})

	Context("when given a user message", func() {
		It("returns a role pair with role user and correct content", func() {
			msgs := []provider.Message{
				{Role: "user", Content: "Hello, world"},
			}
			result := shared.ConvertMessagesToRolePairs(msgs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Role).To(Equal("user"))
			Expect(result[0].Content).To(Equal("Hello, world"))
		})
	})

	Context("when given an assistant message", func() {
		It("returns a role pair with role assistant and correct content", func() {
			msgs := []provider.Message{
				{Role: "assistant", Content: "I can help with that."},
			}
			result := shared.ConvertMessagesToRolePairs(msgs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Role).To(Equal("assistant"))
			Expect(result[0].Content).To(Equal("I can help with that."))
		})
	})

	Context("when given a system message", func() {
		It("returns a role pair with role system and correct content", func() {
			msgs := []provider.Message{
				{Role: "system", Content: "You are a helpful assistant."},
			}
			result := shared.ConvertMessagesToRolePairs(msgs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Role).To(Equal("system"))
			Expect(result[0].Content).To(Equal("You are a helpful assistant."))
		})
	})

	Context("when given a tool message with empty content", func() {
		It("returns a role pair with role tool and empty content", func() {
			msgs := []provider.Message{
				{
					Role:    "tool",
					Content: "",
					ToolCalls: []provider.ToolCall{
						{ID: "call_123", Name: "get_weather"},
					},
				},
			}
			result := shared.ConvertMessagesToRolePairs(msgs)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Role).To(Equal("tool"))
			Expect(result[0].Content).To(BeEmpty())
		})
	})

	Context("when given a multi-role conversation", func() {
		It("returns one role pair per message preserving order", func() {
			msgs := []provider.Message{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: "What is 2+2?"},
				{Role: "assistant", Content: "4"},
			}
			result := shared.ConvertMessagesToRolePairs(msgs)
			Expect(result).To(HaveLen(3))
			Expect(result[0].Role).To(Equal("system"))
			Expect(result[1].Role).To(Equal("user"))
			Expect(result[2].Role).To(Equal("assistant"))
		})
	})
})
