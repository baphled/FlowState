package provider

import (
	"errors"
	"strings"
)

// StreamErrorSeverity classifies a streaming error by the urgency with which
// the consumer (the TUI, the API surface) must react to it.
//
// P18a introduces severity so the chat intent can:
//
//   - render every error inline in the transcript (not silently drop),
//   - escalate *critical* conditions (invalid API key, auth failure, quota
//     exceeded) to slog.Error / stderr so they survive a TUI crash and
//     reach whatever log aggregator the operator has wired up.
//
// The enum deliberately stays small — three tiers cover the display-vs-log
// decision the consumer has to make. If more granularity is ever needed we
// can layer a second dimension on top without renumbering the existing
// constants.
type StreamErrorSeverity int

const (
	// SeverityTransient indicates a condition that is likely self-healing on
	// retry: network blips, context deadlines, brief rate limits. Shown
	// inline; not logged to stderr.
	SeverityTransient StreamErrorSeverity = iota

	// SeverityUser indicates a user-facing failure that is neither
	// obviously retry-able nor obviously critical: parse errors, unknown
	// provider responses, miscellaneous failures. Shown inline only. This
	// is the default classification for unrecognised errors.
	SeverityUser

	// SeverityCritical indicates a condition that requires human
	// intervention: invalid credentials, auth failures, quota exhaustion,
	// missing configuration. Shown inline AND logged to stderr via
	// slog.Error so an operator inspecting the log sees the condition even
	// after the TUI exits.
	SeverityCritical
)

// String returns a short, stable identifier for the severity suitable for
// use as a slog attribute value or a test assertion.
//
// Expected:
//   - s is one of the defined StreamErrorSeverity constants.
//
// Returns:
//   - "transient", "user", or "critical". Unknown values render as "user"
//     to match the default classification.
//
// Side effects:
//   - None.
func (s StreamErrorSeverity) String() string {
	switch s {
	case SeverityTransient:
		return "transient"
	case SeverityCritical:
		return "critical"
	case SeverityUser:
		return "user"
	default:
		return "user"
	}
}

// StreamError is a structured error emitted by the streaming consumer
// boundary. It wraps an underlying error with a pre-computed severity and
// an optional provider name for log attribution.
//
// Providers do not need to construct a StreamError themselves — consumers
// call ClassifyStreamError at the chunk-handling seam and receive a
// *StreamError back. This keeps the addition non-breaking: the
// provider.Provider interface is untouched and existing stringly-typed
// errors flow through ClassifyStreamError unchanged.
type StreamError struct {
	// Err is the underlying error. Always non-nil for a returned
	// *StreamError; ClassifyStreamError returns nil outright when the
	// input error is nil.
	Err error
	// Severity is the classified urgency.
	Severity StreamErrorSeverity
	// Provider is the name of the provider that produced the error, when
	// known. Empty when the boundary could not attribute it.
	Provider string
}

// Error formats the wrapped error, prefixing with the provider name when
// known so stderr / inline output retains attribution.
//
// Expected:
//   - The receiver may be nil; the method returns "<nil>" in that case.
//
// Returns:
//   - A single-line description of the error.
//
// Side effects:
//   - None.
func (e *StreamError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return ""
	}
	if e.Provider != "" {
		return e.Provider + ": " + e.Err.Error()
	}
	return e.Err.Error()
}

