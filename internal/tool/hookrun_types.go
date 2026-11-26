package tool

// Types of the lifecycle-hook command runner (ADR-012, packet 2a). Types live here per the code contract.

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"time"
)

// Byte and time limits of a command hook (ADR-012, Handler kinds and Security).
const (
	// HookStdinCap bounds the JSON document written to a hook's stdin.
	HookStdinCap = 256 << 10
	// HookStdoutCap bounds the stdout a hook may produce; more is a failure.
	HookStdoutCap = 64 << 10
	// HookStderrCap bounds the stderr kept from a hook; the rest is discarded.
	HookStderrCap = 8 << 10
	// HookReasonCap bounds a deny reason, whether from stderr (exit 2) or from the stdout object.
	HookReasonCap = 1 << 10
	// DefaultHookTimeout is the deadline of a hook that sets none.
	DefaultHookTimeout = 10 * time.Second
	// MaxHookTimeout caps any hook deadline.
	MaxHookTimeout = 60 * time.Second
	// DefaultHookGrace is how long a stopped hook's process group has to leave after SIGTERM (ADR-012: 3 s).
	DefaultHookGrace = 3 * time.Second
	// HookDenyExit is the exit status by which a hook denies.
	HookDenyExit = 2
)

// HookVerdict is the runner's reading of a finished hook.
type HookVerdict string

// The three outcomes of a hook run. A failure is decided by the hook's on_failure policy, not here.
const (
	// HookAllow means the hook succeeded and did not deny.
	HookAllow HookVerdict = "allow"
	// HookDeny means the hook denied: exit status 2 or a stdout decision of deny.
	HookDeny HookVerdict = "deny"
	// HookFailure means the hook did not run to a valid result; Failure names why.
	HookFailure HookVerdict = "failure"
)

// Failure codes recorded in HookResult.Failure.
const (
	// HookFailureInputTooLarge is when the stdin document is over HookStdinCap; the hook is not started.
	HookFailureInputTooLarge = "input_too_large"
	// HookFailureStart is when the argv is empty or relative, or the process could not be started.
	HookFailureStart = "start_failed"
	// HookFailureEnvRefused is when a listed environment name looks like a secret or is FLOWSTATE_*.
	HookFailureEnvRefused = "env_name_refused"
	// HookFailureLeaseFD is when a lease descriptor could not be marked close-on-exec; the hook is not started.
	HookFailureLeaseFD = "lease_fd_failed"
	// HookFailureTimeout is when the deadline expired and the process group was stopped.
	HookFailureTimeout = "timeout"
	// HookFailureCanceled is when the caller's context ended and the process group was stopped.
	HookFailureCanceled = "canceled"
	// HookFailureExit is when the hook exited with a status other than 0 and 2, or was killed by a signal.
	HookFailureExit = "exit_status"
	// HookFailureOutputTooLarge is when stdout went over HookStdoutCap.
	HookFailureOutputTooLarge = "output_too_large"
	// HookFailureOutputIncomplete is when a descendant kept the output pipes open after the hook exited.
	HookFailureOutputIncomplete = "output_incomplete"
	// HookFailureInvalidOutput is when stdout is not one JSON object of the permitted shape.
	HookFailureInvalidOutput = "invalid_output"
	// HookFailureFieldNotPermitted is when stdout names a field the point does not permit.
	HookFailureFieldNotPermitted = "field_not_permitted"
	// HookFailureContextUnsupported is when stdout returned context, which no point accepts until packet 4b.
	HookFailureContextUnsupported = "context_unsupported"
)

// HookCommand is one command hook invocation. Argv[0] is an absolute path or a bare name looked up in PATH.
type HookCommand struct {
	// Argv is the executable and its arguments; no shell is involved.
	Argv []string
	// Dir is the working directory, the session's.
	Dir string
	// EnvNames lists the extra environment names copied from the engine; secret-looking names are refused.
	EnvNames []string
	// Timeout is the deadline; zero means DefaultHookTimeout and it never exceeds MaxHookTimeout.
	Timeout time.Duration
	// AllowArgs permits an args rewrite in stdout (tool.before transform hooks only).
	AllowArgs bool
	// AllowStop permits stop in stdout (turn.after only).
	AllowStop bool
}

// HookRunner starts command hooks. The zero value is usable.
type HookRunner struct {
	// Grace is the time between SIGTERM and SIGKILL; zero means DefaultHookGrace.
	Grace time.Duration
	// LeaseFiles are descriptors that must never reach a hook (the run lease lock). The runner marks each
	// close-on-exec before every start, whatever the way it was opened.
	LeaseFiles []*os.File
}

// HookOutput is the decoded stdout object of a successful hook. Every field is optional.
type HookOutput struct {
	// Decision is "allow", "deny" or empty.
	Decision string
	// Reason is the sanitised, bounded deny reason.
	Reason string
	// Args is the rewritten tool arguments, a JSON object, when the command permitted them.
	Args json.RawMessage
	// Stop is true when the hook asked for the run to stop.
	Stop bool
}

// HookResult is what the engine records about one hook run.
type HookResult struct {
	// Verdict is allow, deny or failure.
	Verdict HookVerdict
	// Failure is the failure code when Verdict is HookFailure.
	Failure string
	// Detail is the sanitised error text of a start failure.
	Detail string
	// Reason is the sanitised deny reason, at most HookReasonCap bytes.
	Reason string
	// Output is the decoded stdout object.
	Output HookOutput
	// ExitCode is the exit status, or -1 when the process did not exit normally or never started.
	ExitCode int
	// Stdout and Stderr are the captured output after control-character stripping (newline and tab kept).
	Stdout string
	// Stderr is the captured error output, bounded by HookStderrCap.
	Stderr string
	// Duration is the wall time of the run.
	Duration time.Duration
}

// hookCapture buffers at most limit bytes and records that more arrived; onOverflow, when set, is called once.
type hookCapture struct {
	data       bytes.Buffer
	limit      int
	overflow   bool
	onOverflow func()
}

// hookStdout is the wire form of a hook's stdout object. Pointers record presence of a field.
type hookStdout struct {
	Decision string          `json:"decision"`
	Reason   string          `json:"reason"`
	Args     json.RawMessage `json:"args"`
	Context  *string         `json:"context"`
	Stop     *bool           `json:"stop"`
}

// hookEnded carries whether the runner issued a stop, and the caller's context to tell a cancellation from a
// timeout, plus an overflow stop.
type hookEnded struct {
	parent   context.Context
	stopped  bool
	overflow bool
}
