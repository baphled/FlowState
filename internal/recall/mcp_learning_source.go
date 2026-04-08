package recall

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/learning"
)

// MCPLearningSource implements LearningSource using MemoryClient.
// It provides knowledge access and observation recording via the memory client.
type MCPLearningSource struct {
	client learning.MemoryClient
}

// NewMCPLearningSource creates a new MCPLearningSource.
//
// Expected:
//   - client implements learning.MemoryClient.
//
// Returns:
//   - A learning source backed by the supplied memory client.
//
// Side effects:
//   - None.
func NewMCPLearningSource(client learning.MemoryClient) *MCPLearningSource {
	return &MCPLearningSource{
		client: client,
	}
}

// Query searches for knowledge nodes using search_nodes.
//
// Expected:
//   - ctx is valid for the memory client call.
//   - query contains the search term.
//
// Returns:
//   - Matching nodes as a slice of empty-interface values.
//   - An error when the memory client call fails.
//
// Side effects:
//   - Calls the configured memory client.
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

// Observe records new observations via add_observations.
//
// Expected:
//   - ctx is valid for the memory client call.
//   - observations contains learning.ObservationEntry values.
//
// Returns:
//   - An error when the memory client call fails or an observation has the wrong type.
//
// Side effects:
//   - Calls the configured memory client.
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

// Synthesize provides knowledge synthesis.
//
// Expected:
//   - nodes contains zero or more values to synthesise.
//
// Returns:
//   - A synthesised summary string.
//   - An error when synthesis cannot proceed.
//
// Side effects:
//   - None.
func (m *MCPLearningSource) Synthesize(_ context.Context, nodes []any) (string, error) {
	if len(nodes) == 0 {
		return "", nil
	}
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = fmt.Sprint(n)
	}
	return "Synthesis: " + strings.Join(parts, ", "), nil
}
