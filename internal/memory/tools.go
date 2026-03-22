package memory

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// createEntitiesArgs is the input schema for the create_entities tool.
//
// Expected:
//   - Entities contains valid Entity values with Name, EntityType, and Observations.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type createEntitiesArgs struct {
	Entities []Entity `json:"entities"`
}

// createRelationsArgs is the input schema for the create_relations tool.
//
// Expected:
//   - Relations contains valid Relation values with From, To, and RelationType.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type createRelationsArgs struct {
	Relations []Relation `json:"relations"`
}

// observationEntry represents a single entity's observations to add.
//
// Expected:
//   - EntityName is a non-empty string matching an existing entity.
//   - Contents contains observation strings to add.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type observationEntry struct {
	EntityName string   `json:"entityName"`
	Contents   []string `json:"contents"`
}

// addObservationsArgs is the input schema for the add_observations tool.
//
// Expected:
//   - Observations contains valid observationEntry values.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type addObservationsArgs struct {
	Observations []observationEntry `json:"observations"`
}

// deleteEntitiesArgs is the input schema for the delete_entities tool.
//
// Expected:
//   - EntityNames contains names of entities to delete.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type deleteEntitiesArgs struct {
	EntityNames []string `json:"entityNames"`
}

// deletionEntry represents a single entity's observations to delete.
//
// Expected:
//   - EntityName is a non-empty string matching an existing entity.
//   - Observations contains observation strings to remove.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type deletionEntry struct {
	EntityName   string   `json:"entityName"`
	Observations []string `json:"observations"`
}

// deleteObservationsArgs is the input schema for the delete_observations tool.
//
// Expected:
//   - Deletions contains valid deletionEntry values.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type deleteObservationsArgs struct {
	Deletions []deletionEntry `json:"deletions"`
}

// deleteRelationsArgs is the input schema for the delete_relations tool.
//
// Expected:
//   - Relations contains valid Relation values to remove.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type deleteRelationsArgs struct {
	Relations []Relation `json:"relations"`
}

// readGraphArgs is the input schema for the read_graph tool.
//
// Expected:
//   - No fields required.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type readGraphArgs struct{}

// searchNodesArgs is the input schema for the search_nodes tool.
//
// Expected:
//   - Query is a non-empty search string.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type searchNodesArgs struct {
	Query string `json:"query"`
}

// openNodesArgs is the input schema for the open_nodes tool.
//
// Expected:
//   - Names contains entity names to retrieve.
//
// Returns:
//   - (nothing; type definition only)
//
// Side effects:
//   - None.
type openNodesArgs struct {
	Names []string `json:"names"`
}

// RegisterTools registers all 9 MCP memory tools with the given server.
//
// Mutation tools persist the graph via the store after each operation.
// Query tools return results without modifying persistence.
//
// Expected:
//   - server is an initialised MCP server.
//   - graph is an initialised Graph instance.
//   - store is an initialised JSONLStore for persistence.
//
// Side effects:
//   - Registers 9 tool handlers on the server.
func RegisterTools(server *mcp.Server, graph *Graph, store *JSONLStore) {
	registerCreateEntities(server, graph, store)
	registerCreateRelations(server, graph, store)
	registerAddObservations(server, graph, store)
	registerDeleteEntities(server, graph, store)
	registerDeleteObservations(server, graph, store)
	registerDeleteRelations(server, graph, store)
	registerReadGraph(server, graph)
	registerSearchNodes(server, graph)
	registerOpenNodes(server, graph)
}

// registerCreateEntities registers the create_entities MCP tool.
//
// Expected:
//   - server, graph, and store are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerCreateEntities(server *mcp.Server, graph *Graph, store *JSONLStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_entities",
		Description: "Create new entities in the knowledge graph",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args createEntitiesArgs) (*mcp.CallToolResult, any, error) {
		added := graph.CreateEntities(args.Entities)
		if err := persistGraph(graph, store); err != nil {
			return nil, nil, err
		}
		return jsonResult(added)
	})
}

// registerCreateRelations registers the create_relations MCP tool.
//
// Expected:
//   - server, graph, and store are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerCreateRelations(server *mcp.Server, graph *Graph, store *JSONLStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "create_relations",
		Description: "Create relations between existing entities in the knowledge graph",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args createRelationsArgs) (*mcp.CallToolResult, any, error) {
		added := graph.CreateRelations(args.Relations)
		if err := persistGraph(graph, store); err != nil {
			return nil, nil, err
		}
		return jsonResult(added)
	})
}

