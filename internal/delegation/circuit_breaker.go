package delegation

import (
	"sync"
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
type CircuitBreaker struct {
	maxFailures  int
	failures     int
	state        CircuitState
	halfOpenUsed bool
	mu           sync.Mutex
}

// NewCircuitBreaker creates a new circuit breaker with the specified maximum failure threshold.
//
// Expected:
//   - maxFailures specifies the number of consecutive failures before opening the circuit.
//
// Returns:
//   - A pointer to a new CircuitBreaker initialised in the Closed state.
//
// Side effects:
//   - Allocates a new CircuitBreaker instance.
func NewCircuitBreaker(maxFailures int) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures: maxFailures,
		failures:    0,
		state:       CircuitClosed,
	}
}

// Allow checks whether a request may proceed based on the current circuit state.
//
// Returns:
//   - true if the circuit is Closed or HalfOpen and a request may be attempted.
//   - false if the circuit is Open and requests should be escalated to the user.
//
// Note: In HalfOpen state, Allow() returns true only once. After the first call,
//
//	subsequent calls return false until RecordSuccess() or RecordFailure()
//	is called to transition the state.
//
// Side effects:
//   - Locks the circuit breaker mutex.
//   - Marks HalfOpen as used on the first permitted call.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitHalfOpen:
		if cb.halfOpenUsed {
			return false
		}
		cb.halfOpenUsed = true
		return true
	case CircuitOpen:
		return false
	default:
		return false
	}
}

// RecordSuccess registers a successful operation and resets the failure counter.
//
// When the circuit is in HalfOpen state, a successful operation transitions
// the circuit back to Closed state and resets the failure count.
//
// Side effects:
//   - Locks the circuit breaker mutex.
//   - Mutates the circuit state and failure counter.
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
// the circuit transitions to Open. When in HalfOpen, any failure transitions
// back to Open.
//
// Side effects:
//   - Locks the circuit breaker mutex.
//   - Increments the failure counter.
//   - Mutates the circuit state when thresholds are reached.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++

	switch cb.state {
	case CircuitClosed:
		if cb.failures >= cb.maxFailures {
			cb.state = CircuitOpen
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
	}
}

// State returns the current circuit state.
//
// Returns:
//   - The current CircuitState (Closed, Open, or HalfOpen).
//
// Side effects:
//   - Locks the circuit breaker mutex.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.state
}

// Failures returns the current failure count.
//
// Returns:
//   - The number of consecutive failures recorded.
//
// Side effects:
//   - Locks the circuit breaker mutex.
func (cb *CircuitBreaker) Failures() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.failures
}

// Reset transitions the circuit from Open to HalfOpen state.
//
// This allows a single test request to determine if the underlying issue
// has been resolved. The failure count is preserved to allow analysis.
//
// Side effects:
//   - Locks the circuit breaker mutex.
//   - Moves the circuit into HalfOpen state.
//   - Clears the HalfOpen usage flag.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = CircuitHalfOpen
	cb.halfOpenUsed = false
}
