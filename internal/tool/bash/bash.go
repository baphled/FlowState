// Package bash provides a tool for executing bash commands with timeout.
package bash

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/pathguard"
	"github.com/baphled/flowstate/internal/tool/truncate"
)

// DefaultTimeout is the per-command wall-clock budget applied when no
// explicit override is configured. The historical 30s cap killed long
// builds and test suites in delegated sessions, whose retries then
// tripped the engine's identical-call detector; 300s accommodates
// make-check-class commands without operator tuning.
const DefaultTimeout = 300 * time.Second

// pipeDrainGrace bounds how long the stdio pipes may keep being drained
// after the command exits or its context fires. It stops a descendant that
// escaped the process-group kill while still holding the pipe file
// descriptors from blocking the tool indefinitely.
const pipeDrainGrace = 5 * time.Second

// ErrDeadlineExceeded marks results whose command was killed by the
// tool's own per-command timeout rather than by the parent context or by
// the command exiting non-zero. The engine's tool-loop identical-call
// detector uses errors.Is(result.Error, ErrDeadlineExceeded) to exempt
// timeout-killed retries from the stuck-loop counters.
var ErrDeadlineExceeded = errors.New("bash: command exceeded timeout")

// Tool executes bash commands with a configurable timeout.
type Tool struct {
	guard   *pathguard.Guard
	timeout time.Duration
}

// New creates a new bash execution tool with DefaultTimeout.
//
// Returns:
//   - A configured bash Tool instance.
//
// Side effects:
//   - None.
func New() *Tool {
	return &Tool{timeout: DefaultTimeout}
}

// NewWithTimeout creates a bash tool with an explicit per-command budget.
// A non-positive duration falls back to DefaultTimeout.
//
// Expected: parameters for NewWithTimeout.
// Returns: result of NewWithTimeout.
// Side effects: None.
func NewWithTimeout(d time.Duration) *Tool {
	if d <= 0 {
		d = DefaultTimeout
	}
	return &Tool{timeout: d}
}

// NewWithGuard creates a bash tool that denies commands referencing protected paths.
//
// Expected: parameters for NewWithGuard.
// Returns: result of NewWithGuard.
// Side effects: None.
func NewWithGuard(g *pathguard.Guard) *Tool {
	return &Tool{guard: g, timeout: DefaultTimeout}
}

// NewWithGuardTimeout creates a bash tool with both a path guard and an
// explicit per-command budget. A non-positive duration falls back to
// DefaultTimeout.
//
// Expected: parameters for NewWithGuardTimeout.
// Returns: result of NewWithGuardTimeout.
// Side effects: None.
func NewWithGuardTimeout(g *pathguard.Guard, d time.Duration) *Tool {
	if d <= 0 {
		d = DefaultTimeout
	}
	return &Tool{guard: g, timeout: d}
}

// Name returns the tool identifier.
//
// Returns:
//   - The string "bash".
//
// Side effects:
//   - None.
//
// Expected: parameters for Name.
func (t *Tool) Name() string {
	return "bash"
}

// Description returns a human-readable description of the bash tool.
//
// Returns:
//   - A string describing the tool's purpose.
//
// Side effects:
//   - None.
//
// Expected: parameters for Description.
func (t *Tool) Description() string {
	return "Execute bash commands with a configurable timeout (default 300s). For writing file content, use the `write` tool instead — it handles path validation, directory creation, and proper file permissions automatically. Only use bash for commands that genuinely need a shell (build tools, git operations, process management)."
}

// Schema returns the JSON schema for the bash tool arguments.
//
// Returns:
//   - A tool.Schema describing the required command property.
//
// Side effects:
//   - None.
//
// Expected: parameters for Schema.
func (t *Tool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"command": {
				Type:        "string",
				Description: "The bash command to execute",
			},
		},
		Required: []string{"command"},
	}
}

// IsStateModifying returns true because bash executes arbitrary
// commands that may have persistent side effects on the system.
//
// Expected: parameters for IsStateModifying.
// Returns: result of IsStateModifying.
// Side effects: None.
func (t *Tool) IsStateModifying() bool { return true }

// Guard returns the tool's path guard; nil when constructed unguarded.
// Callers rebuilding the tool with a different timeout use this to
// preserve the guard wiring.
//
// Expected: parameters for Guard.
// Returns: result of Guard.
// Side effects: None.
func (t *Tool) Guard() *pathguard.Guard {
	return t.guard
}

// Timeout returns the tool's per-command wall-clock budget. The engine
// applies this via the tool.TimeoutOverrider contract so the engine-level
// per-tool deadline matches the bash tool's own budget instead of firing
// first and masking the deadline signal.
//
// Expected: parameters for Timeout.
// Returns: result of Timeout.
// Side effects: None.
func (t *Tool) Timeout() time.Duration {
	if t.timeout <= 0 {
		return DefaultTimeout
	}
	return t.timeout
}

// Execute runs the specified bash command and returns its output.
//
// Expected:
//   - ctx is a valid context for the command execution.
//   - input contains a "command" string argument.
//
// Returns:
//   - A tool.Result containing the combined stdout and stderr output.
//   - An error if the command argument is missing.
//
// Side effects:
//   - Executes a bash subprocess in its own process group with a 30-second
//     timeout. On context cancellation the whole group is signalled with
//     SIGKILL, and the stdio pipes are force-closed after pipeDrainGrace so
//     a descendant holding them cannot block the return.
func (t *Tool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	command, ok := input.Arguments["command"].(string)
	if !ok || command == "" {
		return tool.Result{}, errors.New("command argument is required")
	}

	if t.guard != nil {
		if err := t.guard.CheckCommandForTool(ctx, "bash", command); err != nil {
			return tool.Result{Error: err}, nil
		}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, t.Timeout())
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, "bash", "-c", command)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = pipeDrainGrace
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	capped := capOutput(ctx, trimmed)
	if err != nil {
		if timeoutCtx.Err() != nil && errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			return tool.Result{
				Output: capped,
				Error:  fmt.Errorf("command failed: %w: %w: %w", err, timeoutCtx.Err(), ErrDeadlineExceeded),
			}, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return tool.Result{
				Output: capped,
				Error:  fmt.Errorf("command failed: %w: %w", err, ctxErr),
			}, nil
		}
		return tool.Result{
			Output: capped,
			Error:  fmt.Errorf("command failed: %w", err),
		}, nil
	}

	return tool.Result{
		Output: capped,
	}, nil
}

// capOutput applies the shared truncation envelope using the session ID
// from ctx. Output stays unchanged when under the byte/line budget.
//
// Expected: parameters for capOutput.
// Returns: result of capOutput.
// Side effects: None.
func capOutput(ctx context.Context, output string) string {
	sessionID, _ := ctx.Value(session.IDKey{}).(string)
	r := truncate.Apply(output, truncate.Options{
		SessionID: sessionID,
		ToolName:  "bash",
	})
	return r.Content
}
