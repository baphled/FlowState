package learning

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// VectorEmbedder computes vector embeddings from text for the learning store.
type VectorEmbedder interface {
	// Embed returns the embedding vector for the supplied text.
	Embed(ctx context.Context, text string) ([]float64, error)
}

// VectorPoint represents a point for vector store upsert operations.
type VectorPoint struct {
	ID      string
	Vector  []float64
	Payload map[string]any
}

// ScoredVectorPoint represents a vector store search result with relevance score.
type ScoredVectorPoint struct {
	ID      string
	Score   float64
	Payload map[string]any
}

// VectorStoreClient defines vector storage operations needed by Mem0LearningStore.
type VectorStoreClient interface {
	// Upsert stores or updates points within a collection.
	Upsert(ctx context.Context, collection string, points []VectorPoint, wait bool) error
	// Search finds the nearest points for the supplied vector.
	Search(ctx context.Context, collection string, vector []float64, limit int) ([]ScoredVectorPoint, error)
}

// Mem0LearningStore implements Store using vector search for persistence.
type Mem0LearningStore struct {
	store      VectorStoreClient
	embedder   VectorEmbedder
	collection string
}

// NewMem0LearningStore creates a Mem0LearningStore backed by the supplied vector store and embedder.
//
// Expected:
//   - store is a non-nil VectorStoreClient.
//   - embedder is a non-nil VectorEmbedder.
//   - collection is the vector collection name.
//
// Returns:
//   - A new *Mem0LearningStore ready to capture and query learning entries.
//
// Side effects:
//   - None.
func NewMem0LearningStore(store VectorStoreClient, embedder VectorEmbedder, collection string) *Mem0LearningStore {
	return &Mem0LearningStore{store: store, embedder: embedder, collection: collection}
}

// Capture stores a learning entry using vector embedding.
//
// Expected:
//   - entry is a populated Entry with at least UserMessage set.
//
// Returns:
//   - nil on success.
//   - An error if embedding or upsert fails.
//
// Side effects:
//   - Writes the entry as a vector point in the configured collection.
func (m *Mem0LearningStore) Capture(entry Entry) error {
	ctx := context.Background()
	vector, err := m.embedder.Embed(ctx, entry.UserMessage)
	if err != nil {
		return fmt.Errorf("mem0 capture embed: %w", err)
	}

	sourceID := strconv.FormatInt(entry.Timestamp.UnixNano(), 10)
	p := VectorPoint{
		ID:     PointIDFromSource(sourceID),
		Vector: vector,
		Payload: map[string]any{
			"source_id":  sourceID,
			"agent_id":   entry.AgentID,
			"content":    entry.UserMessage,
			"response":   entry.Response,
			"tools_used": entry.ToolsUsed,
			"outcome":    entry.Outcome,
			"timestamp":  entry.Timestamp.Format(time.RFC3339),
		},
	}

	if err := m.store.Upsert(ctx, m.collection, []VectorPoint{p}, false); err != nil {
		return fmt.Errorf("mem0 capture upsert: %w", err)
	}
	return nil
}

// Query searches for learning entries matching the supplied query string.
//
// Expected:
//   - query is the search text to embed.
//
// Returns:
//   - A slice of matching Entry values (empty if none found or on error).
//
// Side effects:
//   - Sends a search request to the configured collection.
func (m *Mem0LearningStore) Query(query string) []Entry {
	ctx := context.Background()
	vector, err := m.embedder.Embed(ctx, query)
	if err != nil {
		return []Entry{}
	}

	points, err := m.store.Search(ctx, m.collection, vector, 10)
	if err != nil {
		return []Entry{}
	}

	entries := make([]Entry, 0, len(points))
	for _, p := range points {
		entries = append(entries, scoredPointToEntry(p))
	}
	return entries
}

// scoredPointToEntry converts a ScoredVectorPoint payload into a learning Entry.
//
// Expected:
//   - p contains a Payload map with string-typed values for known keys.
//
// Returns:
//   - An Entry populated from the payload; missing keys result in zero values.
//
// Side effects:
//   - None.
func scoredPointToEntry(p ScoredVectorPoint) Entry {
	e := Entry{}
	if v, ok := p.Payload["agent_id"].(string); ok {
		e.AgentID = v
	}
	if v, ok := p.Payload["content"].(string); ok {
		e.UserMessage = v
	}
	if v, ok := p.Payload["response"].(string); ok {
		e.Response = v
	}
	if v, ok := p.Payload["outcome"].(string); ok {
		e.Outcome = v
	}
	if v, ok := p.Payload["timestamp"].(string); ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			e.Timestamp = t
		}
	}
	if v, ok := p.Payload["tools_used"].([]any); ok {
		for _, t := range v {
			if s, ok := t.(string); ok {
				e.ToolsUsed = append(e.ToolsUsed, s)
			}
		}
	}
	return e
}

var _ Store = (*Mem0LearningStore)(nil)
