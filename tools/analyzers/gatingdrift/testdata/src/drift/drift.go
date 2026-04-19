// Package drift exercises the gatingdrift analyser's positive-detection
// path. The struct field's docstring names a gating identifier
// (Capabilities.MCPServers) but the package never references that
// identifier — so the analyser must report the drift.
package drift

// Manifest mirrors the agent.Manifest shape just enough to exercise the
// docstring scanner.
type Manifest struct {
	Capabilities Capabilities
}

// Capabilities holds named gates that the engine consults.
type Capabilities struct {
	MCPServers []string
}

// Engine carries the buggy gating-drift case.
type Engine struct { // want `Engine.MCPServerTools docstring names gating identifier "Capabilities.MCPServers" but the enclosing package never reads it`
	// MCPServerTools maps MCP server names to the tool names they expose.
	// Used by buildAllowedToolSet to auto-include tools from servers
	// declared in Capabilities.MCPServers without requiring agents to
	// list individual tool names.
	MCPServerTools map[string][]string
}

// allowedTools returns the "allowed" tool set. NOTE: this implementation
// does NOT consult Capabilities.MCPServers — the gating identifier
// referenced in MCPServerTools' docstring — which is exactly the drift
// the analyser must catch.
func (e *Engine) allowedTools() map[string]bool {
	allowed := make(map[string]bool)
	for _, names := range e.MCPServerTools {
		for _, n := range names {
			allowed[n] = true
		}
	}
	return allowed
}
