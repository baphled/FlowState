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
	// ScopeBoth loads both; a name found in both is refused.
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
	// MaxModelChars is the longest model name a profile may set.
	MaxModelChars = 200
	// MaxDelegates is the most profile names a delegates list may hold.
	MaxDelegates = 32
	// MaxDescriptionChars is the longest description a profile may set.
	MaxDescriptionChars = 500
	// MaxContextWindow is the largest context_window, in tokens, a profile or the config may set.
	MaxContextWindow = 100_000_000
)

// Profile is one validated agent profile.
type Profile struct {
	// Name is the unique profile name, matching ^[a-z][a-z0-9-]{0,63}$.
	Name string `json:"name"`
	// Description says what the agent is for; it is never empty.
	Description string `json:"description"`
	// Tools is the allowlist of tool names; empty means the agent has no tools.
	Tools []string `json:"tools"`
	// Delegates lists the only profiles this profile's delegate tool may spawn:
	// each a valid profile name, at most MaxDelegates, no duplicate, never the
	// profile itself. It is non-empty only when Tools holds DelegateTool, and is
	// empty (never nil) when none is declared, which means nothing is allowed.
	// Cycles between profiles are allowed; the delegation depth cap bounds them.
	Delegates []string `json:"delegates"`
	// Model is a model name, or InheritModel for the parent's.
	Model string `json:"model"`
	// MaxTurns bounds the child's provider turns, 1..MaxTurnsLimit.
	MaxTurns int `json:"max_turns"`
	// ContextWindow is the model's context window in tokens (ADR-010, D3):
	// 1..MaxContextWindow, or 0 when the profile does not set one.
	ContextWindow int `json:"context_window"`
	// Source is the directory class the profile came from.
	Source Source `json:"source"`
	// Path is the file the profile was read from.
	Path string `json:"path"`
	// SHA256 is the hex digest of the file's bytes.
	SHA256 string `json:"sha256"`
	// Body is the system prompt: the non-empty text after the frontmatter.
	Body string `json:"-"`
}
