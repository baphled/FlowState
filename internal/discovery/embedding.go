package discovery

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// EmbeddingProvider defines the interface for embedding generation.
type EmbeddingProvider interface {
	// Embed generates a vector embedding for the given request.
	Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error)
}

// AgentMatch represents a matched agent with confidence score and reasoning.
type AgentMatch struct {
	AgentID    string
	Confidence float64
	Reason     string
}

// EmbeddingDiscovery provides agent discovery using embedding-based cosine similarity.
type EmbeddingDiscovery struct {
	mu        sync.RWMutex
	registry  *agent.Registry
	embedder  EmbeddingProvider
	agentVecs map[string][]float64
}

// NewEmbeddingDiscovery creates a new EmbeddingDiscovery with the given registry and embedder.
//
// Expected:
//   - registry is a non-nil agent Registry containing agent manifests.
//   - embedder is a non-nil EmbeddingProvider for generating text embeddings.
//
// Returns:
//   - A configured EmbeddingDiscovery instance.
//
// Side effects:
//   - None.
func NewEmbeddingDiscovery(registry *agent.Registry, embedder EmbeddingProvider) *EmbeddingDiscovery {
	return &EmbeddingDiscovery{
		registry:  registry,
		embedder:  embedder,
		agentVecs: make(map[string][]float64),
	}
}

// IndexAgents embeds all agents' CapabilityDescription at startup.
//
// Expected:
//   - ctx is a valid context for the embedding operation.
//
// Returns:
//   - nil on success.
//   - An error if embedding fails for any agent.
//
// Side effects:
//   - Populates the internal agentVecs map with embedding vectors.
func (ed *EmbeddingDiscovery) IndexAgents(ctx context.Context) error {
	manifests := ed.registry.List()
	if manifests == nil {
		return nil
	}

	vecs := make(map[string][]float64, len(manifests))
	for _, m := range manifests {
		capDesc := m.Capabilities.CapabilityDescription
		if capDesc == "" {
			continue
		}

		vec, err := ed.embedder.Embed(ctx, provider.EmbedRequest{
			Input: capDesc,
		})
		if err != nil {
			continue
		}

		vecs[m.ID] = vec
	}

	ed.mu.Lock()
	defer ed.mu.Unlock()
	ed.agentVecs = vecs
	return nil
}

// Match embeds the task description and returns ranked agent matches using cosine similarity.
//
// Expected:
//   - ctx is a valid context for the embedding operation.
//   - taskDescription is the task to match against agent capabilities.
//
// Returns:
//   - A slice of AgentMatch sorted by descending confidence.
//   - An empty slice if no agents match or the embedder is unavailable.
//
// Side effects:
//   - None.
func (ed *EmbeddingDiscovery) Match(ctx context.Context, taskDescription string) ([]AgentMatch, error) {
	if ed.embedder == nil || taskDescription == "" {
		return []AgentMatch{}, nil
	}

	taskVec, err := ed.embedder.Embed(ctx, provider.EmbedRequest{
		Input: taskDescription,
	})
	if err != nil {
		return []AgentMatch{}, fmt.Errorf("embedding task description: %w", err)
	}

	ed.mu.RLock()
	vecs := ed.agentVecs
	ed.mu.RUnlock()

	if len(vecs) == 0 {
		return []AgentMatch{}, nil
	}

	var matches []AgentMatch
	for agentID, agentVec := range vecs {
		score := CosineSimilarity(taskVec, agentVec)
		if score > 0 {
			matches = append(matches, AgentMatch{
				AgentID:    agentID,
				Confidence: score,
				Reason:     "capability embedding similarity",
			})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].Confidence > matches[j].Confidence
	})

	return matches, nil
}

// CosineSimilarity computes the cosine similarity between two vectors.
//
// Expected:
//   - a and b are non-empty vectors of equal length.
//
// Returns:
//   - A value in [0, 1] representing similarity; 1.0 for identical direction, 0.0 for orthogonal.
//   - 0 if either vector has zero magnitude or lengths differ.
//
// Side effects:
//   - None.
func CosineSimilarity(a, b []float64) float64 {
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
