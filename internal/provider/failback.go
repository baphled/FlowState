package provider

import (
	"context"
	"fmt"
	"time"
)

type ModelPreference struct {
	Provider string
	Model    string
}

type FailbackChain struct {
	registry     *Registry
	preferences  []ModelPreference
	timeout      time.Duration
	lastProvider string
}

func NewFailbackChain(registry *Registry, preferences []ModelPreference, timeout time.Duration) *FailbackChain {
	return &FailbackChain{
		registry:    registry,
		preferences: preferences,
		timeout:     timeout,
	}
}

func (f *FailbackChain) Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error) {
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

func (f *FailbackChain) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
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
		return resp, nil
	}
	return ChatResponse{}, fmt.Errorf("all providers failed: %w", lastErr)
}

func (f *FailbackChain) LastProvider() string {
	return f.lastProvider
}
