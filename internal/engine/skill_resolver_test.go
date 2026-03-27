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
	})
})
