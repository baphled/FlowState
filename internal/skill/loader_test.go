package skill_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/skill"
)

var _ = Describe("FileSkillLoader", func() {
	var (
		loader   *skill.FileSkillLoader
		basePath string
	)

	BeforeEach(func() {
		var err error
		basePath, err = os.MkdirTemp("", "loader-test-*")
		Expect(err).NotTo(HaveOccurred())
		loader = skill.NewFileSkillLoader(basePath)
	})

	AfterEach(func() {
		os.RemoveAll(basePath)
	})

	Describe("LoadAll", func() {
		Context("when base path does not exist", func() {
			BeforeEach(func() {
				loader = skill.NewFileSkillLoader("/nonexistent/path/should/not/exist")
			})

			It("returns an empty slice without error", func() {
				skills, err := loader.LoadAll()
				Expect(err).NotTo(HaveOccurred())
				Expect(skills).To(BeEmpty())
			})
		})

		Context("when base path is empty", func() {
			It("returns an empty slice", func() {
				skills, err := loader.LoadAll()
				Expect(err).NotTo(HaveOccurred())
				Expect(skills).To(BeEmpty())
			})
		})

		Context("when directory contains only files (no subdirectories)", func() {
			BeforeEach(func() {
				Expect(os.WriteFile(filepath.Join(basePath, "readme.txt"), []byte("not a skill"), 0o600)).To(Succeed())
			})

			It("returns an empty slice", func() {
				skills, err := loader.LoadAll()
				Expect(err).NotTo(HaveOccurred())
				Expect(skills).To(BeEmpty())
			})
		})

		Context("when directory contains valid skill subdirectories", func() {
			BeforeEach(func() {
				skillDir := filepath.Join(basePath, "golang")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: golang
description: Go language expertise
category: language
---

# Golang

Go expertise.
`), 0o600)).To(Succeed())
			})

			It("loads the skill", func() {
				skills, err := loader.LoadAll()
				Expect(err).NotTo(HaveOccurred())
				Expect(skills).To(HaveLen(1))
				Expect(skills[0].Name).To(Equal("golang"))
				Expect(skills[0].Description).To(Equal("Go language expertise"))
			})
		})

		Context("when a subdirectory has no SKILL.md", func() {
			BeforeEach(func() {
				Expect(os.MkdirAll(filepath.Join(basePath, "empty-skill"), 0o755)).To(Succeed())
			})

			It("skips the subdirectory without error", func() {
				skills, err := loader.LoadAll()
				Expect(err).NotTo(HaveOccurred())
				Expect(skills).To(BeEmpty())
			})
		})

		Context("when a subdirectory has invalid SKILL.md", func() {
			BeforeEach(func() {
				skillDir := filepath.Join(basePath, "bad-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
this is not valid yaml: [
---
body content
`), 0o600)).To(Succeed())
			})

			It("skips the invalid skill without error", func() {
				skills, err := loader.LoadAll()
				Expect(err).NotTo(HaveOccurred())
				Expect(skills).To(BeEmpty())
			})
		})

		Context("when multiple valid skills exist", func() {
			BeforeEach(func() {
				for _, name := range []string{"alpha", "beta"} {
					skillDir := filepath.Join(basePath, name)
					Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
					Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: "+name+"\ndescription: "+name+" skill\n---\n# "+name+"\n"), 0o600)).To(Succeed())
				}
			})

			It("loads all skills", func() {
				skills, err := loader.LoadAll()
				Expect(err).NotTo(HaveOccurred())
				Expect(skills).To(HaveLen(2))
			})
		})
	})

	Describe("LoadSkill", func() {
		Context("with a valid SKILL.md", func() {
			var skillPath string

			BeforeEach(func() {
				skillDir := filepath.Join(basePath, "test-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath = filepath.Join(skillDir, "SKILL.md")
				Expect(os.WriteFile(skillPath, []byte(`---
name: test-skill
description: A test skill
category: testing
tier: core
when_to_use: Writing tests
---

# Test Skill

Body content here.
`), 0o600)).To(Succeed())
			})

			It("loads the skill with all fields", func() {
				s, err := loader.LoadSkill(skillPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(s.Name).To(Equal("test-skill"))
				Expect(s.Description).To(Equal("A test skill"))
				Expect(s.Category).To(Equal("testing"))
				Expect(string(s.Tier)).To(Equal("core"))
				Expect(s.WhenToUse).To(Equal("Writing tests"))
				Expect(s.Content).To(ContainSubstring("Body content here."))
				Expect(s.FilePath).To(Equal(skillPath))
			})
		})

		Context("with a SKILL.md missing the name field", func() {
			var skillPath string

			BeforeEach(func() {
				skillDir := filepath.Join(basePath, "no-name")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath = filepath.Join(skillDir, "SKILL.md")
				Expect(os.WriteFile(skillPath, []byte(`---
description: Has description but no name
---

# No Name Skill
`), 0o600)).To(Succeed())
			})

			It("derives the name from the directory", func() {
				s, err := loader.LoadSkill(skillPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(s.Name).To(Equal("no-name"))
			})
		})

		Context("with a non-existent file", func() {
			It("returns an error", func() {
				_, err := loader.LoadSkill("/nonexistent/SKILL.md")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("reading skill file"))
			})
		})

		Context("with no frontmatter", func() {
			var skillPath string

			BeforeEach(func() {
				skillDir := filepath.Join(basePath, "plain")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath = filepath.Join(skillDir, "SKILL.md")
				Expect(os.WriteFile(skillPath, []byte("# Just markdown\n\nNo frontmatter here."), 0o600)).To(Succeed())
			})

			It("loads with name derived from directory", func() {
				s, err := loader.LoadSkill(skillPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(s.Name).To(Equal("plain"))
				Expect(s.Content).To(ContainSubstring("Just markdown"))
			})
		})

		Context("with invalid frontmatter (unclosed)", func() {
			var skillPath string

			BeforeEach(func() {
				skillDir := filepath.Join(basePath, "broken")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillPath = filepath.Join(skillDir, "SKILL.md")
				Expect(os.WriteFile(skillPath, []byte("---\nname: broken\nno closing delimiter"), 0o600)).To(Succeed())
			})

			It("returns an error about invalid frontmatter", func() {
				_, err := loader.LoadSkill(skillPath)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("extracting frontmatter"))
			})
		})
	})
})
