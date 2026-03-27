package widgets_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

var _ = Describe("FormatTodoList", func() {
	Context("when JSON is invalid", func() {
		It("returns a fallback string", func() {
			result := widgets.FormatTodoList("not json at all")
			Expect(result).To(Equal("📋 todos updated"))
		})
	})

	Context("when JSON is an empty array", func() {
		It("returns a cleared message", func() {
			result := widgets.FormatTodoList("[]")
			Expect(result).To(Equal("📋 Todo list cleared"))
		})
	})

	Context("when JSON contains items", func() {
		It("includes the todo header", func() {
			result := widgets.FormatTodoList(`[{"content":"Write tests","status":"pending","priority":"high"}]`)
			Expect(result).To(ContainSubstring("📋 Todos"))
		})

		It("includes item content", func() {
			result := widgets.FormatTodoList(`[{"content":"Write tests","status":"pending","priority":"high"}]`)
			Expect(result).To(ContainSubstring("Write tests"))
		})

		It("shows in_progress status icon", func() {
			result := widgets.FormatTodoList(`[{"content":"Fix bug","status":"in_progress","priority":"medium"}]`)
			Expect(result).To(ContainSubstring("Fix bug"))
		})

		It("shows completed status icon", func() {
			result := widgets.FormatTodoList(`[{"content":"Done task","status":"completed","priority":"low"}]`)
			Expect(result).To(ContainSubstring("Done task"))
		})

		It("shows cancelled status icon", func() {
			result := widgets.FormatTodoList(`[{"content":"Dropped","status":"cancelled","priority":"low"}]`)
			Expect(result).To(ContainSubstring("Dropped"))
		})

		It("counts only active items in the header", func() {
			json := `[
				{"content":"Task A","status":"in_progress","priority":"high"},
				{"content":"Task B","status":"completed","priority":"low"},
				{"content":"Task C","status":"cancelled","priority":"medium"},
				{"content":"Task D","status":"pending","priority":"high"}
			]`
			result := widgets.FormatTodoList(json)
			Expect(result).To(ContainSubstring("2 active"))
		})

		It("includes high priority badge", func() {
			result := widgets.FormatTodoList(`[{"content":"Urgent","status":"pending","priority":"high"}]`)
			Expect(result).To(ContainSubstring("[H]"))
		})

		It("includes medium priority badge", func() {
			result := widgets.FormatTodoList(`[{"content":"Normal","status":"pending","priority":"medium"}]`)
			Expect(result).To(ContainSubstring("[M]"))
		})

		It("includes low priority badge", func() {
			result := widgets.FormatTodoList(`[{"content":"Low","status":"pending","priority":"low"}]`)
			Expect(result).To(ContainSubstring("[L]"))
		})
	})
})
