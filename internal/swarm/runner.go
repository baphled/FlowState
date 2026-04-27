package swarm

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// Default values applied when a manifest leaves a retry/breaker field
// at its zero value. The runner consults the *Effective* helpers on
// Manifest so call sites do not have to remember which fields default.
const (
	// DefaultRetryMaxAttempts caps a member's dispatch attempts when
	// the manifest does not pin one. Three matches §7 A2's example.
	DefaultRetryMaxAttempts = 3

	// DefaultRetryInitialBackoff is the backoff between attempt 1 and
	// attempt 2 by default; subsequent attempts multiply by Multiplier.
	DefaultRetryInitialBackoff = 1 * time.Second

	// DefaultRetryMaxBackoff bounds the exponential growth of the
	// backoff so a long retry loop does not stretch unbounded.
	DefaultRetryMaxBackoff = 60 * time.Second

	// DefaultRetryMultiplier is the exponential-backoff multiplier.
	DefaultRetryMultiplier = 2.0

	// DefaultBreakerThreshold trips the breaker after this many
	// consecutive retryable failures across the swarm.
	DefaultBreakerThreshold = 5

	// DefaultBreakerCooldown is the window the breaker stays open
	// before allowing a half-open probe.
	DefaultBreakerCooldown = 30 * time.Second

	// DefaultBreakerHalfOpenAttempts is the number of probes allowed
	// while the breaker is half-open. A successful probe closes the
	// breaker; a failure re-opens it.
	DefaultBreakerHalfOpenAttempts = 1
)

// RetryPolicy is the configurable retry shape per §7 A2 of the swarm
// manifest addendum. The runner consults a populated RetryPolicy when
// a member dispatch returns a CategoryRetryable error.
type RetryPolicy struct {
	// MaxAttempts is the upper bound on dispatch attempts (including
	// the first). Zero falls back to DefaultRetryMaxAttempts in
	// EffectiveRetryPolicy.
	MaxAttempts int `json:"max_attempts,omitempty" yaml:"max_attempts,omitempty"`

	// InitialBackoff is the wait between attempt 1 and attempt 2.
	// Subsequent attempts multiply by Multiplier and clamp at
	// MaxBackoff.
	InitialBackoff time.Duration `json:"initial_backoff,omitempty" yaml:"initial_backoff,omitempty"`

	// MaxBackoff caps the per-attempt wait so runaway exponential
	// growth doesn't stretch the retry loop unboundedly.
	MaxBackoff time.Duration `json:"max_backoff,omitempty" yaml:"max_backoff,omitempty"`

	// Multiplier is the exponential-backoff multiplier. 0 falls back
	// to DefaultRetryMultiplier.
	Multiplier float64 `json:"multiplier,omitempty" yaml:"multiplier,omitempty"`

	// Jitter, when true, applies a ±25% random jitter to each backoff
	// interval to avoid thundering-herd retries against a shared
	// dependency. Default true; manifests opt out by setting false.
	Jitter bool `json:"jitter" yaml:"jitter"`
}

// CircuitBreakerConfig is the breaker shape per §7 A2. The runner
// counts consecutive retryable failures across the swarm; once the
// count reaches Threshold the breaker trips and short-circuits
// further dispatches with ErrCircuitOpen until Cooldown elapses.
type CircuitBreakerConfig struct {
	// Threshold is the consecutive-retryable-failure count that trips
	// the breaker. Zero falls back to DefaultBreakerThreshold.
	Threshold int `json:"threshold,omitempty" yaml:"threshold,omitempty"`

	// Cooldown is the window the breaker stays open before allowing
	// a half-open probe.
	Cooldown time.Duration `json:"cooldown,omitempty" yaml:"cooldown,omitempty"`

	// HalfOpenAttempts is the number of probes permitted while the
	// breaker is half-open. Defaults to 1.
	HalfOpenAttempts int `json:"half_open_attempts,omitempty" yaml:"half_open_attempts,omitempty"`
}

// breakerState enumerates the three observable states of the
// circuit breaker. Closed = traffic flows; Open = short-circuited;
// HalfOpen = probing.
type breakerState int

const (
	breakerClosed breakerState = iota
	breakerOpen
	breakerHalfOpen
)

// DispatchFunc is the closure shape the runner re-invokes per
// attempt. The runner passes the member id and a per-attempt context
// (today identical to the outer context); future amendments may
// derive a per-attempt deadline from RetryPolicy.MaxBackoff.
type DispatchFunc func(ctx context.Context, memberID string) error

