package app

import (
	"errors"

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
	// CodeSessionCancelled marks cancellation by a signal, a client
	// disconnect or a server shutdown.
	CodeSessionCancelled = "session_cancelled"
	// CodeReceiptInvalid marks receipts failing schema validation.
	CodeReceiptInvalid = "receipt_invalid"
	// CodeMaxTurns marks a run that used all its turns while still asking for tools.
	CodeMaxTurns = "max_turns"
	// CodeOutputTruncated marks a run whose model finished `length` with no complete tool call,
	// again after one retry with a doubled output cap (a limit failure, like max_turns).
	CodeOutputTruncated = "output_truncated"
	// CodeProvider marks provider failures.
	CodeProvider = "provider_error"
	// CodeSessionBusy marks a lease refused at acquire: the session is held by a live
	// or unjudgeable holder (store.ErrSessionBusy, *CompactLeaseHeld, ErrLeaseLive).
	// A lease taken from a running holder mid-run (ErrExecLoopFenced) is not this code.
	CodeSessionBusy = "session_busy"
)

// ErrConfigInvalid marks configuration or database-open failures
// (code config_invalid, exit 4 per CLI-AND-API-CONTRACT §1).
var ErrConfigInvalid = errors.New("config_invalid")

// ErrUsage marks argument misuse (code usage, exit 2 per
// CLI-AND-API-CONTRACT §1); the session domain sentinel is aliased so
// cmd depends only on app.
var ErrUsage = session.ErrUsage

// ErrBudgetExceeded marks budget-cap stops (code budget_exceeded,
// exit 5 per CLI-AND-API-CONTRACT §1).
var ErrBudgetExceeded = errors.New("budget_exceeded")

// ErrSessionCancelled marks cancellation by SIGINT, SIGTERM, SIGHUP, a
// client disconnect or a shutdown (code session_cancelled, exit 130).
var ErrSessionCancelled = errors.New("session_cancelled")

// ErrOutputTruncated reports a turn the output cap cut off twice with no complete tool call (code
// output_truncated, exit 5, HTTP 409): the loop retried once with an escalated cap, or the cap was
// already at the model's ceiling. The run ends failed; the cut-off text stays in the result.
var ErrOutputTruncated = errors.New("output_truncated")

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
	case errors.Is(err, ErrMaxTurns):
		return CodeMaxTurns
	case errors.Is(err, ErrOutputTruncated):
		return CodeOutputTruncated
	case errors.Is(err, ErrSessionCancelled):
		return CodeSessionCancelled
	case errors.Is(err, ErrSwarmNotAccepted):
		return CodeReceiptInvalid
	case errors.Is(err, session.ErrTerminal):
		return CodeSessionTerminal
	case isLeaseRefusal(err):
		return CodeSessionBusy
	case errors.Is(err, store.ErrUnknownSession):
		return CodeSessionMissing
	case errors.Is(err, store.ErrHashMismatch), errors.Is(err, store.ErrSeqGap), errors.Is(err, ErrInvalidCompaction):
		return CodeReplayMismatch
	case errors.Is(err, ErrProvider):
		return CodeProvider
	default:
		return CodeInternal
	}
}

// isLeaseRefusal reports a lease refused at acquire, or an unlock refused because the holder is
// alive. ErrExecLoopFenced alone is not one: it also reports a lease taken from this run mid-run,
// which keeps code internal; a compaction refusal matches it too, so it is tested by type.
func isLeaseRefusal(err error) bool {
	var held *CompactLeaseHeld
	return errors.Is(err, store.ErrSessionBusy) || errors.Is(err, ErrLeaseLive) || errors.As(err, &held)
}
