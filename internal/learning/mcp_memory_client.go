// Package learning provides memory adapter interfaces and MCP-backed implementations for FlowState learning integration.
//
// This file defines the MCPMemoryClient adapter for MCP tool calls.
package learning

import (
	"context"
	"encoding/json"

	"github.com/baphled/flowstate/internal/mcp"
)

// MCPMemoryClient implements MemoryClient by delegating to MCP tool calls.
type MCPMemoryClient struct {
	MCPClient mcp.Client
	MCPServer string
}

// NewMCPMemoryClient creates a new MCPMemoryClient with the given MCP server address or identifier.
//
// Expected:
//   - client implements mcp.Client.
//   - server identifies the target MCP server.
//
// Returns:
//   - A memory client that delegates to the configured MCP server.
//
// Side effects:
//   - None.
func NewMCPMemoryClient(client mcp.Client, server string) *MCPMemoryClient {
	return &MCPMemoryClient{MCPClient: client, MCPServer: server}
}

// CreateEntities creates new entities in the knowledge graph by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - entities contains the entity payload to create.
//
// Returns:
//   - A slice of created entities.
//   - An error when the MCP call or response decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) CreateEntities(ctx context.Context, entities []Entity) ([]Entity, error) {
	var out struct {
		Entities []Entity `json:"entities"`
	}
	if err := m.callAndUnmarshal(ctx, "create_entities", map[string]any{"entities": entities}, &out); err != nil {
		return nil, err
	}
	if out.Entities == nil {
		return nil, &json.UnmarshalTypeError{Value: "missing 'entities' field in MCP response", Type: nil}
	}
	return out.Entities, nil
}

// CreateRelations establishes directed relations between entities by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - relations contains the relation payload to create.
//
// Returns:
//   - A slice of created relations.
//   - An error when the MCP call or response decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) CreateRelations(ctx context.Context, relations []Relation) ([]Relation, error) {
	var out struct {
		Relations []Relation `json:"relations"`
	}
	if err := m.callAndUnmarshal(ctx, "create_relations", map[string]any{"relations": relations}, &out); err != nil {
		return nil, err
	}
	if out.Relations == nil {
		return nil, &json.UnmarshalTypeError{Value: "missing 'relations' field in MCP response", Type: nil}
	}
	return out.Relations, nil
}

// SearchNodes performs a full-text search for entities by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - query contains the search term.
//
// Returns:
//   - Matching entities.
//   - An error when the MCP call or response decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) SearchNodes(ctx context.Context, query string) ([]Entity, error) {
	var out struct {
		Entities []Entity `json:"entities"`
	}
	if err := m.callAndUnmarshal(ctx, "search_nodes", map[string]any{"query": query}, &out); err != nil {
		return nil, err
	}
	if out.Entities == nil {
		return nil, &json.UnmarshalTypeError{Value: "missing 'entities' field in MCP response", Type: nil}
	}
	return out.Entities, nil
}

// OpenNodes retrieves specific entities and their relations by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - names contains the node names to open.
//
// Returns:
//   - The requested knowledge graph.
//   - An error when the MCP call or response decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) OpenNodes(ctx context.Context, names []string) (KnowledgeGraph, error) {
	var out KnowledgeGraph
	if err := m.callAndUnmarshal(ctx, "open_nodes", map[string]any{"names": names}, &out); err != nil {
		return KnowledgeGraph{}, err
	}
	if out.Entities == nil || out.Relations == nil {
		return KnowledgeGraph{}, &json.UnmarshalTypeError{Value: "missing 'entities' or 'relations' field in MCP response", Type: nil}
	}
	return out, nil
}

// callAndUnmarshal calls a tool and decodes the JSON response into target.
//
// Expected:
//   - ctx is valid for the tool call.
//   - toolName identifies the MCP tool.
//   - args contains serialisable tool arguments.
//   - target is a pointer to the destination value.
//
// Returns:
//   - An error when the call fails, the tool reports an error, or decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) callAndUnmarshal(ctx context.Context, toolName string, args map[string]any, target any) error {
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, toolName, args)
	if err != nil {
		return err
	}
	if result.IsError {
		return &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	if err := json.Unmarshal([]byte(result.Content), target); err != nil {
		return err
	}
	return nil
}
