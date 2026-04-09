package qdrant

import (
	"context"
	"fmt"
	"time"

	"github.com/baphled/flowstate/internal/recall"
)

// Source implements recall.Source using Qdrant vector search.
type Source struct {
	store      VectorStore
	embedder   Embedder
	collection string
}

// NewSource creates a Source backed by the supplied VectorStore and Embedder.
//
// Expected:
//   - store is a non-nil VectorStore.
//   - embedder is a non-nil Embedder.
//   - collection is the Qdrant collection name.
//
// Returns:
//   - A new *Source ready to serve recall queries.
//
// Side effects:
//   - None.
func NewSource(store VectorStore, embedder Embedder, collection string) *Source {
	return &Source{store: store, embedder: embedder, collection: collection}
}

// Query returns recall observations matching the supplied query from Qdrant.
//
// Expected:
//   - ctx is non-nil.
//   - query is the text to embed and search for.
//   - limit caps the number of observations returned.
//
// Returns:
//   - A slice of Observations converted from Qdrant ScoredPoints (empty slice if none found).
//   - An error if embedding or vector search fails.
//
// Side effects:
//   - Sends a search request to the configured Qdrant collection.
func (s *Source) Query(ctx context.Context, query string, limit int) ([]recall.Observation, error) {
	vector, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("qdrant source embed: %w", err)
	}

	points, err := s.store.Search(ctx, s.collection, vector, limit)
	if err != nil {
		return nil, fmt.Errorf("qdrant source search: %w", err)
	}

	observations := make([]recall.Observation, 0, len(points))
	for _, p := range points {
		obs := recall.Observation{
			ID:     p.ID,
			Source: "qdrant:" + s.collection,
		}
		if v, ok := p.Payload["agent_id"].(string); ok {
			obs.AgentID = v
		}
		if v, ok := p.Payload["content"].(string); ok {
			obs.Content = v
		}
		if v, ok := p.Payload["timestamp"].(string); ok {
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				obs.Timestamp = t
			}
		}
		observations = append(observations, obs)
	}
	return observations, nil
}

// compile-time assertion: Source implements recall.Source.
var _ recall.Source = (*Source)(nil)
