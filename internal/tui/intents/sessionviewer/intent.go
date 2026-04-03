package sessionviewer

import (
	tea "github.com/charmbracelet/bubbletea"

	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	chatviews "github.com/baphled/flowstate/internal/tui/views/chat"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

// Intent provides a full-screen read-only view of a child session.
//
// It delegates all scroll behaviour and content rendering to the embedded
// SessionViewerModal, wrapping the result in a ScreenLayout for breadcrumbs
// and a help bar.
type Intent struct {
	viewer     *chatviews.SessionViewerModal
	result     *tuiintents.IntentResult
	breadcrumb string
	width      int
	height     int
}

// NewIntent creates a sessionviewer Intent for the given session.
//
// Expected:
//   - sessionID is the unique identifier of the session to view.
//   - content is the pre-rendered session text.
//   - width and height are terminal dimensions.
//
// Returns:
//   - An initialised Intent with a breadcrumb derived from the session ID.
//
// Side effects:
//   - None.
func NewIntent(sessionID, content string, width, height int) *Intent {
	breadcrumb := "Chat"
	if len(sessionID) >= 8 {
		breadcrumb = "Chat > " + sessionID[:8]
	}
	return &Intent{
		viewer:     chatviews.NewSessionViewerModal(sessionID, content, width, height),
		breadcrumb: breadcrumb,
		width:      width,
		height:     height,
	}
}

// Init returns the initial command for the intent.
//
// Returns:
//   - nil; no initial commands are needed for a read-only view.
//
// Side effects:
//   - None.
func (i *Intent) Init() tea.Cmd {
	return nil
}

// Update processes a Bubble Tea message and returns any command to execute.
//
// Expected:
//   - msg is a tea.Msg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd to execute, or nil if no command is needed.
//
// Side effects:
//   - Delegates scroll operations to the viewer on scroll keys.
//   - Sets result and returns NavigateToParentMsg on Esc.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch keyMsg.Type {
	case tea.KeyEsc:
		i.result = &tuiintents.IntentResult{
			Action: "navigate_parent",
		}
		return func() tea.Msg { return tuiintents.NavigateToParentMsg{} }
	case tea.KeyUp, tea.KeyPgUp:
		i.viewer.ScrollUp()
	case tea.KeyDown, tea.KeyPgDown:
		i.viewer.ScrollDown()
	}
	return nil
}

// View renders the session viewer intent as a string.
//
// Returns:
//   - A rendered view with breadcrumbs, the session content, and a navigation help bar.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	content := i.viewer.Render(i.width, i.height)
	sl := layout.NewScreenLayout(&terminal.Info{Width: i.width, Height: i.height}).
		WithBreadcrumbs(i.breadcrumb).
		WithContent(content).
		WithHelp("↑/↓ PgUp/PgDn scroll · Esc back").
		WithFooterSeparator(true)
	return sl.Render()
}

// Result returns the current outcome state of the session viewer intent.
//
// Returns:
//   - The current IntentResult, or nil if no result has been set.
//
// Side effects:
//   - None.
func (i *Intent) Result() *tuiintents.IntentResult {
	return i.result
}

// SetBreadcrumbPath sets the breadcrumb navigation path for the view.
//
// Expected:
//   - path is a non-empty string such as "Chat" or "Chat > session-id".
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the breadcrumb displayed on the next View() call.
func (i *Intent) SetBreadcrumbPath(path string) {
	i.breadcrumb = path
}
