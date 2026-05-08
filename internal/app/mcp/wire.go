package mcp

import (
	"context"
	"log"

	"github.com/baphled/flowstate/internal/config"
	mcpclient "github.com/baphled/flowstate/internal/mcp"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/mcpproxy"
)

// ConnectionResult contains the result of attempting to connect to an MCP
// server. The composition root surfaces these in startup logs to make
// connection failures debuggable from stderr alone.
type ConnectionResult struct {
	Name      string
	Success   bool
	Error     string
	ToolCount int
}

// ConnectServers connects to configured MCP servers and returns proxy
// tools. Connection failures are logged as warnings and do not stop
// processing — a server that fails to connect leaves an unsuccessful
// ConnectionResult in the returned slice but never blocks startup.
//
// Expected:
//   - ctx is a valid context.
//   - client is an initialised MCP Client.
//   - servers is a slice of MCP server configurations.
//
// Returns:
//   - A slice of tool.Tool implementations backed by connected MCP servers.
//   - A slice of ConnectionResult describing each connection attempt.
//   - A map from server name to the names of tools it exposes, for use by
//     the engine when resolving Capabilities.MCPServers declarations.
//
// Side effects:
//   - Connects to MCP servers via the client.
//   - Logs warnings for connection or tool listing failures.
func ConnectServers(
	ctx context.Context,
	client mcpclient.Client,
	servers []config.MCPServerConfig,
) ([]tool.Tool, []ConnectionResult, map[string][]string) {
	var tools []tool.Tool
	var results []ConnectionResult
	serverToolNames := make(map[string][]string)
	for _, serverCfg := range servers {
		if !serverCfg.Enabled {
			continue
		}
		mcpServerConfig := mcpclient.ServerConfig{
			Name:    serverCfg.Name,
			Command: serverCfg.Command,
			Args:    serverCfg.Args,
			Env:     serverCfg.Env,
		}
		if err := client.Connect(ctx, mcpServerConfig); err != nil {
			log.Printf("warning: MCP server %q failed to connect: %v", serverCfg.Name, err)
			results = append(results, ConnectionResult{
				Name:      serverCfg.Name,
				Success:   false,
				Error:     err.Error(),
				ToolCount: 0,
			})
			continue
		}
		serverTools, err := client.ListTools(ctx, serverCfg.Name)
		if err != nil {
			log.Printf("warning: MCP server %q ListTools failed: %v", serverCfg.Name, err)
			results = append(results, ConnectionResult{
				Name:      serverCfg.Name,
				Success:   false,
				Error:     err.Error(),
				ToolCount: 0,
			})
			continue
		}
		names := make([]string, 0, len(serverTools))
		for _, t := range serverTools {
			tools = append(tools, mcpproxy.NewProxy(client, serverCfg.Name, t))
			names = append(names, t.Name)
		}
		serverToolNames[serverCfg.Name] = names
		results = append(results, ConnectionResult{
			Name:      serverCfg.Name,
			Success:   true,
			Error:     "",
			ToolCount: len(serverTools),
		})
	}
	return tools, results, serverToolNames
}

// MergeServers merges discovered MCP servers with configured servers,
// preferring configured servers when names conflict.
//
// Expected:
//   - configured is the user-defined server list from config.
//   - discovered is the auto-detected server list.
//
// Returns:
//   - A merged slice with configured servers taking precedence on name
//     collision; discovered-only servers are appended in their original
//     order.
//
// Side effects:
//   - None.
func MergeServers(configured, discovered []config.MCPServerConfig) []config.MCPServerConfig {
	existing := make(map[string]bool)
	result := make([]config.MCPServerConfig, 0, len(configured)+len(discovered))
	for _, s := range configured {
		result = append(result, s)
		existing[s.Name] = true
	}
	for _, s := range discovered {
		if !existing[s.Name] {
			result = append(result, s)
		}
	}
	return result
}
