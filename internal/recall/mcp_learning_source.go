package recall

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/provider"
)

// MCPLearningSource implements LearningSource using MemoryClient and Provider.
// It provides knowledge access, observation recording, and LLM-based synthesis via background goroutines.
type MCPLearningSource struct {
	client   learning.MemoryClient
	provider provider.Provider
}

// NewMCPLearningSource creates a new MCPLearningSource.
//
// Expected:
//   - client implements learning.MemoryClient.
//   - provider implements provider.Provider.
//
// Returns:
//   - A learning source backed by the supplied memory client and provider.
//
// Side effects:
//   - None.
func NewMCPLearningSource(client learning.MemoryClient, prov provider.Provider) *MCPLearningSource {
	return &MCPLearningSource{
		client:   client,
		provider: prov,
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

// Synthesize generates insights from observations using the LLM provider.
// It launches a background goroutine to synthesize and returns immediately without waiting.
//
// Expected:
//   - ctx is valid context for background goroutine setup.
//   - entity is the entity name to synthesize.
//   - observations contains strings to synthesize.
//
// Returns:
//   - An error if context setup fails; nil if goroutine launches successfully.
//   - Provider errors and memory client errors are logged in the goroutine and do not block the caller.
//
// Side effects:
//   - Launches a background goroutine that calls provider.Chat and adds observations to memory client.
//   - Does not wait for goroutine completion (non-blocking).
func (m *MCPLearningSource) Synthesize(ctx context.Context, entity string, observations []string) error {
	// Launch background goroutine to synthesize
	go func() {
		// Create synthesis prompt from observations
		var promptBuilder strings.Builder
		fmt.Fprintf(&promptBuilder, "Synthesize insights from the following observations about %s:\n", entity)
		for _, obs := range observations {
			fmt.Fprintf(&promptBuilder, "- %s\n", obs)
		}

		// Call provider to synthesize
		req := provider.ChatRequest{
			Provider: "default",
			Messages: []provider.Message{
				{
					Role:    "user",
					Content: promptBuilder.String(),
				},
			},
		}

		resp, err := m.provider.Chat(ctx, req)
		if err != nil {
			// Log error but don't crash
			fmt.Printf("[synthesis error] failed to call provider: %v\n", err)
			return
		}

		// Extract synthesized content
		synthesized := resp.Message.Content

		// Store as observation
		entries := []learning.ObservationEntry{
			{
				EntityName: entity,
				Contents: []string{
					"Synthesized: " + synthesized,
				},
			},
		}

		_, err = m.client.AddObservations(ctx, entries)
		if err != nil {
			// Log error but don't crash
			fmt.Printf("[synthesis error] failed to store observations: %v\n", err)
			return
		}
	}()

	return nil
}
