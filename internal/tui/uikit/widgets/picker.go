// Package widgets — picker.go houses the generic filterable popover widget
// used to host slash commands today and future agent/swarm/model selectors.
//
// The Picker is intentionally free of any slash-command-specific knowledge:
// callers seed it with a list of Items (label + description + opaque
// payload), feed key messages through Update, and receive a PickerEvent
// describing the user's intent (select an item, cancel, or no-op). Filter
// state, cursor position, and viewport offset are all pure state on the
// widget — no I/O, no commands, no theme baked in. Style hooks let callers
// theme the popover without coupling to a particular palette.
package widgets

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Item is anything the picker can display and return on selection.
//
// Label is the primary text (e.g. "/clear"); Description is the secondary
// gloss shown alongside it; Value is the opaque payload the caller uses
// to recognise which Item the user chose. Picker never inspects Value.
type Item struct {
	// Label is the primary text shown in the popover.
	Label string
	// Description is the secondary gloss displayed beside Label.
	Description string
	// Value is the opaque payload returned to the caller on selection.
	Value any
}

// PickerStyle holds the lipgloss styles used by the picker's renderer.
//
// All fields are optional — zero-value styles fall back to lipgloss
// defaults so callers can wire a Picker without a theme during early
// development. Callers with a theme should populate every field via
// DefaultPickerStyle(theme.Theme) so the popover blends with the rest
// of the TUI.
type PickerStyle struct {
	// Container wraps the entire popover (border, padding, background).
	Container lipgloss.Style
	// Item styles a single non-selected row.
	Item lipgloss.Style
	// SelectedItem styles the row under the cursor.
	SelectedItem lipgloss.Style
	// Description styles the secondary description text alongside the label.
	Description lipgloss.Style
	// Empty styles the "no matches" placeholder line.
	Empty lipgloss.Style
}

// PickerEventType discriminates the variants of PickerEvent.
type PickerEventType int

const (
	// EventNone means Update consumed the message without producing a
	// user-facing outcome — the caller should keep the picker open.
	EventNone PickerEventType = iota
	// EventSelect means the user confirmed the cursor's Item via Tab or
	// Enter. The Item field on the PickerEvent carries the selected Item.
	EventSelect
	// EventCancel means the user dismissed the picker via Esc.
	EventCancel
)

// PickerEvent is the one-of result returned alongside any tea.Cmd from
// Picker.Update. Callers switch on Type and read Item only when Type ==
// EventSelect.
type PickerEvent struct {
	// Type is one of EventNone, EventSelect, EventCancel.
	Type PickerEventType
	// Item is set only when Type == EventSelect.
	Item Item
}

// defaultMaxVisible is the row count the popover shows when the caller
// has not explicitly set MaxVisible. It mirrors the providersetup
// default-ish viewport size and stays small enough to avoid swamping
// short terminals.
const defaultMaxVisible = 8

// Picker is a filterable list popover. The widget is render-pure: callers
// own the canonical Items slice, feed the picker the current filter, and
// the picker's only state changes happen in Update (cursor + offset).
type Picker struct {
	items      []Item
	filter     string
	cursor     int
	maxVisible int
	offset     int
	width      int
	style      PickerStyle
}

// NewPicker constructs a Picker over the given items.
//
// Expected:
//   - items is the unfiltered list. Empty is allowed; the View renders
//     an "empty" placeholder.
//
// Returns:
//   - A ready-to-use Picker with cursor at 0 and a default viewport size.
//
// Side effects:
//   - None.
func NewPicker(items []Item) *Picker {
	return &Picker{
		items:      items,
		filter:     "",
		cursor:     0,
		maxVisible: defaultMaxVisible,
		offset:     0,
		width:      0,
		style:      PickerStyle{},
	}
}

// SetItems replaces the unfiltered item slice and resets cursor/offset.
//
// Expected:
//   - items is the new canonical list.
//
// Side effects:
//   - Resets cursor to 0 and offset to 0.
func (p *Picker) SetItems(items []Item) {
	p.items = items
	p.cursor = 0
	p.offset = 0
}

// SetFilter replaces the case-insensitive substring filter.
//
// Expected:
//   - filter may be empty (matches everything).
//
// Side effects:
//   - Clamps cursor to the new filtered length and adjusts offset.
func (p *Picker) SetFilter(filter string) {
	p.filter = filter
	p.clampCursor()
	p.adjustOffset()
}

