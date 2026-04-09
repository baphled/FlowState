// Package learning provides memory adapter interfaces and vector store-backed implementations for FlowState learning integration.
//
// This file defines the VectorStoreMemoryClient adapter for vector store operations.
package learning

import (
	"context"
	"errors"
	"fmt"
)

// Compile-time assertion that VectorStoreMemoryClient implements MemoryClient.
var _ MemoryClient = (*VectorStoreMemoryClient)(nil)

// VectorStoreMemoryClient implements MemoryClient by delegating to a VectorStoreClient and using a VectorEmbedder.
type VectorStoreMemoryClient struct {
	Store      VectorStoreClient
	Embedder   VectorEmbedder
	Collection string
}

// NewVectorStoreMemoryClient creates a new VectorStoreMemoryClient with the given vector store, embedder, and collection name.
//
// Expected:
//   - store is a non-nil VectorStoreClient.
//   - embedder is a non-nil VectorEmbedder.
//   - collection is the vector collection name.
//
// Returns:
//   - A memory client that delegates to the configured vector store and embedder.
//
// Side effects:
//   - None.
func NewVectorStoreMemoryClient(store VectorStoreClient, embedder VectorEmbedder, collection string) *VectorStoreMemoryClient {
	return &VectorStoreMemoryClient{Store: store, Embedder: embedder, Collection: collection}
}

// CreateEntities creates new entities in the knowledge graph.
//
// Expected:
//   - entities is a slice of Entity structs to create.
//
// Returns:
//   - The created entities (echoed back).
//   - An error if embedding or upsert fails.
//
// Side effects:
//   - Stores each entity as a vector point in the collection.
func (v *VectorStoreMemoryClient) CreateEntities(ctx context.Context, entities []Entity) ([]Entity, error) {
	points := make([]VectorPoint, 0, len(entities))
	for _, e := range entities {
		emb, err := v.Embedder.Embed(ctx, e.Name)
		if err != nil {
			return nil, fmt.Errorf("embedding entity name: %w", err)
		}
		points = append(points, VectorPoint{
			ID:      e.Name,
			Vector:  emb,
			Payload: map[string]any{"entityType": e.EntityType, "observations": e.Observations},
		})
	}
	if err := v.Store.Upsert(ctx, v.Collection, points, false); err != nil {
		return nil, fmt.Errorf("upserting entities: %w", err)
	}
	return entities, nil
}

// CreateRelations establishes directed relations between entities.
//
// Expected:
//   - rels is a slice of Relation structs to create.
//
// Returns:
//   - The created relations (echoed back).
//   - An error if embedding or upsert fails.
//
// Side effects:
//   - Stores each relation as a vector point in the collection.
func (v *VectorStoreMemoryClient) CreateRelations(ctx context.Context, rels []Relation) ([]Relation, error) {
	points := make([]VectorPoint, 0, len(rels))
	for _, r := range rels {
		// Use relation string as the text to embed
		relText := fmt.Sprintf("%s:%s:%s", r.From, r.RelationType, r.To)
		emb, err := v.Embedder.Embed(ctx, relText)
		if err != nil {
			return nil, fmt.Errorf("embedding relation: %w", err)
		}
		points = append(points, VectorPoint{
			ID:      relText,
			Vector:  emb,
			Payload: map[string]any{"from": r.From, "relationType": r.RelationType, "to": r.To},
		})
	}
	if err := v.Store.Upsert(ctx, v.Collection, points, false); err != nil {
		return nil, fmt.Errorf("upserting relations: %w", err)
	}
	return rels, nil
}

// AddObservations appends observations to existing entities.
//
// Expected:
//   - observations is a slice of ObservationEntry structs to add.
//
// Returns:
//   - The added observations (echoed back).
//   - An error if embedding or upsert fails.
//
// Side effects:
//   - Updates the entity vector points with new observations.
func (v *VectorStoreMemoryClient) AddObservations(ctx context.Context, observations []ObservationEntry) ([]ObservationEntry, error) {
	points := make([]VectorPoint, 0, len(observations))
	for _, o := range observations {
		emb, err := v.Embedder.Embed(ctx, o.EntityName)
		if err != nil {
			return nil, fmt.Errorf("embedding entity for observation: %w", err)
		}
		points = append(points, VectorPoint{
			ID:      o.EntityName,
			Vector:  emb,
			Payload: map[string]any{"observations": o.Contents},
		})
	}
	if err := v.Store.Upsert(ctx, v.Collection, points, false); err != nil {
		return nil, fmt.Errorf("upserting observations: %w", err)
	}
	return observations, nil
}

// DeleteEntities removes entities (and cascades relations).
//
// Expected:
//   - entityNames is a slice of entity names to delete.
//
// Returns:
//   - The deleted entity names (echoed back).
//   - An error if deletion fails.
//
// Side effects:
//   - Not implemented: VectorStoreClient does not support deletion. (Stub)
func (v *VectorStoreMemoryClient) DeleteEntities(_ context.Context, entityNames []string) ([]string, error) {
	// Not supported by VectorStoreClient interface; stub for compatibility.
	return entityNames, nil
}

