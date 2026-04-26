package app

import (
	"context"
	"errors"
	"log/slog"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
	qdrantrecall "github.com/baphled/flowstate/internal/recall/qdrant"
)

// Recall embedder routing constants.
//
// defaultRecallEmbeddingDim pins the vector dimension of the configured
// Qdrant collection. The historical embedding model is `nomic-embed-text`
// (an Ollama-served 768-dim Cosine model); changing the embedding model
// without re-creating the Qdrant collection silently corrupts recall.
//
// The actual model name now flows from config (see
// config.AppConfig.EmbeddingModel and config.DefaultEmbeddingModel). It is
// passed in to newRecallEmbedder rather than constanted here so a cluster
// can centralise the embedding model in one config knob — see the
// AppConfig.EmbeddingModel godoc for the cluster rationale.
const (
	defaultRecallEmbeddingDim = 768
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
// The embedding model is supplied at construction (see newRecallEmbedder)
// so callers cannot accidentally diverge from the cluster-wide setting in
// config.AppConfig.EmbeddingModel — every embedder built by this app
// shares the same model.
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
// provider with the model resolved from config (cfg.EmbeddingModel, falling
// back to config.DefaultEmbeddingModel when empty).
//
// Expected:
//   - ollamaProvider may be nil — when nil, a no-op embedder is returned so
//     the broker still constructs and non-Qdrant recall sources keep working.
//   - model names the embedding model. Empty falls back to the historical
//     default (see config.DefaultEmbeddingModel).
//
// Returns:
//   - A non-nil qdrant.Embedder. When ollamaProvider is nil, the embedder
//     returns errRecallEmbedderUnavailable from every Embed call.
//
// Side effects:
//   - When ollamaProvider is nil, logs a single warning so operators see
//     the degradation explicitly instead of silently losing vector recall.
func newRecallEmbedder(ollamaProvider embedRequester, model string) qdrantrecall.Embedder {
	if ollamaProvider == nil {
		slog.Warn("recall embedder unavailable: Ollama provider not configured; vector recall disabled (other recall sources still active)")
		return noopRecallEmbedder{}
	}
	if model == "" {
		model = config.DefaultEmbeddingModel
	}
	return &ollamaEmbedderAdapter{provider: ollamaProvider, model: model}
}
