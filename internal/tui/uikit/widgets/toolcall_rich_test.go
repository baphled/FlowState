package widgets_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("ToolCallWidget rich display", func() {
	Describe("running state", func() {
		It("renders icon plus name plus primary arg using tooldisplay.Summary", func() {
			w := widgets.NewToolCallWidget("read", "running")
			w.SetArgs(map[string]any{"filePath": "/etc/passwd"})

			out := w.Render()

			Expect(out).To(ContainSubstring("→"))
			Expect(out).To(ContainSubstring("read: /etc/passwd"))
		})

		It("falls back to bare name when args do not contain a primary key", func() {
			w := widgets.NewToolCallWidget("bash", "running")
			w.SetArgs(map[string]any{"unrelated": "value"})

			out := w.Render()
			Expect(out).To(ContainSubstring("$"))
			Expect(out).To(ContainSubstring("bash"))
		})
	})

	Describe("complete state", func() {
		It("renders the summary, a status badge, and a result preview", func() {
			w := widgets.NewToolCallWidget("read", "complete")
			w.SetArgs(map[string]any{"filePath": "/etc/passwd"})
			w.SetResult("root:x:0:0:root:/root:/bin/bash\nbin:x:1:1:bin:/bin:/sbin/nologin")

			out := w.Render()

			Expect(out).To(ContainSubstring("read: /etc/passwd"))
			Expect(out).To(ContainSubstring("[complete]"))
			Expect(out).To(ContainSubstring("root:x:0:0"))
		})

		It("truncates long results to no more than five preview lines", func() {
			lines := []string{"L1", "L2", "L3", "L4", "L5", "L6", "L7", "L8"}
			w := widgets.NewToolCallWidget("bash", "complete")
			w.SetArgs(map[string]any{"command": "echo many lines"})
			w.SetResult(strings.Join(lines, "\n"))

			out := w.Render()
			previewLines := 0
			for _, line := range lines {
				if strings.Contains(out, line) {
					previewLines++
				}
			}
			Expect(previewLines).To(BeNumerically("<=", 5),
				"preview must show at most 5 lines, got %d", previewLines)
			Expect(previewLines).To(BeNumerically(">=", 1),
				"preview must show at least the first result line")
		})
	})

	Describe("error state", func() {
		It("renders the summary, an error badge, and the error result inline", func() {
			w := widgets.NewToolCallWidget("bash", "error")
			w.SetArgs(map[string]any{"command": "false"})
			w.SetResult("exit status 1")

			out := w.Render()
			Expect(out).To(ContainSubstring("bash: false"))
			Expect(out).To(ContainSubstring("[error]"))
			Expect(out).To(ContainSubstring("exit status 1"))
		})
	})

	Describe("backwards compatibility", func() {
		It("still works without args or result (legacy callers)", func() {
			w := widgets.NewToolCallWidget("read", "running")
			out := w.Render()
			Expect(out).To(ContainSubstring("→"))
			Expect(out).To(ContainSubstring("Reading…"))
		})
	})
})
