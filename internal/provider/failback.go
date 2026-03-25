// Package provider implements LLM provider integrations with failback support.
package provider

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var errNoPreferences = errors.New("no model preferences configured: cannot failback to any provider")

// ModelPreference specifies a preferred model and provider combination.
type ModelPreference struct {
	Provider string
	Model    string
}

// FailbackChain tries providers in order until one succeeds.
type FailbackChain struct {
	registry     *Registry
	preferences  []ModelPreference
	timeout      time.Duration
	lastProvider string
	lastModel    string
}

// NewFailbackChain creates a new failback chain with the given preferences and timeout.
//
// Expected:
//   - registry is a valid, non-nil Registry.
//   - preferences contains at least one ModelPreference.
//   - timeout is a positive duration.
//
// Returns:
//   - A pointer to an initialised FailbackChain.
//
// Side effects:
//   - None.
func NewFailbackChain(registry *Registry, preferences []ModelPreference, timeout time.Duration) *FailbackChain {
	return &FailbackChain{
		registry:    registry,
		preferences: preferences,
		timeout:     timeout,
	}
}

// Stream attempts to stream from providers in preference order.
//
// Uses a peek-and-replay pattern: reads the first chunk from each provider
// to detect async errors (e.g. HTTP 404) before committing. If the first
// chunk carries Error+Done, the provider is skipped and the next is tried.
//
// Expected:
//   - ctx is a valid context.
//   - req contains a valid chat request.
//
// Returns:
//   - A channel of StreamChunk on success (first chunk replayed).
//   - An error if all providers fail (sync or async errors).
//
// Side effects:
//   - Makes network calls to LLM providers.
//   - Updates lastProvider and lastModel on success.
func (f *FailbackChain) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
	if len(f.preferences) == 0 {
		return nil, errNoPreferences
	}
	var lastErr error
	for _, pref := range f.preferences {
		p, err := f.registry.Get(pref.Provider)
		if err != nil {
			lastErr = err
			continue
		}
		req.Model = pref.Model
		timeoutCtx, cancel := context.WithTimeout(ctx, f.timeout)
		ch, err := p.Stream(timeoutCtx, req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		firstChunk, ok := <-ch
		if !ok {
			cancel()
			lastErr = fmt.Errorf("provider %s: stream closed immediately", pref.Provider)
			continue
		}
		if firstChunk.Error != nil && firstChunk.Done {
			cancel()
			lastErr = firstChunk.Error
			continue
		}
		f.lastProvider = pref.Provider
		f.lastModel = pref.Model
		replayCh := f.streamWithReplay(timeoutCtx, cancel, firstChunk, ch)
		return replayCh, nil
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
}

// streamWithReplay creates a replay channel, starts a goroutine that sends
// firstChunk then forwards remaining chunks from ch, and returns the channel.
// All sends are guarded by timeoutCtx so the goroutine exits on cancellation.
//
// Expected:
//   - timeoutCtx is a valid context with a deadline.
//   - cancel is the corresponding cancel function for timeoutCtx.
//   - firstChunk is a valid StreamChunk already read from the provider.
//   - ch is a valid receive-only channel of StreamChunk.
//
// Returns:
//   - A receive-only channel that replays firstChunk followed by remaining chunks.
//
// Side effects:
//   - Starts a goroutine that closes replayCh and calls cancel on completion.
func (f *FailbackChain) streamWithReplay(
	timeoutCtx context.Context,
	cancel context.CancelFunc,
	firstChunk StreamChunk,
	ch <-chan StreamChunk,
) <-chan StreamChunk {
	replayCh := make(chan StreamChunk, 16)
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

// Chat attempts to chat with providers in preference order.
//
// Expected:
//   - ctx is a valid context.
//   - req contains a valid chat request.
//
// Returns:
//   - A ChatResponse on success.
//   - An error if all providers fail.
//
// Side effects:
//   - Makes network calls to LLM providers.
//   - Updates lastProvider on success.
func (f *FailbackChain) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	if len(f.preferences) == 0 {
		return ChatResponse{}, errNoPreferences
	}
	var lastErr error
	for _, pref := range f.preferences {
		p, err := f.registry.Get(pref.Provider)
		if err != nil {
			lastErr = err
			continue
		}
		req.Model = pref.Model
		timeoutCtx, cancel := context.WithTimeout(ctx, f.timeout)
		resp, err := p.Chat(timeoutCtx, req)
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		f.lastProvider = pref.Provider
		f.lastModel = pref.Model
		return resp, nil
	}
	return ChatResponse{}, fmt.Errorf("all providers failed: %w", lastErr)
}

// LastProvider returns the name of the last successfully used provider.
//
// Returns:
//   - The provider name, or empty string if no provider has been used.
//
// Side effects:
//   - None.
func (f *FailbackChain) LastProvider() string {
	return f.lastProvider
}

// LastModel returns the model name of the last successfully used provider.
//
// Returns:
//   - The model name, or empty string if no model has been used.
//
// Side effects:
//   - None.
func (f *FailbackChain) LastModel() string {
	return f.lastModel
}

// DefaultProvider returns the provider name from the first configured preference.
//
// Expected:
//   - preferences is non-empty (populated at construction).
//
// Returns:
//   - The provider name string from the first preference, or empty string if none.
//
// Side effects:
//   - None.
func (f *FailbackChain) DefaultProvider() string {
	if len(f.preferences) > 0 {
		return f.preferences[0].Provider
	}
	return ""
}

// DefaultModel returns the model name from the first configured preference.
//
// Expected:
//   - preferences is non-empty (populated at construction).
//
// Returns:
//   - The model name string from the first preference, or empty string if none.
//
// Side effects:
//   - None.
func (f *FailbackChain) DefaultModel() string {
	if len(f.preferences) > 0 {
		return f.preferences[0].Model
	}
	return ""
}

// SetPreferences updates the model preferences used for failback.
//
// Expected:
//   - preferences is a non-empty slice of ModelPreference values.
//
// Side effects:
//   - Replaces the current preferences list.
func (f *FailbackChain) SetPreferences(preferences []ModelPreference) {
	f.preferences = preferences
}

// ListModels returns all available models from all configured providers.
//
// Returns:
//   - A slice of all available models from all providers.
//   - An error if no models are available.
//
// Side effects:
//   - May make network calls to providers to fetch model lists.
func (f *FailbackChain) ListModels() ([]Model, error) {
	var allModels []Model
	for _, providerName := range f.registry.List() {
		p, err := f.registry.Get(providerName)
		if err != nil {
			continue
		}
		models, err := p.Models()
		if err != nil {
			continue
		}
		allModels = append(allModels, models...)
	}
	if len(allModels) == 0 {
		return nil, errors.New("no models available from any provider")
	}
	return allModels, nil
}
