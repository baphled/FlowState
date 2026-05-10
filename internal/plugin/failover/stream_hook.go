package failover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

const replayBufferSize = 16

// StreamHook is a hook.Hook middleware that handles multi-provider retry with
// peek-and-replay. It queries the Manager for healthy candidates and tries each
// in order, peeking at the first chunk to detect async errors before committing.
type StreamHook struct {
	manager  *Manager
	eventBus *eventbus.EventBus
	agentID  string
}

// NewStreamHook creates a new StreamHook with the given failover manager,
// optional event bus, and agent identifier.
//
// Expected:
//   - manager is a non-nil Manager with preferences and health tracking configured.
//   - bus may be nil; when non-nil, error events are published during failover.
//   - agentID identifies the agent for event metadata.
//
// Returns:
//   - A StreamHook ready for use in a hook chain.
//
// Side effects:
//   - None.
func NewStreamHook(manager *Manager, bus *eventbus.EventBus, agentID string) *StreamHook {
	return &StreamHook{manager: manager, eventBus: bus, agentID: agentID}
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

		// Honour a caller-pinned provider: if the request already has
		// Provider set (e.g. Engine.retryStreamForToolResult pinning
		// LastProvider to keep a multi-turn session on the same
		// provider), promote the matching candidate to the head of the
		// list so it is attempted first. Fallback semantics are
		// preserved — the remaining candidates still act as a failover
		// pool if the pinned provider genuinely fails. This is a
		// priority hint, not a single-shot. See bug-fix note:
		// "Failover Stream Hook Ignores Caller Provider Pin
		// (April 2026)".
		candidates = promotePinned(candidates, req.Provider, req.Model)

		// Honour a parent ctx that is already cancelled or past its
		// deadline at loop entry — this is how an upstream caller signals
		// "don't even try" (explicit user cancel, expired deadline).
		//
		// Between attempts we deliberately do NOT re-check ctx.Err():
		// the per-attempt ctx is detached in attemptCandidate, so a
		// cleanup cascade on the parent (e.g. the previous attempt's
		// derived cancel propagating, a racing goroutine) cannot leak
		// into later attempts. This is the core of the Bug #2 fix.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("all providers failed: %w", err)
		}

		var lastErr error
		// previousFailed records the FIRST candidate that failed before
		// this attempt succeeded. It powers the user-visible
		// "provider_changed" Track B affordance: when the second (or
		// later) candidate succeeds, the user is no longer on their
		// primary model and the chat UI must surface the transition.
		// We track only the first failure (not a list) because the
		// toast renders one alert: "switched away from <primary>". A
		// chain of multiple failures collapses to "primary failed" in
		// the user's mental model — listing every retired candidate
		// would be noise.
		var previousFailed *failedCandidate
		for _, candidate := range candidates {
			req.Provider = candidate.Provider
			req.Model = candidate.Model

			replayCh, err := sh.attemptCandidate(ctx, next, req, candidate)
			if err != nil {
				lastErr = err
				if previousFailed == nil {
					previousFailed = &failedCandidate{
						provider: candidate.Provider,
						model:    candidate.Model,
						reason:   classifyFailoverReason(err),
					}
				}
				continue
			}
			// model_active fires on EVERY successful stream so the chat UI
			// can pivot the persistent toolbar chip from the user's
			// selection to the actual model the moment streaming starts.
			// The user reported (May 2026) that the chip "shows what was
			// selected, not what actually ran" — until this prepend, the
			// chip had no signal during streaming distinguishing the two,
			// so a selection that didn't match the actual call (failover,
			// agent override, manifest override) read wrong until the
			// post-stream reconcile pulled the engine-stamped pair.
			//
			// Order: model_active first, then provider_changed (when
			// failover happened). Both must arrive before any user-visible
			// content; the relative order between them is unspecified at
			// the consumer (the frontend handles them as independent
			// events).
			replayCh = prependModelActiveChunk(replayCh, candidate)
			if previousFailed != nil {
				replayCh = prependProviderChangedChunk(replayCh, previousFailed, candidate)
			}
			return replayCh, nil
		}
		return nil, fmt.Errorf("all providers failed: %w", lastErr)
	}
}

