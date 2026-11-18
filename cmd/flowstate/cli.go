package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/baphled/flowstate/internal/app"
)

// Exit codes per specs/CLI-AND-API-CONTRACT.md §1–2 (stable; CI
// depends on them).
const (
	exitOK             = 0
	exitUnknownSession = 1
	exitUsage          = 2
	exitConfigInvalid  = 4
	exitBudget         = 5
	exitSigint         = 130
	exitTerminal       = 6
	exitReplayMismatch = 7
	exitReceiptInvalid = 8
)

// Error-code strings of the closed error contract.
const (
	codeUsage          = app.CodeUsage
	codeConfigInvalid  = app.CodeConfigInvalid
	codeInternal       = app.CodeInternal
	codeSessionTerm    = app.CodeSessionTerminal
	codeSessionMissing = app.CodeSessionMissing
	codeReplayMismatch = app.CodeReplayMismatch
	codeReceiptInvalid = app.CodeReceiptInvalid
)

// Error renders the cliError as its message.
func (e *cliError) Error() string { return e.Message }

// asCliError maps any error onto a cliError with a contract code.
func asCliError(err error) *cliError {
	var ce *cliError
	if errors.As(err, &ce) {
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

func exitCodeFor(err error) int {
	switch asCliError(err).Code {
	case codeUsage:
		return exitUsage
	case codeConfigInvalid:
		return exitConfigInvalid
	case app.CodeBudgetExceeded:
		return exitBudget
	case app.CodeSessionCancelled:
		return exitSigint
	case codeSessionTerm:
		return exitTerminal
	case codeSessionMissing:
		return exitUnknownSession
	case codeReplayMismatch:
		return exitReplayMismatch
	case codeReceiptInvalid:
		return exitReceiptInvalid
	default:
		return exitUnknownSession
	}
}