// registerAddObservations registers the add_observations MCP tool.
//
// Expected:
//   - server, graph, and store are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerAddObservations(server *mcp.Server, graph *Graph, store *JSONLStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "add_observations",
		Description: "Add new observations to existing entities in the knowledge graph",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args addObservationsArgs) (*mcp.CallToolResult, any, error) {
		for _, obs := range args.Observations {
			if err := graph.AddObservations(obs.EntityName, obs.Contents); err != nil {
				return nil, nil, fmt.Errorf("adding observations to %s: %w", obs.EntityName, err)
			}
		}
		if err := persistGraph(graph, store); err != nil {
			return nil, nil, err
		}
		return jsonResult(args.Observations)
	})
}

// registerDeleteEntities registers the delete_entities MCP tool.
//
// Expected:
//   - server, graph, and store are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerDeleteEntities(server *mcp.Server, graph *Graph, store *JSONLStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_entities",
		Description: "Delete entities from the knowledge graph by name",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args deleteEntitiesArgs) (*mcp.CallToolResult, any, error) {
		graph.DeleteEntities(args.EntityNames)
		if err := persistGraph(graph, store); err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]any{"status": "success", "deleted": args.EntityNames})
	})
}

// registerDeleteObservations registers the delete_observations MCP tool.
//
// Expected:
//   - server, graph, and store are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerDeleteObservations(server *mcp.Server, graph *Graph, store *JSONLStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_observations",
		Description: "Delete specific observations from entities in the knowledge graph",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args deleteObservationsArgs) (*mcp.CallToolResult, any, error) {
		for _, del := range args.Deletions {
			if err := graph.DeleteObservations(del.EntityName, del.Observations); err != nil {
				return nil, nil, fmt.Errorf("deleting observations from %s: %w", del.EntityName, err)
			}
		}
		if err := persistGraph(graph, store); err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]string{"status": "success"})
	})
}

// registerDeleteRelations registers the delete_relations MCP tool.
//
// Expected:
//   - server, graph, and store are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerDeleteRelations(server *mcp.Server, graph *Graph, store *JSONLStore) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "delete_relations",
		Description: "Delete specific relations from the knowledge graph",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args deleteRelationsArgs) (*mcp.CallToolResult, any, error) {
		graph.DeleteRelations(args.Relations)
		if err := persistGraph(graph, store); err != nil {
			return nil, nil, err
		}
		return jsonResult(map[string]string{"status": "success"})
	})
}

// registerReadGraph registers the read_graph MCP tool.
//
// Expected:
//   - server and graph are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerReadGraph(server *mcp.Server, graph *Graph) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "read_graph",
		Description: "Return the entire knowledge graph",
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ readGraphArgs) (*mcp.CallToolResult, any, error) {
		return jsonResult(graph.ReadGraph())
	})
}

// registerSearchNodes registers the search_nodes MCP tool.
//
// Expected:
//   - server and graph are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerSearchNodes(server *mcp.Server, graph *Graph) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_nodes",
		Description: "Search for entities by name, type, or observation content",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args searchNodesArgs) (*mcp.CallToolResult, any, error) {
		return jsonResult(graph.SearchNodes(args.Query))
	})
}

// registerOpenNodes registers the open_nodes MCP tool.
//
// Expected:
//   - server and graph are initialised.
//
// Side effects:
//   - Registers a tool handler on the server.
func registerOpenNodes(server *mcp.Server, graph *Graph) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "open_nodes",
		Description: "Retrieve specific entities and their inter-relations by name",
	}, func(_ context.Context, _ *mcp.CallToolRequest, args openNodesArgs) (*mcp.CallToolResult, any, error) {
		entities, relations := graph.OpenNodes(args.Names)
		return jsonResult(KnowledgeGraph{Entities: entities, Relations: relations})
	})
}

// persistGraph saves the current graph state to the store.
//
// Expected:
//   - graph and store are initialised.
//
// Returns:
//   - error if the save fails.
//
// Side effects:
//   - Writes to the filesystem via the store.
func persistGraph(graph *Graph, store *JSONLStore) error {
	kg := graph.ReadGraph()
	return store.Save(&kg)
}

// jsonResult marshals data to JSON and wraps it in an MCP CallToolResult.
//
// Expected:
//   - data is a JSON-serialisable value.
//
// Returns:
//   - *mcp.CallToolResult with JSON text content, nil output, nil error on success.
//   - nil, nil, error if marshalling fails.
//
// Side effects:
//   - None.
func jsonResult(data any) (*mcp.CallToolResult, any, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling result: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
	}, nil, nil
}
