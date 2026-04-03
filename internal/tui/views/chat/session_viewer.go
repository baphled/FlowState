package chat

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// SessionViewerModal renders a read-only scrollable view of a session.
type SessionViewerModal struct {
	sessionID string
	content   string
	offset    int
	width     int
	height    int
}

// NewSessionViewerModal creates a viewer for the given session content.
//
// Expected:
//   - sessionID is the unique identifier of the session.
//   - content is the pre-rendered session text.
//   - width and height are terminal dimensions.
//
// Returns:
//   - An initialised SessionViewerModal.
//
// Side effects:
//   - None.
func NewSessionViewerModal(sessionID, content string, width, height int) *SessionViewerModal {
	return &SessionViewerModal{
		sessionID: sessionID,
		content:   content,
		offset:    0,
		width:     width,
		height:    height,
	}
}

// ScrollUp scrolls up by one line.
//
// Expected:
//   - None.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Decrements offset, clamped to 0.
func (m *SessionViewerModal) ScrollUp() {
	if m.offset > 0 {
		m.offset--
	}
}

// ScrollDown scrolls down by one line.
//
// Expected:
//   - None.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Increments offset.
func (m *SessionViewerModal) ScrollDown() {
	lines := strings.Split(m.content, "\n")
	maxOffset := len(lines) - m.height + 3
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.offset < maxOffset {
		m.offset++
	}
}

// RenderContent returns the scrollable text lines for the current viewport
// without any border, header, or footer decoration.
//
// Expected:
//   - width and height are terminal dimensions.
//
// Returns:
//   - The visible content lines joined by newlines.
//
// Side effects:
//   - None.
func (m *SessionViewerModal) RenderContent(width, height int) string {
	_ = width
	lines := strings.Split(m.content, "\n")
	contentHeight := height - 10
	if contentHeight < 1 {
		contentHeight = 1
	}
	visibleLines := make([]string, 0, contentHeight)
	for idx := m.offset; idx < len(lines) && len(visibleLines) < contentHeight; idx++ {
		visibleLines = append(visibleLines, lines[idx])
	}
	for len(visibleLines) < contentHeight {
		visibleLines = append(visibleLines, "")
	}
	return strings.Join(visibleLines, "\n")
}

// Render returns the modal content string.
//
// Expected:
//   - None.
//
// Returns:
//   - A bordered, scrollable view of the session content.
//
// Side effects:
//   - None.
func (m *SessionViewerModal) Render(_ int, height int) string {
	header := "Session: " + m.sessionID
	lines := strings.Split(m.content, "\n")

	visibleLines := []string{}
	contentHeight := height - 4
	if contentHeight < 1 {
		contentHeight = 1
	}

	for i := m.offset; i < len(lines) && i < m.offset+contentHeight; i++ {
		visibleLines = append(visibleLines, lines[i])
	}

	for len(visibleLines) < contentHeight {
		visibleLines = append(visibleLines, "")
	}

	footer := "↑/↓ scroll · Esc close"
	viewContent := strings.Join(visibleLines, "\n")

	content := header + "\n" + viewContent + "\n" + footer
	bordered := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Render(content)

	return bordered
}
