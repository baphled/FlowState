// Package mcp provides a client manager for MCP (Model Context Protocol) server connections.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const connectTimeout = 30 * time.Second

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

// sessionInfo tracks an active MCP session.
type sessionInfo struct {
	session    *mcp.ClientSession
	cancelFunc context.CancelFunc
	config     ServerConfig
}

// TransportFactory creates a transport for connecting to an MCP server.
// This allows injection of test transports.
type TransportFactory func(ctx context.Context, config ServerConfig) (mcp.Transport, error)

// Manager manages MCP server connections.
type Manager struct {
	mu               sync.RWMutex
	sessions         map[string]*sessionInfo
	transportFactory TransportFactory
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithTransportFactory sets a custom transport factory for testing.
//
// Expected:
//   - factory is a non-nil TransportFactory function.
//
// Returns:
//   - A ManagerOption that configures the transport factory.
//
// Side effects:
//   - None (configuration only).
func WithTransportFactory(factory TransportFactory) ManagerOption {
	return func(m *Manager) {
		m.transportFactory = factory
	}
}

// NewManager creates a new MCP server connection manager.
//
// Expected:
//   - opts are optional ManagerOption functions to configure the manager.
//
// Returns:
//   - An initialised Manager with no connected servers.
//
// Side effects:
//   - None.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		sessions:         make(map[string]*sessionInfo),
		transportFactory: defaultTransportFactory,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// defaultTransportFactory creates a CommandTransport for production use.
//
// Expected:
//   - config is a non-nil ServerConfig with Command and optional Args/Env.
//
// Returns:
//   - A CommandTransport ready for use, or an error if creation fails.
//
// Side effects:
//   - None (transport is not started).
func defaultTransportFactory(_ context.Context, config ServerConfig) (mcp.Transport, error) {
	cmd := exec.Command(config.Command, config.Args...)
	if len(config.Env) > 0 {
		env := make([]string, 0, len(config.Env))
		for k, v := range config.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = append(cmd.Environ(), env...)
	}
	return &mcp.CommandTransport{Command: cmd}, nil
}

// Connect establishes a connection to an MCP server.
//
// Expected:
//   - config.Name is a unique identifier for the server.
//   - config.Command is the executable to run the MCP server.
//   - config.Args is the list of arguments for the command.
//
// Returns:
//   - An error if a server with the same name is already connected.
//   - An error if the connection fails within the 30-second timeout.
//
// Side effects:
//   - Registers the server connection in the manager.
//   - Starts a subprocess for the MCP server (in production).
func (m *Manager) Connect(ctx context.Context, config ServerConfig) error {
	m.mu.Lock()
	if _, exists := m.sessions[config.Name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("server %q already connected", config.Name)
	}
	m.mu.Unlock()

	timeoutCtx, cancel := context.WithTimeout(ctx, connectTimeout)

	transport, err := m.transportFactory(timeoutCtx, config)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to create transport for %q: %w", config.Name, err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "flowstate-mcp-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(timeoutCtx, transport, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to connect to %q: %w", config.Name, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[config.Name]; exists {
		session.Close()
		cancel()
		return fmt.Errorf("server %q already connected", config.Name)
	}

	m.sessions[config.Name] = &sessionInfo{
		session:    session,
		cancelFunc: cancel,
		config:     config,
	}

	return nil
}

// ListTools returns the tools available on a connected MCP server.
//
// Expected:
//   - serverName is the identifier of a connected MCP server.
//
// Returns:
//   - A slice of ToolInfo describing available tools.
//   - An error if the named server is not found.
//
// Side effects:
//   - Makes a ListTools RPC call to the connected server.
func (m *Manager) ListTools(ctx context.Context, serverName string) ([]ToolInfo, error) {
	m.mu.RLock()
	info, exists := m.sessions[serverName]
	m.mu.RUnlock()

	if !exists {
		return nil, errors.New("server not found")
	}

	result, err := info.session.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list tools: %w", err)
	}

	tools := make([]ToolInfo, len(result.Tools))
	for i, tool := range result.Tools {
		var inputSchema map[string]interface{}
		if tool.InputSchema != nil {
			if schemaMap, ok := tool.InputSchema.(map[string]interface{}); ok {
				inputSchema = schemaMap
			}
		}
		tools[i] = ToolInfo{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: inputSchema,
		}
	}

	return tools, nil
}

// CallTool invokes a tool on a connected MCP server.
//
// Expected:
//   - serverName is the identifier of a connected MCP server.
//   - toolName is the name of the tool to invoke.
//   - args is a map of tool arguments.
//
// Returns:
//   - A ToolResult containing the tool's output.
//   - An error if the named server is not found or tool invocation fails.
//
// Side effects:
//   - Makes a CallTool RPC call to the connected server.
func (m *Manager) CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (*ToolResult, error) {
	m.mu.RLock()
	info, exists := m.sessions[serverName]
	m.mu.RUnlock()

	if !exists {
		return nil, errors.New("server not found")
	}

	result, err := info.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return nil, fmt.Errorf("tool %q not found: %w", toolName, err)
	}

	var content string
	if len(result.Content) > 0 {
		if textContent, ok := result.Content[0].(*mcp.TextContent); ok {
			content = textContent.Text
		}
	}

	return &ToolResult{Content: content, IsError: result.IsError}, nil
}

// ConnectWithTransport establishes a connection using a provided transport.
// This is primarily for testing with InMemoryTransport.
//
// Expected:
//   - name is a unique identifier for the server.
//   - transport is an already-configured MCP transport.
//
// Returns:
//   - An error if a server with the same name is already connected.
//   - An error if the connection fails.
//
// Side effects:
//   - Registers the server connection in the manager.
func (m *Manager) ConnectWithTransport(ctx context.Context, name string, transport mcp.Transport) error {
	m.mu.Lock()
	if _, exists := m.sessions[name]; exists {
		m.mu.Unlock()
		return fmt.Errorf("server %q already connected", name)
	}
	m.mu.Unlock()

	sessionCtx, cancel := context.WithCancel(ctx)

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "flowstate-mcp-client",
		Version: "1.0.0",
	}, nil)

	session, err := client.Connect(sessionCtx, transport, nil)
	if err != nil {
		cancel()
		return fmt.Errorf("failed to connect to %q: %w", name, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.sessions[name]; exists {
		session.Close()
		cancel()
		return fmt.Errorf("server %q already connected", name)
	}

	m.sessions[name] = &sessionInfo{
		session:    session,
		cancelFunc: cancel,
		config:     ServerConfig{Name: name},
	}

	return nil
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
//   - Closes the MCP session.
//   - Cancels the connection context (stopping any subprocess).
//   - Removes the server from the manager's connection map.
func (m *Manager) Disconnect(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	info, exists := m.sessions[name]
	if !exists {
		return errors.New("server not found")
	}

	info.session.Close()
	info.cancelFunc()
	delete(m.sessions, name)

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

	names := make([]string, 0, len(m.sessions))
	for name := range m.sessions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// DisconnectAll closes all server connections.
//
// Returns:
//   - An error if any disconnection fails (aggregated).
//
// Side effects:
//   - Closes all MCP sessions.
//   - Cancels all connection contexts.
//   - Removes all servers from the manager's connection map.
func (m *Manager) DisconnectAll() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for name, info := range m.sessions {
		info.session.Close()
		info.cancelFunc()
		delete(m.sessions, name)
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	return nil
}
