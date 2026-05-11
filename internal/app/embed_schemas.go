// Package app provides the main application container and initialization.
package app

import (
	"embed"
	"io/fs"
)

// embeddedSchemasFS embeds bundled JSON Schemas used by shipped swarm
// manifests. SeedSchemasDir copies these into the user's
// `~/.config/flowstate/schemas/` directory on startup so a fresh
// deployment can validate result-schema gates without manual file
// installation.
//
//go:embed schemas/*.json
var embeddedSchemasFS embed.FS

// EmbeddedSchemasFS returns the embedded fs.FS containing bundled swarm
// JSON Schemas rooted at the module level.
//
// Returns:
//   - An fs.FS rooted at the module root containing schemas/*.json.
//
// Side effects:
//   - None.
func EmbeddedSchemasFS() fs.FS {
	return embeddedSchemasFS
}
