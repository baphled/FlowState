package engine_test

import (
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/skill"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("BuildSystemPrompt skill injection", func() {
	It("injects content for two skills", func() {
		eng := engine.New(engine.Config{
			Skills: []skill.Skill{
				{Name: "pre-action", Content: "PREFLIGHT"},
				{Name: "memory-keeper", Content: "MEMORY"},
			},
		})
		prompt := eng.BuildSystemPrompt()
		Expect(prompt).To(ContainSubstring("# Skill: pre-action"))
		Expect(prompt).To(ContainSubstring("PREFLIGHT"))
		Expect(prompt).To(ContainSubstring("# Skill: memory-keeper"))
		Expect(prompt).To(ContainSubstring("MEMORY"))
	})

	It("injects content for a single skill", func() {
		eng := engine.New(engine.Config{
			Skills: []skill.Skill{
				{Name: "discipline", Content: "DISCIPLINE_CONTENT"},
			},
		})
		prompt := eng.BuildSystemPrompt()
		Expect(prompt).To(ContainSubstring("# Skill: discipline"))
		Expect(prompt).To(ContainSubstring("DISCIPLINE_CONTENT"))
	})

	It("produces no skill marker when Skills is nil", func() {
		eng := engine.New(engine.Config{})
		prompt := eng.BuildSystemPrompt()
		Expect(prompt).NotTo(ContainSubstring("# Skill:"))
	})
})
