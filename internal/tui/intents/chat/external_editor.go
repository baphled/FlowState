package chat

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/components/notification"
)

// ExternalEditorFinishedMsg signals that the external-editor child process
// has exited. It carries the edited content (if the run succeeded) along
// with the path of the temporary file so the receiver can clean up after
// itself, and an optional error describing any failure during the run or
// file I/O.
type ExternalEditorFinishedMsg struct {
	// Content is the final text read back from the temp file after the
	// editor exited. Empty when Err is non-nil.
	Content string
	// TempPath is the path of the temporary file the editor operated on.
	// The receiver is responsible for removing the file once it has
	// consumed Content.
	TempPath string
	// Err records any failure during tempfile setup, editor execution,
	// or read-back. Nil on success.
	Err error
}

// editorProcessRunner builds the tea.Cmd that executes the external editor
// against the provided exec.Cmd and returns the bubbletea callback message
// when it exits.
//
// Production builds route through tea.ExecProcess so the editor temporarily
// seizes the terminal. Tests override this symbol with a deterministic
// fake (see runEditorProcess) that writes predictable content to the temp
// file and invokes fn synchronously.
//
// Expected:
//   - cmd is a non-nil *exec.Cmd whose final argument is the path of the
//     temp file the editor must edit.
//   - fn is the bubbletea callback invoked after the process exits; it
//     receives the exit error (nil on success).
//
// Returns:
//   - A tea.Cmd that drives the editor to completion.
var editorProcessRunner = tea.ExecProcess

// externalEditorBinary resolves which editor binary to launch. It consults
// $EDITOR first and falls back to `vim` on $PATH. Returning an empty string
// tells openExternalEditor to surface a notification and bail out without
// crashing the TUI.
//
// Expected:
//   - The environment may or may not define EDITOR; vim may or may not be
//     installed on the host.
//
// Returns:
//   - The resolved editor binary path (as returned by exec.LookPath).
//   - An empty string when neither $EDITOR nor `vim` is available.
//
// Side effects:
//   - None; reads the environment only.
func externalEditorBinary() string {
	if ed := os.Getenv("EDITOR"); ed != "" {
		return ed
	}
	if path, err := exec.LookPath("vim"); err == nil {
		return path
	}
	return ""
}

// openExternalEditor shells out to $EDITOR (fallback `vim`) on the user's
// current input buffer, replacing the buffer with whatever the editor
// writes back. If no editor is available, a notification is surfaced and
// the keystroke becomes a no-op so the TUI never crashes.
//
// The flow is:
//
//  1. Write the current input buffer (possibly empty) to a temp file.
//  2. Build an exec.Cmd pointed at that file.
//  3. Hand it off to editorProcessRunner (tea.ExecProcess in production,
//     a synchronous fake in tests).
//  4. On exit, ExternalEditorFinishedMsg is dispatched with the edited
//     content or an error; handleExternalEditorFinished consumes it.
//
// Expected:
//   - The Intent has been constructed normally; i.notificationManager may
//     be nil in minimal test setups, in which case no notification is
//     produced but the flow still short-circuits cleanly.
//
// Returns:
//   - A tea.Cmd driving the editor process; nil when no editor is available
//     or the tempfile cannot be created (both cases surface a notification
//     when a manager is configured).
//
// Side effects:
//   - Creates a temp file under $TMPDIR.
//   - May add an info/error notification when the editor cannot be
//     launched.
func (i *Intent) openExternalEditor() tea.Cmd {
	editor := externalEditorBinary()
	if editor == "" {
		i.addEditorNotification(
			"External editor unavailable",
			"Set $EDITOR or install vim to edit in an external editor.",
			notification.LevelWarning,
		)
		return nil
	}

	tmpFile, err := os.CreateTemp("", "flowstate-input-*.txt")
	if err != nil {
		i.addEditorNotification(
			"External editor failed",
			fmt.Sprintf("Could not create temp file: %v", err),
			notification.LevelError,
		)
		return nil
	}
	tmpPath := tmpFile.Name()
	// Seed the file with the current input buffer so the editor opens on
	// the in-flight draft. A fresh buffer yields an empty file, which is
	// the desired behaviour — the editor still opens so the user can
	// compose a message from scratch.
	if _, writeErr := tmpFile.WriteString(i.input); writeErr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		i.addEditorNotification(
			"External editor failed",
			fmt.Sprintf("Could not write temp file: %v", writeErr),
			notification.LevelError,
		)
		return nil
	}
	if closeErr := tmpFile.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		i.addEditorNotification(
			"External editor failed",
			fmt.Sprintf("Could not close temp file: %v", closeErr),
			notification.LevelError,
		)
		return nil
	}

	cmd := exec.Command(editor, tmpPath) //nolint:gosec // editor is user-selected via $EDITOR.
	return editorProcessRunner(cmd, func(runErr error) tea.Msg {
		if runErr != nil {
			return ExternalEditorFinishedMsg{TempPath: tmpPath, Err: runErr}
		}
		data, readErr := os.ReadFile(tmpPath)
		if readErr != nil {
			return ExternalEditorFinishedMsg{TempPath: tmpPath, Err: readErr}
		}
		return ExternalEditorFinishedMsg{Content: string(data), TempPath: tmpPath}
	})
}

// handleExternalEditorFinished consumes an ExternalEditorFinishedMsg: it
// replaces the chat input buffer on success, surfaces an error
// notification on failure, and unconditionally removes the temp file so
// repeated edits do not accumulate state in $TMPDIR.
//
// Expected:
//   - msg was produced by openExternalEditor (directly or via the
//     production tea.ExecProcess pipeline).
//
// Returns:
//   - Always nil; any follow-up rendering happens via the existing
//     viewport-refresh side effect.
//
// Side effects:
//   - Deletes msg.TempPath when non-empty.
//   - On success: replaces i.input with the edited content (whitespace
//     preserved) and refreshes the viewport so the change is visible.
//   - On failure: adds a warning notification via the manager.
func (i *Intent) handleExternalEditorFinished(msg ExternalEditorFinishedMsg) tea.Cmd {
	if msg.TempPath != "" {
		// Best-effort cleanup. A leftover temp file is not fatal.
		_ = os.Remove(msg.TempPath)
	}
	if msg.Err != nil {
		i.addEditorNotification(
			"External editor failed",
			fmt.Sprintf("Editor exited with error: %v", msg.Err),
			notification.LevelError,
		)
		return nil
	}
	i.input = msg.Content
	i.updateViewportForInput()
	return nil
}

// addEditorNotification routes a user-visible editor message through the
// notification manager. Split out of openExternalEditor so every failure
// path produces a consistent, deduplicable entry.
//
// Expected:
//   - title and message are human-readable strings.
//   - level is one of the notification.Level constants.
//
// Side effects:
//   - When i.notificationManager is non-nil, appends a notification with
//     a unique "external-editor-" ID and a 6-second duration.
//   - When nil, the call is a no-op (preserves test-minimal construction).
func (i *Intent) addEditorNotification(title, message string, level notification.Level) {
	if i.notificationManager == nil {
		return
	}
	i.notificationManager.Add(notification.Notification{
		ID:        "external-editor-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:     title,
		Message:   message,
		Level:     level,
		Duration:  6 * time.Second,
		CreatedAt: time.Now(),
	})
}
