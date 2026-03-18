package chat_test

import (
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/tui/views/chat"
)

func TestMarkdownRendering(t *testing.T) {
	t.Run("assistant code block contains ANSI escapes", func(t *testing.T) {
		v := chat.NewView()
		v.AddMessage(chat.Message{
			Role:    "assistant",
			Content: "```go\nfmt.Println(\"hello\")\n```",
		})
		out := v.RenderContent(80)
		if !strings.Contains(out, "\x1b[") {
			t.Errorf("expected ANSI escape sequences in output, got: %q", out)
		}
	})

	t.Run("user message stays plain without ANSI", func(t *testing.T) {
		v := chat.NewView()
		v.AddMessage(chat.Message{Role: "user", Content: "hello"})
		out := v.RenderContent(80)
		if strings.Contains(out, "\x1b[") {
			t.Error("user messages should not contain ANSI escape sequences")
		}
		if !strings.Contains(out, "hello") {
			t.Error("user message content should be preserved")
		}
	})

	t.Run("fallback preserves raw text when renderer fails", func(t *testing.T) {
		v := chat.NewView()
		v.SetMarkdownRenderer(func(content string, _ int) string {
			return content
		})
		v.AddMessage(chat.Message{
			Role:    "assistant",
			Content: "raw fallback text",
		})
		out := v.RenderContent(80)
		if !strings.Contains(out, "raw fallback text") {
			t.Error("expected raw text to be preserved on fallback")
		}
		if strings.Contains(out, "\x1b[") {
			t.Error("fallback should not contain ANSI escape sequences")
		}
	})
}
