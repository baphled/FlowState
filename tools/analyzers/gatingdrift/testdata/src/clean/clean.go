// Package clean exercises the gatingdrift analyser's negative path.
// The struct field's docstring names a gating identifier
// (Capabilities.MCPServers) AND the package reads it — so the analyser
// must NOT report any drift.
package clean

// Manifest mirrors the agent.Manifest shape just enough to exercise the
// docstring scanner.
type Manifest struct {
	Capabilities Capabilities
}

// Capabilities holds named gates that the engine consults.
type Capabilities struct {
	MCPServers []string
}

// Engine is the clean case: docstring matches impl.
type Engine struct {
	// MCPServerTools maps MCP server names to the tool names they expose.
	// Used by buildAllowedToolSet to auto-include tools from servers
	// declared in Capabilities.MCPServers without requiring agents to
	// list individual tool names.
	MCPServerTools map[string][]string

	manifest Manifest
}

// allowedTools returns the "allowed" tool set. The implementation
// consults manifest.Capabilities.MCPServers — the gating identifier
// referenced in MCPServerTools' docstring — so no drift exists.
func (e *Engine) allowedTools() map[string]bool {
	allowed := make(map[string]bool)
	for _, server := range e.manifest.Capabilities.MCPServers {
		for _, n := range e.MCPServerTools[server] {
			allowed[n] = true
		}
	}
	return allowed
}
