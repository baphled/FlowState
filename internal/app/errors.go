package app

import (
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/runtime"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// Closed error-code set from specs/CLI-AND-API-CONTRACT.md §2; the
// thin cmd adapter renders these verbatim.
const (
	// CodeUsage marks argument misuse.
	CodeUsage = "usage"
	// CodeConfigInvalid marks configuration or database-open failures.
	CodeConfigInvalid = "config_invalid"
	// CodeInternal marks unexpected internal failures.
	CodeInternal = "internal"
	// CodeSessionTerminal marks operations on finished sessions.
	CodeSessionTerminal = "session_terminal"
	// CodeSessionMissing marks unknown session ids.
	CodeSessionMissing = "session_not_found"
	// CodeReplayMismatch marks hash or seq verification failures.
	CodeReplayMismatch = "replay_mismatch"
	// CodeBudgetExceeded marks budget-cap stops.
	CodeBudgetExceeded = "budget_exceeded"
	// CodeSessionCancelled marks interrupt-driven cancellation.
	CodeSessionCancelled = "session_cancelled"
	// CodeProvider marks provider failures.
	CodeProvider = "provider_error"
)

// ErrProvider marks provider failures (code provider_error, exit 3
// per CLI-AND-API-CONTRACT §1).
var ErrProvider = fmt.Errorf("provider_error")

// ErrUsage marks argument misuse (code usage, exit 2 per
// CLI-AND-API-CONTRACT §1); the session domain sentinel is aliased so
// cmd depends only on app.
var ErrUsage = session.ErrUsage

// ErrBudgetExceeded marks budget-cap stops (code budget_exceeded,
// exit 5 per CLI-AND-API-CONTRACT §1).
var ErrBudgetExceeded = errors.New("budget_exceeded")

// ErrSessionCancelled marks interrupt-driven cancellation (code
// session_cancelled, exit 130).
var ErrSessionCancelled = errors.New("session_cancelled")

// ErrorCode maps any error onto the closed error-code set of the
// CLI-and-API contract so the thin cmd adapter needs no direct
// session or store knowledge.
func ErrorCode(err error) string {
	switch {
	case errors.Is(err, session.ErrUsage):
		return CodeUsage
	case errors.Is(err, ErrConfigInvalid):
		return CodeConfigInvalid
	case errors.Is(err, ErrBudgetExceeded):
		return CodeBudgetExceeded
	case errors.Is(err, ErrSessionCancelled):
		return CodeSessionCancelled
	case errors.Is(err, session.ErrTerminal):
		return CodeSessionTerminal
	case errors.Is(err, store.ErrUnknownSession):
		return CodeSessionMissing
	case errors.Is(err, store.ErrHashMismatch), errors.Is(err, store.ErrSeqGap):
		return CodeReplayMismatch
	case errors.Is(err, runtime.ErrProvider):
		return CodeProvider
	default:
		return CodeInternal
	}
}
