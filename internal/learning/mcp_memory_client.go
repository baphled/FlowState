// Package learning provides memory adapter interfaces and MCP-backed implementations for FlowState learning integration.
//
// This file defines the MCPMemoryClient adapter for MCP tool calls.
package learning

import (
	"context"
	"encoding/json"

	"github.com/baphled/flowstate/internal/mcp"
)

// Compile-time assertion that MCPMemoryClient implements MemoryClient.
var _ MemoryClient = (*MCPMemoryClient)(nil)

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
	empty, err := m.callAndUnmarshal(ctx, "create_entities", map[string]any{"entities": entities}, &out)
	if err != nil {
		return nil, err
	}
	if empty {
		return []Entity{}, nil
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
	empty, err := m.callAndUnmarshal(ctx, "create_relations", map[string]any{"relations": relations}, &out)
	if err != nil {
		return nil, err
	}
	if empty {
		return []Relation{}, nil
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
	empty, err := m.callAndUnmarshal(ctx, "search_nodes", map[string]any{"query": query}, &out)
	if err != nil {
		return nil, err
	}
	if empty {
		return []Entity{}, nil
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
	empty, err := m.callAndUnmarshal(ctx, "open_nodes", map[string]any{"names": names}, &out)
	if err != nil {
		return KnowledgeGraph{}, err
	}
	if empty {
		return KnowledgeGraph{Entities: []Entity{}, Relations: []Relation{}}, nil
	}
	if out.Entities == nil || out.Relations == nil {
		return KnowledgeGraph{}, &json.UnmarshalTypeError{Value: "missing 'entities' or 'relations' field in MCP response", Type: nil}
	}
	return out, nil
}

// AddObservations appends observations to existing entities by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - observations contains the observation entries to append.
//
// Returns:
//   - The appended observation entries.
//   - An error when the MCP call or response decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) AddObservations(ctx context.Context, observations []ObservationEntry) ([]ObservationEntry, error) {
	var out struct {
		Observations []ObservationEntry `json:"observations"`
	}
	empty, err := m.callAndUnmarshal(ctx, "add_observations", map[string]any{"observations": observations}, &out)
	if err != nil {
		return nil, err
	}
	if empty {
		return []ObservationEntry{}, nil
	}
	return out.Observations, nil
}

// DeleteEntities removes entities and cascades relations by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - entityNames contains the names of entities to delete.
//
// Returns:
//   - The names of deleted entities.
//   - An error when the MCP call or response decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) DeleteEntities(ctx context.Context, entityNames []string) ([]string, error) {
	var out struct {
		Deleted []string `json:"deleted"`
	}
	empty, err := m.callAndUnmarshal(ctx, "delete_entities", map[string]any{"entityNames": entityNames}, &out)
	if err != nil {
		return nil, err
	}
	if empty {
		return []string{}, nil
	}
	return out.Deleted, nil
}

// DeleteObservations removes specific observations from entities by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - deletions specifies which observations to remove from which entities.
//
// Returns:
//   - An error when the MCP call fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) DeleteObservations(ctx context.Context, deletions []DeletionEntry) error {
	var out struct{}
	_, err := m.callAndUnmarshal(ctx, "delete_observations", map[string]any{"deletions": deletions}, &out)
	return err
}

// DeleteRelations removes specific relations from the graph by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//   - relations contains the relations to remove.
//
// Returns:
//   - An error when the MCP call fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) DeleteRelations(ctx context.Context, relations []Relation) error {
	var out struct{}
	_, err := m.callAndUnmarshal(ctx, "delete_relations", map[string]any{"relations": relations}, &out)
	return err
}

// ReadGraph returns the entire knowledge graph by calling the MCP tool.
//
// Expected:
//   - ctx is valid for the tool call.
//
// Returns:
//   - The full KnowledgeGraph.
//   - An error when the MCP call or response decoding fails.
//
// Side effects:
//   - Calls the MCP server.
func (m *MCPMemoryClient) ReadGraph(ctx context.Context) (KnowledgeGraph, error) {
	var out KnowledgeGraph
	empty, err := m.callAndUnmarshal(ctx, "read_graph", map[string]any{}, &out)
	if err != nil {
		return KnowledgeGraph{}, err
	}
	if empty {
		return KnowledgeGraph{Entities: []Entity{}, Relations: []Relation{}}, nil
	}
	return out, nil
}

// WriteLearningRecord persists a learning record by converting it to an entity and calling CreateEntities.
//
// Expected:
//   - record is a non-nil Record with at least an AgentID.
//
// Returns:
//   - An error when the entity creation fails.
//
// Side effects:
//   - Calls CreateEntities on the MCP server.
func (m *MCPMemoryClient) WriteLearningRecord(record *Record) error {
	observations := []string{
		"Outcome: " + record.Outcome,
	}
	for _, tool := range record.ToolsUsed {
		observations = append(observations, "ToolUsed: "+tool)
	}
	entity := Entity{
		Name:         record.AgentID,
		EntityType:   "learning-record",
		Observations: observations,
	}
	_, err := m.CreateEntities(context.Background(), []Entity{entity})
	return err
}

// callAndUnmarshal calls a tool and decodes the JSON response into target.
//
// Some MCP servers (notably the JS reference memory server) return non-JSON
// text such as the literal string "undefined" on a non-error response when a
// search yields no results. Those responses are treated as empty, not as
// decode errors, so callers can surface "no results" semantics to consumers
// instead of failing the entire recall query.
//
// Expected:
//   - ctx is valid for the tool call.
//   - toolName identifies the MCP tool.
//   - args contains serialisable tool arguments.
//   - target is a pointer to the destination value.
//
// Returns:
//   - empty is true when the MCP server returned non-JSON content on a
//     successful response; target is left untouched in that case.
//   - An error when the call fails, the tool reports an error, or decoding
//     a JSON-shaped payload fails.
//
// Side effects:
//   - Calls the MCP server.
//   - Emits a debug-level log when an empty non-JSON response is observed.
func (m *MCPMemoryClient) callAndUnmarshal(ctx context.Context, toolName string, args map[string]any, target any) (empty bool, err error) {
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, toolName, args)
	if err != nil {
		return false, err
	}
	if result.IsError {
		return false, &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	return mcp.DecodeContent(result.Content, target,
		"tool", toolName, "server", m.MCPServer)
}
