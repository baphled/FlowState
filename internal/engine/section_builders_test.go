package engine

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("Section Builders", func() {
	Describe("buildDelegationSection", func() {
		Context("when agents have triggers", func() {
			It("includes agent ID in the table alongside the name", func() {
				agents := []*agent.Manifest{
					{
						ID:   "explorer",
						Name: "Codebase Explorer",
						OrchestratorMeta: agent.OrchestratorMetadata{
							Triggers: []agent.DelegationTrigger{
								{Domain: "exploration", Trigger: "explore"},
							},
							Cost:    "FREE",
							UseWhen: []string{"When you need to explore code"},
						},
					},
				}

				result := buildDelegationSection(agents)

				Expect(result).To(ContainSubstring("Codebase Explorer (explorer)"))
			})

			It("produces a valid markdown table with ID column", func() {
				agents := []*agent.Manifest{
					{
						ID:   "librarian",
						Name: "Knowledge Librarian",
						OrchestratorMeta: agent.OrchestratorMetadata{
							Triggers: []agent.DelegationTrigger{
								{Domain: "knowledge", Trigger: "search"},
							},
							Cost:    "CHEAP",
							UseWhen: []string{"When you need to search knowledge"},
						},
					},
				}

				result := buildDelegationSection(agents)

				Expect(result).To(ContainSubstring("## Delegation Table"))
				Expect(result).To(ContainSubstring("| Agent | Cost | When to use |"))
				Expect(result).To(ContainSubstring("Knowledge Librarian (librarian)"))
				Expect(result).To(ContainSubstring("| CHEAP |"))
			})

			It("handles multiple agents sorted by name", func() {
				agents := []*agent.Manifest{
					{
						ID:   "analyst",
						Name: "Systems Analyst",
						OrchestratorMeta: agent.OrchestratorMetadata{
							Triggers: []agent.DelegationTrigger{
								{Domain: "analysis", Trigger: "analyze"},
							},
							Cost:    "EXPENSIVE",
							UseWhen: []string{"When you need systems analysis"},
						},
					},
					{
						ID:   "explorer",
						Name: "Codebase Explorer",
						OrchestratorMeta: agent.OrchestratorMetadata{
							Triggers: []agent.DelegationTrigger{
								{Domain: "exploration", Trigger: "explore"},
							},
							Cost:    "FREE",
							UseWhen: []string{"When you need to explore code"},
						},
					},
				}

				result := buildDelegationSection(agents)

				lines := strings.Split(result, "\n")
				var explorerLine, analystLine int
				for i, line := range lines {
					if strings.Contains(line, "Codebase Explorer") {
						explorerLine = i
					}
					if strings.Contains(line, "Systems Analyst") {
						analystLine = i
					}
				}
				Expect(explorerLine).To(BeNumerically("<", analystLine))
				Expect(result).To(ContainSubstring("Codebase Explorer (explorer)"))
				Expect(result).To(ContainSubstring("Systems Analyst (analyst)"))
			})
		})

		Context("when no agents have triggers", func() {
			It("returns an empty string", func() {
				agents := []*agent.Manifest{
					{
						ID:   "no-triggers",
						Name: "No Triggers Agent",
						OrchestratorMeta: agent.OrchestratorMetadata{
							Triggers: []agent.DelegationTrigger{},
						},
					},
				}

				result := buildDelegationSection(agents)

				Expect(result).To(BeEmpty())
			})
		})

		Context("when use_when is empty", func() {
			It("handles missing use_when gracefully", func() {
				agents := []*agent.Manifest{
					{
						ID:   "minimal",
						Name: "Minimal Agent",
						OrchestratorMeta: agent.OrchestratorMetadata{
							Triggers: []agent.DelegationTrigger{
								{Domain: "test", Trigger: "test"},
							},
							Cost:    "FREE",
							UseWhen: []string{},
						},
					},
				}

				result := buildDelegationSection(agents)

				Expect(result).To(ContainSubstring("Minimal Agent (minimal)"))
			})
		})
	})

	Describe("buildTemporalSection", func() {
		Context("with a fixed nowFunc", func() {
			It("includes the current date in ISO format", func() {
				fixed := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
				result := buildTemporalSection(func() time.Time { return fixed })

				Expect(result).To(ContainSubstring("2026-05-02"))
			})

			It("includes the day of week", func() {
				fixed := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
				result := buildTemporalSection(func() time.Time { return fixed })

				Expect(result).To(ContainSubstring("Saturday"))
			})

			It("includes UTC timezone indicator", func() {
				fixed := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
				result := buildTemporalSection(func() time.Time { return fixed })

				Expect(result).To(ContainSubstring("UTC"))
			})

			It("renders as a markdown section header", func() {
				fixed := time.Date(2026, 5, 2, 14, 30, 0, 0, time.UTC)
				result := buildTemporalSection(func() time.Time { return fixed })

				Expect(result).To(ContainSubstring("## Temporal Context"))
			})

			It("produces a single-line body with date, weekday, and timezone", func() {
				fixed := time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)
				result := buildTemporalSection(func() time.Time { return fixed })

				Expect(result).To(ContainSubstring("Today is 2026-05-02 (Saturday, UTC)"))
			})
		})
	})

	Describe("buildToolDisciplineSection", func() {
		Context("when the manifest has file tools", func() {
			It("includes the purpose-built file tools rule", func() {
				manifest := agent.Manifest{Capabilities: agent.Capabilities{Tools: []string{"bash", "read", "write", "edit"}}}

				result := buildToolDisciplineSection(manifest)

				Expect(result).To(ContainSubstring("## Tool Discipline"))
				Expect(result).To(ContainSubstring("Use purpose-built file tools, not bash"))
				Expect(result).To(ContainSubstring("use the `write` tool"))
				Expect(result).To(ContainSubstring("use the `edit` tool"))
				Expect(result).To(ContainSubstring("use the `read` tool (with offset/limit for large files)"))
				Expect(result).To(ContainSubstring("`sed -i`"))
				Expect(result).To(ContainSubstring("Bash is for builds, tests, linting, git, and process/system inspection only"))
			})

			It("includes the tool-call economy rule", func() {
				manifest := agent.Manifest{Capabilities: agent.Capabilities{Tools: []string{"read"}}}

				result := buildToolDisciplineSection(manifest)

				Expect(result).To(ContainSubstring("Tool-call economy"))
				Expect(result).To(ContainSubstring("Batch independent tool calls in a single message"))
				Expect(result).To(ContainSubstring("Read only the region of a file you need (offset/limit)"))
				Expect(result).To(ContainSubstring("Prefer one precise edit over multiple rewrites"))
			})

			It("renders when only one file tool is present", func() {
				manifest := agent.Manifest{Capabilities: agent.Capabilities{Tools: []string{"bash", "grep", "glob"}}}

				Expect(buildToolDisciplineSection(manifest)).NotTo(BeEmpty())
			})
		})

		Context("when the manifest has no file tools", func() {
			It("returns an empty string", func() {
				manifest := agent.Manifest{Capabilities: agent.Capabilities{Tools: []string{"suggest_delegate", "delegate"}}}

				Expect(buildToolDisciplineSection(manifest)).To(BeEmpty())
			})

			It("returns an empty string for a manifest with no tools at all", func() {
				Expect(buildToolDisciplineSection(agent.Manifest{})).To(BeEmpty())
			})
		})
	})

	Describe("filterByAllowlist", func() {
		It("filters agents by ID", func() {
			agents := []*agent.Manifest{
				{ID: "explorer", Name: "Explorer"},
				{ID: "librarian", Name: "Librarian"},
				{ID: "analyst", Name: "Analyst"},
			}

			result := filterByAllowlist(agents, []string{"explorer", "analyst"})

			Expect(result).To(HaveLen(2))
			Expect(result[0].ID).To(Equal("explorer"))
			Expect(result[1].ID).To(Equal("analyst"))
		})

		It("returns all agents when allowlist is empty", func() {
			agents := []*agent.Manifest{
				{ID: "explorer", Name: "Explorer"},
				{ID: "librarian", Name: "Librarian"},
			}

			result := filterByAllowlist(agents, []string{})

			Expect(result).To(HaveLen(2))
		})
	})
})
