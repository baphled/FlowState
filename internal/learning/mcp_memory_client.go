// Package learning provides memory adapter interfaces and MCP-backed implementations for FlowState learning integration.
//
// This file defines the MCPMemoryClient adapter for MCP tool calls.
package learning

import (
	"context"
	"encoding/json"
	"fmt"

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
	return m.callAndParseEntities(ctx, "create_entities", map[string]any{"entities": entities})
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
	return m.callAndParseRelations(ctx, "create_relations", map[string]any{"relations": relations})
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
	return m.callAndParseEntities(ctx, "search_nodes", map[string]any{"query": query})
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

// callAndParseEntities invokes an MCP tool and decodes its content into a
// canonical []Entity slice, accepting either of the two JSON shapes that real
// MCP memory servers emit: the documented {"entities": [...]} object form and
// the bare [...] array form returned by the JS reference memory server.
//
// Expected:
//   - ctx is valid for the tool call.
//   - toolName identifies the MCP tool.
//   - args contains serialisable tool arguments.
//
// Returns:
//   - The parsed entities, normalised to a non-nil slice (empty when the MCP
//     server returned a non-JSON empty marker such as "undefined").
//   - An error when the MCP call fails, the tool reports an error, or the
//     content is JSON-shaped but cannot be decoded as either accepted shape.
//
// Side effects:
//   - Calls the MCP server.
//   - Emits a debug-level log when an empty non-JSON response is observed.
func (m *MCPMemoryClient) callAndParseEntities(ctx context.Context, toolName string, args map[string]any) ([]Entity, error) {
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, toolName, args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	return parseEntities([]byte(result.Content), toolName, m.MCPServer)
}

// callAndParseRelations invokes an MCP tool and decodes its content into a
// canonical []Relation slice, accepting either {"relations": [...]} or a bare
// [...] array shape, matching the leniency applied to entity-bearing calls.
//
// Expected:
//   - ctx is valid for the tool call.
//   - toolName identifies the MCP tool.
//   - args contains serialisable tool arguments.
//
// Returns:
//   - The parsed relations, normalised to a non-nil slice (empty when the MCP
//     server returned a non-JSON empty marker such as "undefined").
//   - An error when the MCP call fails, the tool reports an error, or the
//     content is JSON-shaped but cannot be decoded as either accepted shape.
//
// Side effects:
//   - Calls the MCP server.
//   - Emits a debug-level log when an empty non-JSON response is observed.
func (m *MCPMemoryClient) callAndParseRelations(ctx context.Context, toolName string, args map[string]any) ([]Relation, error) {
	result, err := m.MCPClient.CallTool(ctx, m.MCPServer, toolName, args)
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, &json.UnmarshalTypeError{Value: "MCP tool error", Type: nil}
	}
	return parseRelations([]byte(result.Content), toolName, m.MCPServer)
}

// parseEntities decodes a raw MCP content payload into the canonical
// []Entity shape used internally by the learning package.
//
// LLM-driven and reference-implementation MCP memory servers are inconsistent
// about whether they wrap their output in an entities-keyed object or return a
// bare JSON array. Rather than depending on a single shape, the parser tries
// the documented {"entities": [...]} form first and, on the specific
// "cannot unmarshal array into Go value of type struct" failure, retries as a
// bare [...] array. Both succeed against the same canonical []Entity result,
// which is what every caller expects.
//
// Empty MCP content (whitespace, the literal "undefined", or a missing-but-
// well-formed object with no entities field) is treated as an empty slice
// rather than an error so the learning hook does not warn on every quiet
// session.
//
// Expected:
//   - content is the raw MCP ToolResult.Content payload.
//   - toolName and serverName are forwarded to slog for empty-response
//     diagnostics.
//
// Returns:
//   - The parsed entities, never nil (empty slice on empty content).
//   - A wrapped error when the content is JSON-shaped but matches neither
//     accepted shape.
//
// Side effects:
//   - Emits a debug-level log on the empty branch.
func parseEntities(content []byte, toolName, serverName string) ([]Entity, error) {
	var wrapped struct {
		Entities []Entity `json:"entities"`
	}
	empty, err := mcp.DecodeContent(string(content), &wrapped,
		"tool", toolName, "server", serverName)
	if err == nil {
		if empty {
			return []Entity{}, nil
		}
		if wrapped.Entities != nil {
			return wrapped.Entities, nil
		}
		return nil, &json.UnmarshalTypeError{Value: "missing 'entities' field in MCP response", Type: nil}
	}
	var bare []Entity
	if bareErr := json.Unmarshal(content, &bare); bareErr == nil {
		return bare, nil
	}
	return nil, fmt.Errorf("decoding entities from MCP %s response: %w", toolName, err)
}

// parseRelations decodes a raw MCP content payload into the canonical
// []Relation shape, mirroring parseEntities for the relation-bearing tools.
//
// Expected:
//   - content is the raw MCP ToolResult.Content payload.
//   - toolName and serverName are forwarded to slog for empty-response
//     diagnostics.
//
// Returns:
//   - The parsed relations, never nil (empty slice on empty content).
//   - A wrapped error when the content is JSON-shaped but matches neither
//     accepted shape.
//
// Side effects:
//   - Emits a debug-level log on the empty branch.
func parseRelations(content []byte, toolName, serverName string) ([]Relation, error) {
	var wrapped struct {
		Relations []Relation `json:"relations"`
	}
	empty, err := mcp.DecodeContent(string(content), &wrapped,
		"tool", toolName, "server", serverName)
	if err == nil {
		if empty {
			return []Relation{}, nil
		}
		if wrapped.Relations != nil {
			return wrapped.Relations, nil
		}
		return nil, &json.UnmarshalTypeError{Value: "missing 'relations' field in MCP response", Type: nil}
	}
	var bare []Relation
	if bareErr := json.Unmarshal(content, &bare); bareErr == nil {
		return bare, nil
	}
	return nil, fmt.Errorf("decoding relations from MCP %s response: %w", toolName, err)
}
