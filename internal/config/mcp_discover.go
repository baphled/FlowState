package config

import (
	"os"
	"os/exec"
)

// DiscoverMCPServers auto-detects MCP servers available in PATH and returns
// their configurations. Checks for mcp-mem0-server, mcp-vault-server,
// and filesystem MCP via npx.
//
// Returns:
//   - A slice of MCPServerConfig with Enabled=false for discovered servers.
//
// Side effects:
//   - None.
func DiscoverMCPServers() []MCPServerConfig {
	var servers []MCPServerConfig

	if path := findInPath("mcp-mem0-server"); path != "" {
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
