// Package agent loads and validates agent profiles: markdown files with YAML
// frontmatter whose body is a child's system prompt (ADR-009). The package is
// pure and filesystem-only; the application layer supplies the tool catalog
// and the directories.
package agent

// Source names where a profile was found.
type Source string

// Scope selects which profile directories a load reads.
type Scope string

// Profile source and scope values, and the limits a profile must respect.
const (
	// SourceUser marks a profile from the per-user configuration directory.
	SourceUser Source = "user"
	// SourceProject marks a profile from the working directory's .flowstate/agents.
	SourceProject Source = "project"

	// ScopeUser loads only user profiles; it is the default.
	ScopeUser Scope = "user"
	// ScopeProject loads only project profiles.
	ScopeProject Scope = "project"
	// ScopeBoth loads both; a project profile overrides a user profile of the same name.
	ScopeBoth Scope = "both"

	// DelegateTool is the reserved tool name accepted before the delegate tool exists.
	DelegateTool = "delegate"
	// InheritModel is the model value (and default) that means the parent's model.
	InheritModel = "inherit"
	// DefaultMaxTurns applies when a profile omits max_turns.
	DefaultMaxTurns = 25
	// MaxTurnsLimit is the largest max_turns a profile may set.
	MaxTurnsLimit = 100
	// MaxFileBytes is the largest profile file accepted.
	MaxFileBytes = 64 << 10
)

// Profile is one validated agent profile.
type Profile struct {
	// Name is the unique profile name, matching ^[a-z][a-z0-9-]{0,63}$.
	Name string `json:"name"`
	// Description says what the agent is for; it is never empty.
	Description string `json:"description"`
	// Tools is the allowlist of tool names; empty means the agent has no tools.
	Tools []string `json:"tools"`
	// Model is a model name, or InheritModel for the parent's.
	Model string `json:"model"`
	// MaxTurns bounds the child's provider turns, 1..MaxTurnsLimit.
	MaxTurns int `json:"max_turns"`
	// Source is the directory class the profile came from.
	Source Source `json:"source"`
	// Path is the file the profile was read from.
	Path string `json:"path"`
	// SHA256 is the hex digest of the file's bytes.
	SHA256 string `json:"sha256"`
	// Overrides is SourceUser when this project profile replaced a user profile of the same name.
	Overrides Source `json:"overrides,omitempty"`
	// Body is the system prompt: the non-empty text after the frontmatter.
	Body string `json:"-"`
}

// frontmatter is the strict YAML shape of a profile header.
type frontmatter struct {
	Name        *string  `yaml:"name"`
	Description *string  `yaml:"description"`
	Tools       []string `yaml:"tools"`
	Model       *string  `yaml:"model"`
	MaxTurns    *int     `yaml:"max_turns"`
}
