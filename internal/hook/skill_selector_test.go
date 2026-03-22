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
