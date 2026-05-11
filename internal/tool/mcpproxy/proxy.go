// Package mcpproxy provides a tool.Tool implementation that proxies calls
// to a connected MCP server.
package mcpproxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/mcp"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/truncate"
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
//   - ctx is a valid context. May carry a session.IDKey value used to
//     scope the truncate-envelope's overflow spill directory.
//   - input contains the tool name and arguments.
//
// Returns:
//   - A tool.Result containing the (possibly truncated) MCP tool output.
//   - An error if the MCP CallTool RPC fails.
//
// Side effects:
//   - Makes an RPC call to the connected MCP server.
//   - Bug #34 — every MCP output flows through truncate.Apply with the
//     same envelope native tools (bash, read, grep, ls) use. A runaway
//     300KB knowledge-graph dump from mem0 used to land raw in the
//     messages array and eat context-window budget on every subsequent
//     turn. The truncate package preserves under-cap payloads verbatim,
//     so small outputs are unchanged; over-cap outputs are sliced to
//     fit and a recovery hint pointing at the session-scoped spill file
//     is appended. Both the success (non-IsError) and error
//     (IsError=true) branches flow through the envelope — an MCP server
//     returning a giant stack trace dump on failure must not bypass
//     the cap either. Default-on is the security-adjacent choice: the
//     opt-out is cheaper than rediscovering this bug per MCP tool.
func (p *Proxy) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	result, err := p.client.CallTool(ctx, p.serverName, p.name, input.Arguments)
	if err != nil {
		return tool.Result{}, fmt.Errorf("MCP tool %q on %q: %w", p.name, p.serverName, err)
	}
	capped := p.capOutput(ctx, result.Content)
	if result.IsError {
		return tool.Result{
			Output: capped,
			Error:  errors.New(capped),
		}, nil
	}
	return tool.Result{Output: capped}, nil
}

// capOutput funnels MCP tool output through the shared truncate
// envelope using the session ID carried by ctx. The ToolName mirrors
// the proxied MCP tool's name so spill filenames are informative
// during triage. Native tools (bash, read, grep, ls) use the same
// helper shape — this method keeps the MCP proxy aligned with that
// contract.
//
// Expected:
//   - ctx may carry a session.IDKey value; empty sessionID still works
//     (truncate falls back to the "_unscoped" bucket so non-session
//     callers like CLI tests still spill correctly).
//   - output is the raw Content returned by the MCP server.
//
// Returns:
//   - The (possibly truncated) content string. Under-cap inputs are
//     returned verbatim; over-cap inputs are sliced and a recovery
//     hint is appended.
//
// Side effects:
//   - On over-cap input, writes one spill file under the session-scoped
//     overflow directory. IO errors are swallowed by truncate.Apply.
func (p *Proxy) capOutput(ctx context.Context, output string) string {
	sessionID, _ := ctx.Value(session.IDKey{}).(string)
	r := truncate.Apply(output, truncate.Options{
		SessionID: sessionID,
		ToolName:  p.name,
	})
	return r.Content
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
