package session_test

import (
	"context"
	"reflect"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("Message accumulation", func() {
	var (
		mgr        *session.Manager
		mockStream *mockStreamer
		ctx        context.Context
		sess       *session.Session
	)

	BeforeEach(func() {
		mockStream = newMockStreamer()
		mgr = session.NewManager(mockStream)
		ctx = context.Background()
		var err error
		sess, err = mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("session.Message struct", func() {
		It("has ToolName field with omitempty JSON tag", func() {
			typ := reflect.TypeOf(session.Message{})
			field, ok := typ.FieldByName("ToolName")
			Expect(ok).To(BeTrue())
			Expect(field.Tag.Get("json")).To(Equal("tool_name,omitempty"))
		})

		It("has ToolInput field with omitempty JSON tag", func() {
			typ := reflect.TypeOf(session.Message{})
			field, ok := typ.FieldByName("ToolInput")
			Expect(ok).To(BeTrue())
			Expect(field.Tag.Get("json")).To(Equal("tool_input,omitempty"))
		})
	})

	Describe("SendMessage accumulates assistant content on Done", func() {
		It("stores a single assistant message when Done chunk arrives", func() {
			mockStream.addChunk(provider.StreamChunk{Content: "Hello "})
			mockStream.addChunk(provider.StreamChunk{Content: "world"})
			mockStream.addChunk(provider.StreamChunk{Done: true})

			ch, err := mgr.SendMessage(ctx, sess.ID, "Hi")
			Expect(err).NotTo(HaveOccurred())
			drainChannel(ch)

			updated, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(updated.Messages).To(HaveLen(2))
			Expect(updated.Messages[1].Role).To(Equal("assistant"))
			Expect(updated.Messages[1].Content).To(Equal("Hello world"))
		})
	})

	Describe("SendMessage accumulates tool_result with ToolName and ToolInput", func() {
		It("stores a tool_result message with ToolName and ToolInput from preceding tool call", func() {
			mockStream.addChunk(provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "bash",
					Arguments: map[string]any{"command": "ls -la"},
				},
			})
			mockStream.addChunk(provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{Content: "file1.go\nfile2.go"},
			})
			mockStream.addChunk(provider.StreamChunk{Done: true})

			ch, err := mgr.SendMessage(ctx, sess.ID, "Run ls")
			Expect(err).NotTo(HaveOccurred())
			drainChannel(ch)

			updated, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())

			var toolResults []session.Message
			for _, m := range updated.Messages {
				if m.Role == "tool_result" {
					toolResults = append(toolResults, m)
				}
			}
			Expect(toolResults).To(HaveLen(1))
			Expect(toolResults[0].Content).To(Equal("file1.go\nfile2.go"))
			Expect(toolResults[0].ToolName).To(Equal("bash"))
			Expect(toolResults[0].ToolInput).To(Equal("ls -la"))
		})
	})

	Describe("SendMessage flushes partial content before a tool call", func() {
		It("commits accumulated assistant text before the tool call chunk", func() {
			mockStream.addChunk(provider.StreamChunk{Content: "Thinking..."})
			mockStream.addChunk(provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "read",
					Arguments: map[string]any{"filePath": "/tmp/foo.go"},
				},
			})
			mockStream.addChunk(provider.StreamChunk{Done: true})

			ch, err := mgr.SendMessage(ctx, sess.ID, "Check file")
			Expect(err).NotTo(HaveOccurred())
			drainChannel(ch)

			updated, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())

			var assistantMsgs []session.Message
			for _, m := range updated.Messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].Content).To(Equal("Thinking..."))
		})
	})
})

func drainChannel(ch <-chan provider.StreamChunk) {
	for chunk := range ch {
		_ = chunk
	}
}

var _ = Describe("extractPrimaryArg", func() {
	It("returns the command for the bash tool", func() {
		Expect(session.ExtractPrimaryArgForTest("bash", map[string]any{"command": "echo hi"})).To(Equal("echo hi"))
	})

	It("returns empty string for an unknown tool", func() {
		Expect(session.ExtractPrimaryArgForTest("unknown_tool", map[string]any{"foo": "bar"})).To(BeEmpty())
	})

	It("returns the filePath for the read tool", func() {
		Expect(session.ExtractPrimaryArgForTest("read", map[string]any{"filePath": "/foo/bar.go"})).To(Equal("/foo/bar.go"))
	})

	It("returns the pattern for the grep tool", func() {
		Expect(session.ExtractPrimaryArgForTest("grep", map[string]any{"pattern": "func.*Error"})).To(Equal("func.*Error"))
	})

	It("returns the name for the skill_load tool", func() {
		Expect(session.ExtractPrimaryArgForTest("skill_load", map[string]any{"name": "golang"})).To(Equal("golang"))
	})
})
