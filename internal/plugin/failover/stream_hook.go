package failover

import (
	"context"
	"errors"
	"time"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

// RetryBackoffConfig tunes the transient-error backoff retry loop in
// StreamHook.Execute. The zero value is usable: NewStreamHook seeds
// sane defaults; callers (and tests) override via SetRetryBackoff.
type RetryBackoffConfig struct {
	// MaxRounds is the maximum number of candidate-chain passes,
	// counting the initial attempt. 0 and 1 disable retry (single
	// pass); a negative value selects the default (4).
	MaxRounds int
	// MaxTotalWait caps cumulative backoff sleep across all rounds.
	MaxTotalWait time.Duration
	// Sleep waits for d or until ctx is done. Injected so tests can
	// model wall-clock recovery deterministically.
	Sleep func(ctx context.Context, d time.Duration) error
}

// withDefaults returns a copy of the config with unset fields
// populated. MaxRounds semantics (R2-02 fix): 0 and 1 both mean "single
// pass, no retry"; a negative value selects the default round count.
func (c RetryBackoffConfig) withDefaults() RetryBackoffConfig {
	if c.MaxRounds < 0 {
		c.MaxRounds = defaultMaxRetryRounds
	}
	if c.MaxRounds == 0 {
		c.MaxRounds = 1
	}
	if c.MaxTotalWait <= 0 {
		c.MaxTotalWait = defaultMaxTotalWait
	}
	if c.Sleep == nil {
		c.Sleep = ctxAwareSleep
	}
	return c
}

const (
	defaultMaxRetryRounds = 4
	defaultMaxTotalWait   = 90 * time.Second
)

// ctxAwareSleep waits for d or returns ctx.Err() on cancellation.
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

// StreamHook is a hook middleware handling multi-provider retry: it
// queries the Manager for healthy candidates and attempts each in
// order. Under the M01 delivery allowlist the candidate pool fails
// closed, so exhaustion surfaces the explicit "no healthy providers
// available" error instead of dispatching an out-of-scope provider.
type StreamHook struct {
	manager *Manager
	agentID string
	retry   RetryBackoffConfig
}

// NewStreamHook creates a StreamHook over the failover manager.
func NewStreamHook(manager *Manager, _ any, agentID string) *StreamHook {
	return &StreamHook{
		manager: manager,
		agentID: agentID,
		retry:   RetryBackoffConfig{}.withDefaults(),
	}
}

// SetRetryBackoff overrides the retry configuration.
func (sh *StreamHook) SetRetryBackoff(cfg RetryBackoffConfig) {
	sh.retry = cfg.withDefaults()
}

// Execute wraps next with multi-provider retry under a bounded,
// cancellable, error-classified policy (R2-02/R2-03). Each round
// attempts every admitted candidate once; candidates are re-queried
// from the manager EVERY round and re-validated at attempt time
// (health + delivery allowlist via the manager's single admission
// path), so a candidate that went stale mid-flight is skipped instead
// of dispatched. Total dispatches are bounded by
// MaxRounds × pool size and cannot be reset by list refreshes; backoff
// sleeps between rounds honour the cumulative wait budget, the injected
// sleeper, and provider Retry-After; permanent (critical-classified)
// errors stop the loop immediately; context cancellation aborts with
// zero further dispatches.
func (sh *StreamHook) Execute(next hook.HandlerFunc) hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		candidates := sh.manager.Candidates()
		if len(candidates) == 0 {
			return nil, errors.New("no healthy providers available")
		}
		cfg := sh.retry
		attemptTimeout := sh.manager.StreamTimeout()
		var lastErr error
		var waited time.Duration
		for round := 0; round < cfg.MaxRounds; round++ {
			if round > 0 {
				backoff := retryBackoffFor(lastErr, round)
				if waited+backoff > cfg.MaxTotalWait {
					backoff = cfg.MaxTotalWait - waited
				}
				if backoff > 0 {
					if err := cfg.Sleep(ctx, backoff); err != nil {
						return nil, err
					}
					waited += backoff
				}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// R2-03: re-query every round so mid-flight health/policy
			// changes are honoured; the previous in-loop `candidates`
			// reassignment mid-round (never re-iterated) was dead code.
			candidates := sh.manager.Candidates()
			for _, candidate := range candidates {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				// R2-03: attempt-time recheck — the pool snapshot may be
				// stale by the time we reach this candidate; a pair that
				// went rate-limited/hard-down or became policy-forbidden
				// mid-flight is skipped, not dispatched.
				if !sh.manager.IsAdmitted(candidate.Provider, candidate.Model) {
					continue
				}
				attemptCtx := ctx
				var cancel context.CancelFunc
				if attemptTimeout > 0 {
					attemptCtx, cancel = context.WithTimeout(ctx, attemptTimeout)
				}
				attemptReq := *req
				attemptReq.Provider = candidate.Provider
				attemptReq.Model = candidate.Model
				sh.manager.RecordAttempt(candidate.Provider, candidate.Model)
				ch, err := next(attemptCtx, &attemptReq)
				if cancel != nil {
					cancel()
				}
				if err == nil {
					sh.manager.SetLast(candidate.Provider, candidate.Model)
					return ch, nil
				}
				lastErr = err
				if provider.IsCriticalStreamError(err) {
					// Permanent auth/billing/quota-class failure: retrying
					// the same or sibling pairs cannot help — stop now.
					return nil, errors.Join(errors.New("permanent provider failure"), err)
				}
			}
			if len(sh.manager.Candidates()) == 0 {
				break
			}
		}
		return nil, errors.Join(errors.New("all providers failed"), lastErr)
	}
}

// retryBackoffFor computes the inter-round backoff: provider-issued
// Retry-After wins when present, otherwise exponential (100ms base,
// doubling, capped at 5s). Deterministic; wall-clock waiting happens
// only in the injected sleeper.
func retryBackoffFor(err error, round int) time.Duration {
	var perr *provider.Error
	if errors.As(err, &perr) && perr != nil && perr.RateLimit != nil && perr.RateLimit.RetryAfter > 0 {
		return perr.RateLimit.RetryAfter
	}
	backoff := 100 * time.Millisecond << (round - 1)
	if backoff > 5*time.Second {
		backoff = 5 * time.Second
	}
	return backoff
}
