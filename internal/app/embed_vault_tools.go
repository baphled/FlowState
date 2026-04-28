// Package app provides the main application container and initialization.
package app

import (
	"embed"
	"io/fs"
)

// embeddedVaultToolsFS embeds the bundled Qdrant + LlamaIndex helper
// scripts shipped with the binary. They mirror the historical
// dotopencode payload at `~/.config/opencode/scripts/{sync-vault,
// query-vault,mcp-vault-server}` so a fresh machine can be bootstrapped
// from the FlowState binary alone, without a separate dotopencode
// clone.
//
// The scripts have no file extension because the original sources are
// extensionless executables. The wildcard pattern accepts every regular
// file in the directory; today that is exactly the three scripts above.
//
//go:embed vault_tools/*
var embeddedVaultToolsFS embed.FS

// EmbeddedVaultToolsFS returns the embedded fs.FS containing the
// bundled vault-tool scripts rooted at the module level (entries appear
// under "vault_tools/" inside the returned FS, matching how
// EmbeddedAgentsFS exposes "agents/" and EmbeddedSwarmsFS exposes
// "swarms/").
//
// Returns:
//   - An fs.FS rooted at the module root containing vault_tools/*.
//
// Side effects:
//   - None.
func EmbeddedVaultToolsFS() fs.FS {
	return embeddedVaultToolsFS
}
