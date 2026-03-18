// Package mcpproxy provides a tool.Tool implementation that proxies calls
// to a connected MCP server.
package mcpproxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/mcp"
	"github.com/baphled/flowstate/internal/tool"
)

// Proxy wraps an MCP tool as a tool.Tool, delegating execution to an MCP server.
type Proxy struct {
	name        string
	description string
	serverName  string
	inputSchema map[string]interface{}
	client      mcp.Client
}

// NewProxy creates a Proxy that delegates execution to an MCP server tool.
//
// Expected:
//   - client is a connected MCP Client.
//   - serverName identifies the server holding the tool.
//   - info describes the tool discovered via ListTools.
//
// Returns:
//   - A tool.Tool implementation backed by the MCP server.
//
// Side effects:
//   - None.
func NewProxy(client mcp.Client, serverName string, info mcp.ToolInfo) *Proxy {
	return &Proxy{
		name:        info.Name,
		description: info.Description,
		serverName:  serverName,
		inputSchema: info.InputSchema,
		client:      client,
	}
}

// Name returns the unique name of the proxied MCP tool.
//
// Returns:
//   - The tool name as reported by the MCP server.
//
// Side effects:
//   - None.
func (p *Proxy) Name() string {
	return p.name
}

// Description returns a human-readable description of the proxied MCP tool.
//
// Returns:
//   - The tool description as reported by the MCP server.
//
// Side effects:
//   - None.
func (p *Proxy) Description() string {
	return p.description
}

// Execute runs the proxied tool by delegating to the MCP server via CallTool.
//
// Expected:
//   - ctx is a valid context.
//   - input contains the tool name and arguments.
//
// Returns:
//   - A tool.Result containing the MCP tool output.
//   - An error if the MCP CallTool RPC fails.
//
// Side effects:
//   - Makes an RPC call to the connected MCP server.
func (p *Proxy) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	result, err := p.client.CallTool(ctx, p.serverName, p.name, input.Arguments)
	if err != nil {
		return tool.Result{}, fmt.Errorf("MCP tool %q on %q: %w", p.name, p.serverName, err)
	}
	if result.IsError {
		return tool.Result{
			Output: result.Content,
			Error:  errors.New(result.Content),
		}, nil
	}
	return tool.Result{Output: result.Content}, nil
}

// Schema returns the input schema for the proxied MCP tool.
//
// Returns:
//   - A tool.Schema describing the tool's expected input parameters.
//
// Side effects:
//   - None.
func (p *Proxy) Schema() tool.Schema {
	if p.inputSchema == nil {
		return tool.Schema{
			Type:       "object",
			Properties: map[string]tool.Property{},
			Required:   []string{},
		}
	}
	return parseInputSchema(p.inputSchema)
}

// parseInputSchema converts an MCP InputSchema map to a tool.Schema.
//
// Expected:
//   - schema is a non-nil map representing a JSON Schema object.
//
// Returns:
//   - A tool.Schema populated from the InputSchema fields.
//
// Side effects:
//   - None.
func parseInputSchema(schema map[string]interface{}) tool.Schema {
	result := tool.Schema{
		Type:       "object",
		Properties: map[string]tool.Property{},
		Required:   []string{},
	}
	if t, ok := schema["type"].(string); ok {
		result.Type = t
	}
	result.Properties = parseProperties(schema)
	result.Required = parseRequired(schema)
	return result
}

// parseProperties extracts tool.Property entries from an MCP InputSchema properties map.
//
// Expected:
//   - schema is a non-nil map that may contain a "properties" key.
//
// Returns:
//   - A map of property names to tool.Property values.
//
// Side effects:
//   - None.
func parseProperties(schema map[string]interface{}) map[string]tool.Property {
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return map[string]tool.Property{}
	}
	result := make(map[string]tool.Property, len(props))
	for name, val := range props {
		if prop, valid := parseProperty(val); valid {
			result[name] = prop
		}
	}
	return result
}

// parseProperty converts a single property map to a tool.Property.
//
// Expected:
//   - val is an interface{} that may be a map[string]interface{} property descriptor.
//
// Returns:
//   - A tool.Property and true if val is a valid property map.
//   - A zero-value tool.Property and false otherwise.
//
// Side effects:
//   - None.
func parseProperty(val interface{}) (tool.Property, bool) {
	propMap, ok := val.(map[string]interface{})
	if !ok {
		return tool.Property{}, false
	}
	prop := tool.Property{}
	if t, ok := propMap["type"].(string); ok {
		prop.Type = t
	}
	if d, ok := propMap["description"].(string); ok {
		prop.Description = d
	}
	return prop, true
}

// parseRequired extracts required field names from an MCP InputSchema.
//
// Expected:
//   - schema is a non-nil map that may contain a "required" key.
//
// Returns:
//   - A string slice of required field names from the schema.
//
// Side effects:
//   - None.
func parseRequired(schema map[string]interface{}) []string {
	req, ok := schema["required"].([]interface{})
	if !ok {
		return []string{}
	}
	var required []string
	for _, r := range req {
		if s, ok := r.(string); ok {
			required = append(required, s)
		}
	}
	return required
}
