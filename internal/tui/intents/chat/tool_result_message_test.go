package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("toolResultMessage", func() {
	Context("when isError is true", func() {
		It("returns a message with role tool_error", func() {
			msg := chat.ToolResultMessageForTest("bash", "command not found", true)

			Expect(msg.Role).To(Equal("tool_error"))
			Expect(msg.Content).To(Equal("command not found"))
		})

		It("uses tool_error role regardless of tool name", func() {
			msg := chat.ToolResultMessageForTest("todowrite", "some error", true)

			Expect(msg.Role).To(Equal("tool_error"))
		})
	})

	Context("when toolName is todowrite", func() {
		It("returns a message with role todo_update", func() {
			msg := chat.ToolResultMessageForTest("todowrite", "[]", false)

			Expect(msg.Role).To(Equal("todo_update"))
		})

		It("formats the content using FormatTodoList", func() {
			rawJSON := `[{"content":"Write tests","status":"pending","priority":"high"}]`
			msg := chat.ToolResultMessageForTest("todowrite", rawJSON, false)

			Expect(msg.Content).NotTo(BeEmpty())
			Expect(msg.Content).NotTo(Equal(rawJSON))
		})
	})

	Context("when toolName is any other tool", func() {
		It("returns a message with role tool_result", func() {
			msg := chat.ToolResultMessageForTest("bash", "some output", false)

			Expect(msg.Role).To(Equal("tool_result"))
			Expect(msg.Content).To(Equal("some output"))
		})

		It("returns tool_result for read tool", func() {
			msg := chat.ToolResultMessageForTest("read", "file content", false)

			Expect(msg.Role).To(Equal("tool_result"))
			Expect(msg.Content).To(Equal("file content"))
		})
	})
})
