package engine

import (
	"strings"

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
