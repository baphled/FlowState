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

// IntentConfig holds configuration for the session browser intent.
type IntentConfig struct {
	Sessions []SessionEntry
}

// Intent allows users to browse and select from a list of sessions.
type Intent struct {
	sessions        []SessionEntry
	selectedSession int
	result          *intents.IntentResult
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
	switch msg.Type {
	case tea.KeyUp:
		if i.selectedSession > 0 {
			i.selectedSession--
		}
		return nil
	case tea.KeyDown:
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

	for idx, session := range i.sessions {
		prefix := "  "
		if idx+1 == i.selectedSession {
			prefix = "> "
		}
		lines = append(lines, prefix+formatSession(session))
	}

	var buf strings.Builder
	for _, line := range lines {
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	return buf.String()
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
