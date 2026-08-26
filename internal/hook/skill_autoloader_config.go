package hook

import (
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	// DefaultMaxAutoSkillsBytes is the default aggregate byte ceiling for non-baseline auto-loaded skills (35 KB).
	DefaultMaxAutoSkillsBytes = 35 * 1024

	// DefaultPerSkillMaxBytes is the default maximum byte size for a single auto-loaded skill (5 KB).
	DefaultPerSkillMaxBytes = 5 * 1024
)

// KeywordPattern maps a text pattern to skills that should be auto-loaded when matched.
type KeywordPattern struct {
	Pattern string   `yaml:"pattern" json:"pattern"`
	Skills  []string `yaml:"skills" json:"skills"`
}

// SkillAutoLoaderConfig controls the three-tier skill auto-loading behaviour.
type SkillAutoLoaderConfig struct {
	BaselineSkills        []string            `yaml:"baseline_skills" json:"baseline_skills"`
	MaxAutoSkills         int                 `yaml:"max_auto_skills" json:"max_auto_skills"`
	MaxAutoSkillsBytes    int                 `yaml:"max_auto_skills_bytes" json:"max_auto_skills_bytes"`
	PerSkillMaxBytes      int                 `yaml:"per_skill_max_bytes" json:"per_skill_max_bytes"`
	SkipOnSessionContinue bool                `yaml:"skip_on_session_continue" json:"skip_on_session_continue"`
	CategoryMappings      map[string][]string `yaml:"category_mappings" json:"category_mappings"`
	KeywordPatterns       []KeywordPattern    `yaml:"keyword_patterns" json:"keyword_patterns"`
}

// DefaultSkillAutoLoaderConfig returns a config with baseline skills and sensible defaults.
//
// Expected:
//   - No arguments required.
//
// Returns:
//   - A SkillAutoLoaderConfig with baseline skills matching the canonical core-tier set
//
// Side effects:
//   - None.
func DefaultSkillAutoLoaderConfig() *SkillAutoLoaderConfig {
	return &SkillAutoLoaderConfig{
		BaselineSkills: []string{
			"pre-action",
			"memory-keeper",
			"token-cost-estimation",
			"retrospective",
			"note-taking",
			"knowledge-base",
			"discipline",
			"skill-discovery",
			"agent-discovery",
		},
		MaxAutoSkills:         6,
		MaxAutoSkillsBytes:    DefaultMaxAutoSkillsBytes,
		PerSkillMaxBytes:      DefaultPerSkillMaxBytes,
		SkipOnSessionContinue: false,
		CategoryMappings:      map[string][]string{},
		KeywordPatterns:       []KeywordPattern{},
	}
}

// LoadSkillAutoLoaderConfig reads a YAML config file, returning defaults if the file does not exist.
//
// Expected:
//   - path is the filesystem path to a YAML configuration file.
//
// Returns:
//   - A SkillAutoLoaderConfig parsed from the file, or the default config if the file is missing.
//   - An error if the file exists but cannot be read or contains invalid YAML.
//
// Side effects:
//   - Reads the file at path from disk.
func LoadSkillAutoLoaderConfig(path string) (*SkillAutoLoaderConfig, error) {
	cleanPath := filepath.Clean(path)
	if _, err := os.Stat(cleanPath); err != nil {
		if os.IsNotExist(err) {
			return DefaultSkillAutoLoaderConfig(), nil
		}
		return nil, err
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, err
	}
	cfg := DefaultSkillAutoLoaderConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
