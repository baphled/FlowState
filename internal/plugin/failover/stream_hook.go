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
	// counting the initial attempt. MaxRounds <= 1 disables retry.
	MaxRounds int
	// MaxTotalWait caps cumulative backoff sleep across all rounds.
	MaxTotalWait time.Duration
	// Sleep waits for d or until ctx is done. Injected so tests can
	// model wall-clock recovery deterministically.
	Sleep func(ctx context.Context, d time.Duration) error
}

// withDefaults returns a copy of the config with unset fields populated.
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

// Execute wraps next with multi-provider retry. Each round attempts
// every healthy candidate once; the per-attempt provider/model is
// stamped on the request before dispatch.
func (sh *StreamHook) Execute(next hook.HandlerFunc) hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		candidates := sh.manager.Candidates()
		if len(candidates) == 0 {
			return nil, errors.New("no healthy providers available")
		}
		var lastErr error
		for round := range sh.retry.MaxRounds {
			_ = round
			for _, candidate := range candidates {
				attemptReq := *req
				attemptReq.Provider = candidate.Provider
				attemptReq.Model = candidate.Model
				ch, err := next(ctx, &attemptReq)
				if err == nil {
					sh.manager.SetLast(candidate.Provider, candidate.Model)
					return ch, nil
				}
				lastErr = err
				// Re-resolve: a rate-limited head may have removed
				// candidates from the pool.
				candidates = sh.manager.Candidates()
				if len(candidates) == 0 {
					break
				}
			}
			if len(sh.manager.Candidates()) == 0 {
				break
			}
		}
		return nil, errors.Join(errors.New("all providers failed"), lastErr)
	}
}
