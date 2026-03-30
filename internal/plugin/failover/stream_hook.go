package failover

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

const replayBufferSize = 16

// StreamHook is a hook.Hook middleware that handles multi-provider retry with
// peek-and-replay. It queries the Manager for healthy candidates and tries each
// in order, peeking at the first chunk to detect async errors before committing.
type StreamHook struct {
	manager *Manager
}

// NewStreamHook creates a new StreamHook with the given failover manager.
//
// Expected:
//   - manager is a non-nil Manager with preferences and health tracking configured.
//
// Returns:
//   - A StreamHook ready for use in a hook chain.
//
// Side effects:
//   - None.
func NewStreamHook(manager *Manager) *StreamHook {
	return &StreamHook{manager: manager}
}

// Execute returns a hook.HandlerFunc that wraps the next handler with multi-provider
// retry and peek-and-replay logic. For each healthy candidate, it sets the provider
// and model on the request, calls next with a per-attempt timeout, and peeks at the
// first chunk to detect async errors before committing to the stream.
//
// Expected:
//   - next is the downstream handler (e.g. baseStreamHandler).
//
// Returns:
//   - A HandlerFunc that retries across providers on failure.
//
// Side effects:
//   - Sets req.Provider and req.Model for each attempt.
//   - Calls manager.SetLast on success.
func (sh *StreamHook) Execute(next hook.HandlerFunc) hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		candidates := sh.manager.Candidates()
		if len(candidates) == 0 {
			return nil, errors.New("no healthy providers available")
		}

		var lastErr error
		for _, candidate := range candidates {
			req.Provider = candidate.Provider
			req.Model = candidate.Model

			replayCh, err := sh.attemptCandidate(ctx, next, req, candidate)
			if err != nil {
				lastErr = err
				continue
			}
			return replayCh, nil
		}
		return nil, fmt.Errorf("all providers failed: %w", lastErr)
	}
}

// attemptCandidate tries a single provider candidate with per-attempt timeout and
// peek-and-replay. Returns the replay channel on success or an error on failure.
//
// Expected:
//   - ctx is the parent context.
//   - next is the downstream handler.
//   - req has Provider and Model set for this candidate.
//   - candidate identifies the provider/model being attempted.
//
// Returns:
//   - A replay channel on success.
//   - An error if the provider fails synchronously, asynchronously, or via timeout.
//
// Side effects:
//   - Creates per-attempt timeout context.
//   - Calls manager.SetLast on success.
func (sh *StreamHook) attemptCandidate(
	ctx context.Context,
	next hook.HandlerFunc,
	req *provider.ChatRequest,
	candidate provider.ModelPreference,
) (<-chan provider.StreamChunk, error) {
	timeoutCtx, cancel := context.WithTimeout(ctx, sh.manager.StreamTimeout())

	ch, err := next(timeoutCtx, req)
	if err != nil {
		cancel()
		CheckAndMarkRateLimited(sh.manager.Health(), candidate.Provider, candidate.Model, err)
		return nil, err
	}

	firstChunk, ok, peekErr := peekFirstChunk(timeoutCtx, ch, candidate.Provider)
	if peekErr != nil {
		cancel()
		return nil, peekErr
	}
	if !ok {
		cancel()
		return nil, fmt.Errorf("provider %s: stream closed immediately", candidate.Provider)
	}
	if firstChunk.Error != nil && firstChunk.Done {
		cancel()
		CheckAndMarkRateLimited(sh.manager.Health(), candidate.Provider, candidate.Model, firstChunk.Error)
		return nil, firstChunk.Error
	}

	sh.manager.SetLast(candidate.Provider, candidate.Model)
	return streamWithReplay(timeoutCtx, cancel, firstChunk, ch), nil
}

// peekFirstChunk reads the first chunk from the channel with context awareness.
// If the context expires before the first chunk arrives, it returns an error
// so the caller can try the next provider.
//
// Expected:
//   - ctx is a valid context (typically with a timeout).
//   - ch is the stream channel from the provider.
//   - providerName identifies the provider for error messages.
//
// Returns:
//   - The first chunk, true, nil on success.
//   - Zero chunk, false, nil if the channel closed without sending.
//   - Zero chunk, false, error if the context expired.
//
// Side effects:
//   - None.
func peekFirstChunk(
	ctx context.Context,
	ch <-chan provider.StreamChunk,
	providerName string,
) (provider.StreamChunk, bool, error) {
	select {
	case chunk, ok := <-ch:
		return chunk, ok, nil
	case <-ctx.Done():
		return provider.StreamChunk{}, false, fmt.Errorf("provider %s: %w", providerName, ctx.Err())
	}
}

// streamWithReplay creates a replay channel, starts a goroutine that sends
// firstChunk then forwards remaining chunks from ch, and returns the channel.
// All sends are guarded by timeoutCtx so the goroutine exits on cancellation.
//
// Expected:
//   - timeoutCtx is a valid context with a deadline.
//   - cancel is the corresponding cancel function for timeoutCtx.
//   - firstChunk is the first chunk already read from the provider.
//   - ch is the remaining stream channel.
//
// Returns:
//   - A receive-only channel that replays firstChunk followed by remaining chunks.
//
// Side effects:
//   - Starts a goroutine that closes replayCh and calls cancel on completion.
func streamWithReplay(
	timeoutCtx context.Context,
	cancel context.CancelFunc,
	firstChunk provider.StreamChunk,
	ch <-chan provider.StreamChunk,
) <-chan provider.StreamChunk {
	replayCh := make(chan provider.StreamChunk, replayBufferSize)
	go func() {
		defer close(replayCh)
		defer cancel()
		select {
		case replayCh <- firstChunk:
		case <-timeoutCtx.Done():
			return
		}
		for chunk := range ch {
			select {
			case replayCh <- chunk:
			case <-timeoutCtx.Done():
				return
			}
		}
	}()
	return replayCh
}
