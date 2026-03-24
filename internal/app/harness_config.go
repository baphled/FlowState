package app

// HarnessConfig holds configuration for the planning harness.
//
// Each field controls an optional layer of the harness. All layers
// default to disabled to preserve backward compatibility.
type HarnessConfig struct {
	// Enabled controls whether the harness is active at all.
	Enabled bool `yaml:"enabled"`
	// ProjectRoot is the absolute path used by validators and grounders.
	ProjectRoot string `yaml:"project_root"`
	// CriticEnabled controls whether the LLM critic runs after Go validators.
	CriticEnabled bool `yaml:"critic_enabled"`
	// VotingEnabled controls whether self-consistency voting is active.
	VotingEnabled bool `yaml:"voting_enabled"`
}
