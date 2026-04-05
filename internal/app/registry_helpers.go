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
