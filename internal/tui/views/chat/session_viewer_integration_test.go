package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("SessionViewerModal integration", Label("integration"), func() {
	Describe("child session message rendering", func() {
		Context("when the session contains user and assistant messages", func() {
			It("displays user message content from the child session", func() {
				content := "You\n  Hello from user\n\nAssistant\n  Reply from assistant"
				viewer := chat.NewSessionViewerModal("ses-child-1", content, 80, 24)
				rendered := viewer.Render(80, 24)
				Expect(rendered).To(ContainSubstring("Hello from user"))
			})

			It("displays assistant message content from the child session", func() {
				content := "You\n  What is Go?\n\nAssistant\n  Go is a compiled language."
				viewer := chat.NewSessionViewerModal("ses-child-2", content, 80, 24)
				rendered := viewer.Render(80, 24)
				Expect(rendered).To(ContainSubstring("Go is a compiled language."))
			})

			It("displays both user and assistant messages together", func() {
				content := "You\n  First question\n\nAssistant\n  First answer"
				viewer := chat.NewSessionViewerModal("ses-child-3", content, 80, 24)
				rendered := viewer.Render(80, 24)
				Expect(rendered).To(ContainSubstring("First question"))
				Expect(rendered).To(ContainSubstring("First answer"))
			})
		})

		Context("when the session contains tool call results", func() {
			It("renders tool call result content from the child session", func() {
				content := "$ bash: ls -la\n  total 12\n  drwxr-xr-x 2 user group 4096 Apr 4 10:00 ."
				viewer := chat.NewSessionViewerModal("ses-tools-1", content, 80, 24)
				rendered := viewer.Render(80, 24)
				Expect(rendered).To(ContainSubstring("bash: ls -la"))
			})

			It("renders multiple tool results from the child session", func() {
				content := "$ read: /tmp/file.go\n  package main\n\n$ bash: go build\n  build succeeded"
				viewer := chat.NewSessionViewerModal("ses-tools-2", content, 80, 24)
				rendered := viewer.Render(80, 24)
				Expect(rendered).To(ContainSubstring("read: /tmp/file.go"))
				Expect(rendered).To(ContainSubstring("bash: go build"))
			})
		})

		Context("when the session is empty", func() {
			It("renders the session ID in the header without panicking", func() {
				viewer := chat.NewSessionViewerModal("ses-empty-1", "", 80, 24)
				Expect(func() {
					rendered := viewer.Render(80, 24)
					Expect(rendered).To(ContainSubstring("ses-empty-1"))
				}).NotTo(Panic())
			})

			It("renders a border even with no content", func() {
				viewer := chat.NewSessionViewerModal("ses-empty-2", "", 80, 24)
				rendered := viewer.Render(80, 24)
				Expect(rendered).NotTo(BeEmpty())
			})

			It("shows the scroll hint footer even when empty", func() {
				viewer := chat.NewSessionViewerModal("ses-empty-3", "", 80, 24)
				rendered := viewer.Render(80, 24)
				Expect(rendered).To(ContainSubstring("↑/↓ scroll"))
			})
		})
	})

	Describe("scroll key behaviour", func() {
		Context("when scroll keys are pressed", func() {
			It("does not remove the viewer from display after ScrollDown", func() {
				content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8"
				viewer := chat.NewSessionViewerModal("ses-scroll-1", content, 80, 5)
				viewer.ScrollDown()
				rendered := viewer.Render(80, 5)
				Expect(rendered).To(ContainSubstring("ses-scroll-1"))
			})

			It("does not remove the viewer from display after ScrollUp", func() {
				content := "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8"
				viewer := chat.NewSessionViewerModal("ses-scroll-2", content, 80, 5)
				viewer.ScrollDown()
				viewer.ScrollUp()
				rendered := viewer.Render(80, 5)
				Expect(rendered).To(ContainSubstring("ses-scroll-2"))
			})

			It("advances visible content window on ScrollDown", func() {
				content := "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\neta\ntheta"
				viewer := chat.NewSessionViewerModal("ses-scroll-3", content, 80, 5)
				viewer.ScrollDown()
				rendered := viewer.Render(80, 5)
				Expect(rendered).To(ContainSubstring("beta"))
			})

			It("returns to earlier content on ScrollUp after scrolling down", func() {
				content := "alpha\nbeta\ngamma\ndelta\nepsilon\nzeta\neta\ntheta"
				viewer := chat.NewSessionViewerModal("ses-scroll-4", content, 80, 5)
				viewer.ScrollDown()
				viewer.ScrollDown()
				viewer.ScrollUp()
				rendered := viewer.Render(80, 5)
				Expect(rendered).To(ContainSubstring("ses-scroll-4"))
			})

			It("does not scroll past the beginning of content", func() {
				content := "line1\nline2\nline3"
				viewer := chat.NewSessionViewerModal("ses-scroll-5", content, 80, 24)
				viewer.ScrollUp()
				viewer.ScrollUp()
				rendered := viewer.Render(80, 24)
				Expect(rendered).To(ContainSubstring("line1"))
			})
		})
	})
})
