package sessionbrowser

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/intents"
)

// SessionEntry represents a saved session available for selection.
type SessionEntry struct {
	ID           string
	Title        string
	MessageCount int
	LastActive   time.Time
}

// SessionDeleter removes a persisted session and its co-located activity
// timeline. It is the narrow interface satisfied by FileSessionStore so the
// browser intent can trigger deletions without importing the full session
// store surface.
type SessionDeleter interface {
	// Delete removes the session identified by sessionID. Idempotent —
	// a missing session is not considered an error.
	Delete(sessionID string) error
}

// IntentConfig holds configuration for the session browser intent.
type IntentConfig struct {
	// Sessions is the list of saved sessions to display.
	Sessions []SessionEntry
	// Deleter, when non-nil, enables the 'd' keybinding to delete a session.
	// Leaving it nil disables the delete affordance entirely.
	Deleter SessionDeleter
	// ActiveSessionID, when non-empty, marks a session as the currently-open
	// one; pressing 'd' on it will surface a "cannot delete" message
	// instead of opening the confirmation modal.
	ActiveSessionID string
}

// Intent allows users to browse and select from a list of sessions.
type Intent struct {
	sessions         []SessionEntry
	selectedSession  int
	result           *intents.IntentResult
	deleter          SessionDeleter
	activeSessionID  string
	confirmingDelete bool
	pendingDeleteIdx int
	activeBlockMsg   string
}

// NewIntent creates a new session browser intent from the given configuration.
//
// Expected:
//   - cfg is a fully populated IntentConfig with a valid session list.
//
// Returns:
//   - An initialised Intent with selection at the first item.
//
// Side effects:
//   - None.
func NewIntent(cfg IntentConfig) *Intent {
	return &Intent{
		sessions:        cfg.Sessions,
		selectedSession: 0,
		result:          nil,
		deleter:         cfg.Deleter,
		activeSessionID: cfg.ActiveSessionID,
	}
}

// Init initialises the session browser intent.
//
// Returns:
//   - nil.
//
// Side effects:
//   - None.
func (i *Intent) Init() tea.Cmd {
	return nil
}

// Update handles messages from the Bubble Tea event loop.
//
// Expected:
//   - msg is a valid tea.Msg, typically a tea.KeyMsg or tea.WindowSizeMsg.
//
// Returns:
//   - A tea.Cmd, or nil when no command is needed.
//
// Side effects:
//   - Mutates intent state based on the message type.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if ok {
		return i.handleKeyMsg(keyMsg)
	}
	return nil
}

// handleKeyMsg processes keyboard input for navigation and selection.
//
// Expected:
//   - msg is a valid tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd, or nil when no command is needed.
//
// Side effects:
//   - Mutates selection index and result state based on the key pressed.
func (i *Intent) handleKeyMsg(msg tea.KeyMsg) tea.Cmd {
	if i.confirmingDelete {
		return i.handleConfirmDeleteKey(msg)
	}
	// Typed rune handling — 'd' opens the delete confirmation.
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && msg.Runes[0] == 'd' {
		return i.requestDelete()
	}
	switch msg.Type {
	case tea.KeyUp:
		i.activeBlockMsg = ""
		if i.selectedSession > 0 {
			i.selectedSession--
		}
		return nil
	case tea.KeyDown:
		i.activeBlockMsg = ""
		if i.selectedSession < i.itemCount()-1 {
			i.selectedSession++
		}
		return nil
	case tea.KeyEnter:
		return i.selectCurrent()
	case tea.KeyEsc, tea.KeyCtrlC:
		i.result = NewCancelResult()
		return i.dismissModal()
	}
	return nil
}

