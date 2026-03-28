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
	HarnessEnabled    bool                   `json:"harness_enabled" yaml:"harness_enabled"`
	// Harness defines fine-grained output validation and quality layers for this agent.
	// When present, it takes precedence over the legacy HarnessEnabled boolean.
	Harness *HarnessConfig `json:"harness,omitempty" yaml:"harness,omitempty"`
	// Loop defines the delegation loop for coordinator agents.
	// When present, the agent operates in review-cycle mode rather than single-shot mode.
	Loop *LoopConfig `json:"loop,omitempty" yaml:"loop,omitempty"`
	// OrchestratorMeta describes how orchestrators should reference and invoke this agent.
	OrchestratorMeta OrchestratorMetadata `json:"orchestrator_meta" yaml:"orchestrator_meta"`
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
	Tools                 []string `json:"tools" yaml:"tools"`
	Skills                []string `json:"skills" yaml:"skills"`
	AlwaysActiveSkills    []string `json:"always_active_skills" yaml:"always_active_skills"`
	MCPServers            []string `json:"mcp_servers" yaml:"mcp_servers"`
	CapabilityDescription string   `json:"capability_description" yaml:"capability_description"`
}

// ContextManagement configures how an agent manages conversation context.
type ContextManagement struct {
	MaxRecursionDepth   int     `json:"max_recursion_depth" yaml:"max_recursion_depth"`
	SummaryTier         string  `json:"summary_tier" yaml:"summary_tier"`
	SlidingWindowSize   int     `json:"sliding_window_size" yaml:"sliding_window_size"`
	CompactionThreshold float64 `json:"compaction_threshold" yaml:"compaction_threshold"`
	EmbeddingModel      string  `json:"embedding_model" yaml:"embedding_model"`
}

// DelegationTrigger describes when an orchestrator should delegate to this agent.
type DelegationTrigger struct {
	Domain  string `json:"domain" yaml:"domain"`
	Trigger string `json:"trigger" yaml:"trigger"`
}

// OrchestratorMetadata describes how orchestrators should reference and invoke this agent.
// These fields are consumed by dynamic section builders to compose orchestrator prompts.
type OrchestratorMetadata struct {
	Cost        string              `json:"cost" yaml:"cost"`
	Category    string              `json:"category" yaml:"category"`
	Triggers    []DelegationTrigger `json:"triggers" yaml:"triggers"`
	UseWhen     []string            `json:"use_when" yaml:"use_when"`
	AvoidWhen   []string            `json:"avoid_when" yaml:"avoid_when"`
	PromptAlias string              `json:"prompt_alias" yaml:"prompt_alias"`
	KeyTrigger  string              `json:"key_trigger" yaml:"key_trigger"`
}

// Delegation configures whether and how an agent can delegate tasks.
type Delegation struct {
	CanDelegate         bool              `json:"can_delegate" yaml:"can_delegate"`
	DelegationTable     map[string]string `json:"delegation_table" yaml:"delegation_table"`
	DelegationAllowlist []string          `json:"delegation_allowlist" yaml:"delegation_allowlist"`
}

// HarnessConfig defines the output validation and quality layers for an agent.
// When nil, the legacy HarnessEnabled boolean is used as a fallback.
type HarnessConfig struct {
	Enabled       bool     `json:"enabled" yaml:"enabled"`
	Validators    []string `json:"validators,omitempty" yaml:"validators,omitempty"`
	CriticEnabled bool     `json:"critic_enabled" yaml:"critic_enabled"`
	VotingEnabled bool     `json:"voting_enabled" yaml:"voting_enabled"`
	MaxAttempts   int      `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
}

// LoopConfig defines the delegation loop for coordinator agents.
// When nil, the agent operates in single-shot mode without a review cycle.
type LoopConfig struct {
	Enabled     bool              `json:"enabled" yaml:"enabled"`
	Writer      string            `json:"writer,omitempty" yaml:"writer,omitempty"`
	Reviewer    string            `json:"reviewer,omitempty" yaml:"reviewer,omitempty"`
	MaxAttempts int               `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`
	Roles       map[string]string `json:"roles,omitempty" yaml:"roles,omitempty"`
}

// Hooks defines pre and post execution hooks for an agent.
type Hooks struct {
	Before []string `json:"before" yaml:"before"`
	After  []string `json:"after" yaml:"after"`
}

// Instructions contains system prompts for an agent.
type Instructions struct {
	SystemPrompt         string `json:"system_prompt" yaml:"system_prompt"`
	StructuredPromptFile string `json:"structured_prompt_file" yaml:"structured_prompt_file"`
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
