package config

import (
	"os"
	"os/exec"
	"path/filepath"
)

// memoryServerInstallSubpath is the path-relative-to-$HOME at which
// FlowState materialises the bundled mem0 MCP server wrapper. Kept in
// sync with internal/app.DefaultMemoryToolsDir — we cannot import the
// app package from config (the app package depends on config, so the
// reverse import would cycle), so the path is duplicated here as a
// constant. If either side changes, both must move together.
var memoryServerInstallSubpath = filepath.Join(".local", "share", "flowstate", "memory-tools", "mcp-mem0-server")

// DiscoverMCPServers auto-detects MCP servers available in PATH and returns
// their configurations. Checks for mcp-mem0-server, mcp-vault-server,
// and filesystem MCP via npx.
//
// Discovery for the mem0 MCP wrapper probes the FlowState install
// location (`~/.local/share/flowstate/memory-tools/mcp-mem0-server`)
// first, then falls back to PATH. The install location is where
// `flowstate memory-tools install` and the auto-materialise hook in
// app.New drop the bundled binary, so a fresh user with nothing on
// PATH still gets a working memory MCP. Operators who already have
// the binary on PATH continue to work unchanged.
//
// Returns:
//   - A slice of MCPServerConfig with Enabled=false for discovered servers.
//
// Side effects:
//   - None.
func DiscoverMCPServers() []MCPServerConfig {
	var servers []MCPServerConfig

	if path := findMemoryServer(); path != "" {
		servers = append(servers, MCPServerConfig{
			Name:    "memory",
			Command: path,
			Enabled: true,
		})
	}

	if path := findInPath("mcp-vault-server"); path != "" {
		servers = append(servers, MCPServerConfig{
			Name:    "vault-rag",
			Command: path,
			Enabled: true,
		})
	}

	if findInPath("npx") != "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "/tmp"
		}
		servers = append(servers, MCPServerConfig{
			Name:    "filesystem",
			Command: "npx",
			Args:    []string{"-y", "@modelcontextprotocol/server-filesystem", home},
			Enabled: true,
		})
	}

	return servers
}

// findMemoryServer locates the bundled mem0 MCP wrapper by probing the
// FlowState install location first, then falling back to PATH. The
// install-location-first order is load-bearing for the zero-config
// fresh-user contract: app.New auto-materialises the wrapper into the
// install location on first run, so PATH need never be touched.
//
// Returns:
//   - The absolute path to mcp-mem0-server when found, otherwise "".
//
// Side effects:
//   - Reads $HOME via os.UserHomeDir.
//   - Stats the install-location path; consults exec.LookPath as
//     a fallback.
func findMemoryServer() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		installPath := filepath.Join(home, memoryServerInstallSubpath)
		if info, statErr := os.Stat(installPath); statErr == nil && !info.IsDir() {
			return installPath
		}
	}
	return findInPath("mcp-mem0-server")
}

// findInPath searches for an executable in the system PATH.
//
// Expected:
//   - name must be a non-empty executable name.
//
// Returns:
//   - The full path to the executable, or empty string if not found.
//
// Side effects:
//   - None.
func findInPath(name string) string {
	if path := lookPath(name); path != "" {
		return path
	}
	return ""
}

// lookPath wraps exec.LookPath for testing purposes.
//
// Expected:
//   - name must be a non-empty executable name.
//
// Returns:
//   - The full path to the executable, or empty string if not found.
//
// Side effects:
//   - None.
func lookPath(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	return ""
}
