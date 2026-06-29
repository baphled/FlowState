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

// Default bounds for the transient-error backoff retry loop. A swarm member
// that hits a TRANSIENT provider failure (HTTP 429 / rate_limit, a network
// blip, a 5xx) on EVERY capable candidate must not kill the whole run — it
// should back off until the soonest provider recovers and retry, bounded so
// it can never hang. See StreamHook.Execute for the full rationale.
const (
	// Caps the number of full candidate-chain passes (1 initial attempt +
	// N-1 retries). After the cap the loop fails terminally with "all
	// providers failed" — the prior behaviour.
	defaultMaxRetryRounds = 4
	// Caps the cumulative time spent sleeping between retry rounds,
	// independent of the per-round window. Even if every provider reports a
	// long cooldown the loop gives up after this budget so a caller waiting
	// on the stream is never blocked indefinitely.
	defaultMaxTotalWait = 90 * time.Second
	// Floors a computed backoff so a near-zero or already-elapsed cooldown
	// still yields forward progress (a tight re-attempt) without a busy spin.
	minBackoffWait = 50 * time.Millisecond
	// Caps a single round's sleep so one provider reporting a multi-minute
	// Retry-After does not consume the whole MaxTotalWait budget in one wait.
	maxPerRoundWait = 30 * time.Second
)

// RetryBackoffConfig tunes the transient-error backoff retry loop in
// StreamHook.Execute. The zero value is NOT usable directly — NewStreamHook
// seeds sane defaults; callers (and tests) override via SetRetryBackoff.
type RetryBackoffConfig struct {
	// MaxRounds is the maximum number of candidate-chain passes, counting
	// the initial attempt. MaxRounds <= 1 disables retry (single pass,
	// legacy behaviour). Values <= 0 fall back to defaultMaxRetryRounds.
	MaxRounds int
	// MaxTotalWait caps cumulative backoff sleep across all rounds. Values
	// <= 0 fall back to defaultMaxTotalWait.
	MaxTotalWait time.Duration
	// Sleep waits for d or until ctx is done, returning ctx.Err() on
	// cancellation. Injected so tests can model wall-clock recovery
	// deterministically without real sleeps. Nil falls back to the
	// ctx-aware default.
	Sleep func(ctx context.Context, d time.Duration) error
}

// withDefaults returns a copy of the config with any unset field populated
// from the package defaults, so a partially-specified override (common in
// tests) stays safe.
func (c RetryBackoffConfig) withDefaults() RetryBackoffConfig {
	if c.MaxRounds <= 0 {
		c.MaxRounds = defaultMaxRetryRounds
	}
	if c.MaxTotalWait <= 0 {
		c.MaxTotalWait = defaultMaxTotalWait
	}
	if c.Sleep == nil {
		c.Sleep = ctxAwareSleep
	}
	return c
}

// ctxAwareSleep waits for d or returns early with ctx.Err() if ctx is done
// first. It is the production backoff: respecting ctx cancellation/deadline
// during the wait is mandatory so a cancelled request never sleeps on.
func ctxAwareSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// StreamHook is a hook.Hook middleware that handles multi-provider retry with
// peek-and-replay. It queries the Manager for healthy candidates and tries each
// in order, peeking at the first chunk to detect async errors before committing.
type StreamHook struct {
	manager  *Manager
	eventBus *eventbus.EventBus
	agentID  string
	retry    RetryBackoffConfig
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
	return &StreamHook{
		manager:  manager,
		eventBus: bus,
		agentID:  agentID,
		retry:    RetryBackoffConfig{}.withDefaults(),
	}
}

