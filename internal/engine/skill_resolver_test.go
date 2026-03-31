package engine_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("FileSkillResolver", func() {
	var tmpDir string

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
	})

	Describe("Resolve", func() {
		Context("when skill file exists", func() {
			It("returns the skill content", func() {
				skillDir := filepath.Join(tmpDir, "test-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())

				skillContent := "# Test Skill\n\nThis is test skill content."
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				content, err := resolver.Resolve("test-skill")

				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(Equal(skillContent))
			})
		})

		Context("when skill file does not exist", func() {
			It("returns ErrSkillNotFound", func() {
				resolver := engine.NewFileSkillResolver(tmpDir)
				_, err := resolver.Resolve("nonexistent-skill")

				Expect(err).To(MatchError(engine.ErrSkillNotFound))
			})
		})

		Context("when skill file is empty", func() {
			It("returns empty string without error", func() {
				skillDir := filepath.Join(tmpDir, "empty-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(""), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				content, err := resolver.Resolve("empty-skill")

				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(BeEmpty())
			})
		})

		Context("caching behaviour", func() {
			It("returns cached content after the file is deleted", func() {
				skillDir := filepath.Join(tmpDir, "cached-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillContent := "# Skill: cached-skill\n\nCached content."
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o600)).To(Succeed())

				resolver := engine.NewFileSkillResolver(tmpDir)
				content, err := resolver.Resolve("cached-skill")
				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(Equal(skillContent))

				Expect(os.RemoveAll(skillDir)).To(Succeed())

				content, err = resolver.Resolve("cached-skill")
				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(Equal(skillContent))
			})

			It("does not cache errors for missing skills", func() {
				resolver := engine.NewFileSkillResolver(tmpDir)
				_, err := resolver.Resolve("late-skill")
				Expect(err).To(MatchError(engine.ErrSkillNotFound))

				skillDir := filepath.Join(tmpDir, "late-skill")
				Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
				skillContent := "# Skill: late-skill\n\nAppeared later."
				Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o600)).To(Succeed())

				content, err := resolver.Resolve("late-skill")
				Expect(err).NotTo(HaveOccurred())
				Expect(content).To(Equal(skillContent))
			})

			It("caches each skill independently", func() {
				for _, name := range []string{"alpha", "beta"} {
					skillDir := filepath.Join(tmpDir, name)
					Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
					Expect(os.WriteFile(
						filepath.Join(skillDir, "SKILL.md"),
						[]byte("# Skill: "+name),
						0o600,
					)).To(Succeed())
				}

				resolver := engine.NewFileSkillResolver(tmpDir)
				alpha, err := resolver.Resolve("alpha")
				Expect(err).NotTo(HaveOccurred())
				Expect(alpha).To(Equal("# Skill: alpha"))

				beta, err := resolver.Resolve("beta")
				Expect(err).NotTo(HaveOccurred())
				Expect(beta).To(Equal("# Skill: beta"))

				Expect(alpha).NotTo(Equal(beta))
			})
		})
	})
})
