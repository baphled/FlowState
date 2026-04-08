package recall

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/learning"
)

// MCPLearningSource implements LearningSource using MemoryClient
// Provides knowledge access and observation recording via the memory client
// Satisfies the LearningSource interface

type MCPLearningSource struct {
	client learning.MemoryClient
}

// NewMCPLearningSource creates a new MCPLearningSource
func NewMCPLearningSource(client learning.MemoryClient) *MCPLearningSource {
	return &MCPLearningSource{
		client: client,
	}
}

// Query searches for knowledge nodes using search_nodes
func (m *MCPLearningSource) Query(ctx context.Context, query string) ([]any, error) {
	entities, err := m.client.SearchNodes(ctx, query)
	if err != nil {
		return nil, err
	}
	results := make([]any, len(entities))
	for i, e := range entities {
		results[i] = e
	}
	return results, nil
}

// Observe records new observations via add_observations
func (m *MCPLearningSource) Observe(ctx context.Context, observations []any) error {
	entries := make([]learning.ObservationEntry, len(observations))
	for i, o := range observations {
		entry, ok := o.(learning.ObservationEntry)
		if !ok {
			return errors.New("invalid observation type: must be ObservationEntry")
		}
		entries[i] = entry
	}
	_, err := m.client.AddObservations(ctx, entries)
	return err
}

// Synthesize provides knowledge synthesis
func (m *MCPLearningSource) Synthesize(ctx context.Context, nodes []any) (string, error) {
	if len(nodes) == 0 {
		return "", nil
	}
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = fmt.Sprint(n)
	}
	return "Synthesis: " + strings.Join(parts, ", "), nil
}