// SetRetryBackoff overrides the transient-error backoff retry configuration.
// Unset fields in cfg are populated from the package defaults, so callers may
// override only the field they care about. Primarily a test seam for injecting
// a deterministic Sleep, but also the wiring point for an operator-configurable
// cap should the app layer expose one.
//
// Expected:
//   - cfg carries the desired bounds and/or Sleep; unset fields default.
//
// Side effects:
//   - Replaces the receiver's retry config.
func (sh *StreamHook) SetRetryBackoff(cfg RetryBackoffConfig) {
	sh.retry = cfg.withDefaults()
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

		// Thread the agent's OWN ordered preferred_models chain ahead of
		// the manager's base preferences (the global config `default:`
		// chain). resolveChildModelOverride stamps only the agent's
		// tier-0 pair on req.Provider/req.Model; without the full chain
		// the failover loop would, on a tier-0 failure, cascade straight
		// to the global default and skip the agent's tier-1/tier-2
		// fallbacks. For a deployment whose global default cannot
		// reliably emit structured tool calls, that strands a swarm
		// member on the unreliable model and halts the planning loop.
		//
		// prependAgentChain inserts every healthy chain tier (in
		// declared order) at the head of the candidate list, deduped
		// against the base pool, so the loop exhausts the agent's own
		// chain BEFORE the global default. An absent/empty chain is the
		// dominant non-swarm case and leaves candidates untouched —
		// preserving the prior cascade-to-global-default behaviour.
		candidates = sh.prependAgentChain(ctx, candidates)

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
		//
		// When an agent chain was threaded above, req.Provider/req.Model
		// is the chain's tier-0 pair, so promotePinned simply re-affirms
		// the head it already produced (a no-op reorder). The pin path
		// stays load-bearing for the in-turn retry case where no chain
		// is present.
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

		// state carries failure bookkeeping across retry rounds:
		// state.lastErr is surfaced in the terminal wrap, and
		// state.previousFailed records the FIRST candidate that failed so
		// an eventual success still surfaces the user-visible
		// "provider_changed" switch-away toast ("switched away from
		// <primary>"). Only the first failure is tracked — a chain of
		// failures collapses to "primary failed" in the user's mental
		// model, and listing every retired candidate would be noise.
		var state retryState

		// totalWaited accumulates backoff sleep across rounds so the loop
		// gives up once the MaxTotalWait budget is exhausted, independent
		// of the per-round window. This is the hang-guard: even if every
		// provider keeps reporting a long cooldown the caller is never
		// blocked past MaxTotalWait.
		var totalWaited time.Duration

		// Transient-error backoff retry loop. A round is one full pass over
		// the current healthy candidate chain. When EVERY failure in a round
		// is TRANSIENT (HTTP 429 / rate_limit, a network blip, a 5xx) the
		// candidates were all cooled down in health, so a naive return here
		// would kill the whole swarm on a recoverable blip. Instead we back
		// off until the soonest candidate's cooldown expires and re-attempt.
		//
		// The loop is bounded two ways — MaxRounds (attempt count) and
		// MaxTotalWait (cumulative sleep) — so it can never hang. A
		// PERMANENT error (auth/401, invalid model/400, context-length) is
		// never retried: roundAllTransient goes false and we fail fast.
		for round := range sh.retry.MaxRounds {
			if round > 0 {
				candidates = sh.nextRoundCandidates(ctx, req)
				if len(candidates) == 0 {
					break
				}
			}

			replayCh, attempts, ok := sh.runCandidateRound(ctx, next, req, candidates, &state)
			if ok {
				return sh.decorateSuccess(ctx, replayCh, attempts.winner, state.previousFailed), nil
			}

			waited, retry := sh.backoffBeforeRetry(ctx, round, attempts, totalWaited)
			if !retry {
				break
			}
			totalWaited += waited
		}
		return nil, fmt.Errorf("all providers failed: %w", state.lastErr)
	}
}

// nextRoundCandidates re-fetches the healthy candidate chain for a retry round.
// The backoff between rounds waited for cooldowns to lapse, so previously
// rate-limited pairs should now re-enter the pool. The same agent-chain and
// pinned-provider ordering applied on the first pass is re-applied so retry
// rounds honour the agent's preferred order.
//
// Expected:
//   - req carries the pinned provider/model (if any) to re-promote.
//
// Returns:
//   - The ordered healthy candidate chain (possibly empty).
//
// Side effects:
//   - None.
func (sh *StreamHook) nextRoundCandidates(ctx context.Context, req *provider.ChatRequest) []provider.ModelPreference {
	candidates := sh.manager.Candidates()
	candidates = sh.prependAgentChain(ctx, candidates)
	return promotePinned(candidates, req.Provider, req.Model)
}

