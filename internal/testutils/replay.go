package testutils

import (
	"context"
	"errors"

	"github.com/baphled/flowstate/internal/provider"
)

var errReplayProviderNotImplemented = errors.New("replay provider: method not implemented")

// ReplayProvider is a provider.Provider implementation that replays pre-recorded chunks.
// It is used to implement golden file replay for deterministic testing.
type ReplayProvider struct {
	name   string
	chunks []provider.StreamChunk
}

// NewReplayProvider creates a new ReplayProvider with the given name and chunks.
func NewReplayProvider(name string, chunks []provider.StreamChunk) *ReplayProvider {
	return &ReplayProvider{
		name:   name,
		chunks: chunks,
	}
}

// Name returns the provider name.
func (r *ReplayProvider) Name() string {
	return r.name
}

// Stream returns a channel that emits the pre-recorded chunks and then closes.
// The request is ignored; chunks are always replayed as-is.
func (r *ReplayProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(r.chunks))
	for i := range r.chunks {
		ch <- r.chunks[i]
	}
	close(ch)
	return ch, nil
}

// Chat is not implemented for replay providers.
func (r *ReplayProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errReplayProviderNotImplemented
}

// Embed is not implemented for replay providers.
func (r *ReplayProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errReplayProviderNotImplemented
}

// Models is not implemented for replay providers.
func (r *ReplayProvider) Models() ([]provider.Model, error) {
	return nil, errReplayProviderNotImplemented
}
