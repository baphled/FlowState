//go:build e2e

// Package support provides BDD step definitions and helpers.
//
// This file implements the step definitions that exercise the bash tool's
// process-tree teardown on context cancellation. The scenario lives at
// features/tools/bash_tool.feature and drives the real internal/tool/bash
// Tool against a command that backgrounds a long-running descendant holding
// the stdio pipes, then asserts that cancelling the execution context (a)
// makes Execute return promptly, (b) leaves no descendant process alive, and
// (c) surfaces the cancellation error on the tool result.
package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/tool"
	bashtool "github.com/baphled/flowstate/internal/tool/bash"
)

// bashKillDescendantCommand runs a backgrounded sleep renamed via exec -a so
// pgrep can find it by a unique marker. The outer bash blocks on wait while
// the descendant inherits the CombinedOutput pipe file descriptors.
const bashKillDescendantCommand = `bash -c 'exec -a %s sleep 300' & wait`

// bashKillSpawnSettle is the time the runner waits after starting Execute
// before the scenario cancels the context, so the descendant is reliably
// spawned and holding the pipes when the cancel lands.
const bashKillSpawnSettle = 500 * time.Millisecond

// bashKillReturnBudget is the maximum wall-clock time Execute may take to
// return after the execution context is cancelled.
const bashKillReturnBudget = 10 * time.Second

// bashKillTeardownBudget is the maximum time the descendant-absence check
// will poll pgrep before declaring the scenario failed.
const bashKillTeardownBudget = 3 * time.Second

// BashProcessKillSteps holds the per-scenario state for the process-tree
// kill steps: the cancellable execution context, the Execute completion
// channel, and the captured tool result.
type BashProcessKillSteps struct {
	marker       string
	cancel       context.CancelFunc
	done         chan struct{}
	result       tool.Result
	executionErr error
}

// RegisterBashProcessKillSteps registers the bash process-tree kill step
// patterns on the godog scenario context.
//
// Expected:
//   - ctx is a non-nil godog ScenarioContext; s holds the per-scenario state.
//
// Side effects:
//   - Binds step patterns to receiver methods on s.
func RegisterBashProcessKillSteps(ctx *godog.ScenarioContext, s *BashProcessKillSteps) {
	ctx.Step(`^the AI runs a command that leaves a long-running descendant holding the pipes$`, s.theAIRunsACommandLeavingALongRunningDescendant)
	ctx.Step(`^the tool's execution context is cancelled$`, s.theToolsExecutionContextIsCancelled)
	ctx.Step(`^the bash tool returns within 10 seconds$`, s.theBashToolReturnsWithin10Seconds)
	ctx.Step(`^no descendant of the command is still running$`, s.noDescendantOfTheCommandIsStillRunning)
	ctx.Step(`^the tool result reports the cancellation error$`, s.theToolResultReportsTheCancellationError)
}

// theAIRunsACommandLeavingALongRunningDescendant starts the real bash tool
// asynchronously against a command whose backgrounded sleep descendant holds
// the stdio pipes, then waits briefly for the descendant to spawn.
//
// Expected:
//   - The bash tool is available and the runner can spawn processes.
//
// Returns:
//   - nil once Execute is in flight and the settle window has elapsed.
//
// Side effects:
//   - Installs s.cancel and s.done, and spawns the Execute goroutine.
func (s *BashProcessKillSteps) theAIRunsACommandLeavingALongRunningDescendant() error {
	s.marker = fmt.Sprintf("fsbkillmark%d", os.Getpid())
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.done = make(chan struct{})

	input := tool.Input{
		Name:      "bash",
		Arguments: map[string]interface{}{"command": fmt.Sprintf(bashKillDescendantCommand, s.marker)},
	}
	runner := bashtool.New()

	go func() {
		defer close(s.done)
		s.result, s.executionErr = runner.Execute(ctx, input)
	}()

	time.Sleep(bashKillSpawnSettle)
	return nil
}

// theToolsExecutionContextIsCancelled cancels the execution context handed to
// the in-flight Execute call, modelling a user stop or harness timeout.
//
// Returns:
//   - An error if no execution is in flight.
//
// Side effects:
//   - Invokes s.cancel.
func (s *BashProcessKillSteps) theToolsExecutionContextIsCancelled() error {
	if s.cancel == nil {
		return errors.New("no bash execution in flight; run the descendant step first")
	}
	s.cancel()
	return nil
}

// theBashToolReturnsWithin10Seconds asserts that Execute completed within
// the return budget after the cancel was issued.
//
// Returns:
//   - nil when Execute returned within budget, or a timeout error.
//
// Side effects:
//   - On timeout, kills the marker process so the failed scenario does not
//     leak a five-minute sleep onto the host.
func (s *BashProcessKillSteps) theBashToolReturnsWithin10Seconds() error {
	select {
	case <-s.done:
		return nil
	case <-time.After(bashKillReturnBudget):
		killMarkerProcesses(s.marker)
		return fmt.Errorf("bash tool Execute did not return within %s after cancel", bashKillReturnBudget)
	}
}

// noDescendantOfTheCommandIsStillRunning asserts via pgrep that no process
// carrying the scenario marker survives the cancellation.
//
// Returns:
//   - nil once pgrep finds no marker match, or an error after the budget.
//
// Side effects:
//   - None.
func (s *BashProcessKillSteps) noDescendantOfTheCommandIsStillRunning() error {
	deadline := time.Now().Add(bashKillTeardownBudget)
	for time.Now().Before(deadline) {
		found, err := markerProcessesRunning(s.marker)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("descendant processes matching %q still running after %s", s.marker, bashKillTeardownBudget)
}

// theToolResultReportsTheCancellationError asserts that the tool result
// carries an error matching the cancelled context, so upstream errors.Is
// checks against context.Canceled succeed.
//
// Returns:
//   - nil when result.Error wraps context.Canceled, or a descriptive error.
//
// Side effects:
//   - None.
func (s *BashProcessKillSteps) theToolResultReportsTheCancellationError() error {
	select {
	case <-s.done:
	default:
		return errors.New("bash execution has not returned; check the return-within step")
	}
	if s.executionErr != nil {
		return fmt.Errorf("Execute returned a Go error: %w", s.executionErr)
	}
	if s.result.Error == nil {
		return errors.New("expected cancellation error on the tool result, got nil")
	}
	if !errors.Is(s.result.Error, context.Canceled) {
		return fmt.Errorf("expected result error to wrap context.Canceled, got: %v", s.result.Error)
	}
	return nil
}

// markerProcessesRunning reports whether pgrep finds any process whose
// command line carries the marker.
//
// Expected:
//   - marker is a non-empty unique argv marker.
//
// Returns:
//   - true when at least one match exists; an error only when pgrep cannot
//     itself run.
//
// Side effects:
//   - None.
func markerProcessesRunning(marker string) (bool, error) {
	err := exec.Command("pgrep", "-f", marker).Run()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, fmt.Errorf("pgrep failed: %w", err)
}

// killMarkerProcesses force-kills any process carrying the marker so a failed
// scenario does not leak sleep processes onto the host.
//
// Side effects:
//   - Sends SIGKILL to every process matching the marker via pkill.
func killMarkerProcesses(marker string) {
	_ = exec.Command("pkill", "-KILL", "-f", marker).Run()
}
