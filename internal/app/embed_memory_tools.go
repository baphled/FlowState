// Package app provides the main application container and initialization.
package app

import (
	"embed"
	"io/fs"
)

// embeddedMemoryToolsFS embeds the bundled mem0-compatible MCP server
// shipped with the FlowState binary. The payload is the same artefact that
// historically lived at `~/.config/opencode/plugins/lib/dist/mcp-mem0-server.js`
// in the dotopencode repo, plus a portable bash wrapper, so a fresh
// machine can drive the FlowState memory MCP without cloning a second
// repo.
//
// The wildcard pattern accepts every regular file in the directory; today
// that is the bundled JavaScript server and its bash wrapper. Both files
// are extensionless from the embed pattern's perspective — `.js` is a
// content extension, not a marker — so the embed contract matches
// embed_vault_tools.go's "every entry is a payload script" shape.
//
//go:embed memory_tools/*
var embeddedMemoryToolsFS embed.FS

// EmbeddedMemoryToolsFS returns the embedded fs.FS containing the
// bundled memory-tool payload rooted at the module level (entries appear
// under "memory_tools/" inside the returned FS, matching how
// EmbeddedVaultToolsFS exposes "vault_tools/").
//
// Returns:
//   - An fs.FS rooted at the module root containing memory_tools/*.
//
// Side effects:
//   - None.
func EmbeddedMemoryToolsFS() fs.FS {
	return embeddedMemoryToolsFS
}
