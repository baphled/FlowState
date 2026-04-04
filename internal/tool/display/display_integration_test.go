package tooldisplay_test

import (
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	tooldisplay "github.com/baphled/flowstate/internal/tool/display"
)

var _ = Describe("Display integration", Label("integration"), func() {
	const truncateLen = 80

	Context("PrimaryArgKey extracts the correct key for each known tool", func() {
		DescribeTable("tool to primary arg key mapping",
			func(toolName, expectedKey string) {
				Expect(tooldisplay.PrimaryArgKey(toolName)).To(Equal(expectedKey))
			},
			Entry("bash uses command", "bash", "command"),
			Entry("read uses filePath", "read", "filePath"),
			Entry("write uses filePath", "write", "filePath"),
			Entry("edit uses filePath", "edit", "filePath"),
			Entry("glob uses pattern", "glob", "pattern"),
			Entry("grep uses pattern", "grep", "pattern"),
			Entry("skill_load uses name", "skill_load", "name"),
			Entry("unknown tool returns empty", "unknown_tool", ""),
		)
	})

	Context("Summary produces correct output across all known tools", func() {
		DescribeTable("summary format per tool",
			func(toolName, argKey, argValue, expected string) {
				args := map[string]any{argKey: argValue}
				Expect(tooldisplay.Summary(toolName, args)).To(Equal(expected))
			},
			Entry("bash with short command", "bash", "command", "ls -la", "bash: ls -la"),
			Entry("read with filePath", "read", "filePath", "/etc/hosts", "read: /etc/hosts"),
			Entry("grep with pattern", "grep", "pattern", "func.*Error", "grep: func.*Error"),
			Entry("glob with pattern", "glob", "pattern", "**/*.go", "glob: **/*.go"),
			Entry("skill_load with name", "skill_load", "name", "golang", "skill_load: golang"),
		)
	})

	Context("when Summary truncates long bash commands", func() {
		It("truncates commands longer than 80 characters with an ellipsis", func() {
			longArg := strings.Repeat("x", truncateLen+20)
			result := tooldisplay.Summary("bash", map[string]any{"command": longArg})

			Expect(result).To(HaveSuffix("..."))
			Expect(result).To(HaveLen(len("bash: ") + truncateLen + len("...")))
		})

		It("does not truncate commands of exactly 80 characters", func() {
			exactArg := strings.Repeat("y", truncateLen)
			result := tooldisplay.Summary("bash", map[string]any{"command": exactArg})

			Expect(result).NotTo(HaveSuffix("..."))
			Expect(result).To(Equal(fmt.Sprintf("bash: %s", exactArg)))
		})
	})

	Context("when tool display output is matched against session ToolInput", func() {
		It("Summary uses the same arg key that would appear in a session ToolInput", func() {
			toolName := "read"
			toolInputArgs := map[string]any{"filePath": "/home/user/file.go"}

			key := tooldisplay.PrimaryArgKey(toolName)
			summary := tooldisplay.Summary(toolName, toolInputArgs)

			Expect(key).To(Equal("filePath"))
			Expect(summary).To(Equal("read: /home/user/file.go"))
			Expect(toolInputArgs).To(HaveKey(key))
		})

		It("Summary for bash matches the command stored in a session ToolInput", func() {
			toolName := "bash"
			cmd := "go test ./..."
			toolInputArgs := map[string]any{"command": cmd}

			summary := tooldisplay.Summary(toolName, toolInputArgs)

			Expect(summary).To(Equal(fmt.Sprintf("%s: %s", toolName, cmd)))
		})
	})

	Context("when tool is unknown", func() {
		It("Summary returns only the tool name regardless of args provided", func() {
			result := tooldisplay.Summary("delegate", map[string]any{"target": "worker"})
			Expect(result).To(Equal("delegate"))
		})

		It("PrimaryArgKey returns empty string for an unrecognised tool", func() {
			Expect(tooldisplay.PrimaryArgKey("background_output")).To(BeEmpty())
		})
	})
})
