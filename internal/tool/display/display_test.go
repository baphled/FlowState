package tooldisplay_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	tooldisplay "github.com/baphled/flowstate/internal/tool/display"
)

var _ = Describe("PrimaryArgKey", func() {
	It("returns 'command' for bash", func() {
		Expect(tooldisplay.PrimaryArgKey("bash")).To(Equal("command"))
	})

	It("returns 'filePath' for read", func() {
		Expect(tooldisplay.PrimaryArgKey("read")).To(Equal("filePath"))
	})

	It("returns 'filePath' for write", func() {
		Expect(tooldisplay.PrimaryArgKey("write")).To(Equal("filePath"))
	})

	It("returns 'filePath' for edit", func() {
		Expect(tooldisplay.PrimaryArgKey("edit")).To(Equal("filePath"))
	})

	It("returns 'pattern' for glob", func() {
		Expect(tooldisplay.PrimaryArgKey("glob")).To(Equal("pattern"))
	})

	It("returns 'pattern' for grep", func() {
		Expect(tooldisplay.PrimaryArgKey("grep")).To(Equal("pattern"))
	})

	It("returns 'name' for skill_load", func() {
		Expect(tooldisplay.PrimaryArgKey("skill_load")).To(Equal("name"))
	})

	It("returns empty string for unknown tool", func() {
		Expect(tooldisplay.PrimaryArgKey("unknown_tool")).To(BeEmpty())
	})

	It("returns empty string for empty string", func() {
		Expect(tooldisplay.PrimaryArgKey("")).To(BeEmpty())
	})
})

var _ = Describe("Summary", func() {
	Context("when tool has no recognised primary arg key", func() {
		It("returns just the tool name", func() {
			Expect(tooldisplay.Summary("unknown_tool", map[string]any{"foo": "bar"})).To(Equal("unknown_tool"))
		})

		It("returns just the tool name when args is nil", func() {
			Expect(tooldisplay.Summary("unknown_tool", nil)).To(Equal("unknown_tool"))
		})
	})

	Context("when the primary arg is missing or empty", func() {
		It("returns just the tool name when arg key is absent", func() {
			Expect(tooldisplay.Summary("bash", map[string]any{})).To(Equal("bash"))
		})

		It("returns just the tool name when arg value is empty string", func() {
			Expect(tooldisplay.Summary("bash", map[string]any{"command": ""})).To(Equal("bash"))
		})

		It("returns just the tool name when arg value is not a string", func() {
			Expect(tooldisplay.Summary("bash", map[string]any{"command": 42})).To(Equal("bash"))
		})
	})

	Context("when tool is bash", func() {
		It("returns formatted summary for short command", func() {
			Expect(tooldisplay.Summary("bash", map[string]any{"command": "ls -la"})).To(Equal("bash: ls -la"))
		})

		It("truncates command at 80 characters with ellipsis", func() {
			longCmd := "echo " + string(make([]byte, 100))
			result := tooldisplay.Summary("bash", map[string]any{"command": longCmd})
			Expect(len(result)).To(BeNumerically("<=", len("bash: ")+80+3))
			Expect(result).To(HaveSuffix("..."))
		})
	})

	Context("when tool is read", func() {
		It("returns formatted summary with filePath", func() {
			Expect(tooldisplay.Summary("read", map[string]any{"filePath": "/foo/bar.go"})).To(Equal("read: /foo/bar.go"))
		})
	})

	Context("when tool is grep", func() {
		It("returns formatted summary with pattern", func() {
			Expect(tooldisplay.Summary("grep", map[string]any{"pattern": "func.*Error"})).To(Equal("grep: func.*Error"))
		})
	})

	Context("when tool is skill_load", func() {
		It("returns formatted summary with skill name", func() {
			Expect(tooldisplay.Summary("skill_load", map[string]any{"name": "golang"})).To(Equal("skill_load: golang"))
		})
	})
})
