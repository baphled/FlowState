package skill_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/skill"
)

var _ = Describe("Importer", func() {
	var (
		importer  *skill.Importer
		skillsDir string
		ctx       context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		var err error
		skillsDir, err = os.MkdirTemp("", "skills-test-*")
		Expect(err).NotTo(HaveOccurred())
		importer = skill.NewImporter(skillsDir)
	})

	AfterEach(func() {
		os.RemoveAll(skillsDir)
	})

	Describe("Add", func() {
		Context("with a valid SKILL.md", func() {
			var repoDir string

			BeforeEach(func() {
				repoDir = createTestGitRepo(GinkgoT(), `---
name: test-skill
description: A test skill for validation
category: testing
---

# Test Skill

This is a test skill.
`)
			})

			AfterEach(func() {
				os.RemoveAll(repoDir)
			})

			It("installs the skill to the skills directory", func() {
				result, err := importer.AddFromPath(ctx, repoDir)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Name).To(Equal("test-skill"))
				Expect(result.Description).To(Equal("A test skill for validation"))

				installedPath := filepath.Join(skillsDir, "test-skill", "SKILL.md")
				Expect(installedPath).To(BeAnExistingFile())
			})
		})

		Context("when name is missing in frontmatter", func() {
			var repoDir string

			BeforeEach(func() {
				repoDir = createTestGitRepo(GinkgoT(), `---
description: Missing name field
---

# Skill without name
`)
			})

			AfterEach(func() {
				os.RemoveAll(repoDir)
			})

			It("returns ErrInvalidSkill", func() {
				_, err := importer.AddFromPath(ctx, repoDir)

				Expect(err).To(MatchError(skill.ErrInvalidSkill))
			})
		})

		Context("when description is missing in frontmatter", func() {
			var repoDir string

			BeforeEach(func() {
				repoDir = createTestGitRepo(GinkgoT(), `---
name: no-description
---

# Skill without description
`)
			})

			AfterEach(func() {
				os.RemoveAll(repoDir)
			})

			It("returns ErrInvalidSkill", func() {
				_, err := importer.AddFromPath(ctx, repoDir)

				Expect(err).To(MatchError(skill.ErrInvalidSkill))
			})
		})

		Context("when skill already exists", func() {
			var repoDir string

			BeforeEach(func() {
				repoDir = createTestGitRepo(GinkgoT(), `---
name: existing-skill
description: This skill already exists
---

# Existing Skill
`)
				existingSkillDir := filepath.Join(skillsDir, "existing-skill")
				Expect(os.MkdirAll(existingSkillDir, 0o755)).To(Succeed())
				//nolint:gosec // Test file permissions
				Expect(os.WriteFile(filepath.Join(existingSkillDir, "SKILL.md"), []byte("existing"), 0o644)).To(Succeed())
			})

			AfterEach(func() {
				os.RemoveAll(repoDir)
			})

			It("returns ErrSkillExists", func() {
				_, err := importer.AddFromPath(ctx, repoDir)

				Expect(err).To(MatchError(skill.ErrSkillExists))
			})
		})

		Context("with valid SKILL.md in subdirectory", func() {
			var repoDir string

			BeforeEach(func() {
				repoDir = createTestGitRepoWithSubdir(GinkgoT(), "skills/my-skill", `---
name: nested-skill
description: A skill in a subdirectory
---

# Nested Skill
`)
			})

			AfterEach(func() {
				os.RemoveAll(repoDir)
			})

			It("finds and installs the skill", func() {
				result, err := importer.AddFromPath(ctx, repoDir)

				Expect(err).NotTo(HaveOccurred())
				Expect(result.Name).To(Equal("nested-skill"))

				installedPath := filepath.Join(skillsDir, "nested-skill", "SKILL.md")
				Expect(installedPath).To(BeAnExistingFile())
			})
		})
	})
})

func createTestGitRepo(_ GinkgoTInterface, skillContent string) string {
	dir, err := os.MkdirTemp("", "test-repo-*")
	Expect(err).NotTo(HaveOccurred())

	skillPath := filepath.Join(dir, "SKILL.md")
	err = os.WriteFile(skillPath, []byte(skillContent), 0o644) //nolint:gosec // Test file permissions
	Expect(err).NotTo(HaveOccurred())

	return dir
}

func createTestGitRepoWithSubdir(_ GinkgoTInterface, subdir, skillContent string) string {
	dir, err := os.MkdirTemp("", "test-repo-*")
	Expect(err).NotTo(HaveOccurred())

	skillDir := filepath.Join(dir, subdir)
	err = os.MkdirAll(skillDir, 0o755)
	Expect(err).NotTo(HaveOccurred())

	skillPath := filepath.Join(skillDir, "SKILL.md")
	err = os.WriteFile(skillPath, []byte(skillContent), 0o644) //nolint:gosec // Test file permissions
	Expect(err).NotTo(HaveOccurred())

	return dir
}
