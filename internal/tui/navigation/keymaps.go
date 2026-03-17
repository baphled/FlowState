// Package navigation provides standardized keyboard navigation for the FlowState TUI.
package navigation

import (
	"github.com/charmbracelet/bubbles/key"
)

// GlobalKeyMap defines keyboard shortcuts available on all screens.
// These are the application-wide shortcuts that should work consistently
// regardless of which intent or screen is currently active.
//
// Shortcuts:
//   - q/ctrl+c: Quit application
//   - ?: Show context-sensitive help
//   - esc: Go back / Cancel
type GlobalKeyMap struct {
	Quit key.Binding
	Help key.Binding
	Back key.Binding
}

// DefaultGlobalKeyMap returns the standard global key bindings.
//
// Returns:
//   - A GlobalKeyMap value.
//
// Side effects:
//   - None.
func DefaultGlobalKeyMap() GlobalKeyMap {
	return GlobalKeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q/ctrl+c", "quit"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back"),
		),
	}
}

// ShortHelp implements help.KeyMap interface.
//
// Returns:
//   - A []key.Binding value.
//
// Side effects:
//   - None.
func (k GlobalKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit, k.Help, k.Back}
}

// FullHelp implements help.KeyMap interface.
//
// Returns:
//   - A [][]key.Binding value.
//
// Side effects:
//   - None.
func (k GlobalKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Quit, k.Help, k.Back},
	}
}

// ListKeyMap defines keyboard shortcuts for list navigation.
// These are used consistently across all list-based views.
//
// Shortcuts:
//   - up/k: Move up
//   - down/j: Move down
//   - enter: Select item
//   - a: Add item
//   - d: Delete item
//   - e: Edit item
//   - f: Filter
//   - /: Search
//   - pgup/ctrl+u: Page up
//   - pgdown/ctrl+d: Page down
//   - home/g: Go to start
//   - end/G: Go to end
type ListKeyMap struct {
	Up        key.Binding
	Down      key.Binding
	Select    key.Binding
	Add       key.Binding
	Delete    key.Binding
	Edit      key.Binding
	Filter    key.Binding
	Search    key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	GoToStart key.Binding
	GoToEnd   key.Binding
}

// DefaultListKeyMap returns the standard list navigation key bindings.
//
// Returns:
//   - A ListKeyMap value.
//
// Side effects:
//   - None.
func DefaultListKeyMap() ListKeyMap {
	return ListKeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("up/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("down/j", "down"),
		),
		Select: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "select"),
		),
		Add: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "add"),
		),
		Delete: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "delete"),
		),
		Edit: key.NewBinding(
			key.WithKeys("e"),
			key.WithHelp("e", "edit"),
		),
		Filter: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "filter"),
		),
		Search: key.NewBinding(
			key.WithKeys("/"),
			key.WithHelp("/", "search"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup", "ctrl+u"),
			key.WithHelp("pgup", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "ctrl+d"),
			key.WithHelp("pgdown", "page down"),
		),
		GoToStart: key.NewBinding(
			key.WithKeys("home", "g"),
			key.WithHelp("home/g", "go to start"),
		),
		GoToEnd: key.NewBinding(
			key.WithKeys("end", "G"),
			key.WithHelp("end/G", "go to end"),
		),
	}
}

// ShortHelp implements help.KeyMap interface.
//
// Returns:
//   - A []key.Binding value.
//
// Side effects:
//   - None.
func (k ListKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Select, k.Edit}
}

// FullHelp implements help.KeyMap interface.
//
// Returns:
//   - A [][]key.Binding value.
//
// Side effects:
//   - None.
func (k ListKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.GoToStart, k.GoToEnd},
		{k.Select, k.Add, k.Edit, k.Delete, k.Filter, k.Search},
	}
}

// FormKeyMap defines keyboard shortcuts for form navigation.
// These are used consistently across all form-based views.
//
// Shortcuts:
//   - tab: Next field
//   - shift+tab: Previous field
//   - enter: Submit form
//   - esc: Cancel form
//   - space: Toggle checkbox
//   - ctrl+o: Toggle optional fields
type FormKeyMap struct {
	NextField      key.Binding
	PrevField      key.Binding
	Submit         key.Binding
	Cancel         key.Binding
	Toggle         key.Binding
	ToggleOptional key.Binding
}

// DefaultFormKeyMap returns the standard form navigation key bindings.
//
// Returns:
//   - A FormKeyMap value.
//
// Side effects:
//   - None.
func DefaultFormKeyMap() FormKeyMap {
	return FormKeyMap{
		NextField: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "next field"),
		),
		PrevField: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "prev field"),
		),
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "submit"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
		Toggle: key.NewBinding(
			key.WithKeys(" "),
			key.WithHelp("space", "toggle"),
		),
		ToggleOptional: key.NewBinding(
			key.WithKeys("ctrl+o"),
			key.WithHelp("ctrl+o", "toggle optional"),
		),
	}
}

// ShortHelp implements help.KeyMap interface.
//
// Returns:
//   - A []key.Binding value.
//
// Side effects:
//   - None.
func (k FormKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.NextField, k.PrevField, k.Submit, k.Cancel}
}

// FullHelp implements help.KeyMap interface.
//
// Returns:
//   - A [][]key.Binding value.
//
// Side effects:
//   - None.
func (k FormKeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.NextField, k.PrevField, k.Submit, k.Cancel, k.Toggle, k.ToggleOptional},
	}
}

// CombinedKeyMap combines multiple keymaps for use in screens that need
// both global and context-specific bindings.
type CombinedKeyMap struct {
	Global GlobalKeyMap
	List   ListKeyMap
	Form   FormKeyMap
}

// NewCombinedKeyMap creates a new combined keymap with global and list bindings.
//
// Expected:
//   - global must be a valid GlobalKeyMap.
//   - list must be a valid ListKeyMap.
//
// Returns:
//   - A CombinedKeyMap value.
//
// Side effects:
//   - None.
func NewCombinedKeyMap(global GlobalKeyMap, list ListKeyMap) CombinedKeyMap {
	return CombinedKeyMap{
		Global: global,
		List:   list,
	}
}

// NewCombinedFormKeyMap creates a new combined keymap with global and form bindings.
//
// Expected:
//   - global must be a valid GlobalKeyMap.
//   - form must be a valid FormKeyMap.
//
// Returns:
//   - A CombinedKeyMap value.
//
// Side effects:
//   - None.
func NewCombinedFormKeyMap(global GlobalKeyMap, form FormKeyMap) CombinedKeyMap {
	return CombinedKeyMap{
		Global: global,
		Form:   form,
	}
}

// ShortHelp implements help.KeyMap interface.
//
// Returns:
//   - A []key.Binding value.
//
// Side effects:
//   - None.
func (k CombinedKeyMap) ShortHelp() []key.Binding {
	result := k.Global.ShortHelp()
	if k.List.Up.Enabled() {
		result = append(result, k.List.ShortHelp()...)
	}
	if k.Form.NextField.Enabled() {
		result = append(result, k.Form.ShortHelp()...)
	}
	return result
}

// FullHelp implements help.KeyMap interface.
//
// Returns:
//   - A [][]key.Binding value.
//
// Side effects:
//   - None.
func (k CombinedKeyMap) FullHelp() [][]key.Binding {
	result := k.Global.FullHelp()
	if k.List.Up.Enabled() {
		result = append(result, k.List.FullHelp()...)
	}
	if k.Form.NextField.Enabled() {
		result = append(result, k.Form.FullHelp()...)
	}
	return result
}
