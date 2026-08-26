package app_test

import (
	"io/fs"

	"github.com/baphled/flowstate/internal/app"
)

// embeddedAgentCount returns the number of bundled agent manifests under the
// embedded `agents/` directory. Tests use this so assertions track the
// embedded fixture size automatically rather than encoding a stale literal
// every time an agent is added or removed.
func embeddedAgentCount() int {
	embeddedFS := app.EmbeddedAgentsFS()
	agentsDir, err := fs.Sub(embeddedFS, "agents")
	if err != nil {
		return 0
	}
	entries, err := fs.ReadDir(agentsDir, ".")
	if err != nil {
		return 0
	}
	return len(entries)
}
