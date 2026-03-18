// Package mcp provides a client manager for MCP (Model Context Protocol) server connections.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// ToolInfo describes a tool available on an MCP server.
type ToolInfo struct {
	Name        string
	Description string
	InputSchema map[string]interface{}
}

// ToolResult holds the output from a tool call.
type ToolResult struct {
	Content string
	IsError bool
}

// ServerConnection tracks a connected MCP server.
type ServerConnection struct {
	Name    string
	Command string
	Args    []string
}

// Manager manages MCP server connections.
type Manager struct {
	mu      sync.RWMutex
	servers map[string]*ServerConnection
}

// NewManager creates a new MCP server connection manager.
//
// Returns:
//   - An initialised Manager with no connected servers.
//
// Side effects:
//   - None.
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*ServerConnection),
	}
}

// Connect establishes a connection to an MCP server.
//
// Expected:
//   - name is a unique identifier for the server.
//   - command is the executable to run the MCP server.
//   - args is the list of arguments for the command.
//
// Returns:
//   - An error if a server with the same name is already connected.
//
// Side effects:
//   - Registers the server connection in the manager.
func (m *Manager) Connect(_ context.Context, name string, command string, args []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[name]; exists {
		return fmt.Errorf("server %q already connected", name)
	}

	m.servers[name] = &ServerConnection{
		Name:    name,
		Command: command,
		Args:    args,
	}

	return nil
}

// DiscoverTools returns the tools available on a connected MCP server.
//
// Expected:
//   - name is the identifier of a connected MCP server.
//
// Returns:
//   - A slice of ToolInfo describing available tools.
//   - An error if the named server is not found.
//
// Side effects:
//   - None.
func (m *Manager) DiscoverTools(_ context.Context, name string) ([]ToolInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, exists := m.servers[name]; !exists {
		return nil, errors.New("server not found")
	}

	return []ToolInfo{}, nil
}

// CallTool invokes a tool on a connected MCP server.
//
// Expected:
//   - serverName is the identifier of a connected MCP server.
//
// Returns:
//   - A ToolResult containing the tool's output.
//   - An error if the named server is not found.
//
// Side effects:
//   - None (stub implementation).
func (m *Manager) CallTool(_ context.Context, serverName, _ string, _ map[string]interface{}) (*ToolResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, exists := m.servers[serverName]; !exists {
		return nil, errors.New("server not found")
	}

	return &ToolResult{}, nil
}

// Disconnect removes a server connection from the manager.
//
// Expected:
//   - name is the identifier of the server to disconnect.
//
// Returns:
//   - An error if the named server is not found.
//
// Side effects:
//   - Removes the server from the manager's connection map.
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.servers[name]; !exists {
		return errors.New("server not found")
	}

	delete(m.servers, name)
	return nil
}

// ListServers returns a sorted slice of connected server names.
//
// Returns:
//   - A lexicographically sorted slice of server name strings.
//
// Side effects:
//   - None.
func (m *Manager) ListServers() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	names := make([]string, 0, len(m.servers))
	for name := range m.servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
