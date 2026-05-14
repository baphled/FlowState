package carbonyl

import "fmt"

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

type NotRunningError struct{}

func (e *NotRunningError) Error() string {
	return "carbonyl: bridge is not running"
}