// Runner wraps a DispatchFunc with retry + circuit-breaker semantics.
// One Runner per swarm run is the expected wiring; the breaker state
// is per-Runner so a long-lived process running multiple swarms back
// to back does not leak failure counts between runs.
type Runner struct {
	policy  RetryPolicy
	breaker CircuitBreakerConfig
	path    string

	mu              sync.Mutex
	state           breakerState
	consecutiveFail int
	openedAt        time.Time
	halfOpenInFlight int

	// sleep is a seam for tests to skip real wall-clock waits. The
	// production wiring uses time.Sleep; tests inject a no-op when
	// they only care about ordering.
	sleep func(time.Duration)

	// rng is a per-Runner random source for jitter. Carries its own
	// mutex via the standard library so concurrent dispatches over
	// the same Runner do not race.
	rng *rand.Rand
}

// NewRunner builds a Runner with the given policy and breaker
// configuration. The returned Runner has the breaker closed and no
// failures recorded.
//
// Expected:
//   - policy and breaker should be populated via
//     Manifest.EffectiveRetryPolicy / EffectiveCircuitBreaker so
//     defaults apply consistently.
//
// Returns:
//   - A *Runner ready for Dispatch.
//
// Side effects:
//   - None.
func NewRunner(policy RetryPolicy, breaker CircuitBreakerConfig) *Runner {
	return &Runner{
		policy:  policy,
		breaker: breaker,
		state:   breakerClosed,
		sleep:   time.Sleep,
		rng:     rand.New(rand.NewSource(time.Now().UnixNano())), // #nosec G404 -- jitter, not crypto
	}
}

// WithSubSwarmPath returns a derived Runner that attaches path to
// every error it surfaces. Used when the runner sits underneath a
// nested swarm so failure messages carry the full
// parent/child trace.
//
// Expected:
//   - path is the slash-delimited prefix; empty means no prefix.
//
// Returns:
//   - The receiver after path mutation; chains for fluent setup.
//
// Side effects:
//   - Mutates the receiver in place.
func (r *Runner) WithSubSwarmPath(path string) *Runner {
	r.path = path
	return r
}

// Dispatch invokes fn for memberID under the runner's retry policy
// and circuit breaker. Returns nil on first or eventual success;
// returns the last categorised error (or ErrCircuitOpen) otherwise.
//
// Expected:
//   - ctx is the swarm-runtime context; cancellation aborts the
//     retry loop with the context's error.
//   - memberID is the agent / sub-swarm id being dispatched.
//   - fn is the closure that performs the underlying dispatch.
//
// Returns:
//   - nil on success.
//   - A *CategorisedError wrapping the cause on retry exhaustion or
//     immediate terminal failure.
//   - An error wrapping ErrCircuitOpen when the breaker is open.
//
// Side effects:
//   - May call fn multiple times under retry.
//   - Mutates the breaker state under the runner's lock.
//   - Calls r.sleep between retry attempts.
func (r *Runner) Dispatch(ctx context.Context, memberID string, fn DispatchFunc) error {
	if blocked := r.checkBreakerBeforeDispatch(); blocked != nil {
		return r.attachPath(blocked, memberID)
	}

	policy := r.policy
	if policy.MaxAttempts < 1 {
		policy.MaxAttempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < policy.MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return r.attachPath(err, memberID)
		}

		err := fn(ctx, memberID)
		if err == nil {
			r.recordSuccess()
			return nil
		}

		category := CategoryOf(err)
		if category != CategoryRetryable {
			r.recordTerminal()
			return r.attachPath(err, memberID)
		}

		r.recordRetryableFailure()
		lastErr = err

		if attempt+1 < policy.MaxAttempts {
			r.sleep(r.computeBackoff(attempt, policy))
		}
	}

	return r.attachPath(lastErr, memberID)
}

// attachPath wraps err with the runner's sub_swarm_path so callers
// downstream see the full trace. Plain errors are wrapped in a
// fresh CategorisedError; existing CategorisedError values get the
// path filled in if they don't already carry one.
func (r *Runner) attachPath(err error, memberID string) error {
	if err == nil {
		return nil
	}
	var ce *CategorisedError
	if asCategorised(err, &ce) {
		if ce.SubSwarmPath == "" {
			ce.SubSwarmPath = r.path
		}
		if ce.MemberID == "" {
			ce.MemberID = memberID
		}
		return err
	}
	return &CategorisedError{
		Category:     CategoryTerminal,
		MemberID:     memberID,
		SubSwarmPath: r.path,
		Cause:        err,
	}
}

