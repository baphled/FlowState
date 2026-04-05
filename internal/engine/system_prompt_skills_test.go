package engine_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/skill"
)

var _ = Describe("BuildSystemPrompt skill injection", func() {
	var chatProvider *mockProvider

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-provider",
		}
	})

	It("includes skill heading and content when one skill is configured", func() {
		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Skills: []skill.Skill{
				{Name: "pre-action", Content: "PREFLIGHT content"},
			},
		})

		prompt := eng.BuildSystemPrompt()

		Expect(prompt).To(ContainSubstring("# Skill: pre-action"))
		Expect(prompt).To(ContainSubstring("PREFLIGHT content"))
	})

	It("includes all skill headings and content when multiple skills are configured", func() {
		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Skills: []skill.Skill{
				{Name: "pre-action", Content: "PREFLIGHT content"},
				{Name: "memory-keeper", Content: "MEMORY content"},
			},
		})

		prompt := eng.BuildSystemPrompt()

		Expect(prompt).To(ContainSubstring("# Skill: pre-action"))
		Expect(prompt).To(ContainSubstring("PREFLIGHT content"))
		Expect(prompt).To(ContainSubstring("# Skill: memory-keeper"))
		Expect(prompt).To(ContainSubstring("MEMORY content"))
	})

	It("does not include skill content markers when no skills are configured", func() {
		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Skills:       nil,
		})

		prompt := eng.BuildSystemPrompt()

		Expect(prompt).NotTo(ContainSubstring("# Skill:"))
	})
})
