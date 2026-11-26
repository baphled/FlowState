package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/baphled/flowstate/internal/app"
)

// Exit codes per specs/CLI-AND-API-CONTRACT.md §1–2 (stable; CI
// depends on them). Exit 9 (session_busy) was added by ADR-011 D11; hook_refused (2) and hook_stop (5)
// by the ADR-012 D7 amendment; that external spec is outside the repository and gains
// them when next edited.
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
	exitSessionBusy    = 9
)

// Error-code strings of the error contract (session_busy added by ADR-011 D11).
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

// terminalSafeJSON marshals v for a terminal: encoding/json escapes only
// characters below 0x20, U+2028/9 and <>&, so DEL, C1 and format characters
// (bidi overrides, zero-width, tags) are additionally written as \uXXXX. Format
// characters outside the BMP are written as a UTF-16 surrogate pair, as
// encoding/json does, so the output decodes to the original string.
func terminalSafeJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return appendTerminalSafe(make([]byte, 0, len(b)), b, false), nil
}

// appendTerminalSafe appends src to dst, replacing every control and format
// rune with its \uXXXX escape (lower-case hex). keepNewline leaves "\n"
// alone for line-oriented text. Clean spans are copied in one piece and each
// invalid UTF-8 byte is replaced by \ufffd, as json.Marshal does for strings.
func appendTerminalSafe(dst, src []byte, keepNewline bool) []byte {
	start := 0
	for i := 0; i < len(src); {
		if c := src[i]; c < utf8.RuneSelf && c >= 0x20 && c != 0x7f {
			i++ // clean ASCII: the common case, no table lookup
			continue
		}
		r, size := utf8.DecodeRune(src[i:])
		invalid := r == utf8.RuneError && size <= 1
		if !invalid && ((keepNewline && r == '\n') ||
			(!unicode.IsControl(r) && !unicode.Is(unicode.Cf, r))) {
			i += size
			continue
		}
		dst = append(dst, src[start:i]...)
		switch {
		case invalid:
			dst = appendEscape(dst, utf8.RuneError)
			size = 1
		case r > 0xFFFF:
			hi, lo := utf16.EncodeRune(r)
			dst = appendEscape(dst, hi)
			dst = appendEscape(dst, lo)
		default:
			dst = appendEscape(dst, r)
		}
		i += size
		start = i
	}
	return append(dst, src[start:]...)
}

// appendEscape appends r (at most 0xFFFF) as \uXXXX in lower-case hex.
func appendEscape(dst []byte, r rune) []byte {
	const hexDigits = "0123456789abcdef"
	return append(dst, '\\', 'u', hexDigits[r>>12&0xF], hexDigits[r>>8&0xF], hexDigits[r>>4&0xF], hexDigits[r&0xF])
}

// isTerminal reports whether f is a character device, such as a console. A
// file that cannot be inspected is treated as a terminal, so output fails safe.
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	return err != nil || fi.Mode()&os.ModeCharDevice != 0
}

// stdoutIsTerminal is the terminal check transcriptFor applies to the output
// file. It is a variable only so specs can stand in for a real console, which
// would need platform-specific pty code; production code never reassigns it.
var stdoutIsTerminal = isTerminal

// transcriptFor returns the bytes `replay` writes to f. The transcript is a
// verifiable export (payload_sha256 hashes the payload bytes), so files and
// pipes receive it untouched; only a terminal gets control characters escaped.
func transcriptFor(f *os.File, lines []byte) []byte {
	if !stdoutIsTerminal(f) {
		return lines
	}
	return appendTerminalSafe(make([]byte, 0, len(lines)), lines, true)
}

// writeError emits one JSON object on stderr per the error contract.
func writeError(stderr io.Writer, err error) {
	b, merr := terminalSafeJSON(asCliError(err))
	if merr != nil { // never happens for this shape
		_, _ = fmt.Fprintf(stderr, "{\"code\":\"%s\",\"message\":%q}\n", codeInternal, err.Error())
		return
	}
	_, _ = fmt.Fprintln(stderr, string(b))
}

func exitCodeFor(err error) int {
	switch asCliError(err).Code {
	case codeUsage, app.CodeHookRefused:
		return exitUsage
	case codeConfigInvalid:
		return exitConfigInvalid
	case app.CodeBudgetExceeded, app.CodeMaxTurns, app.CodeOutputTruncated, app.CodeHookStop:
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
	case app.CodeSessionBusy:
		return exitSessionBusy
	default:
		return exitUnknownSession
	}
}
