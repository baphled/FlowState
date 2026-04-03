package widgets_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("BlockTool", func() {
	Describe("collapsed state", func() {
		It("renders icon, tool name, and truncated input on one line", func() {
			tool := widgets.NewBlockTool("bash", "hello world", "ignored output")

			output := tool.Render()

			Expect(output).To(ContainSubstring("$ bash: hello world"))
			Expect(output).NotTo(ContainSubstring("ignored output"))
			Expect(strings.Count(output, "\n")).To(Equal(0))
		})

		It("uses the tool-specific icon from ToolIcon", func() {
			Expect(widgets.ToolIcon("skill_load")).To(Equal("→"))
		})
	})

	Context("when expanded", func() {
		It("renders bordered output", func() {
			tool := widgets.NewBlockTool("read", "input", "first line\nsecond line")
			tool.SetExpanded(true)

			output := tool.Render()

			Expect(output).To(ContainSubstring("read input"))
			Expect(output).To(ContainSubstring("first line"))
			Expect(output).To(ContainSubstring("second line"))
		})

		It("truncates output at max lines", func() {
			tool := widgets.NewBlockTool("read", "input", "one\ntwo\nthree\nfour")
			tool.SetExpanded(true)
			tool.SetMaxLines(2)

			output := tool.Render()

			Expect(output).To(ContainSubstring("one"))
			Expect(output).To(ContainSubstring("two"))
			Expect(output).NotTo(ContainSubstring("three"))
		})

		It("renders all lines when under max lines", func() {
			tool := widgets.NewBlockTool("read", "input", "one\ntwo")
			tool.SetExpanded(true)
			tool.SetMaxLines(5)

			output := tool.Render()

			Expect(output).To(ContainSubstring("one"))
			Expect(output).To(ContainSubstring("two"))
		})
	})

	Describe("state methods", func() {
		It("is collapsed by default", func() {
			tool := widgets.NewBlockTool("task", "input", "output")

			Expect(tool.IsExpanded()).To(BeFalse())
		})

		It("toggles expansion state", func() {
			tool := widgets.NewBlockTool("task", "input", "output")

			tool.SetExpanded(true)
			Expect(tool.IsExpanded()).To(BeTrue())

			tool.SetExpanded(false)
			Expect(tool.IsExpanded()).To(BeFalse())
		})
	})
})
