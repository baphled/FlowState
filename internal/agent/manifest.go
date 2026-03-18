package agent

// Manifest defines the complete configuration for a FlowState agent.
type Manifest struct {
	SchemaVersion     string                 `json:"schema_version" yaml:"schema_version"`
	ID                string                 `json:"id" yaml:"id"`
	Name              string                 `json:"name" yaml:"name"`
	Complexity        string                 `json:"complexity" yaml:"complexity"`
	ModelPreferences  map[string][]ModelPref `json:"model_preferences" yaml:"model_preferences"`
	Metadata          Metadata               `json:"metadata" yaml:"metadata"`
	Capabilities      Capabilities           `json:"capabilities" yaml:"capabilities"`
	ContextManagement ContextManagement      `json:"context_management" yaml:"context_management"`
	Delegation        Delegation             `json:"delegation" yaml:"delegation"`
	Hooks             Hooks                  `json:"hooks" yaml:"hooks"`
	Instructions      Instructions           `json:"instructions" yaml:"instructions"`
}

// ModelPref specifies a preferred model for a provider.
type ModelPref struct {
	Provider string `json:"provider" yaml:"provider"`
	Model    string `json:"model" yaml:"model"`
}

// Metadata contains descriptive information about an agent.
type Metadata struct {
	Role      string `json:"role" yaml:"role"`
	Goal      string `json:"goal" yaml:"goal"`
	WhenToUse string `json:"when_to_use" yaml:"when_to_use"`
}

// Capabilities defines the tools and skills available to an agent.
type Capabilities struct {
	Tools              []string `json:"tools" yaml:"tools"`
	Skills             []string `json:"skills" yaml:"skills"`
	AlwaysActiveSkills []string `json:"always_active_skills" yaml:"always_active_skills"`
	MCPServers         []string `json:"mcp_servers" yaml:"mcp_servers"`
}

// ContextManagement configures how an agent manages conversation context.
type ContextManagement struct {
	MaxRecursionDepth   int     `json:"max_recursion_depth" yaml:"max_recursion_depth"`
	SummaryTier         string  `json:"summary_tier" yaml:"summary_tier"`
	SlidingWindowSize   int     `json:"sliding_window_size" yaml:"sliding_window_size"`
	CompactionThreshold float64 `json:"compaction_threshold" yaml:"compaction_threshold"`
	EmbeddingModel      string  `json:"embedding_model" yaml:"embedding_model"`
}

// Delegation configures whether and how an agent can delegate tasks.
type Delegation struct {
	CanDelegate     bool              `json:"can_delegate" yaml:"can_delegate"`
	DelegationTable map[string]string `json:"delegation_table" yaml:"delegation_table"`
}

// Hooks defines pre and post execution hooks for an agent.
type Hooks struct {
	Before []string `json:"before" yaml:"before"`
	After  []string `json:"after" yaml:"after"`
}

// Instructions contains system prompts for an agent.
type Instructions struct {
	SystemPrompt string `json:"system_prompt" yaml:"system_prompt"`
}

// DefaultContextManagement returns sensible default context management settings.
//
// Returns:
//   - A ContextManagement struct with default values for all fields.
//
// Side effects:
//   - None.
func DefaultContextManagement() ContextManagement {
	return ContextManagement{
		MaxRecursionDepth:   2,
		SummaryTier:         "quick",
		SlidingWindowSize:   10,
		CompactionThreshold: 0.75,
		EmbeddingModel:      "nomic-embed-text",
	}
}

// Validate checks that the manifest has required fields.
//
// Returns:
//   - nil if the manifest is valid.
//   - A ValidationError if required fields are missing.
//
// Side effects:
//   - None.
func (m *Manifest) Validate() error {
	if m.ID == "" {
		return &ValidationError{Field: "id", Message: "required"}
	}
	if m.Name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	return nil
}

// ValidationError represents a manifest validation failure.
type ValidationError struct {
	Field   string
	Message string
}

// Error returns the validation error message.
//
// Returns:
//   - A string describing the validation failure.
//
// Side effects:
//   - None.
func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}