// requestDelete transitions the browser into confirming-delete state for the
// currently-selected session, provided the row represents a deletable saved
// session and a Deleter is configured. The New Session row and the active
// session are refused — the latter surfaces a blocking message in the view.
//
// Returns:
//   - nil (state change only).
//
// Side effects:
//   - May set confirmingDelete, pendingDeleteIdx, or activeBlockMsg.
func (i *Intent) requestDelete() tea.Cmd {
	if i.deleter == nil {
		return nil
	}
	if i.selectedSession == 0 {
		return nil
	}
	sessionIdx := i.selectedSession - 1
	if sessionIdx >= len(i.sessions) {
		return nil
	}
	if i.sessions[sessionIdx].ID == i.activeSessionID && i.activeSessionID != "" {
		i.activeBlockMsg = "Cannot delete the active session. Switch sessions first."
		return nil
	}
	i.confirmingDelete = true
	i.pendingDeleteIdx = sessionIdx
	i.activeBlockMsg = ""
	return nil
}

// handleConfirmDeleteKey routes keypresses while the confirm-delete prompt is
// visible. Y/Enter confirm the delete; anything else cancels.
//
// Expected:
//   - msg is a valid tea.KeyMsg.
//
// Returns:
//   - A tea.Cmd that emits SessionDeletedMsg on confirm, or nil on cancel.
//
// Side effects:
//   - Clears confirmingDelete regardless of outcome.
func (i *Intent) handleConfirmDeleteKey(msg tea.KeyMsg) tea.Cmd {
	if msg.Type == tea.KeyEnter {
		return i.performDelete()
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) == 1 {
		r := msg.Runes[0]
		if r == 'y' || r == 'Y' {
			return i.performDelete()
		}
	}
	// Anything else cancels.
	i.confirmingDelete = false
	return nil
}

// performDelete invokes the configured Deleter and, on success, removes the
// session from the in-memory list and adjusts the selection index. On
// failure the list is left untouched so the user can retry. Either way a
// SessionDeletedMsg is emitted carrying the outcome.
//
// Returns:
//   - A tea.Cmd that emits a SessionDeletedMsg.
//
// Side effects:
//   - Clears confirmingDelete.
//   - Mutates sessions and selectedSession on success.
func (i *Intent) performDelete() tea.Cmd {
	idx := i.pendingDeleteIdx
	i.confirmingDelete = false
	if idx < 0 || idx >= len(i.sessions) {
		return nil
	}
	id := i.sessions[idx].ID
	err := i.deleter.Delete(id)
	if err == nil {
		i.sessions = append(i.sessions[:idx], i.sessions[idx+1:]...)
		i.adjustSelectionAfterDelete(idx)
	}
	return func() tea.Msg { return SessionDeletedMsg{SessionID: id, Err: err} }
}

// adjustSelectionAfterDelete keeps the cursor on a sensible row after a
// session is removed from the list. If the deleted row was the last session
// the cursor snaps up one row; otherwise the same visual row is retained,
// which naturally surfaces the next-newer session.
//
// Expected:
//   - deletedIdx is the 0-based session index that was just removed.
//
// Side effects:
//   - Updates selectedSession.
func (i *Intent) adjustSelectionAfterDelete(deletedIdx int) {
	// Cursor position in the list is sessionIndex+1 (0 is the New Session row).
	// If we removed the last session, the previous row becomes the cursor's
	// new home; otherwise keep the cursor where it was.
	maxSessionIdx := len(i.sessions) - 1
	if maxSessionIdx < 0 {
		i.selectedSession = 0
		return
	}
	if deletedIdx > maxSessionIdx {
		i.selectedSession = maxSessionIdx + 1
	}
}

// selectCurrent confirms the currently highlighted item.
//
// Returns:
//   - A tea.Cmd that dismisses the modal and emits a SessionSelectedMsg.
//
// Side effects:
//   - Sets the intent result based on the selected item.
func (i *Intent) selectCurrent() tea.Cmd {
	if i.selectedSession == 0 {
		i.result = NewCreateResult()
		return i.dismissWithMsg(SessionSelectedMsg{IsNew: true})
	}

	sessionIdx := i.selectedSession - 1
	if sessionIdx < len(i.sessions) {
		selectedID := i.sessions[sessionIdx].ID
		i.result = NewSelectResult(selectedID)
		return i.dismissWithMsg(SessionSelectedMsg{SessionID: selectedID})
	}
	return nil
}

