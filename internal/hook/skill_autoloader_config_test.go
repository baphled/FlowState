package hook_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/hook"
)

var _ = Describe("SkillAutoLoaderConfig", func() {
	Describe("DefaultSkillAutoLoaderConfig", func() {
		It("returns baseline skills matching the canonical core-tier set", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.BaselineSkills).To(Equal([]string{
				"pre-action",
				"memory-keeper",
				"token-cost-estimation",
				"retrospective",
				"note-taking",
				"knowledge-base",
				"discipline",
				"skill-discovery",
				"agent-discovery",
			}))
		})

		It("returns max auto skills of 6", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.MaxAutoSkills).To(Equal(6))
		})

		It("returns max auto skills bytes of 35840", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.MaxAutoSkillsBytes).To(Equal(35840))
		})

		It("returns per skill max bytes of 5120", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.PerSkillMaxBytes).To(Equal(5120))
		})

		It("returns empty category mappings", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.CategoryMappings).To(BeEmpty())
		})

		It("returns empty keyword patterns", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.KeywordPatterns).To(BeEmpty())
		})
	})

	Describe("LoadSkillAutoLoaderConfig", func() {
		It("returns default config when file does not exist", func() {
			cfg, err := hook.LoadSkillAutoLoaderConfig("/nonexistent/path/config.yaml")
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.BaselineSkills).To(Equal([]string{
				"pre-action",
				"memory-keeper",
				"token-cost-estimation",
				"retrospective",
				"note-taking",
				"knowledge-base",
				"discipline",
				"skill-discovery",
				"agent-discovery",
			}))
			Expect(cfg.MaxAutoSkills).To(Equal(6))
			Expect(cfg.MaxAutoSkillsBytes).To(Equal(35840))
			Expect(cfg.PerSkillMaxBytes).To(Equal(5120))
		})

		It("loads config from a valid YAML file", func() {
			dir := GinkgoT().TempDir()
			configPath := filepath.Join(dir, "skill-autoloader.yaml")

			yamlContent := hook.SkillAutoLoaderConfig{
				BaselineSkills:     []string{"custom-skill-a", "custom-skill-b"},
				MaxAutoSkills:      10,
				MaxAutoSkillsBytes: 51200,
				PerSkillMaxBytes:   6144,
				CategoryMappings: map[string][]string{
					"deep": {"golang", "architecture"},
				},
				KeywordPatterns: []hook.KeywordPattern{
					{Pattern: "database", Skills: []string{"db-operations", "sql"}},
				},
			}
			data, err := yaml.Marshal(yamlContent)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(configPath, data, 0o600)).To(Succeed())

			cfg, err := hook.LoadSkillAutoLoaderConfig(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.BaselineSkills).To(Equal([]string{"custom-skill-a", "custom-skill-b"}))
			Expect(cfg.MaxAutoSkills).To(Equal(10))
			Expect(cfg.MaxAutoSkillsBytes).To(Equal(51200))
			Expect(cfg.PerSkillMaxBytes).To(Equal(6144))
			Expect(cfg.CategoryMappings).To(HaveKey("deep"))
			Expect(cfg.CategoryMappings["deep"]).To(Equal([]string{"golang", "architecture"}))
			Expect(cfg.KeywordPatterns).To(HaveLen(1))
			Expect(cfg.KeywordPatterns[0].Pattern).To(Equal("database"))
			Expect(cfg.KeywordPatterns[0].Skills).To(Equal([]string{"db-operations", "sql"}))
		})

		It("returns error for invalid YAML", func() {
			dir := GinkgoT().TempDir()
			configPath := filepath.Join(dir, "invalid.yaml")
			Expect(os.WriteFile(configPath, []byte(": invalid: yaml: [broken"), 0o600)).To(Succeed())

			_, err := hook.LoadSkillAutoLoaderConfig(configPath)
			Expect(err).To(HaveOccurred())
		})

		Context("error paths", func() {
			It("returns error when config path parent is not accessible", func() {
				dir := GinkgoT().TempDir()
				nestedDir := filepath.Join(dir, "nested")
				configPath := filepath.Join(nestedDir, "config.yaml")
				Expect(os.MkdirAll(nestedDir, 0o755)).To(Succeed())
				Expect(os.WriteFile(configPath, []byte("baseline_skills: []"), 0o600)).To(Succeed())
				Expect(os.Chmod(nestedDir, 0o000)).To(Succeed())
				DeferCleanup(func() {
					os.Chmod(nestedDir, 0o755)
				})

				_, err := hook.LoadSkillAutoLoaderConfig(configPath)
				Expect(err).To(HaveOccurred())
			})

			It("returns error when config file is not readable", func() {
				dir := GinkgoT().TempDir()
				configPath := filepath.Join(dir, "config.yaml")
				Expect(os.WriteFile(configPath, []byte("baseline_skills: []"), 0o000)).To(Succeed())
				DeferCleanup(func() {
					os.Chmod(configPath, 0o644)
				})

				_, err := hook.LoadSkillAutoLoaderConfig(configPath)
				Expect(err).To(HaveOccurred())
			})
		})

		It("round-trips config through YAML correctly", func() {
			dir := GinkgoT().TempDir()
			configPath := filepath.Join(dir, "roundtrip.yaml")

			original := &hook.SkillAutoLoaderConfig{
				BaselineSkills: []string{"skill-a", "skill-b", "skill-c"},
				MaxAutoSkills:  3,
				CategoryMappings: map[string][]string{
					"quick":   {"golang"},
					"writing": {"documentation-writing", "british-english"},
				},
				KeywordPatterns: []hook.KeywordPattern{
					{Pattern: "test", Skills: []string{"ginkgo-gomega"}},
					{Pattern: "deploy", Skills: []string{"devops", "docker"}},
				},
			}

			data, err := yaml.Marshal(original)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(configPath, data, 0o600)).To(Succeed())

			loaded, err := hook.LoadSkillAutoLoaderConfig(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(loaded.BaselineSkills).To(Equal(original.BaselineSkills))
			Expect(loaded.MaxAutoSkills).To(Equal(original.MaxAutoSkills))
			Expect(loaded.MaxAutoSkillsBytes).To(Equal(original.MaxAutoSkillsBytes))
			Expect(loaded.PerSkillMaxBytes).To(Equal(original.PerSkillMaxBytes))
			Expect(loaded.CategoryMappings).To(Equal(original.CategoryMappings))
			Expect(loaded.KeywordPatterns).To(Equal(original.KeywordPatterns))
		})

		It("allows zero value byte budgets from YAML", func() {
			dir := GinkgoT().TempDir()
			configPath := filepath.Join(dir, "zero-budget.yaml")
			Expect(os.WriteFile(configPath, []byte("max_auto_skills_bytes: 0\nper_skill_max_bytes: 0\n"), 0o600)).To(Succeed())

			cfg, err := hook.LoadSkillAutoLoaderConfig(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.MaxAutoSkillsBytes).To(Equal(0))
			Expect(cfg.PerSkillMaxBytes).To(Equal(0))
		})
	})
})