// Unwrap returns the wrapped error so callers can use errors.Is / errors.As
// to reach through a *StreamError to the underlying cause.
//
// Expected:
//   - The receiver may be nil.
//
// Returns:
//   - The wrapped error, or nil when the receiver is nil.
//
// Side effects:
//   - None.
func (e *StreamError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ClassifyStreamError wraps the given error in a *StreamError with a
// best-effort severity classification.
//
// Classification precedence:
//  1. A nil input returns nil (no classification to perform).
//  2. An input that is already a *StreamError is returned verbatim —
//     previously-classified errors are not re-classified.
//  3. A *provider.Error (direct or wrapped via errors.As) drives severity
//     from its ErrorType. Auth failures, quota exhaustion and billing
//     issues become SeverityCritical; rate limits, overload, network
//     errors and server errors become SeverityTransient; everything else
//     becomes SeverityUser.
//  4. Fallback: keyword matching on the error message. The keyword lists
//     are pragmatic and intentionally small — they cover the cases the
//     P18a BDD scenarios exercise ("connection refused", "API key
//     invalid", "provider timeout", etc.) without attempting to be
//     exhaustive.
//
// Expected:
//   - err is the raw error observed on a stream chunk. May be nil.
//
// Returns:
//   - nil when err is nil.
//   - A *StreamError wrapping err with Severity populated otherwise.
//
// Side effects:
//   - None.
func ClassifyStreamError(err error) *StreamError {
	if err == nil {
		return nil
	}

	var already *StreamError
	if errors.As(err, &already) && already != nil {
		return already
	}

	var perr *Error
	if errors.As(err, &perr) && perr != nil {
		return &StreamError{
			Err:      err,
			Severity: severityFromProviderErrorType(perr.ErrorType),
			Provider: perr.Provider,
		}
	}

	return &StreamError{
		Err:      err,
		Severity: severityFromKeywords(err.Error()),
	}
}

// IsCriticalStreamError reports whether the given error classifies as
// SeverityCritical. Safe to call on a nil error (returns false).
//
// Expected:
//   - err may be nil, a *StreamError, or any other error.
//
// Returns:
//   - true when classification yields SeverityCritical.
//   - false otherwise.
//
// Side effects:
//   - None.
func IsCriticalStreamError(err error) bool {
	se := ClassifyStreamError(err)
	if se == nil {
		return false
	}
	return se.Severity == SeverityCritical
}

// severityFromProviderErrorType maps an ErrorType to a StreamErrorSeverity.
//
// Expected:
//   - t is any provider.ErrorType value.
//
// Returns:
//   - The corresponding severity. Unknown types fall back to SeverityUser.
//
// Side effects:
//   - None.
func severityFromProviderErrorType(t ErrorType) StreamErrorSeverity {
	switch t {
	case ErrorTypeAuthFailure, ErrorTypeBilling, ErrorTypeQuota, ErrorTypeModelNotFound:
		return SeverityCritical
	case ErrorTypeRateLimit, ErrorTypeOverload, ErrorTypeNetworkError, ErrorTypeServerError:
		return SeverityTransient
	default:
		return SeverityUser
	}
}

// criticalKeywords are substrings that mark an error as SeverityCritical
// when no structured *provider.Error is available. Kept deliberately short
// — false positives here push noise to stderr, false negatives merely drop
// a critical condition to inline-only, which is the lesser harm.
var criticalKeywords = []string{
	"api key",
	"authentication",
	"unauthori", // "unauthorized" / "unauthorised"
	"forbidden",
	"quota",
	"invalid credential",
	"401",
	"403",
}

// transientKeywords are substrings that mark an error as SeverityTransient
// when no structured *provider.Error is available.
var transientKeywords = []string{
	"connection refused",
	"connection reset",
	"context deadline",
	"deadline exceeded",
	"timeout",
	"timed out",
	"unexpected eof",
	"broken pipe",
	"no such host",
	"temporary failure",
	"try again",
}

// severityFromKeywords scans a lower-cased error message for known
// substrings and returns the matching severity.
//
// Expected:
//   - msg is the already-formatted error message (not necessarily
//     lower-case).
//
// Returns:
//   - SeverityCritical when any critical keyword matches.
//   - SeverityTransient when any transient keyword matches.
//   - SeverityUser otherwise.
//
// Side effects:
//   - None.
func severityFromKeywords(msg string) StreamErrorSeverity {
	lower := strings.ToLower(msg)
	for _, kw := range criticalKeywords {
		if strings.Contains(lower, kw) {
			return SeverityCritical
		}
	}
	for _, kw := range transientKeywords {
		if strings.Contains(lower, kw) {
			return SeverityTransient
		}
	}
	return SeverityUser
}
