package eventdetails

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents"
)

// Intent displays full metadata for a single SwarmEvent as a bordered modal overlay.
//
// It implements the intents.Intent interface. The overlay is read-only; the user
// scrolls with Up/Down or j/k and dismisses with Escape.
type Intent struct {
	event        streaming.SwarmEvent
	scrollOffset int
	width        int
	height       int
	result       *intents.IntentResult
}

// Result represents the outcome of the event details intent.
type Result struct {
	Dismissed bool
}

// New creates a new event details intent for the given SwarmEvent.
//
// Expected:
//   - event is a valid SwarmEvent with at least ID and Type populated.
//
// Returns:
//   - An initialised Intent ready for display as a modal overlay.
//
// Side effects:
//   - None.
func New(event streaming.SwarmEvent) *Intent {
	return &Intent{
		event: event,
	}
}

// Init initialises the event details intent.
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
//   - A tea.Cmd: a DismissModalMsg cmd for Escape, or nil.
//
// Side effects:
//   - Modifies scrollOffset on arrow key / j/k presses.
//   - Sets result on Escape.
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

// handleKey processes keyboard input for scrolling and dismissal.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - A tea.Cmd for Escape (DismissModalMsg), nil otherwise.
//
// Side effects:
//   - Updates scrollOffset for Up/Down/j/k keys.
//   - Sets result for Escape.
func (i *Intent) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.Type {
	case tea.KeyUp:
		i.scroll(-1)
		return nil

	case tea.KeyDown:
		i.scroll(1)
		return nil

	case tea.KeyPgUp:
		i.scroll(-i.pageDelta())
		return nil

	case tea.KeyPgDown:
		i.scroll(i.pageDelta())
		return nil

	case tea.KeyHome:
		i.jumpToTop()
		return nil

	case tea.KeyEnd:
		i.jumpToBottom()
		return nil

	case tea.KeyEscape:
		return i.dismiss()

	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			switch msg.Runes[0] {
			case 'j':
				i.scroll(1)
				return nil
			case 'k':
				i.scroll(-1)
				return nil
			}
		}
	}

	return nil
}

// pageDelta returns the number of lines to scroll on PgUp/PgDn. It is sized
// to visibleHeight - 1 so one line of context remains visible across a page
// jump; this mirrors standard pager (less/vim) conventions.
//
// Returns:
//   - The per-page scroll delta; never less than 1.
//
// Side effects:
//   - None.
func (i *Intent) pageDelta() int {
	delta := i.visibleHeight() - 1
	if delta < 1 {
		delta = 1
	}
	return delta
}

// jumpToTop moves the scroll offset to the first line of the rendered
// content. Symmetric with jumpToBottom for Home/End navigation.
//
// Side effects:
//   - Sets scrollOffset to 0.
func (i *Intent) jumpToTop() {
	i.scrollOffset = 0
}

// jumpToBottom moves the scroll offset to the last visible page of content.
// When content fits within the viewport, this is a no-op (offset stays 0).
//
// Side effects:
//   - Sets scrollOffset to the clamped max scroll position.
func (i *Intent) jumpToBottom() {
	maxScroll := i.contentLineCount() - i.visibleHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	i.scrollOffset = maxScroll
}

// scroll adjusts the scroll offset by delta, clamping to valid bounds.
//
// Expected:
//   - delta is -1 (up) or +1 (down).
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Updates scrollOffset. Clamps to [0, maxScroll].
func (i *Intent) scroll(delta int) {
	i.scrollOffset += delta
	if i.scrollOffset < 0 {
		i.scrollOffset = 0
	}

	contentLines := i.contentLineCount()
	maxScroll := contentLines - i.visibleHeight()
	if maxScroll < 0 {
		maxScroll = 0
	}
	if i.scrollOffset > maxScroll {
		i.scrollOffset = maxScroll
	}
}

// dismiss emits a DismissModalMsg and records a dismissed result.
//
// Returns:
//   - A tea.Cmd that delivers DismissModalMsg.
//
// Side effects:
//   - Sets the intent result with Dismissed true.
func (i *Intent) dismiss() tea.Cmd {
	i.result = &intents.IntentResult{
		Data: Result{Dismissed: true},
	}
	return func() tea.Msg { return intents.DismissModalMsg{} }
}

// View renders the event detail view.
//
// Returns:
//   - A string containing the rendered event details with header, metadata,
//     and scroll indicator if content overflows.
//
// Side effects:
//   - None.
func (i *Intent) View() string {
	return i.renderContent()
}

