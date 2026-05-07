// Package app provides the main application container and initialization.
package app

import (
	"embed"
	"io/fs"
)

// embeddedGatesFS embeds the bundled v0 ext-gate bundles shipped with
// the binary. Mirrors the agentsFS / embeddedSwarmsFS / embeddedSkillsFS
// embeds: SeedGatesDir copies each bundle into the user's
// `~/.config/flowstate/gates/` directory on first run so the canonical
// example gate (`relevance-gate`) is always present without forcing the
// user to hand-author a manifest plus exec before they have one to
// copy from.
//
// Gate bundles are directories — manifest.yml plus an executable
// (gate.py / gate.sh / gate binary). Like skill bundles, the embed
// pattern lists the explicit file extensions that ship inside each
// bundle so go:embed picks up only the intended files; new file types
// (e.g. shared Python helpers, templates) need an additional pattern
// in this directive when they are introduced.
//
//go:embed gates/*/manifest.yml gates/*/gate.py
var embeddedGatesFS embed.FS

// EmbeddedGatesFS returns the embedded fs.FS containing the bundled
// gate manifests + their exec files rooted at the module level (so
// the entries appear under "gates/<name>/" inside the returned FS,
// matching how EmbeddedAgentsFS exposes "agents/" and EmbeddedSwarmsFS
// exposes "swarms/").
//
// Returns:
//   - An fs.FS rooted at the module root containing
//     gates/<name>/{manifest.yml,gate.py}.
//
// Side effects:
//   - None.
func EmbeddedGatesFS() fs.FS {
	return embeddedGatesFS
}
