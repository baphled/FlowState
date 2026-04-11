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

	Context("when a tool call chunk arrives", func() {
		It("stores a tool_call message with the tool name", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "bash",
					Arguments: map[string]any{"command": "ls -la"},
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var toolCalls []session.Message
			for _, m := range appender.messages {
				if m.Role == "tool_call" {
					toolCalls = append(toolCalls, m)
				}
			}
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].Content).To(Equal("bash"))
			Expect(toolCalls[0].ToolName).To(Equal("bash"))
			Expect(toolCalls[0].ToolInput).To(Equal("ls -la"))
		})

		It("stores tool_call message before the tool_result message", func() {
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

			roles := make([]string, 0, len(appender.messages))
			for _, m := range appender.messages {
				roles = append(roles, m.Role)
			}
			Expect(roles).To(ContainElements("tool_call", "tool_result"))
			toolCallIdx := -1
			toolResultIdx := -1
			for idx, role := range roles {
				if role == "tool_call" {
					toolCallIdx = idx
				}
				if role == "tool_result" {
					toolResultIdx = idx
				}
			}
			Expect(toolCallIdx).To(BeNumerically("<", toolResultIdx))
		})
	})

	Context("when a tool result is an error", func() {
		It("stores a tool_error message when IsError is true", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "bash",
					Arguments: map[string]any{"command": "bad-command"},
				},
			}
			rawCh <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{Content: "command not found", IsError: true},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var toolErrors []session.Message
			for _, m := range appender.messages {
				if m.Role == "tool_error" {
					toolErrors = append(toolErrors, m)
				}
			}
			Expect(toolErrors).To(HaveLen(1))
			Expect(toolErrors[0].Content).To(Equal("command not found"))
		})
	})

	Context("when thinking content arrives", func() {
		It("stores a thinking message", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{Thinking: "I need to analyse this..."}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "thinking" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("I need to analyse this..."))
		})

		It("accumulates multiple thinking chunks into one message", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "First thought."}
			rawCh <- provider.StreamChunk{Thinking: " Second thought."}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "thinking" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("First thought. Second thought."))
		})
	})

	Context("when delegation info arrives", func() {
		It("stores a delegation message when status is completed", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "build-agent",
					Status:      "completed",
					ModelName:   "claude-3-5-sonnet",
					ToolCalls:   3,
					LastTool:    "bash",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var delegationMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "delegation" {
					delegationMsgs = append(delegationMsgs, m)
				}
			}
			Expect(delegationMsgs).To(HaveLen(1))
			Expect(delegationMsgs[0].Content).To(ContainSubstring("build-agent"))
		})

		It("stores a delegation message when status is failed", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "worker-agent",
					Status:      "failed",
					ModelName:   "gpt-4o",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var delegationMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "delegation" {
					delegationMsgs = append(delegationMsgs, m)
				}
			}
			Expect(delegationMsgs).To(HaveLen(1))
			Expect(delegationMsgs[0].Content).To(ContainSubstring("worker-agent"))
		})

		It("does not store a delegation message when status is pending", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "build-agent",
					Status:      "pending",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			for _, m := range appender.messages {
				Expect(m.Role).NotTo(Equal("delegation"))
			}
		})
	})

	Context("when the raw channel closes without a terminal Done chunk", func() {
		It("flushes accumulated assistant content on channel close", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{Content: "hello"}
			rawCh <- provider.StreamChunk{Content: " world"}
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
			Expect(assistantMsgs[0].Content).To(Equal("hello world"))
			Expect(assistantMsgs[0].AgentID).To(Equal("agent-1"))
		})

		It("flushes accumulated thinking content on channel close", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{Thinking: "half a"}
			rawCh <- provider.StreamChunk{Thinking: " thought"}
			close(rawCh)

			out := session.AccumulateStream(appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "thinking" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("half a thought"))
		})

		It("does not double-flush when a Done chunk is followed by channel close", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{Content: "only once"}
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
			Expect(assistantMsgs[0].Content).To(Equal("only once"))
		})
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
