// Package main implements the flowstate CLI entry point.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/app"
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
	exitProviderError  = 3
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

// errConfigInvalid marks configuration failures (code config_invalid).
var errConfigInvalid = errors.New("config_invalid")

// asCliError maps any error onto a cliError with a contract code.
func asCliError(err error) *cliError {
	var ce *cliError
	if errors.As(err, &ce) { //nolint:errorlint // legacy shape check
		return ce
	}
	return &cliError{Code: app.ErrorCode(err), Message: err.Error()}
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
	case app.CodeBudgetExceeded:
		return exitBudgetExceeded
	case app.CodeSessionCancelled:
		return exitInterrupted
	case app.CodeProvider:
		return exitProviderError
	default:
		return exitUnknownSession
	}
}
