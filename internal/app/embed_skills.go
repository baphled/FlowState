// Package app provides the main application container and initialization.
package app

import (
	"embed"
	"io/fs"
)

// embeddedSkillsFS embeds the bundled skill manifests shipped with the
// binary. Mirrors the agentsFS embed in embed.go: SeedSkillsDir copies
// every embedded SKILL.md (and any sibling resources) into the
// configured cfg.SkillDir on startup, populating the loader's view of
// the world so engine.LoadAlwaysActiveSkills can deliver the four
// always-active skills (pre-action, discipline, skill-discovery,
// agent-discovery) along with the rest of the baseline.
//
// Each skill bundle is a subdirectory of skills/ containing SKILL.md.
// The embed pattern intentionally targets the SKILL.md file directly
// rather than the bundle directory tree because go:embed requires
// glob-explicit file paths — bundles that grow sibling resources
// (references, examples, scripts) need an additional pattern in this
// directive when they are introduced.
//
//go:embed skills/*/SKILL.md
var embeddedSkillsFS embed.FS

// EmbeddedSkillsFS returns the embedded fs.FS containing the bundled
// skill manifests.
//
// The embedded filesystem contains skill SKILL.md files that are
// compiled into the binary. SeedSkillsDir consumes this FS during
// `app.New` to materialise the bundles into the user's XDG_CONFIG
// skills directory the first time FlowState starts.
//
// Returns:
//   - An fs.FS rooted at the module root containing skills/<name>/SKILL.md.
//
// Side effects:
//   - None.
func EmbeddedSkillsFS() fs.FS {
	return embeddedSkillsFS
}
