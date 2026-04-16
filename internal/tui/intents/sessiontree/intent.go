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

// SelectedMsg carries the session ID selected by the user in the tree.
//
// It is emitted as the first message in a tea.Sequence when the user presses
// Enter, guaranteeing the chat intent receives the selection before the
// dispatcher tears down the modal via DismissModalMsg.
type SelectedMsg struct {
	SessionID string
}

// Intent displays a hierarchical session tree as a modal overlay.
//
// It implements the intents.Intent interface. The tree is built once at
// construction from a flat list of SessionNode values. Keyboard navigation
// supports arrow keys, Enter selection, Escape cancel, and r-refresh with
// NodeID cursor invariant.
type Intent struct {
	currentSessionID string
	cursorID         string
	sessions         []SessionNode
	parentMap        map[string]string
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
//   - msg is a valid tea.Msg (tea.KeyMsg, tea.WindowSizeMsg, etc.).
//
// Returns:
//   - A tea.Cmd: tea.Sequence for Enter, a DismissModalMsg cmd for Escape, or nil.
//
// Side effects:
//   - Modifies cursorID on arrow key presses.
//   - Sets result on Enter or Escape.
//   - Rebuilds tree on r key press.
//   - Stores window dimensions on WindowSizeMsg.
func (i *Intent) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		i.width = msg.Width
		i.height = msg.Height
		return nil

	case tea.KeyMsg:
		return i.handleKey(msg)
	}

	return nil
}

// handleKey processes keyboard input for navigation, selection, cancellation,
// and refresh.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd for Enter (tea.Sequence) or Escape (DismissModalMsg), nil otherwise.
//
// Side effects:
//   - Updates cursorID for arrow keys.
//   - Sets result for Enter or Escape.
//   - Rebuilds tree and resolves cursor fallback for r key.
func (i *Intent) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyUp:
		i.moveCursor(-1)
		return nil

	case tea.KeyDown:
		i.moveCursor(1)
		return nil

	case tea.KeyEnter:
		return i.selectCurrent()

	case tea.KeyEscape:
		return i.cancel()

	case tea.KeyRunes:
		if len(msg.Runes) == 1 && msg.Runes[0] == 'r' {
			i.refresh()
			return nil
		}
	}

	return nil
}

// moveCursor shifts the cursor by delta positions in the flat display list.
//
// Expected:
//   - delta is -1 (up) or +1 (down).
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates cursorID to the node at the new flat index. Clamps to bounds.
//   - Rebuilds display lines to reflect the new cursor position.
func (i *Intent) moveCursor(delta int) {
	if len(i.lines) == 0 {
		return
	}

	idx := i.cursorFlatIndex()
	idx += delta

	if idx < 0 {
		idx = 0
	}
	if idx >= len(i.lines) {
		idx = len(i.lines) - 1
	}

	i.cursorID = i.lines[idx].sessionID
	i.rebuildTree()
}

// cursorFlatIndex returns the index of the current cursor in the flat display
// list, or 0 if the cursor is not found.
//
// Returns:
//   - An int representing the flat index of the cursor.
//
// Side effects:
//   - None.
func (i *Intent) cursorFlatIndex() int {
	for idx, line := range i.lines {
		if line.sessionID == i.cursorID {
			return idx
		}
	}
	return 0
}

// selectCurrent emits a SelectedMsg followed by a DismissModalMsg via
// tea.Sequence, and records the result.
//
// Returns:
//   - A tea.Cmd that delivers SelectedMsg then DismissModalMsg in order.
//
// Side effects:
//   - Sets the intent result with the selected session ID.
func (i *Intent) selectCurrent() tea.Cmd {
	selectedID := i.cursorID
	i.result = &intents.IntentResult{
		Data: Result{SelectedSessionID: selectedID},
	}

	return tea.Sequence(
		func() tea.Msg { return SelectedMsg{SessionID: selectedID} },
		func() tea.Msg { return intents.DismissModalMsg{} },
	)
}

// cancel emits a DismissModalMsg and records a cancelled result.
//
// Returns:
//   - A tea.Cmd that delivers DismissModalMsg.
//
// Side effects:
//   - Sets the intent result with Cancelled true.
func (i *Intent) cancel() tea.Cmd {
	i.result = &intents.IntentResult{
		Data: Result{Cancelled: true},
	}
	return func() tea.Msg { return intents.DismissModalMsg{} }
}

// refresh rebuilds the tree from the current sessions and resolves cursor
// position using the NodeID cursor invariant.
//
// If the current cursorID no longer exists in the rebuilt tree, the cursor
// walks its parent chain in the original session list until a surviving
// ancestor is found. If no ancestor survives, the cursor moves to root.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Rebuilds root, lines, and potentially updates cursorID.
func (i *Intent) refresh() {
	oldCursorID := i.cursorID

	// Snapshot the parent map from the PREVIOUS tree so deleted nodes can
	// still be walked up to their ancestors after the rebuild.
	oldParentMap := i.parentMap

	i.rebuildTree()

	// Check whether the cursor node survived the rebuild.
	if i.nodeExistsInLines(oldCursorID) {
		i.cursorID = oldCursorID
		i.rebuildTree()
		return
	}

	// Walk parent chain to find nearest surviving ancestor.
	current := oldCursorID
	for {
		parent, ok := oldParentMap[current]
		if !ok || parent == "" {
			break
		}
		if i.nodeExistsInLines(parent) {
			i.cursorID = parent
			i.rebuildTree()
			return
		}
		current = parent
	}

	// No ancestor found; fall back to root.
	if len(i.lines) > 0 {
		i.cursorID = i.lines[0].sessionID
	}
	i.rebuildTree()
}

// nodeExistsInLines checks whether a session ID appears in the current flat
// display list.
//
// Expected:
//   - sessionID is a non-empty string.
//
// Returns:
//   - true if the node is present in lines, false otherwise.
//
// Side effects:
//   - None.
func (i *Intent) nodeExistsInLines(sessionID string) bool {
	for _, line := range i.lines {
		if line.sessionID == sessionID {
			return true
		}
	}
	return false
}

// buildParentMap creates a lookup from session ID to parent ID using the
// current sessions slice.
//
// Returns:
//   - A map[string]string where keys are session IDs and values are parent IDs.
//
// Side effects:
//   - None.
func (i *Intent) buildParentMap() map[string]string {
	m := make(map[string]string, len(i.sessions))
	for _, s := range i.sessions {
		m[s.SessionID] = s.ParentID
	}
	return m
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

// SetSessions replaces the session list used for tree rebuilds.
//
// Expected:
//   - sessions is a flat list of SessionNode values.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates the sessions field. Does not rebuild the tree; call refresh
//     (r key) or rebuildTree explicitly to apply changes.
func (i *Intent) SetSessions(sessions []SessionNode) {
	i.sessions = sessions
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

// rebuildTree constructs the internal tree, flattens it for display, and
// rebuilds the parent lookup map.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates root, lines, and parentMap fields.
func (i *Intent) rebuildTree() {
	i.parentMap = i.buildParentMap()
	i.root = buildTree(i.sessions)
	i.lines = flattenTree(i.root, i.currentSessionID, i.cursorID)
}
