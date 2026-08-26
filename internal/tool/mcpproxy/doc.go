// Package mcpproxy adapts MCP server tools to the internal tool.Tool interface.
//
// Each Proxy wraps a single MCP tool discovered via ListTools and delegates
// Execute calls to the MCP server through the mcp.Client interface. This
// allows MCP tools to participate in the engine's tool dispatch alongside
// built-in tools like bash, file, and web.
//
// Usage:
//
//	proxy := mcpproxy.NewProxy(client, "server-name", toolInfo)
//	result, err := proxy.Execute(ctx, tool.Input{Arguments: args})
package mcpproxy
