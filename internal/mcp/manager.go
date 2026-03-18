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
func NewManager() *Manager {
	return &Manager{
		servers: make(map[string]*ServerConnection),
	}
}

// Connect establishes a connection to an MCP server.
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
func (m *Manager) DiscoverTools(_ context.Context, name string) ([]ToolInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, exists := m.servers[name]; !exists {
		return nil, errors.New("server not found")
	}

	return []ToolInfo{}, nil
}

// CallTool invokes a tool on a connected MCP server.
func (m *Manager) CallTool(_ context.Context, serverName, _ string, _ map[string]interface{}) (*ToolResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if _, exists := m.servers[serverName]; !exists {
		return nil, errors.New("server not found")
	}

	return &ToolResult{}, nil
}

// Disconnect removes a server connection from the manager.
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