// asCategorised mirrors errors.As for the local CategorisedError.
// Pulled into a helper for readability inside attachPath.
func asCategorised(err error, target **CategorisedError) bool {
	for current := err; current != nil; {
		if ce, ok := current.(*CategorisedError); ok {
			*target = ce
			return true
		}
		unwrapped, ok := current.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		current = unwrapped.Unwrap()
	}
	return false
}

// checkBreakerBeforeDispatch consults the breaker state and returns a
// non-nil error when the dispatch must be short-circuited.
// CooldownElapsed transitions Open -> HalfOpen so a probe can fire.
func (r *Runner) checkBreakerBeforeDispatch() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch r.state {
	case breakerClosed:
		return nil
	case breakerHalfOpen:
		if r.halfOpenInFlight >= r.effectiveHalfOpenAttempts() {
			return fmt.Errorf("%w: half-open attempts exhausted", ErrCircuitOpen)
		}
		r.halfOpenInFlight++
		return nil
	case breakerOpen:
		if time.Since(r.openedAt) < r.effectiveCooldown() {
			return fmt.Errorf("%w: cooldown active", ErrCircuitOpen)
		}
		r.state = breakerHalfOpen
		r.halfOpenInFlight = 1
		return nil
	default:
		return nil
	}
}

// recordSuccess closes the breaker and zeroes the failure counter.
func (r *Runner) recordSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consecutiveFail = 0
	r.state = breakerClosed
	r.halfOpenInFlight = 0
}

// recordTerminal leaves the breaker untouched — terminal errors
// indicate user / config faults rather than transient flakiness, so
// they should not trip the breaker against the wider system.
func (r *Runner) recordTerminal() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.state == breakerHalfOpen && r.halfOpenInFlight > 0 {
		r.halfOpenInFlight--
	}
}

// recordRetryableFailure increments the consecutive-failure counter
// and trips the breaker open when the threshold is hit.
func (r *Runner) recordRetryableFailure() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consecutiveFail++
	switch r.state {
	case breakerHalfOpen:
		r.state = breakerOpen
		r.openedAt = time.Now()
		r.halfOpenInFlight = 0
	case breakerClosed:
		if r.consecutiveFail >= r.effectiveThreshold() {
			r.state = breakerOpen
			r.openedAt = time.Now()
		}
	}
}

// computeBackoff returns the per-attempt wait per the manifest's
// retry policy. attempt is zero-based (0 = wait between attempt 1
// and attempt 2).
func (r *Runner) computeBackoff(attempt int, policy RetryPolicy) time.Duration {
	base := policy.InitialBackoff
	if base <= 0 {
		base = DefaultRetryInitialBackoff
	}
	multiplier := policy.Multiplier
	if multiplier <= 0 {
		multiplier = DefaultRetryMultiplier
	}
	maxBackoff := policy.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = DefaultRetryMaxBackoff
	}

	wait := float64(base)
	for i := 0; i < attempt; i++ {
		wait *= multiplier
	}
	if wait > float64(maxBackoff) {
		wait = float64(maxBackoff)
	}
	if policy.Jitter {
		wait = applyJitter(r.rng, wait)
	}
	return time.Duration(wait)
}

// applyJitter applies a ±25% random offset to wait. The 25% bound
// matches §7 A2's example "jitter: 0.25".
func applyJitter(rng *rand.Rand, wait float64) float64 {
	delta := wait * 0.25
	offset := (rng.Float64() * 2 * delta) - delta
	out := wait + offset
	if out < 0 {
		return 0
	}
	return out
}

func (r *Runner) effectiveThreshold() int {
	if r.breaker.Threshold > 0 {
		return r.breaker.Threshold
	}
	return DefaultBreakerThreshold
}

func (r *Runner) effectiveCooldown() time.Duration {
	if r.breaker.Cooldown > 0 {
		return r.breaker.Cooldown
	}
	return DefaultBreakerCooldown
}

func (r *Runner) effectiveHalfOpenAttempts() int {
	if r.breaker.HalfOpenAttempts > 0 {
		return r.breaker.HalfOpenAttempts
	}
	return DefaultBreakerHalfOpenAttempts
}
