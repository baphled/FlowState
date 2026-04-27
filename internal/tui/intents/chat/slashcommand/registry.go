package slashcommand

import (
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// Handler executes a slash command. When the Command exposes a
// non-nil ItemsForPicker, arg is the widgets.Item the user chose in
// the sub-picker; otherwise arg is nil.
type Handler func(ctx CommandContext, arg *widgets.Item) tea.Cmd

// Command is one registered slash command surfaced to the user.
type Command struct {
	// Name is the command label without the leading "/" (e.g. "clear").
	Name string
	// Description is the short gloss shown beside Name in the popover.
	Description string
	// Handler runs when the user confirms the command (with or without
	// a sub-picker argument).
	Handler Handler
	// ItemsForPicker, when non-nil, is invoked to populate a follow-up
	// sub-picker. Returning a nil or empty slice short-circuits to a
	// direct Handler invocation.
	ItemsForPicker func(ctx CommandContext) []widgets.Item
	// OpenWizard, when non-nil, is invoked instead of ItemsForPicker /
	// Handler to drive a multi-step builder flow. The chat intent
	// advances the returned Wizard through its steps, swapping the
	// active sub-picker between text-input prompts and pickers as the
	// wizard requests. Mutually exclusive with ItemsForPicker — when
	// both are set, OpenWizard takes precedence.
	OpenWizard func(ctx CommandContext) Wizard
}

// Registry holds the registered commands. Concurrency-safe so future
// plugins can add commands at runtime without coordinating locks at the
// call site.
type Registry struct {
	mu       sync.RWMutex
	commands []Command
}

// NewRegistry constructs an empty Registry.
//
// Returns:
//   - A Registry ready for Register calls.
//
// Side effects:
//   - None.
func NewRegistry() *Registry {
	return &Registry{}
}

// Register adds a command to the registry. Registering the same Name
// twice replaces the earlier entry so re-registration during reloads
// behaves predictably.
//
// Expected:
//   - cmd.Name is non-empty.
//
// Side effects:
//   - Mutates the registry under its mutex.
func (r *Registry) Register(cmd Command) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for idx := range r.commands {
		if r.commands[idx].Name == cmd.Name {
			r.commands[idx] = cmd
			return
		}
	}
	r.commands = append(r.commands, cmd)
}

// All returns a snapshot of every registered command.
//
// Returns:
//   - A defensive copy of the registry's slice.
//
// Side effects:
//   - None.
func (r *Registry) All() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Command, len(r.commands))
	copy(out, r.commands)
	return out
}

// Filter returns commands whose Name starts with prefix
// (case-insensitive). An empty prefix returns every command.
//
// Expected:
//   - prefix is the user's typed string after the leading "/" (may be
//     empty).
//
// Returns:
//   - A defensive copy of the matching commands in registration order.
//
// Side effects:
//   - None.
func (r *Registry) Filter(prefix string) []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if prefix == "" {
		out := make([]Command, len(r.commands))
		copy(out, r.commands)
		return out
	}
	needle := strings.ToLower(prefix)
	var out []Command
	for _, cmd := range r.commands {
		if strings.HasPrefix(strings.ToLower(cmd.Name), needle) {
			out = append(out, cmd)
		}
	}
	return out
}

// Lookup returns the command with the given exact Name (case-insensitive).
//
// Expected:
//   - name is the command's Name without the leading "/".
//
// Returns:
//   - A pointer to the matching Command, or nil when no match exists.
//
// Side effects:
//   - None.
func (r *Registry) Lookup(name string) *Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	target := strings.ToLower(name)
	for idx := range r.commands {
		if strings.ToLower(r.commands[idx].Name) == target {
			cmd := r.commands[idx]
			return &cmd
		}
	}
	return nil
}

// Items returns every registered command rendered as widgets.Item values
// keyed on the Command itself so handlers can recover the source command
// from a sub-picker selection.
//
// Returns:
//   - A slice of Items in registration order.
//
// Side effects:
//   - None.
func (r *Registry) Items() []widgets.Item {
	cmds := r.All()
	out := make([]widgets.Item, len(cmds))
	for idx, cmd := range cmds {
		out[idx] = widgets.Item{
			Label:       "/" + cmd.Name,
			Description: cmd.Description,
			Value:       cmd,
		}
	}
	return out
}

// FilterItems is the picker-shaped version of Filter — it returns Items
// for commands whose Name starts with prefix.
//
// Expected:
//   - prefix matches the buffer the user has typed after the leading "/".
//
// Returns:
//   - A slice of Items for matching commands.
//
// Side effects:
//   - None.
func (r *Registry) FilterItems(prefix string) []widgets.Item {
	cmds := r.Filter(prefix)
	out := make([]widgets.Item, len(cmds))
	for idx, cmd := range cmds {
		out[idx] = widgets.Item{
			Label:       "/" + cmd.Name,
			Description: cmd.Description,
			Value:       cmd,
		}
	}
	return out
}
