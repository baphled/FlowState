package provider

import (
	"context"
	"log/slog"
	"sync/atomic"
)

var _ Provider = (*ConcurrencyLimitedProvider)(nil)

// ConcurrencyLimitedProvider wraps a Provider to bound the number of
// SIMULTANEOUS in-flight chat calls (Stream and Chat) made to it. It exists to
// respect a provider's per-account concurrent-request cap: the engine can fan
// a turn's tool calls — including several `delegate` calls from a swarm lead —
// out as concurrent goroutines, each opening its own provider stream. Without a
// bound, N concurrent delegates open N concurrent streams and can trip the
// provider's limit (observed as HTTP 429 against z.ai).
//
// The bound is a buffered-channel semaphore of size maxConcurrent. Callers
// that arrive when all slots are taken QUEUE (block) on acquire rather than
// being dropped, respecting context cancellation while queued.
//
// Embeddings and Models are pass-throughs: embeddings target a different
// backend (ollama) and must not contend for the chat semaphore.
type ConcurrencyLimitedProvider struct {
	inner      Provider
	name       string // cached from inner.Name() for logging/labelling
	sem        chan struct{}
	inFlight   atomic.Int64 // current number of acquired slots
	queueDepth atomic.Int64 // current number of callers waiting to acquire
}

// NewConcurrencyLimitedProvider returns a ConcurrencyLimitedProvider that
// permits at most maxConcurrent simultaneous in-flight Stream/Chat calls to
// inner.
//
// Expected:
//   - inner is a non-nil Provider implementation.
//   - maxConcurrent is >= 1. Callers that want "unlimited" should not wrap the
//     provider at all rather than passing 0; a 0 here would make every acquire
//     block forever.
//
// Returns:
//   - A ConcurrencyLimitedProvider delegating to inner under a semaphore.
//
// Side effects:
//   - None.
func NewConcurrencyLimitedProvider(inner Provider, maxConcurrent int) *ConcurrencyLimitedProvider {
	return &ConcurrencyLimitedProvider{
		inner: inner,
		name:  inner.Name(),
		sem:   make(chan struct{}, maxConcurrent),
	}
}

// Name delegates to the wrapped provider.
//
// Expected:
//   - None.
//
// Returns:
//   - The name of the wrapped provider.
//
// Side effects:
//   - None.
func (c *ConcurrencyLimitedProvider) Name() string { return c.inner.Name() }

// acquire blocks until a semaphore slot is free or ctx is cancelled.
//
// Returns:
//   - nil once a slot has been acquired (the caller MUST later release it).
//   - ctx.Err() if ctx is cancelled while queued (no slot is held).
//
// Side effects:
//   - Updates in-flight and queue-depth atomic counters.
//   - Emits slog.Debug on acquire or cancellation.
func (c *ConcurrencyLimitedProvider) acquire(ctx context.Context) error {
	c.queueDepth.Add(1)
	select {
	case c.sem <- struct{}{}:
		c.queueDepth.Add(-1)
		c.inFlight.Add(1)
		slog.Debug("concurrency slot acquired",
			"provider", c.name,
			"in_flight", c.inFlight.Load(),
			"queue_depth", c.queueDepth.Load(),
		)
		return nil
	case <-ctx.Done():
		c.queueDepth.Add(-1)
		slog.Debug("concurrency slot cancelled while queued",
			"provider", c.name,
			"queue_depth", c.queueDepth.Load(),
			"error", ctx.Err(),
		)
		return ctx.Err()
	}
}

// release frees a previously acquired semaphore slot.
//
// Side effects:
//   - Decrements the in-flight atomic counter.
//   - Emits slog.Debug after release.
func (c *ConcurrencyLimitedProvider) release() {
	<-c.sem
	c.inFlight.Add(-1)
	slog.Debug("concurrency slot released",
		"provider", c.name,
		"in_flight", c.inFlight.Load(),
	)
}

