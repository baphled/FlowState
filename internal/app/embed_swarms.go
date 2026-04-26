// Package app provides the main application container and initialization.
package app

import (
	"embed"
	"io/fs"
)

// embeddedSwarmsFS embeds the bundled swarm manifests shipped with the
// binary. Mirrors the agentsFS embed in embed.go: SeedSwarmsDir copies
// these files into the user's `~/.config/flowstate/swarms/` directory
// on first run so the canonical schema examples (`planning-loop.yml`
// and `solo.yml`) are always present without forcing the user to
// hand-author a manifest before they have one to copy from.
//
//go:embed swarms/*.yml
var embeddedSwarmsFS embed.FS

// EmbeddedSwarmsFS returns the embedded fs.FS containing the bundled
// swarm manifests rooted at the module level (so the entries appear
// under "swarms/" inside the returned FS, matching how
// EmbeddedAgentsFS exposes "agents/").
//
// Returns:
//   - An fs.FS rooted at the module root containing swarms/*.yml.
//
// Side effects:
//   - None.
func EmbeddedSwarmsFS() fs.FS {
	return embeddedSwarmsFS
}
