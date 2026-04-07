package delegation

import (
	"sync"
	"time"
)

// CircuitState represents the operational state of a circuit breaker.
type CircuitState string

const (
	// CircuitClosed indicates normal operation where requests are allowed.
	CircuitClosed CircuitState = "closed"
	// CircuitOpen indicates the circuit has rejected too many requests and should escalate.
	CircuitOpen CircuitState = "open"
	// CircuitHalfOpen indicates a single test request after manual reset.
	CircuitHalfOpen CircuitState = "half_open"
)

// CircuitBreaker implements the circuit breaker pattern for managing reject-regenerate loops.
//
// A circuit breaker prevents endless cycles of regeneration by tracking failures and
// transitioning through states: Closed (normal) → Open (blocked) → HalfOpen (test).
//
// The breaker supports two time-based features:
//   - Failure window: failures expire after a configurable duration, preventing
//     permanent circuit opening from transient failures
//   - Half-open timeout: the circuit automatically transitions from Open to HalfOpen
//     after a configurable duration, allowing self-healing without manual reset
type CircuitBreaker struct {
	// maxFailures is the number of consecutive failures before opening the circuit.
	maxFailures int
	// failures counts consecutive failures in the current failure window.
	failures int
	// state is the current circuit state.
	state CircuitState
	// halfOpenUsed tracks whether the single HalfOpen request has been consumed.
	halfOpenUsed bool
	// lastFailure records the timestamp of the most recent failure.
	lastFailure time.Time
	// failureWindow is the duration after which failures expire.
	// When zero, failures never expire.
	failureWindow time.Duration
	// lastStateChange records when the circuit last transitioned state.
	lastStateChange time.Time
	// halfOpenTimeout is the duration before auto-transitioning from Open to HalfOpen.
	// When zero, the circuit never auto-resets.
	halfOpenTimeout time.Duration
	// mu protects concurrent access to the circuit breaker state.
	mu sync.Mutex
}

// Option configures a CircuitBreaker with custom settings.
// Options are applied via functional options pattern.
type Option func(*CircuitBreaker)

// WithFailureWindow sets the duration after which failures expire.
// When set, failures are automatically reset and the circuit returns to Closed
// if enough time has passed since the last failure, regardless of the current state.
//
// Example:
//
//	cb := NewCircuitBreaker(5, WithFailureWindow(5*time.Minute))
//
// This allows the circuit to recover from transient failures without
// requiring manual intervention or auto-reset.
func WithFailureWindow(d time.Duration) Option {
	return func(cb *CircuitBreaker) { cb.failureWindow = d }
}

// WithHalfOpenTimeout sets the duration before the circuit automatically
// transitions from Open to HalfOpen state, allowing a test request.
//
// Example:
//
//	cb := NewCircuitBreaker(5, WithHalfOpenTimeout(30*time.Second))
//
// When the timeout expires, the next Allow() call will transition the circuit
// to HalfOpen and return true. Subsequent Allow() calls will return false
// until RecordSuccess() or RecordFailure() is called.
func WithHalfOpenTimeout(d time.Duration) Option {
	return func(cb *CircuitBreaker) { cb.halfOpenTimeout = d }
}

// NewCircuitBreaker creates a new circuit breaker with the specified maximum failure threshold.
//
// The maxFailures parameter determines how many consecutive failures are allowed
// before the circuit opens. Use options to configure failure window and auto-reset
// timeouts.
//
// Example:
//
//	cb := NewCircuitBreaker(3)                          // basic
//	cb := NewCircuitBreaker(3, WithFailureWindow(5*time.Minute))  // with expiry
//	cb := NewCircuitBreaker(3, WithHalfOpenTimeout(30*time.Second)) // with auto-reset
func NewCircuitBreaker(maxFailures int, opts ...Option) *CircuitBreaker {
	cb := &CircuitBreaker{
		maxFailures:     maxFailures,
		failures:        0,
		state:           CircuitClosed,
		lastStateChange: time.Now(),
	}
	for _, opt := range opts {
		opt(cb)
	}
	return cb
}

// Allow checks whether a request may proceed based on the current circuit state.
//
// Returns true if the circuit is Closed or HalfOpen and a request may be attempted.
// Returns false if the circuit is Open and requests should be escalated.
//
// In Closed state, Allow() always returns true. If failure window is configured
// and failures have expired, the failure count is reset.
//
// In HalfOpen state, Allow() returns true exactly once, allowing a single test
// request. Subsequent calls return false until RecordSuccess() or RecordFailure().
//
// In Open state, Allow() returns false unless:
//   - Failure window is configured and failures have expired (transitions to Closed)
//   - HalfOpen timeout has expired (transitions to HalfOpen, returns true once)
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		if cb.failureWindow > 0 && cb.failures > 0 {
			if time.Since(cb.lastFailure) > cb.failureWindow {
				cb.failures = 0
			}
		}
		return true
	case CircuitHalfOpen:
		if cb.halfOpenUsed {
			return false
		}
		cb.halfOpenUsed = true
		return true
	case CircuitOpen:
		// Check if failures have expired - if so, reset to Closed
		if cb.failureWindow > 0 && cb.failures > 0 {
			if time.Since(cb.lastFailure) > cb.failureWindow {
				cb.state = CircuitClosed
				cb.failures = 0
				return true
			}
		}
		// Check if auto-reset timeout has expired - transition to HalfOpen
		if cb.halfOpenTimeout > 0 && time.Since(cb.lastStateChange) > cb.halfOpenTimeout {
			cb.state = CircuitHalfOpen
			cb.halfOpenUsed = true // Immediately consume the half-open slot
			return true
		}
		return false
	default:
		return false
	}
}

// RecordSuccess registers a successful operation and resets the failure counter.
//
// When the circuit is in HalfOpen state, a successful operation transitions
// the circuit back to Closed state and resets the failure count, indicating
// the underlying issue has resolved.
//
// When the circuit is already in Closed state, only the failure count is reset.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitHalfOpen:
		cb.state = CircuitClosed
		cb.failures = 0
	case CircuitClosed:
		cb.failures = 0
	}
}

// RecordFailure registers a failed operation and may transition the circuit to Open.
//
// When the circuit is Closed and the failure count reaches maxFailures,
// the circuit transitions to Open, blocking further requests until recovery.
//
// When the circuit is in HalfOpen, any failure transitions back to Open,
// indicating the underlying issue persists.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = time.Now()

	switch cb.state {
	case CircuitClosed:
		if cb.failures >= cb.maxFailures {
			cb.state = CircuitOpen
			cb.lastStateChange = time.Now()
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
		cb.lastStateChange = time.Now()
	}
}

// State returns the current circuit state.
//
// The state indicates whether requests are allowed (Closed), blocked (Open),
// or in recovery testing (HalfOpen).
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.state
}

// Failures returns the current consecutive failure count.
//
// The failure count is reset by RecordSuccess() or automatically expired
// after the failure window duration (if configured).
func (cb *CircuitBreaker) Failures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.failures
}

// Reset transitions the circuit from Open to HalfOpen state.
//
// This allows a single test request to determine if the underlying issue
// has been resolved. The failure count is preserved for analysis.
// Contrast with automatic recovery via failure window expiry or half-open timeout.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitHalfOpen
	cb.halfOpenUsed = false
	cb.lastStateChange = time.Now()
}
