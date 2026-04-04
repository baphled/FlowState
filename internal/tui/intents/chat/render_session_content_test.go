package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("renderSessionContent", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "test-session",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	Context("with an empty session", func() {
		It("returns an empty string", func() {
			sess := &session.Session{
				ID:       "sess-empty",
				AgentID:  "test-agent",
				Messages: []session.Message{},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).To(BeEmpty())
		})
	})

	Context("with a user message", func() {
		It("maps user message to chat.Message with user role", func() {
			sess := &session.Session{
				ID:      "sess-user",
				AgentID: "test-agent",
				Messages: []session.Message{
					{Role: "user", Content: "Hello world", AgentID: "test-agent"},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(ContainSubstring("Hello world"))
		})
	})

	Context("with a tool_result message", func() {
		It("maps tool_result with ToolName and ToolInput", func() {
			sess := &session.Session{
				ID:      "sess-tool",
				AgentID: "test-agent",
				Messages: []session.Message{
					{
						Role:      "tool_result",
						Content:   "file content here",
						ToolName:  "read",
						ToolInput: "/tmp/foo.go",
						AgentID:   "test-agent",
					},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
		})
	})

	Context("with a tool_call message", func() {
		It("maps tool_call with name:input summary", func() {
			sess := &session.Session{
				ID:      "sess-toolcall",
				AgentID: "test-agent",
				Messages: []session.Message{
					{
						Role:      "tool_call",
						Content:   "bash",
						ToolName:  "bash",
						ToolInput: "echo hello",
						AgentID:   "test-agent",
					},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
		})
	})

	Context("with a tool_error message", func() {
		It("renders tool_error content", func() {
			sess := &session.Session{
				ID:      "sess-toole",
				AgentID: "test-agent",
				Messages: []session.Message{
					{
						Role:    "tool_error",
						Content: "command not found",
						AgentID: "test-agent",
					},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(ContainSubstring("command not found"))
		})
	})

	Context("with a thinking message", func() {
		It("renders thinking content", func() {
			sess := &session.Session{
				ID:      "sess-think",
				AgentID: "test-agent",
				Messages: []session.Message{
					{
						Role:    "thinking",
						Content: "I need to analyse this carefully",
						AgentID: "test-agent",
					},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(ContainSubstring("I need to analyse this carefully"))
		})
	})

	Context("with a delegation message", func() {
		It("renders delegation content", func() {
			sess := &session.Session{
				ID:      "sess-deleg",
				AgentID: "test-agent",
				Messages: []session.Message{
					{
						Role:    "delegation",
						Content: "│ build-agent [completed]\n  Model: claude-3-5-sonnet\n  3 tool calls (last: bash)",
						AgentID: "test-agent",
					},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(ContainSubstring("build-agent"))
		})
	})

	Context("with a skill_load message", func() {
		It("renders skill_load content", func() {
			sess := &session.Session{
				ID:      "sess-skill",
				AgentID: "test-agent",
				Messages: []session.Message{
					{
						Role:    "skill_load",
						Content: "golang",
						AgentID: "test-agent",
					},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(ContainSubstring("golang"))
		})
	})

	Context("with a todo_update message", func() {
		It("renders todo_update content", func() {
			sess := &session.Session{
				ID:      "sess-todo",
				AgentID: "test-agent",
				Messages: []session.Message{
					{
						Role:    "todo_update",
						Content: "- [x] Task completed",
						AgentID: "test-agent",
					},
				},
			}
			result := intent.RenderSessionContentForTest(sess)
			Expect(result).NotTo(BeEmpty())
			Expect(result).To(ContainSubstring("Task completed"))
		})
	})
})
