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
// Expected:
//   - ctx is a valid context.
//   - req contains a valid chat request.
//
// Returns:
//   - A channel of StreamChunk on success.
//   - An error if all providers fail.
//
// Side effects:
//   - Makes network calls to LLM providers.
//   - Updates lastProvider on success.
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
		f.lastProvider = pref.Provider
		f.lastModel = pref.Model
		wrappedCh := make(chan StreamChunk, 16)
		go func() {
			defer close(wrappedCh)
			defer cancel()
			for chunk := range ch {
				wrappedCh <- chunk
			}
		}()
		return wrappedCh, nil
	}
	return nil, fmt.Errorf("all providers failed: %w", lastErr)
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
