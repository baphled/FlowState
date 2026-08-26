// Package mcp provides an MCP client for connecting to external MCP servers
// via the Model Context Protocol using stdio transport.
package mcp

import "context"

// Client defines the interface for connecting to and interacting with MCP servers.
type Client interface {
	// Connect establishes a connection to an MCP server.
	Connect(ctx context.Context, config ServerConfig) error

	// Disconnect closes the connection to a named server.
	Disconnect(serverName string) error

	// ListTools returns the tools available on a connected MCP server.
	ListTools(ctx context.Context, serverName string) ([]ToolInfo, error)

	// CallTool invokes a tool on a connected MCP server.
	CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*ToolResult, error)

	// DisconnectAll closes all server connections.
	DisconnectAll() error
}

// ServerConfig holds connection parameters for a single MCP server.
type ServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}
