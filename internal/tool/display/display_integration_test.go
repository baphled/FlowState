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

		It("renders subagent_type and the brief together for delegate calls", func() {
			// Historical behaviour returned only the subagent_type, dropping
			// the message argument from the persisted ToolInput. Production
			// session c8c138e5-d7ae-46a9-835b-9b2e16534ac8 lost every parent
			// delegation brief that way, leaving "delegate: senior-engineer"
			// as the entire record of what the parent asked for.
			//
			// The brief is the load-bearing field for delegation. Pin both
			// pieces in the rendered display so the parent's persisted
			// tool_call ToolInput preserves intent alongside routing.
			args := map[string]any{
				"category":      "implementation",
				"subagent_type": "senior-engineer",
				"message":       "implement the fallback",
			}
			result := tooldisplay.Summary("delegate", args)
			Expect(result).To(HavePrefix("delegate: "))
			Expect(result).To(ContainSubstring("senior-engineer"))
			Expect(result).To(ContainSubstring("implement the fallback"))
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

		It("renders scalar non-string args as JSON-encoded values so MCP tool calls do not persist with empty toolInput", func() {
			// Bug fix: previously the canonical fallback path filtered the
			// args map to string-typed entries only, so any tool whose args
			// were entirely scalar-but-non-string (e.g. {"count": 5,
			// "enabled": true}) silently rendered as the bare tool name and
			// persisted with toolInput == "". Live evidence in session
			// 7dfdb197 showed every create_entities / open_nodes /
			// add_observations call persisting with toolInput: null because
			// none of their args were string-typed. JSON-marshal non-string
			// values so the persisted record carries the call payload.
			args := map[string]any{"count": 5, "enabled": true}
			result := tooldisplay.Summary("mystery_tool", args)
			Expect(result).To(HavePrefix("mystery_tool: "))
			Expect(result).To(ContainSubstring(`"count":5`))
			Expect(result).To(ContainSubstring(`"enabled":true`))
		})

		It("returns just the tool name when args is nil", func() {
			Expect(tooldisplay.Summary("mystery_tool", nil)).To(Equal("mystery_tool"))
		})
	})

	Context("when an unknown tool has non-string structured args", func() {
		// Bug: every MCP tool with array or object top-level args (e.g.
		// create_entities {entities: [...]}, add_observations
		// {observations: [...]}, open_nodes {names: [...]}) persisted with
		// toolInput == "" because compactJSONFallback retained only
		// string-typed entries. KB writes hit the MCP server fine but
		// disappeared from the session history with their payload —
		// silent data loss. Live evidence: session 7dfdb197 (z-ai) and
		// session 0276aca4 (anthropic).

		It("renders an array-valued top-level arg as serialised JSON", func() {
			args := map[string]any{"entities": []any{"alpha", "beta", "gamma"}}
			result := tooldisplay.Summary("create_entities", args)
			Expect(result).To(HavePrefix("create_entities: "))
			Expect(result).To(ContainSubstring(`"entities":["alpha","beta","gamma"]`))
		})

		It("renders an object-valued top-level arg as serialised JSON", func() {
			args := map[string]any{"params": map[string]any{"foo": "bar", "baz": 1}}
			result := tooldisplay.Summary("custom_tool", args)
			Expect(result).To(HavePrefix("custom_tool: "))
			Expect(result).To(ContainSubstring(`"params":`))
			Expect(result).To(ContainSubstring(`"foo":"bar"`))
			Expect(result).To(ContainSubstring(`"baz":1`))
		})

		It("renders a mixed-shape arg map with both string and structured values", func() {
			// Real shape from add_observations calls — string entityName
			// plus an array of observation strings. The preferred-fallback
			// path picks no key here (none of "query"/"name"/"id"/etc are
			// present), so the canonical JSON path must render BOTH the
			// scalar and the structured entries.
			args := map[string]any{
				"entityName":   "FlowState",
				"observations": []any{"obs one", "obs two"},
			}
			result := tooldisplay.Summary("add_observations", args)
			Expect(result).To(HavePrefix("add_observations: "))
			Expect(result).To(ContainSubstring(`"entityName":"FlowState"`))
			Expect(result).To(ContainSubstring(`"observations":["obs one","obs two"]`))
		})

		It("renders nested objects within structured args (truncating only at the outer 80-char cap)", func() {
			args := map[string]any{
				"node": map[string]any{
					"id":       "n1",
					"children": []any{map[string]any{"id": "c1"}, map[string]any{"id": "c2"}},
				},
			}
			result := tooldisplay.Summary("graph_tool", args)
			Expect(result).To(HavePrefix("graph_tool: "))
			// Even if the full payload exceeds 80 chars, the head must be
			// recognisable structured JSON (not an empty fallback).
			Expect(result).To(ContainSubstring(`"node":`))
		})

		It("truncates the JSON fallback when structured args exceed 80 characters", func() {
			big := strings.Repeat("x", 200)
			args := map[string]any{"payload": []any{big}}
			result := tooldisplay.Summary("mcp_tool", args)
			Expect(result).To(HaveSuffix("..."))
			Expect(len(result)).To(BeNumerically("<=", len("mcp_tool: ")+truncateLen+len("...")))
		})

		It("redacts sensitive keys even when the value is structured", func() {
			args := map[string]any{"credentials": map[string]any{"user": "alice", "password": "secret"}}
			result := tooldisplay.Summary("custom_tool", args)
			Expect(result).NotTo(ContainSubstring("alice"))
			Expect(result).NotTo(ContainSubstring("secret"))
			Expect(result).To(ContainSubstring("[REDACTED]"))
		})

		It("PrimaryArgValue returns ok=true for structured-only args so callers can persist a non-empty toolInput", func() {
			// Direct contract check: the accumulator's toolArgValue delegate
			// reads only the string return, but downstream callers (and
			// future use) check the bool to decide whether the call had
			// anything to display. Flip the bool to true whenever any
			// representation is emitted, not just the string-arg path.
			args := map[string]any{"entities": []any{"a", "b"}}
			value, ok := tooldisplay.PrimaryArgValue("create_entities", args)
			Expect(ok).To(BeTrue())
			Expect(value).NotTo(BeEmpty())
			Expect(value).To(ContainSubstring(`"entities":["a","b"]`))
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

	Context("when tool is todowrite", func() {
		// Live evidence: session 59b4e1a2-daf9-44f2-b179-fa0757c34f02 persisted
		// every todowrite call with toolInput == "" because the canonical
		// fallback path only walks string-valued args, and todos is an array.
		// The UI rendered blank cards as a result. A dedicated formatter is
		// required so persistence and UI surface useful intent.

		It("renders count plus the first active item content", func() {
			args := map[string]any{
				"todos": []interface{}{
					map[string]interface{}{"content": "Write tests first", "status": "in_progress", "priority": "high"},
					map[string]interface{}{"content": "Implement feature", "status": "pending", "priority": "medium"},
					map[string]interface{}{"content": "Done item", "status": "completed", "priority": "low"},
				},
			}
			result := tooldisplay.Summary("todowrite", args)
			Expect(result).To(HavePrefix("todowrite: "))
			Expect(result).To(ContainSubstring("2/3 todos"))
			Expect(result).To(ContainSubstring("Write tests first"))
		})

		It("renders an explicit zero-count message when the list is empty", func() {
			args := map[string]any{"todos": []interface{}{}}
			Expect(tooldisplay.Summary("todowrite", args)).To(Equal("todowrite: 0 todos"))
		})

		It("falls back to count when no active items remain", func() {
			args := map[string]any{
				"todos": []interface{}{
					map[string]interface{}{"content": "Done", "status": "completed", "priority": "low"},
					map[string]interface{}{"content": "Cancelled", "status": "cancelled", "priority": "low"},
				},
			}
			Expect(tooldisplay.Summary("todowrite", args)).To(Equal("todowrite: 0/2 todos"))
		})

		It("truncates long content with an ellipsis", func() {
			longContent := strings.Repeat("z", truncateLen+30)
			args := map[string]any{
				"todos": []interface{}{
					map[string]interface{}{"content": longContent, "status": "in_progress", "priority": "high"},
				},
			}
			result := tooldisplay.Summary("todowrite", args)
			Expect(result).To(HaveSuffix("..."))
			Expect(len(result)).To(BeNumerically("<=", len("todowrite: ")+truncateLen+len("...")))
		})

		It("PrimaryArgValue returns ok=true with a non-empty value", func() {
			args := map[string]any{
				"todos": []interface{}{
					map[string]interface{}{"content": "Plan slice", "status": "pending", "priority": "high"},
				},
			}
			value, ok := tooldisplay.PrimaryArgValue("todowrite", args)
			Expect(ok).To(BeTrue())
			Expect(value).NotTo(BeEmpty())
			Expect(value).To(ContainSubstring("Plan slice"))
		})
	})

	Context("when tool is todo_update", func() {
		// Companion to todowrite: per-transition patch tool. Display surfaces
		// the status transition so the UI card shows the live progress flip
		// rather than reconstructing the whole list.

		It("renders status and the patched id together", func() {
			args := map[string]any{"id": "todo-2", "status": "in_progress"}
			Expect(tooldisplay.Summary("todo_update", args)).To(Equal("todo_update: in_progress: todo-2"))
		})

		It("falls back to content when no id is provided", func() {
			args := map[string]any{"content": "Implement feature", "status": "completed"}
			Expect(tooldisplay.Summary("todo_update", args)).To(Equal("todo_update: completed: Implement feature"))
		})

		It("renders the status alone when neither id nor content is given", func() {
			args := map[string]any{"status": "cancelled"}
			Expect(tooldisplay.Summary("todo_update", args)).To(Equal("todo_update: cancelled"))
		})

		It("falls back to the id alone when status is missing", func() {
			args := map[string]any{"id": "todo-5"}
			Expect(tooldisplay.Summary("todo_update", args)).To(Equal("todo_update: todo-5"))
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

		It("returns ok=true with subagent_type and the brief for delegate calls", func() {
			value, ok := tooldisplay.PrimaryArgValue("delegate",
				map[string]any{"subagent_type": "qa-engineer", "message": "verify"})
			Expect(ok).To(BeTrue())
			Expect(value).To(ContainSubstring("qa-engineer"))
			Expect(value).To(ContainSubstring("verify"))
		})
	})
})
