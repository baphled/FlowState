package swarm

import (
	"errors"
	"fmt"
)

// ErrorCategory classifies a swarm-runtime error so the runner can
// decide whether to retry, halt, or absorb it. The taxonomy mirrors
// §7 A3 of the swarm-manifest addendum:
//
//   - CategoryRecoverable — internal-only; the runtime handled the
//     condition (e.g. an idempotent retry succeeded). Not surfaced
//     to user-facing layers; included here so the runner can record
//     "we recovered" without inventing a side-channel.
//   - CategoryRetryable — the runner's retry policy applies.
//     Transient network / IO / coord-store hiccups land here. The
//     runner re-invokes the dispatch closure under the manifest's
//     retry policy and counts each consecutive miss against the
//     circuit breaker.
//   - CategoryTerminal — propagate immediately. Manifest validation
//     failures, permission denials, gate fail-fast errors all land
//     here. The runner does not retry; the breaker does not count
//     it.
type ErrorCategory int

const (
	// CategoryUnknown is the zero value. Wrapped errors that don't
	// pass through NewCategorisedError surface here; the runner treats
	// CategoryUnknown the same as CategoryTerminal so an
	// uncategorised plain error never silently triggers a retry.
	CategoryUnknown ErrorCategory = iota

	// CategoryRecoverable names errors the runtime handled internally.
	CategoryRecoverable

	// CategoryRetryable names transient errors the retry policy applies to.
	CategoryRetryable

	// CategoryTerminal names errors that propagate immediately without retry.
	CategoryTerminal
)

// String renders the category for log output. Matches the §7 A3
// "severity" strings on the manifest schema so structured logs can
// equality-match.
func (c ErrorCategory) String() string {
	switch c {
	case CategoryRecoverable:
		return "recoverable"
	case CategoryRetryable:
		return "retryable"
	case CategoryTerminal:
		return "terminal"
	default:
		return "unknown"
	}
}

// CategorisedError is the structured error type that carries an
// ErrorCategory plus the sub-swarm path through the error chain. The
// runner wraps every member-dispatch / gate / lead-synthesis error
// with this type so callers can branch via errors.As.
//
// SubSwarmPath is the slash-delimited trace from the root swarm to the
// failing member (e.g. "bug-hunt/cluster-2/explorer"). The runner
// concatenates parent path + member id at the failure boundary; nested
// sub-swarms layer their own prefix via Context.NestSubSwarm.
type CategorisedError struct {
	// Category is the taxonomy slot the error falls into.
	Category ErrorCategory

	// MemberID names the swarm member whose dispatch produced the
	// error. Empty for swarm-boundary failures (lead synthesis, pre /
	// post-swarm gate misses).
	MemberID string

	// SubSwarmPath is the slash-delimited path from the root swarm to
	// the failure point. Empty when the runner has no path to attach
	// (e.g. a unit test invoking the dispatcher without a Context).
	SubSwarmPath string

	// Cause is the underlying error. Surfaced via Unwrap so
	// errors.Is / errors.As pierce the wrapping cleanly.
	Cause error
}

// NewCategorisedError builds a categorised error around cause. The
// returned pointer is suitable for errors.As recovery; callers that
// also want a sub_swarm_path should set SubSwarmPath on the result
// before returning it.
//
// Expected:
//   - category is the taxonomy slot.
//   - cause is non-nil; passing nil is treated as "wrap a sentinel"
//     and yields a CategorisedError whose Cause is a placeholder so
//     Error() does not panic.
//   - memberID is the swarm member id; may be empty.
//
// Returns:
//   - A populated *CategorisedError.
//
// Side effects:
//   - None.
func NewCategorisedError(category ErrorCategory, cause error, memberID string) *CategorisedError {
	if cause == nil {
		cause = errors.New("(nil cause)")
	}
	return &CategorisedError{
		Category: category,
		MemberID: memberID,
		Cause:    cause,
	}
}

// Error renders the error in a stable shape so log readers can pin
// the format. Includes the category, the path / member context, and
// the underlying cause's message.
func (e *CategorisedError) Error() string {
	if e == nil {
		return "<nil CategorisedError>"
	}
	scope := pathScope(e.SubSwarmPath, e.MemberID)
	if scope == "" {
		return fmt.Sprintf("[%s] %s", e.Category, e.Cause)
	}
	return fmt.Sprintf("[%s] %s: %s", e.Category, scope, e.Cause)
}

// Unwrap exposes the cause for errors.Is / errors.As traversal.
func (e *CategorisedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsRetryable reports whether err's resolved category is
// CategoryRetryable. Uncategorised errors and CategoryUnknown values
// return false so a plain errors.New value never silently triggers a
// retry — explicit categorisation is required.
func IsRetryable(err error) bool {
	return CategoryOf(err) == CategoryRetryable
}

// CategoryOf returns the resolved ErrorCategory for err. Walks the
// errors.As chain so a CategorisedError nested under a fmt.Errorf
// wrapper is still discoverable. Returns CategoryUnknown when no
// CategorisedError is found in the chain.
func CategoryOf(err error) ErrorCategory {
	if err == nil {
		return CategoryUnknown
	}
	var ce *CategorisedError
	if errors.As(err, &ce) {
		return ce.Category
	}
	return CategoryUnknown
}

// pathScope renders the SubSwarmPath/MemberID pair as a slash-joined
// scope string, omitting either side when empty. Centralised so
// Error() and the runner share the same formatting.
func pathScope(path, member string) string {
	switch {
	case path != "" && member != "":
		return path + "/" + member
	case path != "":
		return path
	case member != "":
		return member
	default:
		return ""
	}
}

// ErrCircuitOpen is the sentinel the runner returns when the circuit
// breaker is tripped and a member dispatch is short-circuited.
// Callers test errors.Is(err, ErrCircuitOpen) to detect the shape.
var ErrCircuitOpen = errors.New("swarm: circuit breaker open")
