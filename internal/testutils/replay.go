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
//
// Expected:
//   - name: The provider name to return from Name().
//   - chunks: Pre-recorded provider.StreamChunks to replay.
//
// Returns:
//   - A new ReplayProvider instance configured with the given chunks.
//
// Side effects:
//   - None.
func NewReplayProvider(name string, chunks []provider.StreamChunk) *ReplayProvider {
	return &ReplayProvider{
		name:   name,
		chunks: chunks,
	}
}

// Name returns the provider name.
//
// Expected:
//   - None.
//
// Returns:
//   - The provider name set at construction.
//
// Side effects:
//   - None.
func (r *ReplayProvider) Name() string {
	return r.name
}

// Stream returns a channel that emits the pre-recorded chunks and then closes.
//
// Expected:
//   - The ReplayProvider was constructed with chunks via NewReplayProvider.
//
// Returns:
//   - A channel that will emit all pre-recorded chunks, then close. Always returns nil error.
//
// Side effects:
//   - Creates a buffered channel and sends all pre-recorded chunks into it.
func (r *ReplayProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(r.chunks))
	for i := range r.chunks {
		ch <- r.chunks[i]
	}
	close(ch)
	return ch, nil
}

// Chat is not implemented for replay providers.
//
// Expected:
//   - None.
//
// Returns:
//   - An empty ChatResponse and errReplayProviderNotImplemented error.
//
// Side effects:
//   - None.
func (r *ReplayProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errReplayProviderNotImplemented
}

// Embed is not implemented for replay providers.
//
// Expected:
//   - None.
//
// Returns:
//   - A nil slice and errReplayProviderNotImplemented error.
//
// Side effects:
//   - None.
func (r *ReplayProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errReplayProviderNotImplemented
}

// Models is not implemented for replay providers.
//
// Expected:
//   - None.
//
// Returns:
//   - A nil slice and errReplayProviderNotImplemented error.
//
// Side effects:
//   - None.
func (r *ReplayProvider) Models() ([]provider.Model, error) {
	return nil, errReplayProviderNotImplemented
}
