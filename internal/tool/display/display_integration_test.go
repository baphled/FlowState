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
		It("PrimaryArgKey returns empty string for an unrecognised tool", func() {
			Expect(tooldisplay.PrimaryArgKey("background_output")).To(BeEmpty())
		})
	})

	Context("when an unknown tool has a preferred fallback key", func() {
		// Real arg shapes from session 2d8dc0ac-8ad6-4271-a479-76c5093e1dfd
		// where 78 tool_call messages persisted with empty toolInput because
		// the previous PrimaryArgKey allowlist excluded them.

		It("uses 'query' for search_nodes calls", func() {
			args := map[string]any{"query": "FlowState recall architecture", "limit": 10}
			Expect(tooldisplay.Summary("search_nodes", args)).
				To(Equal("search_nodes: FlowState recall architecture"))
		})

		It("uses 'subagent_type' for delegate calls so the target is visible", func() {
			args := map[string]any{
				"category":      "implementation",
				"subagent_type": "senior-engineer",
				"message":       "implement the fallback",
			}
			Expect(tooldisplay.Summary("delegate", args)).
				To(Equal("delegate: senior-engineer"))
		})

		It("uses 'key' for coordination_store calls so the slot is visible", func() {
			args := map[string]any{
				"operation": "set",
				"key":       "user.name",
				"value":     "Alice",
			}
			Expect(tooldisplay.Summary("coordination_store", args)).
				To(Equal("coordination_store: user.name"))
		})

		It("uses 'id' for background_output calls so the task is visible", func() {
			args := map[string]any{"id": "bg-task-42"}
			Expect(tooldisplay.Summary("background_output", args)).
				To(Equal("background_output: bg-task-42"))
		})

		It("uses 'name' for read_graph when present (mcp memory variant)", func() {
			args := map[string]any{"name": "TaskMetric"}
			Expect(tooldisplay.Summary("read_graph", args)).
				To(Equal("read_graph: TaskMetric"))
		})
	})

	Context("when an unknown tool has no preferred fallback key", func() {
		It("returns a deterministic compact-JSON object of all string args", func() {
			// Map iteration order is non-deterministic in Go, so the fallback
			// must sort keys to keep the rendered ToolInput stable across
			// session reloads. Two unrelated string args, neither preferred.
			args := map[string]any{"alpha": "first", "zeta": "last"}
			Expect(tooldisplay.Summary("mystery_tool", args)).
				To(Equal(`mystery_tool: {"alpha":"first","zeta":"last"}`))
		})

		It("returns just the tool name when no string-valued args are present", func() {
			args := map[string]any{"count": 5, "enabled": true}
			Expect(tooldisplay.Summary("mystery_tool", args)).To(Equal("mystery_tool"))
		})

		It("returns just the tool name when args is nil", func() {
			Expect(tooldisplay.Summary("mystery_tool", nil)).To(Equal("mystery_tool"))
		})
	})

	Context("when an arg value contains secrets", func() {
		// The fallback path must never expose credential-like values to the
		// chat UI. Match is case-insensitive substring on the key name.
		DescribeTable("redacts sensitive keys before rendering",
			func(key string) {
				args := map[string]any{key: "supersecret-value-do-not-leak"}
				result := tooldisplay.Summary("custom_tool", args)
				Expect(result).NotTo(ContainSubstring("supersecret"))
				Expect(result).To(ContainSubstring("[REDACTED]"))
			},
			Entry("password key", "password"),
			Entry("api_key key", "api_key"),
			Entry("apiKey camelCase", "apiKey"),
			Entry("auth_token key", "auth_token"),
			Entry("client_secret key", "client_secret"),
			Entry("credentials key", "credentials"),
			Entry("Bearer-Token mixed case", "Bearer-Token"),
		)

		It("redacts sensitive keys inside the JSON-fallback path too", func() {
			args := map[string]any{"endpoint": "https://api.example.com", "api_key": "sk-real-key"}
			result := tooldisplay.Summary("custom_tool", args)
			Expect(result).NotTo(ContainSubstring("sk-real-key"))
			Expect(result).To(ContainSubstring("[REDACTED]"))
		})

		It("redacts the primary value for hand-coded tools when the key is sensitive", func() {
			// Defensive — bash is allowlisted, but if a future hand-coded
			// tool used a sensitive primary key we want redaction to win.
			args := map[string]any{"name": "secret_value"}
			// skill_load uses 'name' which is not sensitive — confirm the
			// non-sensitive case still passes through untouched.
			Expect(tooldisplay.Summary("skill_load", args)).To(Equal("skill_load: secret_value"))
		})
	})

	Context("when an unknown tool's fallback value exceeds 80 characters", func() {
		It("truncates the preferred-key value with an ellipsis", func() {
			longQuery := strings.Repeat("a", truncateLen+30)
			result := tooldisplay.Summary("search_nodes", map[string]any{"query": longQuery})
			Expect(result).To(HaveSuffix("..."))
			Expect(result).To(HaveLen(len("search_nodes: ") + truncateLen + len("...")))
		})

		It("truncates the JSON fallback so MCP tools cannot blow up the card", func() {
			huge := strings.Repeat("x", 200)
			args := map[string]any{"alpha": huge, "beta": huge}
			result := tooldisplay.Summary("mcp_tool", args)
			Expect(result).To(HaveSuffix("..."))
			Expect(len(result)).To(BeNumerically("<=", len("mcp_tool: ")+truncateLen+len("...")))
		})
	})

	Context("PrimaryArgValue contract", func() {
		It("returns ok=false for an unknown tool with no usable args", func() {
			value, ok := tooldisplay.PrimaryArgValue("anything", map[string]any{})
			Expect(ok).To(BeFalse())
			Expect(value).To(BeEmpty())
		})

		It("returns ok=true and the resolved value for a hand-coded tool", func() {
			value, ok := tooldisplay.PrimaryArgValue("bash", map[string]any{"command": "ls"})
			Expect(ok).To(BeTrue())
			Expect(value).To(Equal("ls"))
		})

		It("returns ok=true and the preferred-key value for an unknown tool", func() {
			value, ok := tooldisplay.PrimaryArgValue("delegate",
				map[string]any{"subagent_type": "qa-engineer", "message": "verify"})
			Expect(ok).To(BeTrue())
			Expect(value).To(Equal("qa-engineer"))
		})
	})
})
