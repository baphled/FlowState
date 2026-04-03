package session_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("AccumulateStream", func() {
	var (
		appender *fakeAppender
	)

	BeforeEach(func() {
		appender = &fakeAppender{}
	})

	It("forwards all chunks from rawCh to the returned channel", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "hello"}
		rawCh <- provider.StreamChunk{Content: " world"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)

		var chunks []provider.StreamChunk
		for chunk := range out {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(HaveLen(3))
	})

	It("appends an assistant message to the appender on Done", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "Hello "}
		rawCh <- provider.StreamChunk{Content: "world"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
		drainChannel(out)

		Expect(appender.messages).To(HaveLen(1))
		Expect(appender.messages[0].Role).To(Equal("assistant"))
		Expect(appender.messages[0].Content).To(Equal("Hello world"))
		Expect(appender.messages[0].AgentID).To(Equal("agent-1"))
	})

	It("appends a tool_result message with ToolName and ToolInput", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{
			ToolCall: &provider.ToolCall{
				Name:      "bash",
				Arguments: map[string]any{"command": "ls -la"},
			},
		}
		rawCh <- provider.StreamChunk{
			ToolResult: &provider.ToolResultInfo{Content: "file1.go"},
		}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
		drainChannel(out)

		var toolResults []session.Message
		for _, m := range appender.messages {
			if m.Role == "tool_result" {
				toolResults = append(toolResults, m)
			}
		}
		Expect(toolResults).To(HaveLen(1))
		Expect(toolResults[0].ToolName).To(Equal("bash"))
		Expect(toolResults[0].ToolInput).To(Equal("ls -la"))
		Expect(toolResults[0].Content).To(Equal("file1.go"))
	})

	It("flushes buffered content before a tool call", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "thinking..."}
		rawCh <- provider.StreamChunk{
			ToolCall: &provider.ToolCall{
				Name:      "read",
				Arguments: map[string]any{"filePath": "/foo.go"},
			},
		}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
		drainChannel(out)

		var assistantMsgs []session.Message
		for _, m := range appender.messages {
			if m.Role == "assistant" {
				assistantMsgs = append(assistantMsgs, m)
			}
		}
		Expect(assistantMsgs).To(HaveLen(1))
		Expect(assistantMsgs[0].Content).To(Equal("thinking..."))
	})

	It("uses the provided sessionID and agentID for appended messages", func() {
		rawCh := make(chan provider.StreamChunk, 2)
		rawCh <- provider.StreamChunk{Content: "resp"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(appender, "my-session", "my-agent", rawCh)
		drainChannel(out)

		Expect(appender.sessionIDs).To(ContainElement("my-session"))
		Expect(appender.messages[0].AgentID).To(Equal("my-agent"))
	})
})

var _ = Describe("MessageAppender interface", func() {
	It("Manager implements MessageAppender", func() {
		var _ session.MessageAppender = session.NewManager(newMockStreamer())
	})
})

// fakeAppender is a test double implementing session.MessageAppender.
type fakeAppender struct {
	messages   []session.Message
	sessionIDs []string
}

func (f *fakeAppender) AppendMessage(sessionID string, msg session.Message) {
	f.sessionIDs = append(f.sessionIDs, sessionID)
	f.messages = append(f.messages, msg)
}
