package qdrant

import "context"

// Embedder computes vector embeddings from text.
type Embedder interface {
	// Embed returns the embedding vector for the supplied text.
	//
	// Expected:
	//   - ctx is non-nil.
	//   - text is non-empty.
	//
	// Returns:
	//   - A float64 slice of the embedding vector.
	//   - An error if the embedding computation fails.
	//
	// Side effects:
	//   - None.
	Embed(ctx context.Context, text string) ([]float64, error)
}

// ProviderEmbedder is the interface satisfied by provider implementations for embedding.
type ProviderEmbedder interface {
	// Embed returns the embedding vector for the supplied text.
	//
	// Expected:
	//   - ctx is non-nil.
	//   - text is a non-empty string to embed.
	//
	// Returns:
	//   - A float64 slice of the embedding vector.
	//   - An error if the computation fails.
	//
	// Side effects:
	//   - May send a network request to the embedding model.
	Embed(ctx context.Context, text string) ([]float64, error)
}

// OllamaEmbedder wraps a provider to compute embeddings.
type OllamaEmbedder struct {
	provider ProviderEmbedder
}

// NewOllamaEmbedder creates an OllamaEmbedder backed by the supplied provider.
//
// Expected:
//   - p is a non-nil provider implementing Embed.
//
// Returns:
//   - A new *OllamaEmbedder ready to compute embeddings.
//
// Side effects:
//   - None.
func NewOllamaEmbedder(p ProviderEmbedder) *OllamaEmbedder {
	return &OllamaEmbedder{provider: p}
}

// Embed returns the embedding vector for the supplied text.
//
// Expected:
//   - ctx is non-nil.
//   - text is a non-empty string to embed.
//
// Returns:
//   - A float64 slice of the embedding vector on success.
//   - An error if the underlying provider fails.
//
// Side effects:
//   - Delegates to the configured ProviderEmbedder.
func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float64, error) {
	return e.provider.Embed(ctx, text)
}

// MockEmbedder is a deterministic test double for Embedder.
type MockEmbedder struct {
	Vector []float64
	Err    error
}

// Embed returns the configured deterministic vector or error.
//
// Expected:
//   - ctx is non-nil.
//
// Returns:
//   - The configured Vector on success.
//   - The configured Err on failure.
//
// Side effects:
//   - None.
func (m *MockEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return m.Vector, m.Err
}

var _ Embedder = (*OllamaEmbedder)(nil)
var _ Embedder = (*MockEmbedder)(nil)
