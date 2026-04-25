package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("StreamChunkMsg tool args propagation", func() {
	Context("when the stream chunk carries a provider.ToolCall with arguments", func() {
		It("forwards the raw Arguments map onto the StreamChunkMsg as ToolCallArgs", func() {
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:   "call_1",
					Name: "read",
					Arguments: map[string]any{
						"filePath": "/etc/passwd",
					},
				},
			}
			close(ch)

			msg := chat.ReadStreamChunkForTest(ch)

			Expect(msg.ToolCallArgs).NotTo(BeNil(), "ToolCallArgs should be populated when chunk carries Arguments")
			Expect(msg.ToolCallArgs).To(HaveKeyWithValue("filePath", "/etc/passwd"))
		})

		It("preserves the bash command argument verbatim (no truncation)", func() {
			cmd := "ls -la /tmp/some/very/very/very/long/path/that/would/normally/get/truncated/by/the/summary/formatter"
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "call_2",
					Name:      "bash",
					Arguments: map[string]any{"command": cmd},
				},
			}
			close(ch)

			msg := chat.ReadStreamChunkForTest(ch)
			Expect(msg.ToolCallArgs).To(HaveKeyWithValue("command", cmd))
		})
	})

	Context("when the stream chunk has no tool call", func() {
		It("leaves ToolCallArgs nil", func() {
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "hello"}
			close(ch)

			msg := chat.ReadStreamChunkForTest(ch)
			Expect(msg.ToolCallArgs).To(BeNil())
		})
	})
})