// DeleteObservations removes specific observations from entities.
//
// Expected:
//   - deletions is a slice of DeletionEntry structs specifying observations to remove.
//
// Returns:
//   - An error if deletion fails.
//
// Side effects:
//   - Not implemented: VectorStoreClient does not support partial observation deletion. (Stub)
func (v *VectorStoreMemoryClient) DeleteObservations(_ context.Context, _ []DeletionEntry) error {
	// Not supported by VectorStoreClient interface; stub for compatibility.
	return nil
}

// DeleteRelations removes specific relations from the graph.
//
// Expected:
//   - rels is a slice of Relation structs to delete.
//
// Returns:
//   - An error if deletion fails.
//
// Side effects:
//   - Not implemented: VectorStoreClient does not support deletion. (Stub)
func (v *VectorStoreMemoryClient) DeleteRelations(_ context.Context, _ []Relation) error {
	// Not supported by VectorStoreClient interface; stub for compatibility.
	return nil
}

// ReadGraph returns the entire knowledge graph.
//
// Expected:
//   - No arguments.
//
// Returns:
//   - A KnowledgeGraph with all entities and relations (empty; not supported).
//   - An error if retrieval fails.
//
// Side effects:
//   - Not implemented: VectorStoreClient does not support full graph read. (Stub)
func (v *VectorStoreMemoryClient) ReadGraph(_ context.Context) (KnowledgeGraph, error) {
	// Not supported by VectorStoreClient interface; stub for compatibility.
	return KnowledgeGraph{}, nil
}

// SearchNodes performs a full-text search for entities.
//
// Expected:
//   - query is the search string.
//
// Returns:
//   - A slice of matching Entity values (empty if none found or on error).
//   - An error if embedding or search fails.
//
// Side effects:
//   - Sends a search request to the vector store.
func (v *VectorStoreMemoryClient) SearchNodes(ctx context.Context, query string) ([]Entity, error) {
	emb, err := v.Embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding search query: %w", err)
	}
	points, err := v.Store.Search(ctx, v.Collection, emb, 10)
	if err != nil {
		return nil, fmt.Errorf("vector search: %w", err)
	}
	entities := make([]Entity, 0, len(points))
	for _, p := range points {
		name := p.ID
		var entityType string
		var obs []string
		if et, ok := p.Payload["entityType"].(string); ok {
			entityType = et
		}
		if o, ok := p.Payload["observations"].([]string); ok {
			obs = o
		}
		entities = append(entities, Entity{Name: name, EntityType: entityType, Observations: obs})
	}
	return entities, nil
}

// OpenNodes retrieves specific entities and their relations.
//
// Expected:
//   - names is a slice of entity names to retrieve.
//
// Returns:
//   - A KnowledgeGraph with the requested entities (relations not supported).
//   - An error if retrieval fails.
//
// Side effects:
//   - Sends search requests for each entity name.
func (v *VectorStoreMemoryClient) OpenNodes(ctx context.Context, names []string) (KnowledgeGraph, error) {
	entities := make([]Entity, 0, len(names))
	for _, name := range names {
		emb, err := v.Embedder.Embed(ctx, name)
		if err != nil {
			return KnowledgeGraph{}, fmt.Errorf("embedding entity name: %w", err)
		}
		points, err := v.Store.Search(ctx, v.Collection, emb, 1)
		if err != nil || len(points) == 0 {
			continue
		}
		p := points[0]
		var entityType string
		var obs []string
		if et, ok := p.Payload["entityType"].(string); ok {
			entityType = et
		}
		if o, ok := p.Payload["observations"].([]string); ok {
			obs = o
		}
		entities = append(entities, Entity{Name: name, EntityType: entityType, Observations: obs})
	}
	return KnowledgeGraph{Entities: entities, Relations: nil}, nil
}

// WriteLearningRecord persists a learning record to storage.
//
// Expected:
//   - record is a non-nil pointer to Record.
//
// Returns:
//   - An error if embedding or upsert fails.
//
// Side effects:
//   - Stores the record as a vector point in the collection.
func (v *VectorStoreMemoryClient) WriteLearningRecord(record *Record) error {
	if record == nil {
		return errors.New("record is nil")
	}
	// Use AgentID as the embedding text since that's the primary identifier
	emb, err := v.Embedder.Embed(context.Background(), record.AgentID)
	if err != nil {
		return fmt.Errorf("embedding record: %w", err)
	}
	p := VectorPoint{
		ID:     record.AgentID,
		Vector: emb,
		Payload: map[string]any{
			"agent_id":   record.AgentID,
			"tools_used": record.ToolsUsed,
			"outcome":    record.Outcome,
		},
	}
	if err := v.Store.Upsert(context.Background(), v.Collection, []VectorPoint{p}, false); err != nil {
		return fmt.Errorf("upserting learning record: %w", err)
	}
	return nil
}
