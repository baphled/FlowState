package chat

import (
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/components/notification"
)

// This file exposes a narrow set of hooks intended for external test
// harnesses (BDD step glue in features/support, primarily). The helpers
// live outside export_test.go because that file is compiled only into
// the chat package's own _test binary, which is invisible to other
// packages. Keeping them here means the same BDD wiring that drives the
// real chat.Intent from features/support continues to work without
// reaching into unexported fields via reflection.
//
// All exported symbols end in ForTest and are documented as test-only —
// production callers have no reason to invoke them.

// SetRunningInTestsForTest toggles the package-level runningInTests flag
// so external harnesses can suppress long-running initialisation paths
// (notably session WAL restoration) when constructing Intents in tests.
//
// Expected:
//   - running is the desired flag value; callers typically set true in
//     Before hooks and false in After hooks.
//
// Side effects:
//   - Mutates package-level state shared by all Intents constructed in
//     the current process.
func SetRunningInTestsForTest(running bool) {
	runningInTests = running
}

// SetInputForTest overwrites the chat intent's input buffer. Used by the
// external-editor BDD scenario so the editor's read-back assertion
// starts from a known state.
//
// Expected:
//   - input is an arbitrary string; empty is valid and exercises the
//     "open on empty draft" path.
//
// Side effects:
//   - Replaces i.input; does not touch the viewport or view.
func (i *Intent) SetInputForTest(input string) {
	i.input = input
}

// OpenExternalEditorForTest exposes openExternalEditor so external step
// glue can drive the editor command directly without synthesising a
// full tea.KeyMsg pipeline.
//
// Returns:
//   - The tea.Cmd produced by openExternalEditor; nil when no editor is
//     resolvable or tempfile setup fails (see the underlying method).
//
// Side effects:
//   - Delegates to openExternalEditor (temp-file creation, notification
//     on failure, etc.).
func (i *Intent) OpenExternalEditorForTest() tea.Cmd {
	return i.openExternalEditor()
}

// HandleExternalEditorFinishedForTest exposes handleExternalEditorFinished
// so tests can feed back a simulated ExternalEditorFinishedMsg without
// re-entering the main Update switch.
//
// Expected:
//   - msg was produced by the tea.Cmd that OpenExternalEditorForTest
//     returned, or synthesised with equivalent fields.
//
// Returns:
//   - Always nil (mirrors handleExternalEditorFinished).
//
// Side effects:
//   - On msg.Err == nil: replaces i.input with msg.Content and refreshes
//     the viewport.
//   - On msg.Err != nil: surfaces an error notification and leaves the
//     input buffer unchanged.
//   - Unconditionally removes msg.TempPath when non-empty.
func (i *Intent) HandleExternalEditorFinishedForTest(msg ExternalEditorFinishedMsg) tea.Cmd {
	return i.handleExternalEditorFinished(msg)
}

// NotificationManagerForTest returns the chat intent's notification
// manager so tests (in and out of the chat package) can inspect active
// notifications without reaching into unexported fields.
//
// Returns:
//   - The notification.Manager backing the intent's notifications
//     component.
//
// Side effects:
//   - None.
func (i *Intent) NotificationManagerForTest() notification.Manager {
	return i.notifications.Manager()
}

// SetActiveToolCallForTest stamps the chat intent's activeToolCall
// field so tests can simulate the "a tool is currently executing"
// state without having to prime the full streaming pipeline.
//
// Expected:
//   - name is the display name that would normally be stored by
//     handleStreamChunkMsg when a tool-call chunk arrives.
//
// Side effects:
//   - Replaces i.activeToolCall.
func (i *Intent) SetActiveToolCallForTest(name string) {
	i.activeToolCall = name
}

// ActiveToolCallForTest returns the intent's current activeToolCall
// field so tests can assert the cancel path clears it.
//
// Returns:
//   - The current tool-call name, or "" when no tool is active.
//
// Side effects:
//   - None.
func (i *Intent) ActiveToolCallForTest() string {
	return i.activeToolCall
}

// SetStreamCancelForTest installs a cancel closure on the chat intent so
// the Ctrl+K flow can observe and invoke it without having to wire a
// real streaming provider. Typical usage pairs a sync.Once-backed fake
// with SetActiveToolCallForTest so the test starts in a realistic
// "tool mid-execution" state.
//
// Expected:
//   - cancel is any idempotent function; nil clears the current cancel.
//
// Side effects:
//   - Replaces i.streamCancel.
func (i *Intent) SetStreamCancelForTest(cancel func()) {
	i.streamCancel = cancel
}

// CancelActiveToolForTest exposes cancelActiveTool so BDD step glue can
// drive the Ctrl+K branch directly without rerouting through the full
// key-message dispatch chain.
//
// Returns:
//   - The tea.Cmd from cancelActiveTool (always nil; see its contract).
//
// Side effects:
//   - Whatever side effects cancelActiveTool produces on the primed
//     Intent: stream cancel, view streaming flag, notification,
//     viewport refresh.
func (i *Intent) CancelActiveToolForTest() tea.Cmd {
	return i.cancelActiveTool()
}

// UserCancelledForTest exposes the internal userCancelled flag so
// tests can assert cancelActiveTool marked the cancel as user-
// initiated (gating handleStreamChunk's error-surfacing path).
//
// Returns:
//   - True when cancelActiveStream was invoked by a user action since
//     the last reset; false otherwise.
//
// Side effects:
//   - None.
func (i *Intent) UserCancelledForTest() bool {
	return i.userCancelled
}

// SetEditorProcessRunnerForTest replaces the package-level editor
// process runner with a test double. Returns a cleanup closure that
// restores the original runner; callers MUST invoke it in test teardown
// to prevent scenario cross-contamination.
//
// The replacement strategy preserves the real openExternalEditor flow
// (tempfile creation, $EDITOR resolution, callback plumbing) while
// short-circuiting the actual process spawn. A typical fake writes a
// known sentinel into the temp file and invokes cb(nil) synchronously.
//
// Expected:
//   - fn is a non-nil replacement runner that accepts the same arguments
//     as tea.ExecProcess.
//
// Returns:
//   - A zero-arg closure that restores the previous runner when called.
//
// Side effects:
//   - Mutates the package-level editorProcessRunner.
func SetEditorProcessRunnerForTest(
	fn func(cmd *exec.Cmd, cb tea.ExecCallback) tea.Cmd,
) func() {
	original := editorProcessRunner
	editorProcessRunner = fn
	return func() {
		editorProcessRunner = original
	}
}
