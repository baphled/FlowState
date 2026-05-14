package carbonyl

import (
	"errors"
	"fmt"
)

// StartError indicates that the Carbonyl subprocess could not be
// launched. Binary holds the path that was attempted; Cause is the
// underlying error from exec.CommandContext.
type StartError struct {
	Binary string
	Cause  error
}

func (e *StartError) Error() string {
	if e.Binary != "" {
		return fmt.Sprintf("carbonyl: start failed for %s: %v", e.Binary, e.Cause)
	}
	return fmt.Sprintf("carbonyl: start failed: %v", e.Cause)
}

func (e *StartError) Unwrap() error {
	return e.Cause
}

// ProcessCrashError indicates that the Carbonyl subprocess exited
// unexpectedly after a successful start. PID is the operating-system
// process identifier; Cause describes the exit reason.
type ProcessCrashError struct {
	PID   int
	Cause error
}

func (e *ProcessCrashError) Error() string {
	return fmt.Sprintf("carbonyl: process %d crashed: %v", e.PID, e.Cause)
}

func (e *ProcessCrashError) Unwrap() error {
	return e.Cause
}

// NotRunningError is returned by Stop and Wait when the bridge is not
// currently running.
type NotRunningError struct{}

func (e *NotRunningError) Error() string {
	return "carbonyl: bridge is not running"
}

// errNoAPIHandler is returned when neither an APIMux nor an APIServer
// is available on the application. Without an HTTP handler the
// ephemeral server cannot serve the Vue SPA or API routes.
var errNoAPIHandler = errors.New("carbonyl: no API handler available")
