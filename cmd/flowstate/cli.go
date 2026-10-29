// Package main implements the flowstate CLI entry point.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
)

// Exit codes per specs/CLI-AND-API-CONTRACT.md §1–2 (stable; CI
// asserts them via VA-C1).
const (
	exitOK             = 0
	exitUnknownSession = 1
	exitUsage          = 2
	exitConfigInvalid  = 4
	exitTerminal       = 6
	exitReplayMismatch = 7
	exitBudgetExceeded = 5
	exitInterrupted    = 130
)

// Error codes of the closed set in the contract §2.
const (
	codeUsage          = "usage"
	codeConfigInvalid  = "config_invalid"
	codeInternal       = "internal"
	codeSessionTerm    = "session_terminal"
	codeSessionMissing = "session_not_found"
	codeReplayMismatch = "replay_mismatch"
)

// cliError is a failure carrying one of the closed error codes; it
// renders as a single JSON object on stderr.
type cliError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *cliError) Error() string { return e.Message }

// errConfigInvalid marks configuration failures (code config_invalid).
var errConfigInvalid = errors.New("config_invalid")

// asCliError maps any error onto a cliError with a contract code.
func asCliError(err error) *cliError {
	var ce *cliError
	if errors.As(err, &ce) {
		return ce
	}
	switch {
	case errors.Is(err, session.ErrUsage):
		return &cliError{Code: codeUsage, Message: err.Error()}
	case errors.Is(err, errConfigInvalid):
		return &cliError{Code: codeConfigInvalid, Message: err.Error()}
	case errors.Is(err, errBudgetExceeded):
		return &cliError{Code: "budget_exceeded", Message: err.Error()}
	case errors.Is(err, errSessionCancelled):
		return &cliError{Code: "session_cancelled", Message: err.Error()}
	case errors.Is(err, session.ErrTerminal):
		return &cliError{Code: codeSessionTerm, Message: err.Error()}
	case errors.Is(err, store.ErrUnknownSession):
		return &cliError{Code: codeSessionMissing, Message: err.Error()}
	case errors.Is(err, store.ErrHashMismatch), errors.Is(err, store.ErrSeqGap):
		return &cliError{Code: codeReplayMismatch, Message: err.Error()}
	default:
		return &cliError{Code: codeInternal, Message: err.Error()}
	}
}

// writeError emits one JSON object on stderr per the error contract.
func writeError(stderr io.Writer, err error) {
	b, merr := json.Marshal(asCliError(err))
	if merr != nil { // never happens for this shape
		_, _ = fmt.Fprintf(stderr, "{\"code\":\"%s\",\"message\":%q}\n", codeInternal, err.Error())
		return
	}
	_, _ = fmt.Fprintln(stderr, string(b))
}

// exitCodeFor maps an error onto its stable exit code.
func exitCodeFor(err error) int {
	switch asCliError(err).Code {
	case codeUsage:
		return exitUsage
	case codeConfigInvalid:
		return exitConfigInvalid
	case codeSessionTerm:
		return exitTerminal
	case codeSessionMissing:
		return exitUnknownSession
	case codeReplayMismatch:
		return exitReplayMismatch
	default:
		return exitUnknownSession
	}
}
