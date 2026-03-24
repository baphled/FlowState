package app

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultHarnessConfig returns a HarnessConfig populated with sensible defaults.
//
// Returns:
//   - A HarnessConfig with Enabled=true and the current working directory as ProjectRoot.
//
// Side effects:
//   - Calls os.Getwd to determine the project root directory.
func DefaultHarnessConfig() HarnessConfig {
	projectRoot, err := os.Getwd()
	if err != nil {
		projectRoot = "."
	}
	return HarnessConfig{
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