// Filter returns the current filter substring.
//
// Returns:
//   - The active filter string (empty when none).
//
// Side effects:
//   - None.
func (p *Picker) Filter() string {
	return p.filter
}

// SetMaxVisible sets the viewport's visible-row budget.
//
// Expected:
//   - n is at least 1; values below 1 are coerced to 1.
//
// Side effects:
//   - May adjust offset so cursor remains visible.
func (p *Picker) SetMaxVisible(n int) {
	if n < 1 {
		n = 1
	}
	p.maxVisible = n
	p.adjustOffset()
}

// SetWidth sets the popover render width.
//
// Expected:
//   - w is the available column count; zero leaves rendering unbounded.
//
// Side effects:
//   - None directly; affects future View output.
func (p *Picker) SetWidth(w int) {
	p.width = w
}

// SetStyle replaces the picker's render styles.
//
// Expected:
//   - style is a fully or partially populated PickerStyle.
//
// Side effects:
//   - None directly; affects future View output.
func (p *Picker) SetStyle(style PickerStyle) {
	p.style = style
}

// Cursor returns the cursor's current index into the filtered slice.
//
// Returns:
//   - The zero-based cursor position; 0 when no items match the filter.
//
// Side effects:
//   - None.
func (p *Picker) Cursor() int {
	return p.cursor
}

// Offset returns the current viewport offset (test-only inspection).
//
// Returns:
//   - The zero-based index of the first visible row in the filtered slice.
//
// Side effects:
//   - None.
func (p *Picker) Offset() int {
	return p.offset
}

// Filtered returns the items matching the current filter, in their
// original order. Matching is case-insensitive substring on Label.
//
// Returns:
//   - A fresh slice containing only the matched items.
//
// Side effects:
//   - None.
func (p *Picker) Filtered() []Item {
	if p.filter == "" {
		return append([]Item(nil), p.items...)
	}
	needle := strings.ToLower(p.filter)
	var out []Item
	for _, item := range p.items {
		if strings.Contains(strings.ToLower(item.Label), needle) {
			out = append(out, item)
		}
	}
	return out
}

// Selected returns the Item currently under the cursor, or nil when no
// items match the filter.
//
// Returns:
//   - A pointer to the cursor's Item, or nil when the filtered slice is
//     empty.
//
// Side effects:
//   - None.
func (p *Picker) Selected() *Item {
	filtered := p.Filtered()
	if len(filtered) == 0 {
		return nil
	}
	if p.cursor < 0 || p.cursor >= len(filtered) {
		return nil
	}
	chosen := filtered[p.cursor]
	return &chosen
}

// Update consumes a tea.Msg and returns a (cmd, event) pair. Recognised
// keys: Up, Down, Tab, Enter, Esc. Unrecognised messages produce
// EventNone.
//
// Expected:
//   - msg is any tea.Msg; only tea.KeyMsg values trigger state changes.
//
// Returns:
//   - A tea.Cmd (always nil today; reserved so callers can adopt
//     Bubble Tea command emission later without an API break) and the
//     PickerEvent describing the user's intent.
//
// Side effects:
//   - May mutate cursor and offset.
func (p *Picker) Update(msg tea.Msg) (tea.Cmd, PickerEvent) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil, PickerEvent{Type: EventNone}
	}
	return p.handleKey(keyMsg)
}

// handleKey dispatches recognised keys and returns the resulting event.
//
// Expected:
//   - msg is a tea.KeyMsg from the Bubble Tea event loop.
//
// Returns:
//   - (nil, EventNone | EventSelect | EventCancel).
//
// Side effects:
//   - May mutate cursor and offset.
func (p *Picker) handleKey(msg tea.KeyMsg) (tea.Cmd, PickerEvent) {
	switch msg.Type {
	case tea.KeyUp:
		p.moveCursor(-1)
		return nil, PickerEvent{Type: EventNone}
	case tea.KeyDown:
		p.moveCursor(1)
		return nil, PickerEvent{Type: EventNone}
	case tea.KeyTab, tea.KeyEnter:
		return p.selectionEvent()
	case tea.KeyEsc:
		return nil, PickerEvent{Type: EventCancel}
	}
	return nil, PickerEvent{Type: EventNone}
}