// InFlight returns the current number of in-flight requests (acquired slots).
//
// Expected:
//   - None.
//
// Returns:
//   - The current in-flight count as an int.
//
// Side effects:
//   - None.
func (c *ConcurrencyLimitedProvider) InFlight() int {
	return int(c.inFlight.Load())
}

// QueueDepth returns the current number of callers waiting to acquire a slot.
//
// Expected:
//   - None.
//
// Returns:
//   - The current queue depth as an int.
//
// Side effects:
//   - None.
func (c *ConcurrencyLimitedProvider) QueueDepth() int {
	return int(c.queueDepth.Load())
}

// MaxConcurrent returns the maximum number of concurrent in-flight calls
// allowed by this limiter (the semaphore buffer size).
//
// Expected:
//   - None.
//
// Returns:
//   - The maximum concurrent call cap as an int.
//
// Side effects:
//   - None.
func (c *ConcurrencyLimitedProvider) MaxConcurrent() int {
	return cap(c.sem)
}

// Stream acquires a concurrency slot, opens the inner stream, and releases the
// slot only once the returned channel is fully drained and closed — so the slot
// reflects a genuinely in-flight stream, not merely the call that started it.
//
// Expected:
//   - ctx is a valid context. If it is cancelled while queued, Stream returns
//     ctx.Err() without opening an inner stream.
//   - req contains a well-formed chat request.
//
// Returns:
//   - A channel of StreamChunk values on success.
//   - An error if ctx is cancelled while queued, or if inner.Stream fails (in
//     which case the slot is released immediately).
//
// Side effects:
//   - Holds a concurrency slot for the lifetime of the returned stream.
func (c *ConcurrencyLimitedProvider) Stream(
	ctx context.Context, req ChatRequest,
) (<-chan StreamChunk, error) {
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}

	innerCh, err := c.inner.Stream(ctx, req)
	if err != nil {
		// No stream to drain — give the slot back immediately.
		c.release()
		return nil, err
	}

	// Forward inner chunks to the caller, releasing the slot when the inner
	// channel closes (i.e. the stream has fully drained). A separate output
	// channel lets us own the close/release without racing the producer.
	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		defer c.release()
		for chunk := range innerCh {
			select {
			case out <- chunk:
			case <-ctx.Done():
				// Caller (or request) is gone. Drain the inner channel so the
				// upstream producer is not blocked on an unread send, then
				// release via the deferred call.
				for range innerCh {
				}
				return
			}
		}
	}()

	return out, nil
}

// Chat acquires a concurrency slot for the duration of the inner Chat call and
// releases it before returning.
//
// Expected:
//   - ctx is a valid context. If it is cancelled while queued, Chat returns
//     ctx.Err() without calling the inner provider.
//   - req contains a well-formed chat request.
//
// Returns:
//   - A ChatResponse on success.
//   - An error if ctx is cancelled while queued, or if inner.Chat fails.
//
// Side effects:
//   - Holds a concurrency slot for the duration of the inner Chat call.
func (c *ConcurrencyLimitedProvider) Chat(
	ctx context.Context, req ChatRequest,
) (ChatResponse, error) {
	if err := c.acquire(ctx); err != nil {
		return ChatResponse{}, err
	}
	defer c.release()
	return c.inner.Chat(ctx, req)
}

// Embed delegates to the wrapped provider WITHOUT acquiring a chat slot.
// Embeddings target a separate backend and must not contend for the chat
// concurrency budget.
//
// Expected:
//   - ctx is a valid context.
//   - req contains a non-empty input for embedding.
//
// Returns:
//   - The embedding vector on success, or an error from the inner provider.
//
// Side effects:
//   - None (not gated by the concurrency semaphore).
func (c *ConcurrencyLimitedProvider) Embed(
	ctx context.Context, req EmbedRequest,
) ([]float64, error) {
	return c.inner.Embed(ctx, req)
}

// Models delegates to the wrapped provider.
//
// Expected:
//   - None.
//
// Returns:
//   - The list of available models, or an error from the inner provider.
//
// Side effects:
//   - None.
func (c *ConcurrencyLimitedProvider) Models() ([]Model, error) { return c.inner.Models() }
