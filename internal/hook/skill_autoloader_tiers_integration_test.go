package hook_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/hook"
)

var _ = Describe("SelectSkills three-tier integration", Label("integration"), func() {
	var cfg *hook.SkillAutoLoaderConfig

	BeforeEach(func() {
		cfg = &hook.SkillAutoLoaderConfig{
			BaselineSkills: []string{"pre-action", "memory-keeper"},
			MaxAutoSkills:  3,
			KeywordPatterns: []hook.KeywordPattern{
				{Pattern: "golang", Skills: []string{"golang", "ginkgo-gomega"}},
				{Pattern: "database", Skills: []string{"golang-database"}},
			},
		}
	})

	Context("Tier 1 — baseline skills", func() {
		It("includes all baseline skills regardless of prompt", func() {
			input := hook.SkillSelectionInput{AgentID: "agent", Prompt: "anything"}

			result := hook.SelectSkills(input, cfg)

			Expect(result.Skills).To(ContainElements("pre-action", "memory-keeper"))
		})

		It("marks baseline skills with source 'baseline'", func() {
			input := hook.SkillSelectionInput{AgentID: "agent", Prompt: "anything"}

			result := hook.SelectSkills(input, cfg)

			for _, src := range result.Sources {
				if src.Skill == "pre-action" || src.Skill == "memory-keeper" {
					Expect(src.Source).To(Equal("baseline"))
				}
			}
		})
	})

	Context("Tier 2 — agent manifest skills", func() {
		It("includes agent default skills when the manifest provides them", func() {
			input := hook.SkillSelectionInput{
				AgentID:            "agent",
				Prompt:             "help me",
				AgentDefaultSkills: []string{"clean-code"},
			}

			result := hook.SelectSkills(input, cfg)

			Expect(result.Skills).To(ContainElement("clean-code"))
		})

		It("marks agent skills with source 'agent'", func() {
			input := hook.SkillSelectionInput{
				AgentID:            "agent",
				Prompt:             "help me",
				AgentDefaultSkills: []string{"clean-code"},
			}

			result := hook.SelectSkills(input, cfg)

			var agentSrc *hook.SkillSource
			for i := range result.Sources {
				if result.Sources[i].Skill == "clean-code" {
					agentSrc = &result.Sources[i]
					break
				}
			}
			Expect(agentSrc).NotTo(BeNil())
			Expect(agentSrc.Source).To(Equal("agent"))
		})
	})

	Context("Tier 3 — keyword-matched skills", func() {
		It("injects keyword-matched skills when prompt matches a pattern", func() {
			input := hook.SkillSelectionInput{AgentID: "agent", Prompt: "help me write golang code"}

			result := hook.SelectSkills(input, cfg)

			Expect(result.Skills).To(ContainElement("golang"))
		})

		It("marks keyword skills with source 'keyword' and the matched pattern", func() {
			input := hook.SkillSelectionInput{AgentID: "agent", Prompt: "help me write golang code"}

			result := hook.SelectSkills(input, cfg)

			var kwSrc *hook.SkillSource
			for i := range result.Sources {
				if result.Sources[i].Skill == "golang" {
					kwSrc = &result.Sources[i]
					break
				}
			}
			Expect(kwSrc).NotTo(BeNil())
			Expect(kwSrc.Source).To(Equal("keyword"))
			Expect(kwSrc.Pattern).To(Equal("golang"))
		})
	})

	Context("all three tiers combined", func() {
		It("produces a result containing baseline, agent, and keyword skills together", func() {
			input := hook.SkillSelectionInput{
				AgentID:            "agent",
				Prompt:             "golang database queries",
				AgentDefaultSkills: []string{"clean-code"},
			}

			result := hook.SelectSkills(input, cfg)

			Expect(result.Skills).To(ContainElement("pre-action"))
			Expect(result.Skills).To(ContainElement("memory-keeper"))
			Expect(result.Skills).To(ContainElement("clean-code"))
			Expect(result.Skills).To(ContainElement("golang"))
		})
	})

	Context("MaxAutoSkills cap", func() {
		It("stops adding non-baseline skills once the cap is reached", func() {
			capCfg := &hook.SkillAutoLoaderConfig{
				BaselineSkills: []string{"pre-action"},
				MaxAutoSkills:  1,
				KeywordPatterns: []hook.KeywordPattern{
					{Pattern: "golang", Skills: []string{"golang", "ginkgo-gomega", "golang-database"}},
				},
			}
			input := hook.SkillSelectionInput{AgentID: "agent", Prompt: "golang database testing"}

			result := hook.SelectSkills(input, capCfg)

			baseline := 0
			nonBaseline := 0
			for _, s := range result.Skills {
				if s == "pre-action" {
					baseline++
				} else {
					nonBaseline++
				}
			}
			Expect(baseline).To(Equal(1))
			Expect(nonBaseline).To(BeNumerically("<=", capCfg.MaxAutoSkills))
		})
	})

	Context("deduplication", func() {
		It("does not include a skill twice when it appears in both agent defaults and keyword patterns", func() {
			input := hook.SkillSelectionInput{
				AgentID:            "agent",
				Prompt:             "golang help",
				AgentDefaultSkills: []string{"golang"},
			}

			result := hook.SelectSkills(input, cfg)

			count := 0
			for _, s := range result.Skills {
				if s == "golang" {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})

		It("does not include a baseline skill twice when it also appears in agent defaults", func() {
			input := hook.SkillSelectionInput{
				AgentID:            "agent",
				Prompt:             "help",
				AgentDefaultSkills: []string{"pre-action"},
			}

			result := hook.SelectSkills(input, cfg)

			count := 0
			for _, s := range result.Skills {
				if s == "pre-action" {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})
	})
})
