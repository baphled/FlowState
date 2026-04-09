// Package learning provides structured distillation of learning observations.
package learning

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Distiller defines the interface for distilling learning entries into structured knowledge.
type Distiller interface {
	// Distill processes a learning entry and extracts structured observations,
	// creating entities and relations in the knowledge graph.
	Distill(entry Entry) (Entity, []Relation, error)
}

// StructuredDistiller implements Distiller for extracting structured fields from learning entries.
type StructuredDistiller struct {
	client MemoryClient
}

// NewStructuredDistiller creates a new StructuredDistiller with the given memory client.
func NewStructuredDistiller(client MemoryClient) *StructuredDistiller {
	return &StructuredDistiller{client: client}
}

// Distill extracts structured fields from the entry, creates an observation entity,
// and establishes relations to tools used, writing everything to the knowledge graph.
func (d *StructuredDistiller) Distill(entry Entry) (Entity, []Relation, error) {
	ctx := context.Background()

	// Create the observation entity
	entity := Entity{
		Name:         entry.AgentID,
		EntityType:   "observation",
		Observations: d.extractObservations(entry),
	}

	// Create relations for tools used
	var relations []Relation
	for _, tool := range entry.ToolsUsed {
		relations = append(relations, Relation{
			From:         entry.AgentID,
			RelationType: "used_tool",
			To:           tool,
		})
	}

	// Write to knowledge graph
	_, err := d.client.CreateEntities(ctx, []Entity{entity})
	if err != nil {
		return Entity{}, nil, fmt.Errorf("creating entity: %w", err)
	}

	_, err = d.client.CreateRelations(ctx, relations)
	if err != nil {
		return Entity{}, nil, fmt.Errorf("creating relations: %w", err)
	}

	return entity, relations, nil
}

// extractObservations builds the observations slice from the entry fields.
func (d *StructuredDistiller) extractObservations(entry Entry) []string {
	return []string{
		"AgentID: " + entry.AgentID,
		fmt.Sprintf("ToolsUsed: [%s]", strings.Join(entry.ToolsUsed, " ")),
		"Outcome: " + entry.Outcome,
		"Timestamp: " + entry.Timestamp.Format(time.RFC3339),
	}
}