// decorateSuccess prepends the model_active (and, on failover, provider_changed)
// observability chunks to a winning replay channel.
//
// The model_active chunk fires on EVERY successful stream so the chat UI can
// pivot the toolbar chip from the user's selection to the actual model the
// moment streaming starts. The provider_changed chunk fires only when an
// earlier candidate failed, announcing the switch-away from the primary. Order
// is model_active first; the relative order is unspecified at the consumer.
//
// Expected:
//   - replayCh is the winning candidate's stream.
//   - winner identifies the provider/model that succeeded.
//   - previousFailed is the first failure this call, or nil.
//
// Returns:
//   - The decorated replay channel.
//
// Side effects:
//   - None.
func (sh *StreamHook) decorateSuccess(
	ctx context.Context,
	replayCh <-chan provider.StreamChunk,
	winner provider.ModelPreference,
	previousFailed *failedCandidate,
) <-chan provider.StreamChunk {
	replayCh = prependModelActiveChunk(ctx, replayCh, winner)
	if previousFailed != nil {
		replayCh = prependProviderChangedChunk(ctx, replayCh, previousFailed, winner)
	}
	return replayCh
}

// backoffBeforeRetry decides whether to back off and start another retry round,
// and performs the wait. It returns the duration actually slept and whether the
// caller should retry. We retry only when: the round actually attempted a
// candidate, EVERY failure was transient, this is not the last permitted round,
// and the cumulative wait budget is not exhausted. A ctx cancel/deadline during
// the sleep aborts the retry (returns retry=false) so a cancelled request never
// sleeps on.
//
// Expected:
//   - round is the zero-based round index just completed.
//   - attempts summarises the failed round.
//   - totalWaited is the cumulative sleep so far.
//
// Returns:
//   - The duration slept (zero when not retrying) and whether to retry.
//
// Side effects:
//   - Sleeps via the configured Sleep func.
func (sh *StreamHook) backoffBeforeRetry(
	ctx context.Context,
	round int,
	attempts roundOutcome,
	totalWaited time.Duration,
) (time.Duration, bool) {
	if round == sh.retry.MaxRounds-1 || !attempts.allTransient || len(attempts.pairs) == 0 {
		return 0, false
	}
	wait := computeBackoff(sh.manager.Health(), attempts.pairs)
	if remaining := sh.retry.MaxTotalWait - totalWaited; wait > remaining {
		wait = remaining
	}
	if wait <= 0 {
		return 0, false
	}
	if err := sh.retry.Sleep(ctx, wait); err != nil {
		return 0, false
	}
	return wait, true
}

// roundOutcome summarises one pass over the candidate chain for the retry
// loop. It never escapes Execute.
type roundOutcome struct {
	// winner is the candidate whose stream succeeded; only meaningful when
	// runCandidateRound reports ok==true.
	winner provider.ModelPreference
	// pairs are the (provider, model) candidates attempted this round, in
	// order, used to compute the soonest cooldown for backoff.
	pairs []provider.ModelPreference
	// allTransient is true when every failure this round was a TRANSIENT
	// provider error (rate-limit / overload / network / 5xx). A single
	// permanent failure (auth, model-not-found, context-window) sets it
	// false so the loop fails fast instead of retrying.
	allTransient bool
}

// retryState carries the failure bookkeeping that must persist ACROSS retry
// rounds: the most recent error (surfaced in the terminal "all providers
// failed" wrap) and the first failed candidate (powering the user-visible
// provider_changed switch-away toast on an eventual success).
type retryState struct {
	lastErr        error
	previousFailed *failedCandidate
}

// runCandidateRound attempts each candidate once, returning the success replay
// channel (ok==true) or a summary used to decide on backoff. The shared
// retryState is updated in place so its semantics persist across rounds.
//
// Expected:
//   - candidates is the current healthy chain for this round (non-empty).
//   - state is the cross-round failure bookkeeping (non-nil).
//
// Returns:
//   - The replay channel and winning candidate on the first success.
//   - The round outcome (attempted pairs, transient-only flag) when no
//     candidate succeeded; ok is false in that case.
//
// Side effects:
//   - Sets req.Provider / req.Model per attempt.
//   - Updates state.lastErr / state.previousFailed.
//   - Publishes a provider.error event for each failed attempt via publishFailoverError.
func (sh *StreamHook) runCandidateRound(
	ctx context.Context,
	next hook.HandlerFunc,
	req *provider.ChatRequest,
	candidates []provider.ModelPreference,
	state *retryState,
) (<-chan provider.StreamChunk, roundOutcome, bool) {
	outcome := roundOutcome{allTransient: true}
	for _, candidate := range candidates {
		req.Provider = candidate.Provider
		req.Model = candidate.Model
		outcome.pairs = append(outcome.pairs, candidate)

		replayCh, err := sh.attemptCandidate(ctx, next, req, candidate)
		if err != nil {
			state.lastErr = err
			if !isTransientFailoverError(err) {
				outcome.allTransient = false
			}
			if state.previousFailed == nil {
				state.previousFailed = &failedCandidate{
					provider: candidate.Provider,
					model:    candidate.Model,
					reason:   classifyFailoverReason(err),
				}
			}
			continue
		}
		outcome.winner = candidate
		return replayCh, outcome, true
	}
	return nil, outcome, false
}