// dismissWithMsg returns a sequenced command that first dismisses the modal,
// then emits the given message to notify the parent intent.
//
// Expected:
//   - msg is a valid tea.Msg to deliver after the dismiss.
//
// Returns:
//   - A tea.Cmd that emits DismissModalMsg first, then the given message.
//
// Side effects:
//   - None.
func (i *Intent) dismissWithMsg(msg tea.Msg) tea.Cmd {
	return tea.Sequence(
		func() tea.Msg { return intents.DismissModalMsg{} },
		func() tea.Msg { return msg },
	)
}

// dismissModal returns a command to dismiss the modal overlay.
//
// Returns:
//   - A tea.Cmd that emits a DismissModalMsg to close the modal.
//
// Side effects:
//   - None.
func (i *Intent) dismissModal() tea.Cmd {
	return func() tea.Msg { return intents.DismissModalMsg{} }
}

// itemCount returns the total number of items in the list including the
// "New Session" entry.
//
// Returns:
//   - An int representing the total item count.
//
// Side effects:
//   - None.
func (i *Intent) itemCount() int {
	return len(i.sessions) + 1
}

// View renders the session browser interface.
//
// Returns:
//   - A string containing the rendered session list.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	return i.renderContent()
}

// renderContent renders the list of sessions with a "New Session" entry at the top.
//
// Returns:
//   - A string containing the rendered session list.
//
// Side effects:
//   - None.
func (i *Intent) renderContent() string {
	var lines []string

	newSessionPrefix := "  "
	if i.selectedSession == 0 {
		newSessionPrefix = "> "
	}
	lines = append(lines, newSessionPrefix+"\u271a New Session")

	if len(i.sessions) == 0 {
		lines = append(lines, "  No sessions yet")
	}

	for idx, session := range i.sessions {
		prefix := "  "
		if idx+1 == i.selectedSession {
			prefix = "> "
		}
		lines = append(lines, prefix+formatSession(session))
	}

	if i.activeBlockMsg != "" {
		lines = append(lines, "", i.activeBlockMsg)
	}

	if i.confirmingDelete && i.pendingDeleteIdx >= 0 && i.pendingDeleteIdx < len(i.sessions) {
		target := i.sessions[i.pendingDeleteIdx].Title
		lines = append(lines, "", fmt.Sprintf("Delete session '%s' and its activity timeline? (y/N)", target))
	}

	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
}

// IsConfirmingDelete reports whether the browser is currently prompting the
// user to confirm a destructive delete.
//
// Returns:
//   - true when the confirmation prompt is visible, false otherwise.
//
// Side effects:
//   - None.
func (i *Intent) IsConfirmingDelete() bool {
	return i.confirmingDelete
}

// formatSession formats a session entry for display.
//
// Expected:
//   - entry is a valid SessionEntry with Title, MessageCount, and LastActive fields populated.
//
// Returns:
//   - A string containing the session title with message count and relative time.
//
// Side effects:
//   - None.
func formatSession(entry SessionEntry) string {
	return fmt.Sprintf("%s (%d messages, last active: %s)", entry.Title, entry.MessageCount, formatRelativeTime(entry.LastActive))
}

// formatRelativeTime converts a timestamp to a human-readable relative duration.
//
// Expected:
//   - t is a non-zero time.Time value.
//
// Returns:
//   - A string such as "2h ago", "3d ago", or "just now".
//
// Side effects:
//   - None.
func formatRelativeTime(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// Result returns the intent result.
//
// Returns:
//   - The IntentResult if a session was selected, created, or escape was pressed.
//   - nil if no action has been taken yet.
//
// Side effects:
//   - None.
func (i *Intent) Result() *intents.IntentResult {
	return i.result
}

// SelectedSession returns the index of the currently selected item.
//
// Returns:
//   - An int representing the selected item index (0 is "New Session").
//
// Side effects:
//   - None.
func (i *Intent) SelectedSession() int {
	return i.selectedSession
}