// Result returns the intent result.
//
// Returns:
//   - The IntentResult if the intent was dismissed.
//   - nil if no action has been taken yet.
//
// Side effects:
//   - None.
func (i *Intent) Result() *intents.IntentResult {
	return i.result
}

// ScrollOffset returns the current scroll position for test assertions.
//
// Returns:
//   - An int representing the scroll offset.
//
// Side effects:
//   - None.
func (i *Intent) ScrollOffset() int {
	return i.scrollOffset
}

// renderContent builds the full event detail string.
//
// Returns:
//   - A string with event header, timestamp, agent ID, and sorted metadata.
//
// Side effects:
//   - None.
func (i *Intent) renderContent() string {
	var parts []string

	// Header: event type + status (bold).
	headerStyle := lipgloss.NewStyle().Bold(true)
	header := headerStyle.Render("[" + string(i.event.Type) + "] " + i.event.Status)
	parts = append(parts,
		header,
		"",
		"Timestamp: "+i.event.Timestamp.Format("2006-01-02T15:04:05Z07:00"),
		"Agent: "+i.event.AgentID,
		"ID: "+i.event.ID,
	)

	// Metadata section.
	if len(i.event.Metadata) > 0 {
		parts = append(parts, "", headerStyle.Render("Metadata"), strings.Repeat("\u2500", 20))
		parts = append(parts, i.renderMetadata()...)
	}

	// Footer hint.
	footer := "Esc: close  \u2191/\u2193 j/k: scroll  PgUp/PgDn: page  Home/End: jump"
	parts = append(parts, "", lipgloss.NewStyle().Faint(true).Render(footer))

	return strings.Join(parts, "\n")
}

// renderMetadata builds sorted key-value lines from the event metadata.
//
// For tool events, tool_name and is_error are rendered first (prominently).
// For delegation events, source_agent and description are rendered first.
// For plan/review events, content and verdict are rendered first.
// Remaining keys follow in alphabetical order.
//
// Returns:
//   - A slice of formatted key-value strings.
//
// Side effects:
//   - None.
func (i *Intent) renderMetadata() []string {
	if len(i.event.Metadata) == 0 {
		return nil
	}

	var lines []string
	rendered := make(map[string]bool)

	// Render prominent keys first based on event type.
	prominentKeys := i.prominentKeysForType()
	for _, key := range prominentKeys {
		if val, ok := i.event.Metadata[key]; ok {
			lines = append(lines, formatKeyValue(key, val))
			rendered[key] = true
		}
	}

	// Render remaining keys alphabetically.
	var remaining []string
	for key := range i.event.Metadata {
		if !rendered[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)

	for _, key := range remaining {
		lines = append(lines, formatKeyValue(key, i.event.Metadata[key]))
	}

	return lines
}

// prominentKeysForType returns the metadata keys that should be rendered
// first for the given event type.
//
// Returns:
//   - A slice of key names to display prominently.
//
// Side effects:
//   - None.
func (i *Intent) prominentKeysForType() []string {
	switch i.event.Type {
	case streaming.EventToolCall:
		return []string{"tool_name", "is_error"}
	case streaming.EventDelegation:
		return []string{"source_agent", "description"}
	case streaming.EventPlan:
		return []string{"content"}
	case streaming.EventReview:
		return []string{"verdict"}
	default:
		return nil
	}
}

// formatKeyValue formats a single metadata key-value pair.
//
// Expected:
//   - key is a non-empty string.
//   - val is any value that can be formatted with %v.
//
// Returns:
//   - A formatted string like "  key: value".
//
// Side effects:
//   - None.
func formatKeyValue(key string, val interface{}) string {
	return fmt.Sprintf("  %s: %v", key, val)
}

// contentLineCount returns the total number of lines in the rendered content.
//
// Returns:
//   - An int representing the line count.
//
// Side effects:
//   - None.
func (i *Intent) contentLineCount() int {
	content := i.renderContent()
	return strings.Count(content, "\n") + 1
}

// visibleHeight returns the number of lines visible in the modal viewport.
// Uses 70% of terminal height minus border overhead, matching
// RenderBorderedOverlay sizing.
//
// Returns:
//   - An int representing the visible line count.
//
// Side effects:
//   - None.
func (i *Intent) visibleHeight() int {
	if i.height <= 0 {
		return 20 // sensible default
	}
	// Match RenderBorderedOverlay: 70% of termH, clamped 12..40, minus 4 for border+padding.
	boxH := i.height * 70 / 100
	if boxH < 12 {
		boxH = 12
	}
	if boxH > 40 {
		boxH = 40
	}
	return boxH - 4
}
