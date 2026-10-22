package prompt

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
