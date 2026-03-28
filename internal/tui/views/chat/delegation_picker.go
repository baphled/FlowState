package chat

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/session"
	"github.com/charmbracelet/lipgloss"
)

// DelegationPickerModal renders a list of delegation sessions for selection.
type DelegationPickerModal struct {
	sessions []*session.Session
	cursor   int
	width    int
	height   int
}

// NewDelegationPickerModal creates a delegation picker with the given sessions.
//
// Expected:
//   - sessions may be empty or nil.
//   - width and height are terminal dimensions.
//
// Returns:
//   - An initialised DelegationPickerModal.
//
// Side effects:
//   - None.
func NewDelegationPickerModal(sessions []*session.Session, width, height int) *DelegationPickerModal {
	return &DelegationPickerModal{
		sessions: sessions,
		cursor:   0,
		width:    width,
		height:   height,
	}
}

// MoveUp moves the cursor up one item.
//
// Expected:
//   - None.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Decrements cursor, clamped to 0.
func (m *DelegationPickerModal) MoveUp() {
	if m.cursor > 0 {
		m.cursor--
	}
}

// MoveDown moves the cursor down one item.
//
// Expected:
//   - None.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Increments cursor, clamped to len(sessions)-1.
func (m *DelegationPickerModal) MoveDown() {
	if m.cursor < len(m.sessions)-1 {
		m.cursor++
	}
}

// Selected returns the currently highlighted session, or nil if empty.
//
// Expected:
//   - None.
//
// Returns:
//   - The session at cursor position, or nil.
//
// Side effects:
//   - None.
func (m *DelegationPickerModal) Selected() *session.Session {
	if len(m.sessions) == 0 {
		return nil
	}
	if m.cursor >= len(m.sessions) {
		return nil
	}
	return m.sessions[m.cursor]
}

// Render returns the modal content string.
//
// Expected:
//   - None.
//
// Returns:
//   - A formatted list of sessions with cursor indicator.
//
// Side effects:
//   - None.
func (m *DelegationPickerModal) Render(_ int, _ int) string {
	if len(m.sessions) == 0 {
		return renderDelegationPickerEmpty()
	}

	lines := []string{"Delegations"}
	for i, sess := range m.sessions {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		line := formatDelegationSessionLine(cursor, sess)
		lines = append(lines, line)
	}

	content := strings.Join(lines, "\n")
	bordered := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Render(content)

	return bordered
}

// renderDelegationPickerEmpty returns the empty state message for the delegation picker.
//
// Expected:
//   - None.
//
// Returns:
//   - A bordered string indicating no delegations are available.
//
// Side effects:
//   - None.
func renderDelegationPickerEmpty() string {
	content := "Delegations\n\nNo delegations in this session"
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Render(content)
}

// formatDelegationSessionLine formats a single delegation session line with cursor indicator.
//
// Expected:
//   - cursor is "  " (no selection) or "> " (selected).
//   - sess is a valid session pointer.
//
// Returns:
//   - A formatted string with agent ID and status.
//
// Side effects:
//   - None.
func formatDelegationSessionLine(cursor string, sess *session.Session) string {
	return fmt.Sprintf("%s%-20s  %s", cursor, sess.AgentID, sess.Status)
}
