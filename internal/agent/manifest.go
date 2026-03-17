package agent

type AgentManifest struct {
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

type ModelPref struct {
	Provider string `json:"provider" yaml:"provider"`
	Model    string `json:"model" yaml:"model"`
}

type Metadata struct {
	Role      string `json:"role" yaml:"role"`
	Goal      string `json:"goal" yaml:"goal"`
	WhenToUse string `json:"when_to_use" yaml:"when_to_use"`
}

type Capabilities struct {
	Tools              []string `json:"tools" yaml:"tools"`
	Skills             []string `json:"skills" yaml:"skills"`
	AlwaysActiveSkills []string `json:"always_active_skills" yaml:"always_active_skills"`
	MCPServers         []string `json:"mcp_servers" yaml:"mcp_servers"`
}

type ContextManagement struct {
	MaxRecursionDepth   int     `json:"max_recursion_depth" yaml:"max_recursion_depth"`
	SummaryTier         string  `json:"summary_tier" yaml:"summary_tier"`
	SlidingWindowSize   int     `json:"sliding_window_size" yaml:"sliding_window_size"`
	CompactionThreshold float64 `json:"compaction_threshold" yaml:"compaction_threshold"`
	EmbeddingModel      string  `json:"embedding_model" yaml:"embedding_model"`
}

type Delegation struct {
	CanDelegate     bool              `json:"can_delegate" yaml:"can_delegate"`
	DelegationTable map[string]string `json:"delegation_table" yaml:"delegation_table"`
}

type Hooks struct {
	Before []string `json:"before" yaml:"before"`
	After  []string `json:"after" yaml:"after"`
}

type Instructions struct {
	SystemPrompt string `json:"system_prompt" yaml:"system_prompt"`
}

func DefaultContextManagement() ContextManagement {
	return ContextManagement{
		MaxRecursionDepth:   2,
		SummaryTier:         "quick",
		SlidingWindowSize:   10,
		CompactionThreshold: 0.75,
		EmbeddingModel:      "nomic-embed-text",
	}
}

func (m *AgentManifest) Validate() error {
	if m.ID == "" {
		return &ValidationError{Field: "id", Message: "required"}
	}
	if m.Name == "" {
		return &ValidationError{Field: "name", Message: "required"}
	}
	return nil
}

type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}
