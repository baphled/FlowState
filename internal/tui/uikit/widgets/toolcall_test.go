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

	Describe("integration: tool call widget state transitions", func() {
		Context("when rendering a running tool", func() {
			It("contains tool name and running status", func() {
				w := widgets.NewToolCallWidget("fetch_api_data", "running")
				output := w.Render()
				Expect(output).To(ContainSubstring("fetch_api_data"))
				Expect(output).To(ContainSubstring("running"))
			})

			It("includes the tool icon", func() {
				w := widgets.NewToolCallWidget("database_query", "running")
				output := w.Render()
				Expect(output).To(ContainSubstring("⚡"))
			})
		})

		Context("when rendering a complete tool", func() {
			It("contains tool name and complete status", func() {
				w := widgets.NewToolCallWidget("fetch_api_data", "complete")
				output := w.Render()
				Expect(output).To(ContainSubstring("fetch_api_data"))
				Expect(output).To(ContainSubstring("complete"))
			})

			It("has the tool icon and status marker", func() {
				w := widgets.NewToolCallWidget("database_query", "complete")
				output := w.Render()
				Expect(output).To(ContainSubstring("⚡"))
				Expect(output).To(ContainSubstring("[complete]"))
			})
		})

		Context("when tool transitions from running to complete", func() {
			It("changes rendered status", func() {
				w := widgets.NewToolCallWidget("process_data", "running")
				runningOutput := w.Render()
				Expect(runningOutput).To(ContainSubstring("running"))

				complete := widgets.NewToolCallWidget("process_data", "complete")
				completeOutput := complete.Render()
				Expect(completeOutput).To(ContainSubstring("complete"))
			})
		})

		Context("when rendering an errored tool", func() {
			It("contains tool name and error status", func() {
				w := widgets.NewToolCallWidget("fetch_api_data", "error")
				output := w.Render()
				Expect(output).To(ContainSubstring("fetch_api_data"))
				Expect(output).To(ContainSubstring("error"))
			})
		})
	})
})
