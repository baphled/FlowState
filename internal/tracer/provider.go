package tracer

import (
	"context"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// TracingProvider wraps a Provider to record per-method latency and errors via a Recorder.
type TracingProvider struct {
	inner    provider.Provider
	recorder Recorder
}

// NewTracingProvider returns a TracingProvider wrapping inner with the given Recorder.
func NewTracingProvider(inner provider.Provider, recorder Recorder) *TracingProvider {
	return &TracingProvider{inner: inner, recorder: recorder}
}

// Name delegates to the wrapped provider.
func (t *TracingProvider) Name() string { return t.inner.Name() }

// Stream delegates to the wrapped provider, recording latency via the Recorder.
func (t *TracingProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	start := time.Now()
	ch, err := t.inner.Stream(ctx, req)
	t.recorder.RecordProviderLatency(t.inner.Name(), "stream", float64(time.Since(start).Milliseconds()))
	return ch, err
}

// Chat delegates to the wrapped provider, recording latency via the Recorder.
func (t *TracingProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	start := time.Now()
	resp, err := t.inner.Chat(ctx, req)
	t.recorder.RecordProviderLatency(t.inner.Name(), "chat", float64(time.Since(start).Milliseconds()))
	return resp, err
}

// Embed delegates to the wrapped provider, recording latency via the Recorder.
func (t *TracingProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	start := time.Now()
	v, err := t.inner.Embed(ctx, req)
	t.recorder.RecordProviderLatency(t.inner.Name(), "embed", float64(time.Since(start).Milliseconds()))
	return v, err
}

// Models delegates to the wrapped provider.
func (t *TracingProvider) Models() ([]provider.Model, error) { return t.inner.Models() }