// isTransientFailoverError reports whether err is a TRANSIENT provider failure
// that warrants a backoff-and-retry rather than a terminal abort. It reuses
// provider.IsRetriableErrorType (rate-limit / overload / network / server) for
// typed errors and the rate-limit keyword fallback for untyped ones. Permanent
// failures — auth (401/403), model-not-found / invalid model (400), context
// length, billing/quota — return false so the loop fails fast.
//
// Expected:
//   - err may be nil (returns false).
//
// Returns:
//   - true when err is a retryable transient provider error.
//
// Side effects:
//   - None.
func isTransientFailoverError(err error) bool {
	if err == nil {
		return false
	}
	var provErr *provider.Error
	if errors.As(err, &provErr) {
		return provider.IsRetriableErrorType(provErr.ErrorType)
	}
	// Untyped error: only the rate-limit keyword shapes are treated as
	// transient. Anything else is conservatively permanent — retrying an
	// unclassifiable failure risks spinning on a genuine hard error.
	return (&RateLimitDetector{}).isRateLimitedError(err)
}

// computeBackoff returns how long to wait before the next retry round. It picks
// the SOONEST RateLimitedUntil across the attempted pairs (so we wake as soon
// as any provider recovers, respecting carrier-issued Retry-After, which is
// baked into the cooldown via cooldownForProviderError). The result is floored
// at minBackoffWait (forward progress without a busy spin) and capped at
// maxPerRoundWait (one long cooldown must not consume the whole budget).
//
// Expected:
//   - health is the manager's HealthManager.
//   - pairs are the candidates attempted this round (non-empty).
//
// Returns:
//   - The clamped wait duration.
//
// Side effects:
//   - None.
func computeBackoff(health *HealthManager, pairs []provider.ModelPreference) time.Duration {
	var soonest time.Time
	for _, p := range pairs {
		until, ok := health.RateLimitedUntil(p.Provider, p.Model)
		if !ok {
			// This pair is already healthy again — re-attempt immediately.
			return minBackoffWait
		}
		if soonest.IsZero() || until.Before(soonest) {
			soonest = until
		}
	}
	if soonest.IsZero() {
		return minBackoffWait
	}
	wait := time.Until(soonest)
	if wait < minBackoffWait {
		wait = minBackoffWait
	}
	if wait > maxPerRoundWait {
		wait = maxPerRoundWait
	}
	return wait
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
// a {"type":"provider_changed",...} SSE event.
//
// Format choices:
//   - From / To are "<provider>+<model>" joined strings (the legacy
//     shape). They are retained on the wire for backwards-compat with
//     consumers still parsing the joined form.
//   - FromProvider / FromModel / ToProvider / ToModel are the new
//     split shape that mirrors sseModelActive's (provider, model)
//     pair. The chat UI's chip prefers these — splitting on "+"
//     re-introduced a parse step and off-by-one bugs around model ids
//     that themselves contain "+" (rare; openrouter). All four are
//     populated on every emit so consumers can migrate gracefully.
//   - Reason is a stable machine-readable string ("rate_limited",
//     "model_not_found", "auth_failure", "timeout", "unknown") that the
//     frontend maps to plain English. Keeping the mapping on the
//     frontend side decouples copy-changes from the Go release cycle.
type providerChangedPayload struct {
	From         string `json:"from"`
	To           string `json:"to"`
	FromProvider string `json:"from_provider"`
	FromModel    string `json:"from_model"`
	ToProvider   string `json:"to_provider"`
	ToModel      string `json:"to_model"`
	Reason       string `json:"reason"`
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
//   - ctx is the parent context — when it cancels (SSE consumer disconnect,
//     user Escape, navigation away), the wrapper goroutine MUST exit
//     promptly and close `out`. Without ctx-awareness on the send the
//     goroutine parked on a full `out` (consumer stopped draining) would
//     leak for the full per-attempt stream timeout — see M2 fix.
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
//     ctx-aware sends exit on parent cancel; upstream's own goroutine
//     terminates on its per-attempt timeoutCtx.
func prependProviderChangedChunk(
	ctx context.Context,
	upstream <-chan provider.StreamChunk,
	failed *failedCandidate,
	newCandidate provider.ModelPreference,
) <-chan provider.StreamChunk {
	if failed == nil {
		return upstream
	}
	payload := providerChangedPayload{
		From:         failed.provider + "+" + failed.model,
		To:           newCandidate.Provider + "+" + newCandidate.Model,
		FromProvider: failed.provider,
		FromModel:    failed.model,
		ToProvider:   newCandidate.Provider,
		ToModel:      newCandidate.Model,
		Reason:       failed.reason,
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
		select {
		case out <- transition:
		case <-ctx.Done():
			return
		}
		for chunk := range upstream {
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
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
//   - ctx is the parent context — when it cancels (SSE consumer disconnect,
//     user Escape, navigation away), the wrapper goroutine MUST exit
//     promptly and close `out`. Without ctx-awareness on the send the
//     goroutine parked on a full `out` (consumer stopped draining) would
//     leak for the full per-attempt stream timeout — see M2 fix.
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
//     ctx-aware sends exit on parent cancel; upstream's own goroutine
//     terminates on its per-attempt timeoutCtx.
//   - On a marshal failure (unreachable in practice for a 2-string struct)
//     returns the upstream verbatim — the chip stays on the optimistic
//     selection rather than a malformed stream.
func prependModelActiveChunk(
	ctx context.Context,
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
		select {
		case out <- active:
		case <-ctx.Done():
			return
		}
		for chunk := range upstream {
			select {
			case out <- chunk:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}

// prependAgentChain threads the agent's own ordered preferred_models
// chain (carried on ctx via session.WithPreferredModels) ahead of the
// manager's base preferences, so the failover loop exhausts the agent's
// declared tiers before falling back to the global config default.
//
// Behaviour:
//   - No chain on ctx (the dominant non-swarm case): the base
//     candidate list is returned unchanged, preserving the prior
//     cascade-to-global-default semantics exactly.
//   - Chain present: each chain tier is health-filtered (rate-limited
//     pairs are dropped, just as Manager.healthyCandidates does for
//     the base pool) and emitted at the head in declared order. Base
//     candidates already named by a chain tier are not duplicated —
//     the chain's position wins. Remaining base candidates follow as
//     the final fallback pool (the global default lives here).
//
// The result is always a fresh slice when a chain is applied; the input
// is returned directly when there is nothing to do.
//
// Expected:
//   - candidates is the manager's healthy base preference list
//     (callers ensure it is non-empty).
//
// Returns:
//   - candidates unchanged when no chain is present; otherwise the
//     health-filtered chain tiers followed by the deduped base pool.
//
// Side effects:
//   - None.
func (sh *StreamHook) prependAgentChain(ctx context.Context, candidates []provider.ModelPreference) []provider.ModelPreference {
	chain := session.PreferredModelsFromContext(ctx)
	if len(chain) == 0 {
		return candidates
	}
	health := sh.manager.Health()
	seen := make(map[provider.ModelPreference]struct{}, len(chain))
	ordered := make([]provider.ModelPreference, 0, len(chain)+len(candidates))
	for _, pref := range chain {
		if _, dup := seen[pref]; dup {
			continue
		}
		// Health-filter chain tiers on the same axis as the base pool
		// so a rate-limited agent tier is skipped rather than retried
		// into a guaranteed 429. A nil health manager (legacy test
		// surface) admits every tier.
		if health != nil && health.IsRateLimited(pref.Provider, pref.Model) {
			seen[pref] = struct{}{}
			continue
		}
		seen[pref] = struct{}{}
		ordered = append(ordered, pref)
	}
	for _, c := range candidates {
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		ordered = append(ordered, c)
	}
	if len(ordered) == 0 {
		// Every chain tier was rate-limited and the base pool was
		// empty/all-deduped — fall back to the original list so the
		// caller's "no healthy providers" gate still fires correctly.
		return candidates
	}
	return ordered
}

// promotePinned returns candidates with the caller-pinned provider/model
// moved to the head of the list. If pinnedProvider is empty the input
// slice is returned unchanged.
//
// Matching semantics:
//   - empty pinnedModel: match by provider name only. If a matching
//     candidate is found, promote it; otherwise return the input
//     unchanged (caller is specifying "any model from this provider"
//     and the candidates list is authoritative on which models exist).
//   - non-empty pinnedModel: match by (provider, model) exactly. If a
//     matching candidate is found, promote it. If NO candidate matches,
//     INSERT the pinned pair at the head — this is the cascade-honour
//     branch: the caller has explicitly stamped a pair via the engine's
//     ctx override (Agent Provider Cascade, May 2026), and the
//     failover pool must respect the caller's intent rather than
//     silently overwriting req.Provider/Model from its base
//     preferences. The remaining candidates stay in place as the
//     fallback pool for the case where the pinned pair fails health-
//     check during attempt.
//
// The matched entry is kept in its ModelPreference form so downstream
// logic (health-marking, last-set) sees the exact pairing.
//
// Expected:
//   - candidates is a non-empty slice (callers ensure this).
//   - pinnedProvider may be empty (no-op).
//
// Returns:
//   - A slice with the matched (or inserted) pinned pair first,
//     followed by the remaining candidates in their original relative
//     order; or the input slice unchanged when no promotion or
//     insertion applies.
//
// Side effects:
//   - None (returns a new slice when promotion or insertion happens;
//     returns the input slice directly when no change is needed, so
//     callers must not mutate it in place regardless).
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
	if idx == -1 {
		// No exact match. When the caller pinned both provider AND
		// model the cascade-honour branch inserts the pair at the
		// head; the failover pool follows as the fallback. Provider-
		// only pins fall through to the input slice unchanged — the
		// caller did not specify a model so the pool's existing entry
		// for that provider (if any) was sufficient and we must not
		// synthesise a fake (provider, "") row.
		if pinnedModel == "" {
			return candidates
		}
		head := provider.ModelPreference{Provider: pinnedProvider, Model: pinnedModel}
		reordered := make([]provider.ModelPreference, 0, len(candidates)+1)
		reordered = append(reordered, head)
		reordered = append(reordered, candidates...)
		return reordered
	}
	if idx == 0 {
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
		// A stall before the first chunk (the per-attempt StreamTimeout or a
		// clamped parent deadline) is a transport-level failure. Tag it as a
		// retriable NetworkError and health-mark it so a persistently-stalling
		// provider is cooled down and SKIPPED on subsequent turns instead of
		// re-stalled every turn. Without the typed wrap, markProviderHealth's
		// keyword-only fallback misses the bare context-deadline error.
		peekErr = failoverTransportError(candidate.Provider, peekErr)
		markProviderHealth(sh.manager.Health(), candidate.Provider, candidate.Model, peekErr)
		sh.publishFailoverError(ctx, candidate, peekErr)
		return nil, peekErr
	}
	if !ok {
		cancel()
		// A stream that opens then closes without emitting any chunk produced
		// nothing usable — a transport-level failure. Health-mark it so it is
		// skipped next turn rather than re-tried into the same empty result.
		closeErr := failoverTransportError(candidate.Provider,
			fmt.Errorf("provider %s: stream closed immediately", candidate.Provider))
		markProviderHealth(sh.manager.Health(), candidate.Provider, candidate.Model, closeErr)
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
//
// failoverTransportError tags a transport-level failover failure — a pre-first-
// chunk stall (peek-timeout) or an immediately-closed stream — as a retriable
// NetworkError so markProviderHealth's typed branch applies a cooldown and the
// candidate is skipped on subsequent turns instead of re-stalled. The original
// error is preserved via RawError and surfaced in the wrapper's Error() string,
// so upstream errors.As/errors.Is and the existing substring observability
// assertions ("stream closed immediately", context-deadline) still hold.
//
// Expected:
//   - providerName identifies the failing candidate.
//   - err is the underlying transport error (non-nil).
//
// Returns:
//   - A retriable *provider.Error{NetworkError} wrapping err.
//
// Side effects:
//   - None.
func failoverTransportError(providerName string, err error) *provider.Error {
	return &provider.Error{
		ErrorType:   provider.ErrorTypeNetworkError,
		Provider:    providerName,
		Message:     err.Error(),
		IsRetriable: true,
		RawError:    err,
	}
}

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
//   - ErrorTypeModelNotFound (H9) — wrong model ID in agent manifest;
//     the provider remains healthy for other models, so blackballing it
//     for 24h cascades into a total outage when other providers are down.
//     next call must succeed without waiting on a 24h persisted cooldown.
//
// Deliberately NOT in the set: Billing, Quota — these are
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
	case provider.ErrorTypeContextWindowExceeded, provider.ErrorTypeAuthFailure, provider.ErrorTypeModelNotFound:
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
