// Package learning provides the MemoryClient interface for interacting with an MCP-compatible memory server.
//
// This interface defines methods for all 9 MCP memory operations, enabling creation, mutation, and querying of a knowledge graph.
// It is designed to be implemented by adapters that communicate with MCP servers, with no dependency on internal/memory/.
//
// All methods accept context.Context for cancellation and deadlines, and return Go types or errors.
//
// See MCP protocol documentation for details on argument and result semantics.
package learning

import (
	"context"
)

// Entity represents a knowledge graph entity.
type Entity struct {
	Name         string   `json:"name"`
	EntityType   string   `json:"entityType"`
	Observations []string `json:"observations"`
}

// Relation represents a directed relation between entities.
type Relation struct {
	From         string `json:"from"`
	RelationType string `json:"relationType"`
	To           string `json:"to"`
}

// KnowledgeGraph is a snapshot of the graph state.
type KnowledgeGraph struct {
	Entities  []Entity   `json:"entities"`
	Relations []Relation `json:"relations"`
}

// ObservationEntry associates observations with an entity.
type ObservationEntry struct {
	EntityName string   `json:"entityName"`
	Contents   []string `json:"contents"`
}

// DeletionEntry specifies which observations to remove from an entity.
type DeletionEntry struct {
	EntityName   string   `json:"entityName"`
	Observations []string `json:"observations"`
}

// MemoryClient defines all MCP memory operations as idiomatic Go methods.
type MemoryClient interface {
	// CreateEntities creates new entities in the knowledge graph.
	CreateEntities(ctx context.Context, entities []Entity) ([]Entity, error)

	// CreateRelations establishes directed relations between entities.
	CreateRelations(ctx context.Context, relations []Relation) ([]Relation, error)

	// AddObservations appends observations to existing entities.
	AddObservations(ctx context.Context, observations []ObservationEntry) ([]ObservationEntry, error)

	// DeleteEntities removes entities (and cascades relations).
	DeleteEntities(ctx context.Context, entityNames []string) ([]string, error)

	// DeleteObservations removes specific observations from entities.
	DeleteObservations(ctx context.Context, deletions []DeletionEntry) error

	// DeleteRelations removes specific relations from the graph.
	DeleteRelations(ctx context.Context, relations []Relation) error

	// ReadGraph returns the entire knowledge graph.
	ReadGraph(ctx context.Context) (KnowledgeGraph, error)

	// SearchNodes performs a full-text search for entities.
	SearchNodes(ctx context.Context, query string) ([]Entity, error)

	// OpenNodes retrieves specific entities and their relations.
	OpenNodes(ctx context.Context, names []string) (KnowledgeGraph, error)

	// WriteLearningRecord persists a learning record to storage.
	WriteLearningRecord(record *LearningRecord) error
}