// failedCandidate captures the provider/model pair of a candidate that was
// retired during the current Execute call, plus the reason classification
// derived from its error. It is internal state for the retry loop and
// never escapes the StreamHook.
type failedCandidate struct {
	provider string
	model    string
	reason   string
}

// providerChangedPayload is the wire shape for the user-visible failover
// transition event. The fields are JSON-marshalled into chunk.Content
// of a provider.StreamChunk{EventType: "provider_changed"}; the SSE
// dispatcher in internal/api/server.go forwards the bytes verbatim into
// a {"type":"provider_changed","from":...,"to":...,"reason":...} SSE
// event.
//
// Format choices:
//   - From / To are "<provider>+<model>" strings so the chat UI can show
//     the previous and current model in one toast line. The frontend
//     splits on "+" to extract model for friendly display.
//   - Reason is a stable machine-readable string ("rate_limited",
//     "model_not_found", "auth_failure", "timeout", "unknown") that the
//     frontend maps to plain English. Keeping the mapping on the
//     frontend side decouples copy-changes from the Go release cycle.
type providerChangedPayload struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Reason string `json:"reason"`
}

// classifyFailoverReason maps an error from a failed failover candidate
// to a stable reason string the frontend can map to plain language.
//
// Why we don't reuse provider.ErrorType directly: a couple of common
// transitions ("network", "deadline exceeded") aren't always wrapped in
// provider.Error, and the user-visible vocabulary is intentionally
// simpler than the full provider taxonomy. Reasons here are a subset
// hand-picked for the toast copy.
//
// Expected:
//   - err is non-nil (callers only invoke when a candidate has failed).
//
// Returns:
//   - A stable string in the closed set:
//     "rate_limited", "model_not_found", "auth_failure",
//     "billing", "quota", "overload", "timeout", "unavailable", "unknown".
//
// Side effects:
//   - None.
func classifyFailoverReason(err error) string {
	if err == nil {
		return "unknown"
	}
	var provErr *provider.Error
	if errors.As(err, &provErr) {
		switch provErr.ErrorType {
		case provider.ErrorTypeRateLimit:
			return "rate_limited"
		case provider.ErrorTypeBilling:
			return "billing"
		case provider.ErrorTypeQuota:
			return "quota"
		case provider.ErrorTypeOverload:
			return "overload"
		case provider.ErrorTypeAuthFailure:
			return "auth_failure"
		case provider.ErrorTypeModelNotFound:
			return "model_not_found"
		case provider.ErrorTypeNetworkError:
			return "unavailable"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return "unknown"
}

// prependProviderChangedChunk wraps an upstream replay channel so the
// FIRST chunk delivered to the consumer is a synthetic
// provider.StreamChunk carrying the failover transition metadata. The
// subsequent chunks are the upstream's real output, untouched.
//
// Why a wrapper instead of mutating the upstream channel: the failover
// hook does not own the channel — streamWithReplay's goroutine does,
// and that goroutine reads from the provider's stream. We compose a
// new channel that the wrapper goroutine populates: send the
// transition chunk, then forward upstream verbatim. The wrapper closes
// the new channel when upstream closes, preserving the close-signals
// the SSE consumer relies on (see handleSessionStream's `case chunk,
// ok := <-liveCh; if !ok { writeSSEDone }`).
//
// Expected:
//   - upstream is the replay channel returned by streamWithReplay.
//   - failed is the candidate that triggered the transition.
//   - newCandidate is the candidate that succeeded.
//
// Returns:
//   - A buffered channel of size 1 + replayBufferSize that delivers
//     the transition chunk first, then forwards upstream chunks.
//
// Side effects:
//   - Spawns one goroutine that reads from upstream until it closes,
//     then closes the wrapper channel. The goroutine cannot leak —
//     when the SSE consumer disconnects, upstream's goroutine cancels
//     and closes its channel, which terminates this loop.
func prependProviderChangedChunk(
	upstream <-chan provider.StreamChunk,
	failed *failedCandidate,
	newCandidate provider.ModelPreference,
) <-chan provider.StreamChunk {
	if failed == nil {
		return upstream
	}
	payload := providerChangedPayload{
		From:   failed.provider + "+" + failed.model,
		To:     newCandidate.Provider + "+" + newCandidate.Model,
		Reason: failed.reason,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// Defensive: a marshal failure on a 3-field struct is
		// unreachable in practice, but if it happens we'd rather skip
		// the affordance than corrupt the stream.
		return upstream
	}

	transition := provider.StreamChunk{
		EventType: "provider_changed",
		Content:   string(encoded),
	}

	out := make(chan provider.StreamChunk, replayBufferSize+1)
	go func() {
		defer close(out)
		out <- transition
		for chunk := range upstream {
			out <- chunk
		}
	}()
	return out
}

// modelActivePayload is the wire shape for the always-on "model_active"
// affordance. The fields are JSON-marshalled into chunk.Content of a
// provider.StreamChunk{EventType: "model_active"}; the SSE dispatcher
// in internal/api/server.go forwards the bytes verbatim into a
// {"type":"model_active","provider":"<id>","model":"<id>"} SSE event.
//
// Why provider/model as separate fields (not the "<provider>+<model>"
// string used by provider_changed): the chip rendering in the chat UI
// reads currentProviderId / currentModelId as separate keys against the
// availableModels list. Splitting on "+" works for provider_changed's
// transient toast; for the persistent chip pivot, exposing the canonical
// pair directly avoids a parser round-trip and a class of off-by-one
// bugs around model ids that themselves contain "+" (rare; openrouter).
type modelActivePayload struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// prependModelActiveChunk wraps an upstream replay channel so the FIRST
// chunk delivered to the consumer is a synthetic provider.StreamChunk
// announcing the actual (provider, model) pair the failover hook chose.
// Subsequent chunks are the upstream's real output, untouched.
//
// Why on every successful stream (not just failover): the chip needs to
// reflect the actual model the moment streaming starts, not the user's
// selection. Without this, the user reported (May 2026) that the chip
// "shows what was selected, not what actually ran". On the common case
// (selection matches actual) the event is a no-op for the user; on the
// divergent case (failover, agent override, manifest override), the chip
// snaps to the truth immediately rather than waiting on reconcile.
//
// Why a wrapper instead of mutating the upstream channel: same rationale
// as prependProviderChangedChunk above — the failover hook does not own
// the upstream channel, so we compose a new one and forward.
//
// Expected:
//   - upstream is the replay channel returned by streamWithReplay.
//   - candidate is the candidate that succeeded; its Provider and Model
//     are the values to surface to the consumer.
//
// Returns:
//   - A buffered channel of size 1 + replayBufferSize that delivers the
//     model_active chunk first, then forwards upstream chunks.
//
// Side effects:
//   - Spawns one goroutine that reads from upstream until it closes,
//     then closes the wrapper channel. The goroutine cannot leak —
//     when the SSE consumer disconnects, upstream's goroutine cancels
//     and closes its channel, which terminates this loop.
//   - On a marshal failure (unreachable in practice for a 2-string struct)
//     returns the upstream verbatim — the chip stays on the optimistic
//     selection rather than a malformed stream.
func prependModelActiveChunk(
	upstream <-chan provider.StreamChunk,
	candidate provider.ModelPreference,
) <-chan provider.StreamChunk {
	payload := modelActivePayload{
		Provider: candidate.Provider,
		Model:    candidate.Model,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return upstream
	}

	active := provider.StreamChunk{
		EventType: "model_active",
		Content:   string(encoded),
	}

	out := make(chan provider.StreamChunk, replayBufferSize+1)
	go func() {
		defer close(out)
		out <- active
		for chunk := range upstream {
			out <- chunk
		}
	}()
	return out
}

// promotePinned returns candidates with the caller-pinned provider/model
// moved to the head of the list. If pinnedProvider is empty or no candidate
// matches, the input slice is returned unchanged. When pinnedModel is
// empty, matching is by provider name only; otherwise both provider and
// model must match. The matched entry is kept in its ModelPreference form
// so downstream logic (health-marking, last-set) sees the exact pairing.
//
// Expected:
//   - candidates is a non-empty slice (callers ensure this).
//   - pinnedProvider may be empty (no-op).
//
// Returns:
//   - A slice with the matched candidate first, followed by the remaining
//     candidates in their original relative order; or the input slice
//     unchanged if no match.
//
// Side effects:
//   - None (returns a new slice when promotion happens; returns the input
//     slice directly when no promotion is needed, so callers must not
//     mutate it in place regardless).
func promotePinned(candidates []provider.ModelPreference, pinnedProvider, pinnedModel string) []provider.ModelPreference {
	if pinnedProvider == "" {
		return candidates
	}
	idx := -1
	for i, c := range candidates {
		if c.Provider != pinnedProvider {
			continue
		}
		if pinnedModel != "" && c.Model != pinnedModel {
			continue
		}
		idx = i
		break
	}
	if idx <= 0 {
		return candidates
	}
	reordered := make([]provider.ModelPreference, 0, len(candidates))
	reordered = append(reordered, candidates[idx])
	reordered = append(reordered, candidates[:idx]...)
	reordered = append(reordered, candidates[idx+1:]...)
	return reordered
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
	// Detach cancellation so a previous attempt's cleanup (its derived
	// cancel() firing, a racing goroutine, or any transient parent-chain
	// cancel not originating from the caller) cannot short-circuit this
	// attempt. Values (e.g. session.IDKey) still propagate.
	//
	// A genuine parent deadline is still honoured: we clamp the per-attempt
	// timeout to whichever is shorter — the configured stream timeout or
	// the remaining time until the parent's deadline. Explicit
	// parent-cancel is enforced one level up in Execute before this
	// function is called.
	detached := context.WithoutCancel(ctx)
	attemptTimeout := sh.manager.StreamTimeout()
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < attemptTimeout {
			attemptTimeout = remaining
		}
	}
	timeoutCtx, cancel := context.WithTimeout(detached, attemptTimeout)

	ch, err := next(timeoutCtx, req)
	if err != nil {
		cancel()
		markProviderHealth(sh.manager.Health(), candidate.Provider, candidate.Model, err)
		sh.publishFailoverError(ctx, candidate, err)
		return nil, err
	}

	firstChunk, ok, peekErr := peekFirstChunk(timeoutCtx, ch, candidate.Provider)
	if peekErr != nil {
		cancel()
		sh.publishFailoverError(ctx, candidate, peekErr)
		return nil, peekErr
	}
	if !ok {
		cancel()
		closeErr := fmt.Errorf("provider %s: stream closed immediately", candidate.Provider)
		sh.publishFailoverError(ctx, candidate, closeErr)
		return nil, closeErr
	}
	if firstChunk.Error != nil && firstChunk.Done {
		cancel()
		markProviderHealth(sh.manager.Health(), candidate.Provider, candidate.Model, firstChunk.Error)
		sh.publishFailoverError(ctx, candidate, firstChunk.Error)
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

// markProviderHealth marks a provider as unavailable for a cooldown determined by
// the error type. It applies differentiated durations: non-retriable errors use 24-hour
// cooldown; retriable errors use error-type-specific durations from CooldownForErrorType.
//
// When the provider error carries a RateLimit struct with a non-zero
// RetryAfter (parsed from the upstream's `retry-after` header), that
// duration overrides the per-error-type table — the carrier already
// told us how long to wait, and guessing is worse than respecting the
// signal. RetryAfter == 0 (header absent or unparseable) falls back to
// the per-error-type cooldown so other providers and pre-Phase-3
// callers see no change in behaviour.
//
// User-correctable errors (H7+H8): when the typed provider.Error classifies
// as a user-correctable category — currently ErrorTypeContextWindowExceeded
// (H7) and ErrorTypeAuthFailure (H8) — this function deliberately does NOT
// mark the provider as unavailable. In both cases the fault is attributed
// to the caller's input (oversized prompt) or configuration (mistyped /
// rotated API key), not to the provider, and a long persisted cooldown
// produces a worse failure mode than surfacing the error:
//
//   - ContextWindowExceeded: every provider in the failover chain would
//     refuse the same oversized prompt the same way, so blackballing them
//     in turn just empties the chain on a long cooldown that doesn't
//     recover until well after the request is gone.
//   - AuthFailure: the next call with a fixed credential should succeed
//     immediately. The previous 24h cooldown — persisted to disk — meant a
//     single typo blackballed the provider for 24h across restarts with
//     no admin reset path (see H8 follow-up for `flowstate health reset`).
//
// The error still surfaces to the caller and the per-call observability
// event still fires; only the persistent health-state mutation is skipped.
// The set is intentionally named rather than a blanket IsRetriable gate:
// non-retriable categories like Billing/Quota/ModelNotFound are
// per-credential exhaustion where the long cooldown IS the right signal,
// and failing over to a different provider is meaningful.
//
// Expected:
//   - health is non-nil.
//   - err may be nil (no-op).
//
// Side effects:
//   - May update HealthManager state.
func markProviderHealth(health RateLimitAware, providerName, model string, err error) {
	if err == nil {
		return
	}
	var provErr *provider.Error
	if errors.As(err, &provErr) {
		if isUserCorrectableError(provErr.ErrorType) {
			return
		}
		cooldown := cooldownForProviderError(provErr)
		health.MarkRateLimited(providerName, model, time.Now().Add(cooldown))
		return
	}
	CheckAndMarkRateLimited(health, providerName, model, err)
}

// isUserCorrectableError reports whether the error attributes the failure to
// something the user can fix locally (their prompt or their credentials)
// rather than to the provider's availability. For these categories, marking
// the provider as unhealthy is the wrong response: a long persisted cooldown
// either delays the inevitable user-facing surface (the next request will
// fail the same way until the user fixes the input) or punishes a fixable
// mistake across restarts (a typo'd API key blackballs the provider for 24h
// even after the user corrects it).
//
// Members:
//   - ErrorTypeContextWindowExceeded (H7) — oversized prompt, every
//     provider in the chain would refuse it the same way.
//   - ErrorTypeAuthFailure (H8) — typo'd / rotated key; once fixed the
//     next call must succeed without waiting on a 24h persisted cooldown.
//
// Deliberately NOT in the set: Billing, Quota, ModelNotFound — these are
// per-credential exhaustion where the long cooldown is the right signal
// and failing over to a different provider is meaningful. RateLimit /
// Overload / NetworkError / ServerError stay outside the gate too — they
// are genuinely the provider's fault and the cooldown table is the
// appropriate response.
//
// Expected:
//   - t is a provider error classification.
//
// Returns:
//   - true when t describes a user-correctable failure.
//   - false otherwise.
//
// Side effects:
//   - None.
func isUserCorrectableError(t provider.ErrorType) bool {
	switch t { //nolint:exhaustive // user-correctable subset by design.
	case provider.ErrorTypeContextWindowExceeded, provider.ErrorTypeAuthFailure:
		return true
	default:
		return false
	}
}

// cooldownForProviderError returns the cooldown to apply for a given
// provider.Error. When the error carries a RateLimit with a non-zero
// RetryAfter, that value wins; otherwise the per-error-type table
// applies. Centralised so the failover hook and the rate-limit detector
// share a single carrier-vs-default precedence rule.
//
// Expected:
//   - provErr is a non-nil *provider.Error.
//
// Returns:
//   - The cooldown duration to apply.
//
// Side effects:
//   - None.
func cooldownForProviderError(provErr *provider.Error) time.Duration {
	if provErr.RateLimit != nil && provErr.RateLimit.RetryAfter > 0 {
		return provErr.RateLimit.RetryAfter
	}
	return CooldownForErrorType(provErr.ErrorType)
}

// publishFailoverError publishes a provider error event when a failover
// candidate fails, provided the event bus is configured.
//
// Expected:
//   - ctx is the context which may contain the session ID.
//   - candidate identifies the failed provider/model pair.
//   - err describes the failure.
//
// Returns:
//   - None.
//
// Side effects:
//   - Publishes a provider.error event on the event bus when non-nil.
func (sh *StreamHook) publishFailoverError(ctx context.Context, candidate provider.ModelPreference, err error) {
	if sh.eventBus == nil {
		return
	}

	// Extract session ID from context if available
	sessionID := ""
	if id, ok := ctx.Value(session.IDKey{}).(string); ok {
		sessionID = id
	}

	sh.eventBus.Publish(events.EventProviderError, events.NewProviderErrorEvent(events.ProviderErrorEventData{
		SessionID:    sessionID,
		AgentID:      sh.agentID,
		ProviderName: candidate.Provider,
		ModelName:    candidate.Model,
		Error:        err,
		Phase:        "failover",
	}))
}
