package app

import (
	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
)

// SetupAgentRegistryForTest is a test helper that exposes setupAgentRegistry for
// BDD and integration tests outside this package.
//
// Expected:
//   - cfg is a non-nil AppConfig with AgentDir and optional AgentDirs configured.
//
// Returns:
//   - A populated agent.Registry built using the layered discovery rules.
//
// Side effects:
//   - Reads agent manifest files from the configured directories.
func SetupAgentRegistryForTest(cfg *config.AppConfig) *agent.Registry {
	return setupAgentRegistry(cfg)
}

// NewSwarmAgentRegistryAdapterForTest constructs the unexported
// swarmAgentRegistryAdapter for tests outside this package so they can
// exercise the alias-aware presence check the swarm validator depends on.
//
// Expected:
//   - inner is the agent.Registry the adapter should wrap. nil is allowed
//     and produces an adapter whose Get always returns false.
//
// Returns:
//   - A swarm.AgentRegistry-compatible value that delegates Get to
//     agent.Registry.GetByNameOrAlias.
//
// Side effects:
//   - None.
func NewSwarmAgentRegistryAdapterForTest(inner *agent.Registry) interface {
	Get(id string) (any, bool)
} {
	return swarmAgentRegistryAdapter{inner: inner}
}
