// Package app provides the main application container and initialization.
package app

import (
	"embed"
	"io/fs"
)

//go:embed agents/*.json
var agentsFS embed.FS

// EmbeddedAgentsFS returns the embedded fs.FS containing bundled agent manifests.
//
// The embedded filesystem contains agent JSON manifests that are compiled into the binary.
//
// Returns:
//   - An fs.FS rooted at the module root containing agents/*.json
//
// Side effects:
//   - None.
func EmbeddedAgentsFS() fs.FS {
	return agentsFS
}
