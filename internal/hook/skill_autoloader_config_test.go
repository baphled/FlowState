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
		It("returns baseline skills of pre-action and memory-keeper", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.BaselineSkills).To(Equal([]string{"pre-action", "memory-keeper"}))
		})

		It("returns max auto skills of 6", func() {
			cfg := hook.DefaultSkillAutoLoaderConfig()
			Expect(cfg.MaxAutoSkills).To(Equal(6))
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
			Expect(cfg.BaselineSkills).To(Equal([]string{"pre-action", "memory-keeper"}))
			Expect(cfg.MaxAutoSkills).To(Equal(6))
		})

		It("loads config from a valid YAML file", func() {
			dir := GinkgoT().TempDir()
			configPath := filepath.Join(dir, "skill-autoloader.yaml")

			yamlContent := hook.SkillAutoLoaderConfig{
				BaselineSkills: []string{"custom-skill-a", "custom-skill-b"},
				MaxAutoSkills:  10,
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
			Expect(loaded.CategoryMappings).To(Equal(original.CategoryMappings))
			Expect(loaded.KeywordPatterns).To(Equal(original.KeywordPatterns))
		})
	})
})
