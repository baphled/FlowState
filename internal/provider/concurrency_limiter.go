package provider

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

var _ Provider = (*ConcurrencyLimitedProvider)(nil)

// RateLimitCooldowner is implemented by wrappers that can enforce a
// provider-wide backoff after a rate-limit response, blocking all
// callers until the cooldown expires. The ConcurrencyLimitedProvider
// implements this; the failover hook calls SetCooldown after detecting
// a 429 or rate-limit error so ALL engines are gated, not just the one
// that triggered it.
type RateLimitCooldowner interface {
	SetCooldown(d time.Duration)
}

// ConcurrencyLimitedProvider wraps a Provider to bound the number of
// SIMULTANEOUS in-flight chat calls (Stream and Chat) made to it, and to
// enforce a global post-rate-limit cooldown across all callers.
//
// The semaphore prevents overwhelming a provider with parallel streams.
// The cooldown gate prevents back-to-back hammering after a rate-limit:
// when any caller receives a retriable error (429, 5xx, network), the
// cooldown blocks ALL subsequent callers for the provider-specified
// retry-after duration (or a sensible default). This eliminates the
// thundering-herd pattern where multiple independent engine instances
// each retry the same rate-limited provider with zero inter-request delay.
type ConcurrencyLimitedProvider struct {
	inner      Provider
	name       string // cached from inner.Name() for logging/labelling
	sem        chan struct{}
	inFlight   atomic.Int64 // current number of acquired slots
	queueDepth atomic.Int64 // current number of callers waiting to acquire

	cooldownMu    sync.Mutex
	cooldownUntil time.Time
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

// WrappedProvider returns the wrapped provider for recursive diagnostics.
//
// Expected: parameters for WrappedProvider.
// Returns: result of WrappedProvider.
// Side effects: None.
func (c *ConcurrencyLimitedProvider) WrappedProvider() Provider { return c.inner }

// acquire blocks until a semaphore slot is free, any active cooldown has
// expired, and ctx has not been cancelled.
//
// Returns:
//   - nil once a slot has been acquired (the caller MUST later release it).
//   - ctx.Err() if ctx is cancelled while queued or waiting for cooldown.
//
// Side effects:
//   - Updates in-flight and queue-depth atomic counters.
//   - Emits slog.Debug on acquire, cooldown-wait, or cancellation.
//
// Expected: parameters for acquire.
func (c *ConcurrencyLimitedProvider) acquire(ctx context.Context) error {
	c.queueDepth.Add(1)

	// Gate 1: wait out any active cooldown so we don't hammer a
	// recently-rate-limited provider with back-to-back requests.
	c.waitCooldown(ctx)

	// Gate 2: acquire a concurrency semaphore slot.
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

// waitCooldown blocks until any active rate-limit cooldown expires or ctx
// is cancelled. When no cooldown is set this returns immediately.
//
// Side effects:
//   - Sleeps for the remaining cooldown duration.
//
// Expected: parameters for waitCooldown.
// Returns: result of waitCooldown.
func (c *ConcurrencyLimitedProvider) waitCooldown(ctx context.Context) {
	c.cooldownMu.Lock()
	remaining := time.Until(c.cooldownUntil)
	c.cooldownMu.Unlock()

	if remaining <= 0 {
		return
	}

	slog.Debug("concurrency cooldown active",
		"provider", c.name,
		"remaining", remaining.Round(time.Second),
	)

	timer := time.NewTimer(remaining)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}

// SetCooldown imposes a provider-wide backoff of d duration, blocking all
// subsequent callers until the cooldown expires. If a cooldown is already
// active, the longer of the two durations wins — we never shorten an
// existing cooldown.
//
// Expected:
//   - d is a positive duration (the provider's retry-after or a sensible
//     default for the error type).
//
// Side effects:
//   - Updates the cooldown deadline.
//
// Returns: result of SetCooldown.
func (c *ConcurrencyLimitedProvider) SetCooldown(d time.Duration) {
	if d <= 0 {
		return
	}
	c.cooldownMu.Lock()
	defer c.cooldownMu.Unlock()

	expiry := time.Now().Add(d)
	if expiry.After(c.cooldownUntil) {
		c.cooldownUntil = expiry
		slog.Debug("concurrency cooldown set",
			"provider", c.name,
			"duration", d.Round(time.Second),
		)
	}
}

// release frees a previously acquired semaphore slot.
//
// Side effects:
//   - Decrements the in-flight atomic counter.
//   - Emits slog.Debug after release.
//
// Expected: parameters for release.
// Returns: result of release.
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
		c.release()
		c.applyCooldownFromError(err)
		return nil, err
	}

	out := make(chan StreamChunk)
	go func() {
		defer close(out)
		defer c.release()
		for chunk := range innerCh {
			if chunk.Error != nil && chunk.Done {
				c.applyCooldownFromError(chunk.Error)
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
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

	resp, err := c.inner.Chat(ctx, req)
	if err != nil {
		c.applyCooldownFromError(err)
	}
	return resp, err
}

// applyCooldownFromError checks whether err is a retriable provider error
// (rate-limit, overload, network, server error) and, if so, imposes a
// provider-wide cooldown so ALL callers back off — not just the one that
// triggered it. User-correctable errors (auth, context-window, model-not-found)
// are deliberately excluded: the cooldown gate is for provider-side
// availability, not caller-side input mistakes.
//
// When the error carries a RateLimit with a non-zero RetryAfter, that
// duration is used directly (respecting the carrier's retry-after header).
// Otherwise a sensible default is applied per error type:
//
//   - RateLimit → 60s     (was 1h in HealthManager; cooldown is a shorter gate)
//   - Overload   → 45s
//   - NetworkError → 20s
//   - ServerError → 60s
//   - untyped     → 10s  (conservative: unknown failure, brief backoff)
//
// The HealthManager still applies its own longer cooldowns for persistent
// circuit-breaking; this is the CONCURRENCY gate for the thundering-herd.
//
// Expected: parameters for applyCooldownFromError.
// Returns: result of applyCooldownFromError.
// Side effects: None.
func (c *ConcurrencyLimitedProvider) applyCooldownFromError(err error) {
	if err == nil {
		return
	}
	var provErr *Error
	if !errors.As(err, &provErr) {
		// Non-provider errors (e.g. context cancellation, test mocks
		// returning raw errors.New) are not rate-limit signals.
		return
	}

	switch provErr.ErrorType {
	case ErrorTypeRateLimit:
		if provErr.RateLimit != nil && provErr.RateLimit.RetryAfter > 0 {
			c.SetCooldown(provErr.RateLimit.RetryAfter)
		} else {
			c.SetCooldown(60 * time.Second)
		}
	case ErrorTypeOverload:
		c.SetCooldown(45 * time.Second)
	case ErrorTypeNetworkError:
		c.SetCooldown(20 * time.Second)
	case ErrorTypeServerError:
		c.SetCooldown(60 * time.Second)
	case ErrorTypeBilling, ErrorTypeQuota:
		c.SetCooldown(5 * time.Minute)
	case ErrorTypeAuthFailure, ErrorTypeContextWindowExceeded, ErrorTypeModelNotFound:
		return
	default:
		c.SetCooldown(10 * time.Second)
	}
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
