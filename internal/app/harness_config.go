package app

// HarnessConfig holds configuration for the planning harness.
//
// Each field controls an optional layer of the harness. All layers
// default to disabled to preserve backward compatibility.
type HarnessConfig struct {
	// Enabled controls whether the harness is active at all.
	Enabled bool
	// ProjectRoot is the absolute path used by validators and grounders.
	ProjectRoot string
	// CriticEnabled controls whether the LLM critic runs after Go validators.
	CriticEnabled bool
	// VotingEnabled controls whether self-consistency voting is active.
	VotingEnabled bool
}
