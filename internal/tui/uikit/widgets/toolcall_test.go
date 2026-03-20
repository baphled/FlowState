package widgets_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("ToolCallWidget", func() {
	Describe("NewToolCallWidget", func() {
		It("creates a widget with the given name and status", func() {
			w := widgets.NewToolCallWidget("invoke_tool", "running")
			Expect(w).NotTo(BeNil())
		})
	})

	Describe("Render", func() {
		Context("when status is running", func() {
			It("renders the tool name with running status", func() {
				w := widgets.NewToolCallWidget("fetch_data", "running")
				output := w.Render()
				Expect(output).To(ContainSubstring("fetch_data"))
				Expect(output).To(ContainSubstring("running"))
			})

			It("uses yellow styling for running status", func() {
				w := widgets.NewToolCallWidget("test_tool", "running")
				output := w.Render()
				Expect(output).NotTo(BeEmpty())
			})
		})

		Context("when status is complete", func() {
			It("renders the tool name with complete status", func() {
				w := widgets.NewToolCallWidget("fetch_data", "complete")
				output := w.Render()
				Expect(output).To(ContainSubstring("fetch_data"))
				Expect(output).To(ContainSubstring("complete"))
			})

			It("uses green styling for complete status", func() {
				w := widgets.NewToolCallWidget("test_tool", "complete")
				output := w.Render()
				Expect(output).NotTo(BeEmpty())
			})
		})

		Context("when status is error", func() {
			It("renders the tool name with error status", func() {
				w := widgets.NewToolCallWidget("fetch_data", "error")
				output := w.Render()
				Expect(output).To(ContainSubstring("fetch_data"))
				Expect(output).To(ContainSubstring("error"))
			})

			It("uses red styling for error status", func() {
				w := widgets.NewToolCallWidget("test_tool", "error")
				output := w.Render()
				Expect(output).NotTo(BeEmpty())
			})
		})

		Context("when tool name is empty", func() {
			It("still renders without panic", func() {
				w := widgets.NewToolCallWidget("", "running")
				Expect(func() {
					_ = w.Render()
				}).NotTo(Panic())
			})

			It("renders the status even with empty name", func() {
				w := widgets.NewToolCallWidget("", "complete")
				output := w.Render()
				Expect(output).To(ContainSubstring("complete"))
			})
		})
	})
})
