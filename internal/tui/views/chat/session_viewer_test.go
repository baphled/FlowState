package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("SessionViewerModal", func() {
	Describe("RenderContent", func() {
		It("returns visible lines without border", func() {
			viewer := chat.NewSessionViewerModal("ses-abc123", "line1\nline2\nline3", 80, 24)
			result := viewer.RenderContent(80, 24)
			Expect(result).To(ContainSubstring("line1"))
			Expect(result).NotTo(ContainSubstring("╭"))
			Expect(result).NotTo(ContainSubstring("╰"))
		})

		It("returns visible lines without header or footer", func() {
			viewer := chat.NewSessionViewerModal("ses-abc123", "hello", 80, 24)
			result := viewer.RenderContent(80, 24)
			Expect(result).NotTo(ContainSubstring("Session:"))
			Expect(result).NotTo(ContainSubstring("↑/↓ scroll"))
		})

		It("respects offset when scrolled", func() {
			content := "line1\nline2\nline3\nline4\nline5"
			viewer := chat.NewSessionViewerModal("ses-1", content, 80, 5)
			viewer.ScrollDown()
			result := viewer.RenderContent(80, 5)
			Expect(result).To(ContainSubstring("line2"))
		})

		It("returns empty-padded lines for short content", func() {
			viewer := chat.NewSessionViewerModal("ses-1", "only one line", 80, 24)
			result := viewer.RenderContent(80, 24)
			Expect(result).To(ContainSubstring("only one line"))
		})
	})

	Describe("NewSessionViewerModal", func() {
		It("creates a viewer with given session ID and content", func() {
			viewer := chat.NewSessionViewerModal("ses-abc123", "test content", 80, 24)
			Expect(viewer).NotTo(BeNil())
		})
	})

	Describe("rendering", func() {
		It("renders session ID in header", func() {
			viewer := chat.NewSessionViewerModal("ses-abc123", "hello content", 80, 24)
			rendered := viewer.Render(80, 24)
			Expect(rendered).To(ContainSubstring("ses-abc123"))
		})

		It("renders content from the session", func() {
			viewer := chat.NewSessionViewerModal("ses-1", "hello world", 80, 24)
			rendered := viewer.Render(80, 24)
			Expect(rendered).To(ContainSubstring("hello world"))
		})

		It("renders empty content gracefully", func() {
			viewer := chat.NewSessionViewerModal("ses-1", "", 80, 24)
			rendered := viewer.Render(80, 24)
			Expect(rendered).To(ContainSubstring("ses-1"))
		})

		It("renders multiline content", func() {
			content := "line1\nline2\nline3"
			viewer := chat.NewSessionViewerModal("ses-1", content, 80, 24)
			rendered := viewer.Render(80, 24)
			Expect(rendered).To(ContainSubstring("line1"))
			Expect(rendered).To(ContainSubstring("line2"))
			Expect(rendered).To(ContainSubstring("line3"))
		})
	})

	Describe("scrolling", func() {
		It("ScrollUp clamps to zero", func() {
			viewer := chat.NewSessionViewerModal("ses-1", "line1\nline2\nline3", 80, 24)
			viewer.ScrollUp()
			viewer.ScrollUp()
			rendered := viewer.Render(80, 24)
			Expect(rendered).To(ContainSubstring("line1"))
		})

		It("ScrollDown increments offset", func() {
			content := "line1\nline2\nline3\nline4\nline5"
			viewer := chat.NewSessionViewerModal("ses-1", content, 80, 5)
			viewer.ScrollDown()
			rendered := viewer.Render(80, 5)
			Expect(rendered).To(ContainSubstring("ses-1"))
		})

		It("does not panic when scrolling empty content", func() {
			viewer := chat.NewSessionViewerModal("ses-1", "", 80, 24)
			Expect(func() {
				viewer.ScrollUp()
				viewer.ScrollDown()
			}).NotTo(Panic())
		})
	})
})
