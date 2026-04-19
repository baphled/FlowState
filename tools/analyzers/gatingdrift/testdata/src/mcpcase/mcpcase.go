// Package mcpcase is a cherry-revert reproduction of commit b960869
// ("wire MCP tools to bypass manifest whitelist") in miniature. The
// docstring still claims the field is gated by Capabilities.MCPServers
// but the impl iterates the local map without consulting the manifest
// — exactly the bug the gatingdrift analyser exists to catch.
package mcpcase

// Manifest, Capabilities exist only to give the docstring's gating
// identifier a real Go declaration to refer to.
type Manifest struct {
	Capabilities Capabilities
}

// Capabilities holds named gates the engine claims to consult.
type Capabilities struct {
	MCPServers []string
}

// Engine reproduces the buggy state: the docstring still names the gate
// but the buildAllowedToolSet impl no longer reads it.
type Engine struct { // want `Engine.MCPServerTools docstring names gating identifier "Capabilities.MCPServers" but the enclosing package never reads it`
	// MCPServerTools maps MCP server names to the tool names they expose.
	// Used by buildAllowedToolSet to auto-include tools from servers
	// declared in Capabilities.MCPServers without requiring agents to
	// list individual tool names.
	MCPServerTools map[string][]string

	manifest Manifest
}

// buildAllowedToolSet is the b960869 buggy version — the iteration no
// longer touches manifest.Capabilities.MCPServers, so the docstring is
// stale and the manifest gate is silently bypassed.
func (e *Engine) buildAllowedToolSet() map[string]bool {
	allowed := make(map[string]bool)
	for _, toolNames := range e.MCPServerTools {
		for _, name := range toolNames {
			allowed[name] = true
		}
	}
	return allowed
}
