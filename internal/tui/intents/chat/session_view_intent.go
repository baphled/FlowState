package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/uikit/layout"
	"github.com/baphled/flowstate/internal/ui/terminal"
)

// SessionViewIntentConfig holds configuration for creating a SessionViewIntent.
type SessionViewIntentConfig struct {
	SessionID string
	Content   string
	Width     int
	Height    int
}

// SessionViewIntent provides a read-only view of a session with scroll and navigation.
type SessionViewIntent struct {
	sessionID  string
	content    string
	width      int
	height     int
	offset     int
	result     *tuiintents.IntentResult
	breadcrumb string
}

// NewSessionViewIntent creates a new SessionViewIntent from the given configuration.
//
// Expected:
//   - cfg.SessionID is the unique identifier for the session.
//   - cfg.Content is the pre-rendered session text.
//   - cfg.Width and cfg.Height are terminal dimensions.
//
// Returns:
//   - An initialised SessionViewIntent with default breadcrumb path.
//
// Side effects:
//   - None.
func NewSessionViewIntent(cfg SessionViewIntentConfig) *SessionViewIntent {
	breadcrumb := "Chat"
	if len(cfg.SessionID) >= 8 {
		breadcrumb = "Chat > " + cfg.SessionID[:8]
	}
	return &SessionViewIntent{
		sessionID:  cfg.SessionID,
		content:    cfg.Content,
		width:      cfg.Width,
		height:     cfg.Height,
		offset:     0,
		result:     nil,
		breadcrumb: breadcrumb,
	}
}

// Init returns the initial command for the intent.
//
// Returns:
//
//	nil (no initial commands needed for read-only view).
//
// Side effects:
//   - None.
func (i *SessionViewIntent) Init() tea.Cmd {
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
//   - Updates offset on scroll keys.
//   - Sets result to NavigateToParentMsg on Esc.
func (i *SessionViewIntent) Update(msg tea.Msg) tea.Cmd {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch keyMsg.Type {
	case tea.KeyEsc:
		i.result = &tuiintents.IntentResult{
			Action: "navigate_parent",
		}
	case tea.KeyUp, tea.KeyPgUp:
		if i.offset > 0 {
			i.offset--
		}
	case tea.KeyDown, tea.KeyPgDown:
		lines := strings.Split(i.content, "\n")
		maxOffset := len(lines) - i.height + 3
		if maxOffset < 0 {
			maxOffset = 0
		}
		if i.offset < maxOffset {
			i.offset++
		}
	case tea.KeyHome:
		i.offset = 0
	case tea.KeyEnd:
		lines := strings.Split(i.content, "\n")
		i.offset = len(lines) - 1
	}
	return nil
}

// View renders the session view intent as a string.
//
// Returns:
//   - A rendered view with the session content, breadcrumbs, and navigation help.
//
// Side effects:
//   - None.
func (i *SessionViewIntent) View() string {
	lines := strings.Split(i.content, "\n")
	contentHeight := i.height - 10
	if contentHeight < 1 {
		contentHeight = 1
	}

	visibleLines := make([]string, 0, contentHeight)
	for idx := i.offset; idx < len(lines) && len(visibleLines) < contentHeight; idx++ {
		visibleLines = append(visibleLines, lines[idx])
	}

	for len(visibleLines) < contentHeight {
		visibleLines = append(visibleLines, "")
	}

	viewContent := strings.Join(visibleLines, "\n")

	sl := layout.NewScreenLayout(&terminal.Info{Width: i.width, Height: i.height}).
		WithBreadcrumbs(i.breadcrumb).
		WithContent(viewContent).
		WithHelp("↑/↓ PgUp/PgDn scroll · Esc back").
		WithFooterSeparator(true)

	return sl.Render()
}

// Result returns the current outcome state of the session view intent.
//
// Returns:
//   - The current IntentResult, or nil if no result has been set.
//
// Side effects:
//   - None.
func (i *SessionViewIntent) Result() *tuiintents.IntentResult {
	return i.result
}

// SetBreadcrumbPath sets the breadcrumb navigation path for the view.
//
// Expected:
//   - path is a non-empty string like "Chat" or "Chat > session-id".
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the breadcrumb display on next View() call.
func (i *SessionViewIntent) SetBreadcrumbPath(path string) {
	i.breadcrumb = path
}