// selectionEvent returns the EventSelect for the cursor's Item, or
// EventNone when no items match the filter.
//
// Returns:
//   - (nil, EventSelect{Item}) when an item is under the cursor.
//   - (nil, EventNone) otherwise.
//
// Side effects:
//   - None.
func (p *Picker) selectionEvent() (tea.Cmd, PickerEvent) {
	chosen := p.Selected()
	if chosen == nil {
		return nil, PickerEvent{Type: EventNone}
	}
	return nil, PickerEvent{Type: EventSelect, Item: *chosen}
}

// moveCursor shifts the cursor by delta and re-clamps offset.
//
// Expected:
//   - delta is +1 (down) or -1 (up); other values are clamped by the
//     range checks below.
//
// Side effects:
//   - May update cursor and offset.
func (p *Picker) moveCursor(delta int) {
	count := len(p.Filtered())
	if count == 0 {
		p.cursor = 0
		p.offset = 0
		return
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= count {
		p.cursor = count - 1
	}
	p.adjustOffset()
}

// clampCursor pulls the cursor back into the new filtered length when
// SetFilter or SetItems shrinks the visible count.
//
// Side effects:
//   - May update cursor.
func (p *Picker) clampCursor() {
	count := len(p.Filtered())
	if count == 0 {
		p.cursor = 0
		return
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= count {
		p.cursor = count - 1
	}
}

// adjustOffset ensures cursor sits inside the viewport window
// [offset, offset+maxVisible) — mirrors the providersetup pattern.
//
// Side effects:
//   - May update offset.
func (p *Picker) adjustOffset() {
	count := len(p.Filtered())
	rows := p.visibleRows()
	if count <= rows {
		p.offset = 0
		return
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
		return
	}
	if p.cursor >= p.offset+rows {
		p.offset = p.cursor - rows + 1
	}
}

// visibleRows reports the viewport row budget, defaulting and flooring
// at 1 so renderers always have at least one row to draw.
//
// Returns:
//   - max(1, maxVisible).
//
// Side effects:
//   - None.
func (p *Picker) visibleRows() int {
	if p.maxVisible < 1 {
		return 1
	}
	return p.maxVisible
}

// View renders the popover. Keep it cheap — Update handlers re-render
// every keypress.
//
// Returns:
//   - The rendered popover string. Empty when there are no items at all.
//
// Side effects:
//   - None.
func (p *Picker) View() string {
	filtered := p.Filtered()
	if len(filtered) == 0 {
		return p.renderEmpty()
	}
	start, end := visibleSlice(len(filtered), p.offset, p.visibleRows())
	var lines []string
	for idx := start; idx < end; idx++ {
		lines = append(lines, p.renderRow(filtered[idx], idx == p.cursor))
	}
	body := strings.Join(lines, "\n")
	return p.style.Container.Render(body)
}

// renderEmpty produces the placeholder line shown when no items match
// the filter.
//
// Returns:
//   - The styled "no matches" line wrapped by the container style.
//
// Side effects:
//   - None.
func (p *Picker) renderEmpty() string {
	msg := "(no matches)"
	if p.style.Empty.GetForeground() != nil {
		msg = p.style.Empty.Render(msg)
	}
	return p.style.Container.Render(msg)
}

// renderRow renders a single popover row with optional selection styling.
//
// Expected:
//   - item is the row's payload.
//   - selected reports whether the cursor sits on this row.
//
// Returns:
//   - The styled row string with Label and Description joined.
//
// Side effects:
//   - None.
func (p *Picker) renderRow(item Item, selected bool) string {
	label := item.Label
	desc := item.Description
	if desc != "" {
		desc = p.style.Description.Render(desc)
		desc = "  " + desc
	}
	row := label + desc
	if selected {
		return p.style.SelectedItem.Render(row)
	}
	return p.style.Item.Render(row)
}

// visibleSlice clamps [offset, offset+rows) into [0, count].
//
// Expected:
//   - count is the filtered length.
//   - offset and rows are non-negative.
//
// Returns:
//   - Clamped (start, end) bounds satisfying 0 <= start <= end <= count.
//
// Side effects:
//   - None.
func visibleSlice(count, offset, rows int) (int, int) {
	if count == 0 {
		return 0, 0
	}
	start := offset
	if start < 0 {
		start = 0
	}
	if start > count {
		start = count
	}
	end := start + rows
	if end > count {
		end = count
	}
	return start, end
}
