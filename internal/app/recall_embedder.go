package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/baphled/flowstate/internal/provider"
	qdrantrecall "github.com/baphled/flowstate/internal/recall/qdrant"
)

// Recall embedder routing constants.
//
// These values hard-route the recall and distiller embedder to a local Ollama
// instance running nomic-embed-text. The model produces 768-dim Cosine vectors
// — matching the existing flowstate Qdrant collection shape (768-dim Cosine).
//
// Previously the embedder was wired to whichever provider.Provider was passed
// to buildRecallBroker / buildDistiller — typically the user's chat provider
// (anthropic), which does not expose an embeddings endpoint. Every recall
// query failed with "anthropic does not support embeddings".
//
// These constants live in one place so a future configurable-embedder refactor
// has a single seam to replace.
const (
	defaultRecallEmbeddingModel = "nomic-embed-text"
	defaultRecallEmbeddingDim   = 768
)

// errRecallEmbedderUnavailable is returned by the no-op embedder used when
// no Ollama provider is available at startup. Callers see a clear sentinel
// instead of a silent zero vector that would corrupt the Qdrant collection.
var errRecallEmbedderUnavailable = errors.New("recall embedder unavailable: Ollama provider not configured")

// embedRequester is the narrow interface satisfied by *ollama.Provider — it
// is the only surface of provider.Provider the recall path needs. Defining it
// here keeps the factory testable without constructing a full chat provider.
type embedRequester interface {
	Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error)
}

// ollamaEmbedderAdapter bridges an Ollama-style embedRequester to qdrant.Embedder.
//
// It pins the embedding model to defaultRecallEmbeddingModel so callers cannot
// accidentally produce vectors of the wrong dimension for the configured
// Qdrant collection.
type ollamaEmbedderAdapter struct {
	provider embedRequester
	model    string
}

// Embed returns the embedding vector for text using the pinned embedding model.
//
// Expected:
//   - ctx is non-nil.
//   - text is the string to embed.
//
// Returns:
//   - A float64 slice of the embedding vector on success.
//   - An error wrapping the underlying provider failure.
//
// Side effects:
//   - Sends a request to the underlying Ollama embeddings API.
func (a *ollamaEmbedderAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	return a.provider.Embed(ctx, provider.EmbedRequest{Input: text, Model: a.model})
}

// noopRecallEmbedder is the fallback embedder used when no Ollama provider is
// available. It returns errRecallEmbedderUnavailable so the Qdrant source's
// failure is visible (logged once at broker construction) rather than silent.
type noopRecallEmbedder struct{}

// Embed always returns errRecallEmbedderUnavailable.
//
// Expected:
//   - ctx is non-nil (unused).
//   - text is unused.
//
// Returns:
//   - A nil slice and errRecallEmbedderUnavailable.
//
// Side effects:
//   - None.
func (noopRecallEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	return nil, errRecallEmbedderUnavailable
}

// newRecallEmbedder returns a qdrant.Embedder routed to the supplied Ollama
// provider with the model pinned to defaultRecallEmbeddingModel.
//
// Expected:
//   - ollamaProvider may be nil — when nil, a no-op embedder is returned so
//     the broker still constructs and non-Qdrant recall sources keep working.
//
// Returns:
//   - A non-nil qdrant.Embedder. When ollamaProvider is nil, the embedder
//     returns errRecallEmbedderUnavailable from every Embed call.
//
// Side effects:
//   - When ollamaProvider is nil, logs a single warning so operators see
//     the degradation explicitly instead of silently losing vector recall.
func newRecallEmbedder(ollamaProvider embedRequester) qdrantrecall.Embedder {
	if ollamaProvider == nil {
		slog.Warn("recall embedder unavailable: Ollama provider not configured; vector recall disabled (other recall sources still active)")
		return noopRecallEmbedder{}
	}
	return &ollamaEmbedderAdapter{provider: ollamaProvider, model: defaultRecallEmbeddingModel}
}
