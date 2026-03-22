package hook_test

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/hook"
)

var _ = Describe("SkillSelector Types", func() {
	Describe("SkillSelection", func() {
		It("can be instantiated with skills and sources", func() {
			selection := &hook.SkillSelection{
				Skills: []string{"skill1", "skill2"},
				Sources: []hook.SkillSource{
					{Skill: "skill1", Source: "config", Pattern: "pattern1"},
				},
			}

			Expect(selection.Skills).To(HaveLen(2))
			Expect(selection.Skills[0]).To(Equal("skill1"))
			Expect(selection.Sources).To(HaveLen(1))
			Expect(selection.Sources[0].Skill).To(Equal("skill1"))
		})

		It("marshals to JSON with correct tags", func() {
			selection := &hook.SkillSelection{
				Skills: []string{"skill1"},
				Sources: []hook.SkillSource{
					{Skill: "skill1", Source: "config", Pattern: "pattern1"},
				},
			}

			data, err := json.Marshal(selection)
			Expect(err).NotTo(HaveOccurred())

			var unmarshaled hook.SkillSelection
			err = json.Unmarshal(data, &unmarshaled)
			Expect(err).NotTo(HaveOccurred())

			Expect(unmarshaled.Skills).To(Equal(selection.Skills))
			Expect(unmarshaled.Sources).To(HaveLen(1))
			Expect(unmarshaled.Sources[0].Skill).To(Equal("skill1"))
		})

		It("round-trips through JSON correctly", func() {
			original := &hook.SkillSelection{
				Skills: []string{"skill1", "skill2", "skill3"},
				Sources: []hook.SkillSource{
					{Skill: "skill1", Source: "config", Pattern: "pattern1"},
					{Skill: "skill2", Source: "agent", Pattern: "pattern2"},
				},
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var restored hook.SkillSelection
			err = json.Unmarshal(data, &restored)
			Expect(err).NotTo(HaveOccurred())

			Expect(restored.Skills).To(Equal(original.Skills))
			Expect(restored.Sources).To(HaveLen(2))
			Expect(restored.Sources[0]).To(Equal(original.Sources[0]))
			Expect(restored.Sources[1]).To(Equal(original.Sources[1]))
		})
	})

	Describe("SkillSource", func() {
		It("can be instantiated with skill, source, and pattern", func() {
			source := &hook.SkillSource{
				Skill:   "test-skill",
				Source:  "config",
				Pattern: "test-pattern",
			}

			Expect(source.Skill).To(Equal("test-skill"))
			Expect(source.Source).To(Equal("config"))
			Expect(source.Pattern).To(Equal("test-pattern"))
		})

		It("marshals to JSON with correct tags", func() {
			source := &hook.SkillSource{
				Skill:   "test-skill",
				Source:  "config",
				Pattern: "test-pattern",
			}

			data, err := json.Marshal(source)
			Expect(err).NotTo(HaveOccurred())

			var unmarshaled hook.SkillSource
			err = json.Unmarshal(data, &unmarshaled)
			Expect(err).NotTo(HaveOccurred())

			Expect(unmarshaled.Skill).To(Equal("test-skill"))
			Expect(unmarshaled.Source).To(Equal("config"))
			Expect(unmarshaled.Pattern).To(Equal("test-pattern"))
		})

		It("round-trips through JSON correctly", func() {
			original := &hook.SkillSource{
				Skill:   "golang",
				Source:  "agent",
				Pattern: "go.*",
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var restored hook.SkillSource
			err = json.Unmarshal(data, &restored)
			Expect(err).NotTo(HaveOccurred())

			Expect(restored).To(Equal(*original))
		})
	})

	Describe("SkillSelectionInput", func() {
		It("can be instantiated with all fields", func() {
			input := &hook.SkillSelectionInput{
				AgentID:            "agent-1",
				Category:           "golang",
				Prompt:             "test prompt",
				ExistingSkills:     []string{"skill1", "skill2"},
				AgentDefaultSkills: []string{"default1"},
			}

			Expect(input.AgentID).To(Equal("agent-1"))
			Expect(input.Category).To(Equal("golang"))
			Expect(input.Prompt).To(Equal("test prompt"))
			Expect(input.ExistingSkills).To(HaveLen(2))
			Expect(input.AgentDefaultSkills).To(HaveLen(1))
		})

		It("marshals to JSON with correct tags", func() {
			input := &hook.SkillSelectionInput{
				AgentID:            "agent-1",
				Category:           "golang",
				Prompt:             "test prompt",
				ExistingSkills:     []string{"skill1"},
				AgentDefaultSkills: []string{"default1"},
			}

			data, err := json.Marshal(input)
			Expect(err).NotTo(HaveOccurred())

			var unmarshaled hook.SkillSelectionInput
			err = json.Unmarshal(data, &unmarshaled)
			Expect(err).NotTo(HaveOccurred())

			Expect(unmarshaled.AgentID).To(Equal("agent-1"))
			Expect(unmarshaled.Category).To(Equal("golang"))
			Expect(unmarshaled.Prompt).To(Equal("test prompt"))
		})

		It("round-trips through JSON correctly", func() {
			original := &hook.SkillSelectionInput{
				AgentID:            "agent-123",
				Category:           "testing",
				Prompt:             "write a test for this function",
				ExistingSkills:     []string{"golang", "ginkgo-gomega", "clean-code"},
				AgentDefaultSkills: []string{"pre-action", "memory-keeper"},
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var restored hook.SkillSelectionInput
			err = json.Unmarshal(data, &restored)
			Expect(err).NotTo(HaveOccurred())

			Expect(restored.AgentID).To(Equal(original.AgentID))
			Expect(restored.Category).To(Equal(original.Category))
			Expect(restored.Prompt).To(Equal(original.Prompt))
			Expect(restored.ExistingSkills).To(Equal(original.ExistingSkills))
			Expect(restored.AgentDefaultSkills).To(Equal(original.AgentDefaultSkills))
		})

		It("handles empty slices correctly", func() {
			input := &hook.SkillSelectionInput{
				AgentID:            "agent-1",
				Category:           "golang",
				Prompt:             "test",
				ExistingSkills:     []string{},
				AgentDefaultSkills: []string{},
			}

			data, err := json.Marshal(input)
			Expect(err).NotTo(HaveOccurred())

			var restored hook.SkillSelectionInput
			err = json.Unmarshal(data, &restored)
			Expect(err).NotTo(HaveOccurred())

			Expect(restored.ExistingSkills).To(BeEmpty())
			Expect(restored.AgentDefaultSkills).To(BeEmpty())
		})
	})
})

var _ = Describe("SelectSkills", func() {
	var (
		input  hook.SkillSelectionInput
		config *hook.SkillAutoLoaderConfig
	)

	BeforeEach(func() {
		input = hook.SkillSelectionInput{}
		config = &hook.SkillAutoLoaderConfig{
			BaselineSkills:   []string{"pre-action", "memory-keeper"},
			MaxAutoSkills:    6,
			CategoryMappings: map[string][]string{},
			KeywordPatterns:  []hook.KeywordPattern{},
		}
	})

	Context("with empty input and minimal config", func() {
		It("returns baseline skills only", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(Equal([]string{"pre-action", "memory-keeper"}))
			Expect(result.Sources).To(HaveLen(2))
		})

		It("marks baseline skills with source 'baseline'", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Sources[0]).To(Equal(hook.SkillSource{
				Skill:   "pre-action",
				Source:  "baseline",
				Pattern: "",
			}))
			Expect(result.Sources[1]).To(Equal(hook.SkillSource{
				Skill:   "memory-keeper",
				Source:  "baseline",
				Pattern: "",
			}))
		})
	})

	Context("with empty baseline config", func() {
		BeforeEach(func() {
			config.BaselineSkills = []string{}
		})

		It("returns empty selection when no skills configured", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(BeEmpty())
			Expect(result.Sources).To(BeEmpty())
		})
	})

	Context("with agent default skills", func() {
		BeforeEach(func() {
			input.AgentDefaultSkills = []string{"golang", "clean-code", "ginkgo-gomega"}
		})

		It("includes agent skills after baseline", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(Equal([]string{
				"pre-action", "memory-keeper",
				"golang", "clean-code", "ginkgo-gomega",
			}))
		})

		It("marks agent skills with source 'agent'", func() {
			result := hook.SelectSkills(input, config)

			agentSources := filterSourcesBySource(result.Sources, "agent")
			Expect(agentSources).To(HaveLen(3))
			Expect(agentSources[0]).To(Equal(hook.SkillSource{
				Skill:   "golang",
				Source:  "agent",
				Pattern: "",
			}))
		})
	})

	Context("with keyword patterns", func() {
		BeforeEach(func() {
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "ginkgo", Skills: []string{"ginkgo-gomega", "bdd-workflow"}},
				{Pattern: "database", Skills: []string{"golang-database"}},
			}
		})

		It("matches keywords in the prompt", func() {
			input.Prompt = "write a ginkgo test for the service"

			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(ContainElements("ginkgo-gomega", "bdd-workflow"))
		})

		It("marks keyword-matched skills with source 'keyword' and the pattern", func() {
			input.Prompt = "write a ginkgo test"

			result := hook.SelectSkills(input, config)

			keywordSources := filterSourcesBySource(result.Sources, "keyword")
			Expect(keywordSources).To(ContainElement(hook.SkillSource{
				Skill:   "ginkgo-gomega",
				Source:  "keyword",
				Pattern: "ginkgo",
			}))
		})

		It("matches keywords case-insensitively", func() {
			input.Prompt = "Write a GINKGO test for the DATABASE layer"

			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(ContainElements("ginkgo-gomega", "bdd-workflow", "golang-database"))
		})

		It("does not match unrelated keywords", func() {
			input.Prompt = "write a simple function"

			result := hook.SelectSkills(input, config)

			Expect(result.Skills).NotTo(ContainElement("ginkgo-gomega"))
			Expect(result.Skills).NotTo(ContainElement("golang-database"))
		})
	})

	Context("with deduplication", func() {
		BeforeEach(func() {
			input.AgentDefaultSkills = []string{"pre-action", "golang"}
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "go code", Skills: []string{"golang", "clean-code"}},
			}
		})

		It("does not duplicate skills from baseline and agent tiers", func() {
			result := hook.SelectSkills(input, config)

			occurrences := countOccurrences(result.Skills, "pre-action")
			Expect(occurrences).To(Equal(1))
		})

		It("does not duplicate skills from agent and keyword tiers", func() {
			input.Prompt = "write go code"

			result := hook.SelectSkills(input, config)

			occurrences := countOccurrences(result.Skills, "golang")
			Expect(occurrences).To(Equal(1))
		})

		It("tracks the earliest source for deduplicated skills", func() {
			input.Prompt = "write go code"

			result := hook.SelectSkills(input, config)

			golangSources := filterSourcesBySkill(result.Sources, "golang")
			Expect(golangSources).To(HaveLen(1))
			Expect(golangSources[0].Source).To(Equal("agent"))
		})
	})

	Context("with MaxAutoSkills cap", func() {
		BeforeEach(func() {
			config.MaxAutoSkills = 2
			input.AgentDefaultSkills = []string{"golang", "clean-code", "ginkgo-gomega"}
		})

		It("caps non-baseline skills at MaxAutoSkills", func() {
			result := hook.SelectSkills(input, config)

			nonBaselineSkills := removeBaseline(result.Skills, config.BaselineSkills)
			Expect(nonBaselineSkills).To(HaveLen(2))
		})

		It("always includes all baseline skills regardless of cap", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(ContainElements("pre-action", "memory-keeper"))
		})

		It("caps combined agent and keyword skills", func() {
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "test", Skills: []string{"tdd-first"}},
			}
			input.Prompt = "write a test"

			result := hook.SelectSkills(input, config)

			nonBaselineSkills := removeBaseline(result.Skills, config.BaselineSkills)
			Expect(len(nonBaselineSkills)).To(BeNumerically("<=", 2))
		})
	})

	Context("with empty prompt", func() {
		BeforeEach(func() {
			input.AgentDefaultSkills = []string{"golang"}
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "ginkgo", Skills: []string{"ginkgo-gomega"}},
			}
		})

		It("includes baseline and agent skills but no keyword matches", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(Equal([]string{
				"pre-action", "memory-keeper", "golang",
			}))
		})
	})

	Context("with comprehensive source tracking", func() {
		BeforeEach(func() {
			input.AgentDefaultSkills = []string{"golang"}
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "ginkgo", Skills: []string{"ginkgo-gomega"}},
			}
			input.Prompt = "write a ginkgo test"
		})

		It("tracks sources for skills from all three tiers", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Sources).To(ContainElements(
				hook.SkillSource{Skill: "pre-action", Source: "baseline", Pattern: ""},
				hook.SkillSource{Skill: "memory-keeper", Source: "baseline", Pattern: ""},
				hook.SkillSource{Skill: "golang", Source: "agent", Pattern: ""},
				hook.SkillSource{Skill: "ginkgo-gomega", Source: "keyword", Pattern: "ginkgo"},
			))
		})

		It("has matching lengths for skills and sources", func() {
			result := hook.SelectSkills(input, config)

			Expect(result.Sources).To(HaveLen(len(result.Skills)))
		})
	})

	Context("with nil config fields", func() {
		It("handles nil BaselineSkills", func() {
			config.BaselineSkills = nil

			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(BeEmpty())
			Expect(result.Sources).To(BeEmpty())
		})

		It("handles nil KeywordPatterns", func() {
			config.KeywordPatterns = nil
			input.Prompt = "some prompt"

			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(Equal([]string{"pre-action", "memory-keeper"}))
		})

		It("handles nil AgentDefaultSkills", func() {
			input.AgentDefaultSkills = nil

			result := hook.SelectSkills(input, config)

			Expect(result.Skills).To(Equal([]string{"pre-action", "memory-keeper"}))
		})
	})
})

func filterSourcesBySource(sources []hook.SkillSource, source string) []hook.SkillSource {
	var filtered []hook.SkillSource
	for _, s := range sources {
		if s.Source == source {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func filterSourcesBySkill(sources []hook.SkillSource, skill string) []hook.SkillSource {
	var filtered []hook.SkillSource
	for _, s := range sources {
		if s.Skill == skill {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func countOccurrences(skills []string, target string) int {
	count := 0
	for _, s := range skills {
		if s == target {
			count++
		}
	}
	return count
}

func removeBaseline(skills, baseline []string) []string {
	baselineSet := make(map[string]bool)
	for _, b := range baseline {
		baselineSet[b] = true
	}
	var result []string
	for _, s := range skills {
		if !baselineSet[s] {
			result = append(result, s)
		}
	}
	return result
}
