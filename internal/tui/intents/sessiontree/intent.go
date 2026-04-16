package sessiontree

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/intents"
)

// SessionNode represents a session in the flat input list provided by the caller.
type SessionNode struct {
	SessionID string
	AgentID   string
	ParentID  string
}

// Result represents the outcome of the session tree intent.
type Result struct {
	SelectedSessionID string
	Cancelled         bool
}

// Intent displays a hierarchical session tree as a modal overlay.
//
// It implements the intents.Intent interface. The tree is built once at
// construction from a flat list of SessionNode values. Keyboard navigation
// is deferred to a later task; Update currently handles only WindowSizeMsg.
type Intent struct {
	currentSessionID string
	cursorID         string
	sessions         []SessionNode
	root             *treeNode
	lines            []displayLine
	result           *intents.IntentResult
	width            int
	height           int
}

// New creates a new session tree intent from the given session list.
//
// Expected:
//   - currentSessionID identifies the active session to mark with a bullet.
//   - sessions is a flat list of SessionNode values; one must have ParentID == "".
//
// Returns:
//   - An initialised Intent with cursor set to currentSessionID.
//
// Side effects:
//   - None.
func New(currentSessionID string, sessions []SessionNode) *Intent {
	i := &Intent{
		currentSessionID: currentSessionID,
		cursorID:         currentSessionID,
		sessions:         sessions,
	}
	i.rebuildTree()
	return i
}

// Init initialises the session tree intent.
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
//   - msg is a valid tea.Msg, typically a tea.WindowSizeMsg.
//
// Returns:
//   - A tea.Cmd, or nil when no command is needed.
//
// Side effects:
//   - Stores window dimensions when a WindowSizeMsg is received.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		i.width = wsm.Width
		i.height = wsm.Height
	}
	return nil
}

// View renders the session tree interface.
//
// Returns:
//   - A string containing the rendered tree with title, connectors, and markers.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	if len(i.sessions) == 0 {
		return "Session Tree\n\nNo sessions\n"
	}

	return "Session Tree\n\n" + renderTree(i.lines)
}

// Result returns the intent result.
//
// Returns:
//   - The IntentResult if a session was selected or the intent was cancelled.
//   - nil if no action has been taken yet.
//
// Side effects:
//   - None.
func (i *Intent) Result() *intents.IntentResult {
	return i.result
}

// CursorID returns the session ID currently under the cursor.
//
// Returns:
//   - A string identifying the cursor position.
//
// Side effects:
//   - None.
func (i *Intent) CursorID() string {
	return i.cursorID
}

// SetCursor moves the cursor to the given session ID and rebuilds display lines.
//
// Expected:
//   - sessionID is the ID of a session present in the tree.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates cursorID and rebuilds display lines.
func (i *Intent) SetCursor(sessionID string) {
	i.cursorID = sessionID
	i.rebuildTree()
}

// Width returns the stored terminal width from the last WindowSizeMsg.
//
// Returns:
//   - An int representing the terminal width in columns.
//
// Side effects:
//   - None.
func (i *Intent) Width() int {
	return i.width
}

// Height returns the stored terminal height from the last WindowSizeMsg.
//
// Returns:
//   - An int representing the terminal height in rows.
//
// Side effects:
//   - None.
func (i *Intent) Height() int {
	return i.height
}

// rebuildTree constructs the internal tree and flattens it for display.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates root and lines fields.
func (i *Intent) rebuildTree() {
	i.root = buildTree(i.sessions)
	i.lines = flattenTree(i.root, i.currentSessionID, i.cursorID)
}
