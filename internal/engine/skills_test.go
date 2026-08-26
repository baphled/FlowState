package engine_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("LoadAlwaysActiveSkills", func() {
	var (
		tempDir string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "skills-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	createSkill := func(name, content string) {
		skillDir := filepath.Join(tempDir, name)
		Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
		skillFile := filepath.Join(skillDir, "SKILL.md")
		Expect(os.WriteFile(skillFile, []byte(content), 0o600)).To(Succeed())
	}

	Describe("loading matching skills from directory", func() {
		BeforeEach(func() {
			createSkill("memory-keeper", "---\nname: memory-keeper\n---\n# Memory Keeper\nContent here")
			createSkill("pre-action", "---\nname: pre-action\n---\n# Pre Action\nPre-action content")
			createSkill("other-skill", "---\nname: other-skill\n---\n# Other\nOther content")
		})

		It("returns only skills matching the requested names", func() {
			appLevel := []string{"memory-keeper"}
			agentLevel := []string{"pre-action"}

			skills := engine.LoadAlwaysActiveSkills(tempDir, appLevel, agentLevel)

			Expect(skills).To(HaveLen(2))
			names := []string{skills[0].Name, skills[1].Name}
			Expect(names).To(ContainElements("memory-keeper", "pre-action"))
		})
	})

	Describe("deduplication", func() {
		BeforeEach(func() {
			createSkill("memory-keeper", "---\nname: memory-keeper\n---\n# Memory Keeper")
		})

		It("deduplicates skills appearing in both app-level and agent-level", func() {
			appLevel := []string{"memory-keeper"}
			agentLevel := []string{"memory-keeper"}

			skills := engine.LoadAlwaysActiveSkills(tempDir, appLevel, agentLevel)

			Expect(skills).To(HaveLen(1))
			Expect(skills[0].Name).To(Equal("memory-keeper"))
		})
	})

	Describe("missing skills", func() {
		BeforeEach(func() {
			createSkill("memory-keeper", "---\nname: memory-keeper\n---\n# Memory Keeper")
		})

		It("silently skips skills not found on disk", func() {
			appLevel := []string{"memory-keeper", "non-existent-skill"}
			agentLevel := []string{}

			skills := engine.LoadAlwaysActiveSkills(tempDir, appLevel, agentLevel)

			Expect(skills).To(HaveLen(1))
			Expect(skills[0].Name).To(Equal("memory-keeper"))
		})
	})

	Describe("empty input", func() {
		It("returns empty slice when no skill names provided", func() {
			skills := engine.LoadAlwaysActiveSkills(tempDir, []string{}, []string{})

			Expect(skills).To(BeEmpty())
		})

		It("returns empty slice when nil slices provided", func() {
			skills := engine.LoadAlwaysActiveSkills(tempDir, nil, nil)

			Expect(skills).To(BeEmpty())
		})
	})

	Describe("merging app-level and agent-level skills", func() {
		BeforeEach(func() {
			createSkill("skill-a", "---\nname: skill-a\n---\n# Skill A")
			createSkill("skill-b", "---\nname: skill-b\n---\n# Skill B")
			createSkill("skill-c", "---\nname: skill-c\n---\n# Skill C")
		})

		It("includes skills from both app-level and agent-level", func() {
			appLevel := []string{"skill-a", "skill-b"}
			agentLevel := []string{"skill-c"}

			skills := engine.LoadAlwaysActiveSkills(tempDir, appLevel, agentLevel)

			Expect(skills).To(HaveLen(3))
			names := make([]string, len(skills))
			for i, s := range skills {
				names[i] = s.Name
			}
			Expect(names).To(ContainElements("skill-a", "skill-b", "skill-c"))
		})
	})

	Describe("skills directory handling", func() {
		It("returns empty slice for non-existent directory", func() {
			skills := engine.LoadAlwaysActiveSkills("/non/existent/path", []string{"some-skill"}, nil)

			Expect(skills).To(BeEmpty())
		})
	})
})
