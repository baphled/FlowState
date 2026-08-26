package app

import (
	"fmt"
	"os"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"gopkg.in/yaml.v3"
)

// DefaultHarnessConfig returns a config.HarnessConfig populated with sensible defaults.
//
// Returns:
//   - A config.HarnessConfig with Enabled=true and the current working directory as ProjectRoot.
//
// Side effects:
//   - Calls os.Getwd to determine the project root directory.
func DefaultHarnessConfig() config.HarnessConfig {
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = "."
	}
	return config.HarnessConfig{
		Enabled:       true,
		ProjectRoot:   projectRoot,
		CriticEnabled: false,
		VotingEnabled: false,
	}
}

// HarnessConfigYAML returns the default harness configuration as a YAML string.
//
// Returns:
//   - A YAML-encoded string of the default HarnessConfig and nil on success.
//   - An empty string and error if marshalling fails.
//
// Side effects:
//   - Calls DefaultHarnessConfig which calls os.Getwd.
func HarnessConfigYAML() (string, error) {
	cfg := DefaultHarnessConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshalling harness config: %w", err)
	}
	return string(data), nil
}

// PlanReviewerAgentID is the agent ID for the plan reviewer agent.
const PlanReviewerAgentID = "plan-reviewer"

// DefaultHarnessConfigForAgent returns a HarnessConfig for the given agent manifest,
// accounting for whether the plan-reviewer agent is in the delegation table.
//
// When the delegation table contains plan-reviewer, the LLMCritic is disabled because
// the reviewer agent performs quality and feasibility review. The SchemaValidator
// remains active regardless, as it checks structural requirements.
//
// Returns:
//   - A HarnessConfig with Enabled=true, ProjectRoot set to the current working directory,
//     CriticEnabled=true if no reviewer is present, and VotingEnabled=false.
//
// Expected:
//   - The returned configuration enables the harness and toggles critic behaviour based on delegation state.
//
// Side effects:
//   - Calls os.Getwd to determine the project root directory.
func DefaultHarnessConfigForAgent(manifest agent.Manifest) config.HarnessConfig {
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = "."
	}

	criticEnabled := !hasReviewerInDelegation(manifest)

	return config.HarnessConfig{
		Enabled:       true,
		ProjectRoot:   projectRoot,
		CriticEnabled: criticEnabled,
		VotingEnabled: false,
	}
}

// hasReviewerInDelegation checks if the plan-reviewer agent is in the delegation allowlist.
//
// Returns:
//   - True if the delegation allowlist contains plan-reviewer as a target.
//   - False if delegation is disabled, allowlist is empty, or reviewer is not present.
//
// Expected:
//   - The helper returns a boolean that reflects whether plan-reviewer appears in the delegation targets.
//
// Side effects:
//   - None.
func hasReviewerInDelegation(manifest agent.Manifest) bool {
	if !manifest.Delegation.CanDelegate {
		return false
	}
	for _, id := range manifest.Delegation.DelegationAllowlist {
		if id == PlanReviewerAgentID {
			return true
		}
	}
	return false
}
