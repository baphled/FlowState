package prompt

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// FrontmatterMetadata holds parsed YAML frontmatter from prompts.
//
// This structure is extracted from markdown prompt files and provides metadata
// about agents, their capabilities, and delegation configuration.
type FrontmatterMetadata struct {
	ID                  string   `yaml:"id"`
	Name                string   `yaml:"name"`
	Role                string   `yaml:"role"`
	Goal                string   `yaml:"goal"`
	WhenToUse           string   `yaml:"when_to_use"`
	Complexity          string   `yaml:"complexity"`
	AlwaysActiveSkills  []string `yaml:"always_active_skills"`
	Tools               []string `yaml:"tools"`
	CanDelegate         bool     `yaml:"can_delegate"`
	DelegationAllowlist []string `yaml:"delegation_allowlist"`
}

// ParseFrontmatter extracts YAML frontmatter from markdown content.
//
// Expected:
//   - content may or may not start with "---\n".
//   - If frontmatter is present, it is bounded by "---\n" delimiters.
//
// Returns:
//   - (metadata, contentWithoutFrontmatter, error)
//   - If no frontmatter is found, returns (nil, originalContent, nil) safely.
//   - If YAML parsing fails, returns (nil, originalContent, error).
//
// Side effects:
//   - None.
func ParseFrontmatter(content string) (*FrontmatterMetadata, string, error) {
	if !strings.HasPrefix(content, "---\n") {
		// No frontmatter, return original content unchanged
		return nil, content, nil
	}

	// Find closing delimiter
	remaining := content[4:] // Skip opening "---\n"
	idx := strings.Index(remaining, "\n---\n")
	if idx == -1 {
		// Malformed: opening --- but no closing ---
		// Return original content unchanged (graceful degradation)
		return nil, content, nil
	}

	yamlBlock := remaining[:idx]
	contentAfter := remaining[idx+5:] // Skip "\n---\n"

	// Parse YAML
	var meta FrontmatterMetadata
	if err := yaml.Unmarshal([]byte(yamlBlock), &meta); err != nil {
		return nil, content, fmt.Errorf("parsing frontmatter YAML: %w", err)
	}

	return &meta, contentAfter, nil
}
