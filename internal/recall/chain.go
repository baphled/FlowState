package recall

import (
	"context"
	"math"
	"sort"
	"sync"

	"github.com/google/uuid"

	"github.com/baphled/flowstate/internal/provider"
)

// ChainContextStore provides shared semantic context for all agents in a delegation chain.
type ChainContextStore interface {
	// Append adds a message from the given agent to the chain's shared context.
	Append(agentID string, msg provider.Message) error
	// Search returns semantically relevant messages from any agent in the chain.
	Search(ctx context.Context, query string, topK int) ([]SearchResult, error)
	// GetByAgent returns the most recent messages from a specific agent.
	// When agentID is empty, messages from all agents are returned.
	GetByAgent(agentID string, last int) ([]provider.Message, error)
	// ChainID returns the unique identifier for the chain this store is scoped to.
	ChainID() string
}

// chainEntry is an internal record associating a message with its originating agent.
type chainEntry struct {
	agentID string
	message provider.Message
	vector  []float64
}

// InMemoryChainStore implements ChainContextStore using an in-memory store.
//
// All messages are stored in insertion order. Concurrent access is protected
// by a read-write mutex.
type InMemoryChainStore struct {
	chainID           string
	entries           []chainEntry
	embeddingProvider provider.Provider
	mu                sync.RWMutex
}

// NewInMemoryChainStore creates a new in-memory chain context store with a unique ChainID.
//
// Expected:
//   - embeddingProvider may be nil; if nil, Search falls back to recency-ordered retrieval.
//
// Returns:
//   - A fully initialised InMemoryChainStore with a UUID v4 ChainID.
//
// Side effects:
//   - None.
func NewInMemoryChainStore(embeddingProvider provider.Provider) *InMemoryChainStore {
	return &InMemoryChainStore{
		chainID:           uuid.NewString(),
		entries:           make([]chainEntry, 0),
		embeddingProvider: embeddingProvider,
	}
}

// ChainID returns the unique identifier for this chain context store.
//
// Returns:
//   - A UUID v4 string identifying this chain.
//
// Side effects:
//   - None.
func (s *InMemoryChainStore) ChainID() string {
	return s.chainID
}

// Append adds a message from the given agent to the chain's shared context.
//
// Expected:
//   - agentID is a non-empty string identifying the originating agent.
//   - msg is a valid provider.Message.
//
// Returns:
//   - nil on success.
//
// Side effects:
//   - Appends to the in-memory entries slice under a write lock.
//   - If an embedding provider is configured, stores the embedding vector for the message.
func (s *InMemoryChainStore) Append(agentID string, msg provider.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := chainEntry{agentID: agentID, message: msg}
	if s.embeddingProvider != nil {
		vector, err := s.embeddingProvider.Embed(context.Background(), provider.EmbedRequest{Input: msg.Content})
		if err == nil {
			entry.vector = vector
		}
	}
	s.entries = append(s.entries, entry)
	return nil
}

// Search returns semantically relevant messages from across all agents in the chain.
//
// When the embedding provider is nil, Search falls back to returning the most recent
// topK messages across all agents via GetByAgent.
//
// Expected:
//   - ctx is a valid context for the embedding operation.
//   - query is the search query string.
//   - topK is the maximum number of results to return.
//
// Returns:
//   - A slice of SearchResult. When degrading, Score is set to 0 for all results.
//   - An error if the embedding operation fails unrecoverably.
//
// Side effects:
//   - May call the embedding provider.
func (s *InMemoryChainStore) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	if s.embeddingProvider == nil {
		return s.degradedSearch(topK)
	}

	vector, err := s.embeddingProvider.Embed(ctx, provider.EmbedRequest{Input: query})
	if err != nil {
		return s.degradedSearch(topK)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.searchByVector(vector, topK), nil
}

// degradedSearch returns the most recent topK messages as SearchResults with score 0.
//
// Expected:
//   - topK is the maximum number of results to return.
//
// Returns:
//   - A slice of SearchResult with Score 0, or an empty slice if no entries exist.
//   - An error (currently always nil).
//
// Side effects:
//   - None.
func (s *InMemoryChainStore) degradedSearch(topK int) ([]SearchResult, error) {
	messages, err := s.GetByAgent("", topK)
	if err != nil || len(messages) == 0 {
		return []SearchResult{}, err
	}
	results := make([]SearchResult, len(messages))
	for i, m := range messages {
		results[i] = SearchResult{Message: m, Score: 0}
	}
	return results, nil
}

// cosineSimilarity returns the cosine similarity between two vectors.
//
// Expected:
//   - a and b are non-nil, equal-length float64 slices.
//
// Returns:
//   - Cosine similarity as a float64 in [-1, 1].
//
// Side effects:
//   - None.
func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// searchByVector performs cosine similarity search against all stored entries.
//
// Expected:
//   - queryVec is the embedding vector to compare against stored entries.
//   - topK is the maximum number of results to return.
//   - The caller holds at least a read lock on s.mu.
//
// Returns:
//   - A slice of SearchResult sorted by descending similarity score, capped at topK.
//
// Side effects:
//   - None.
func (s *InMemoryChainStore) searchByVector(queryVec []float64, topK int) []SearchResult {
	var results []SearchResult
	for i := range s.entries {
		entry := &s.entries[i]
		if entry.message.Role == "tool" || len(entry.vector) == 0 {
			continue
		}
		score := cosineSimilarity(queryVec, entry.vector)
		results = append(results, SearchResult{
			Message: entry.message,
			Score:   score,
		})
	}
	if len(results) == 0 {
		return results
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if topK > 0 && len(results) > topK {
		results = results[:topK]
	}
	return results
}

// GetByAgent returns the most recent messages from the specified agent.
//
// When agentID is empty, messages from all agents are returned in insertion order,
// limited to the last N entries.
//
// Expected:
//   - agentID identifies the agent whose messages to retrieve; empty string returns all.
//   - last is the maximum number of messages to return.
//
// Returns:
//   - A slice of messages, up to last in count.
//   - An error (currently always nil for the in-memory implementation).
//
// Side effects:
//   - None.
func (s *InMemoryChainStore) GetByAgent(agentID string, last int) ([]provider.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []provider.Message
	for i := range s.entries {
		if agentID == "" || s.entries[i].agentID == agentID {
			matched = append(matched, s.entries[i].message)
		}
	}

	if len(matched) == 0 {
		return []provider.Message{}, nil
	}

	if last > 0 && len(matched) > last {
		matched = matched[len(matched)-last:]
	}

	return matched, nil
}
