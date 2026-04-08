// Package learning provides memory adapter interfaces and MCP-backed implementations for FlowState learning integration.
//
// This file defines the MCPMemoryClient struct and constructor, which will implement the MemoryClient interface by delegating to MCP tool calls.
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
func NewMCPMemoryClient(client mcp.Client, server string) *MCPMemoryClient {
	return &MCPMemoryClient{MCPClient: client, MCPServer: server}
}

// CreateEntities creates new entities in the knowledge graph by calling the MCP tool.
func (m *MCPMemoryClient) CreateEntities(ctx context.Context, entities []Entity) ([]Entity, error) {
	args := map[string]any{"entities": entities}
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, "create_entities", args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	var out struct {
		Entities []Entity `json:"entities"`
	}
	err = json.Unmarshal([]byte(result.Content), &out)
	if err != nil {
		return nil, err
	}
	return out.Entities, nil
}

// CreateRelations establishes directed relations between entities by calling the MCP tool.
func (m *MCPMemoryClient) CreateRelations(ctx context.Context, relations []Relation) ([]Relation, error) {
	args := map[string]any{"relations": relations}
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, "create_relations", args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	var out struct {
		Relations []Relation `json:"relations"`
	}
	err = json.Unmarshal([]byte(result.Content), &out)
	if err != nil {
		return nil, err
	}
	if out.Relations == nil {
		return nil, &json.UnmarshalTypeError{Value: "missing 'relations' field in MCP response", Type: nil}
	}
	return out.Relations, nil
}

// SearchNodes performs a full-text search for entities by calling the MCP tool.
func (m *MCPMemoryClient) SearchNodes(ctx context.Context, query string) ([]Entity, error) {
	args := map[string]any{"query": query}
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, "search_nodes", args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	var out struct {
		Entities []Entity `json:"entities"`
	}
	err = json.Unmarshal([]byte(result.Content), &out)
	if err != nil {
		return nil, err
	}
	if out.Entities == nil {
		return nil, &json.UnmarshalTypeError{Value: "missing 'entities' field in MCP response", Type: nil}
	}
	return out.Entities, nil
}

// OpenNodes retrieves specific entities and their relations by calling the MCP tool.
func (m *MCPMemoryClient) OpenNodes(ctx context.Context, names []string) (KnowledgeGraph, error) {
	args := map[string]any{"names": names}
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, "open_nodes", args)
	if err != nil {
		return KnowledgeGraph{}, err
	}
	if result.IsError {
		return KnowledgeGraph{}, &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	var out KnowledgeGraph
	err = json.Unmarshal([]byte(result.Content), &out)
	if err != nil {
		return KnowledgeGraph{}, err
	}
	if out.Entities == nil || out.Relations == nil {
		return KnowledgeGraph{}, &json.UnmarshalTypeError{Value: "missing 'entities' or 'relations' field in MCP response", Type: nil}
	}
	return out, nil
}
