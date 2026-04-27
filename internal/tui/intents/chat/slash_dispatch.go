package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/tui/intents/chat/slashcommand"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// slashState tracks the picker visible to the user when they type "/".
//
// Two modes:
//   - command picker: filtered by the user's typed buffer (after the "/").
//   - sub picker: populated by a Command's ItemsForPicker.
//
// The chat intent owns slashState and routes key events through it
// before the normal text-input pipeline.
type slashState struct {
	// activeCommandPicker is non-nil when the picker is showing the
	// filterable command list.
	activeCommandPicker *widgets.Picker
	// activeSubPicker is non-nil when a Command with an ItemsForPicker
	// is awaiting an argument selection.
	activeSubPicker *widgets.Picker
	// pendingCommand is the Command whose sub-picker is open.
	pendingCommand *slashcommand.Command
	// savedInput preserves the chat input buffer so Esc can restore it
	// when the user dismisses the picker.
	savedInput string
}

// SetSlashRegistryForTest swaps the slash registry used by the chat
// intent. Tests use this to isolate from the live builtins set.
//
// Expected:
//   - reg may be nil; nil disables slash dispatch entirely.
//
// Side effects:
//   - Replaces i.slashRegistry.
func (i *Intent) SetSlashRegistryForTest(reg *slashcommand.Registry) {
	i.slashRegistry = reg
}

// SlashRegistryForTest exposes the active registry for assertions.
//
// Returns:
//   - The current slashcommand.Registry, or nil when none is wired.
//
// Side effects:
//   - None.
func (i *Intent) SlashRegistryForTest() *slashcommand.Registry {
	return i.slashRegistry
}

// SlashPickerActiveForTest reports whether the slash picker is visible.
//
// Returns:
//   - true when either the command or sub-picker is open.
//
// Side effects:
//   - None.
func (i *Intent) SlashPickerActiveForTest() bool {
	return i.slashState.activeCommandPicker != nil || i.slashState.activeSubPicker != nil
}

// EnsureDefaultSlashRegistry wires the canonical builtins onto the
// intent if no registry has been configured yet. Invoked from the
// constructor and from SetSlashRegistryForTest helpers.
//
// Side effects:
//   - May mutate i.slashRegistry.
func (i *Intent) EnsureDefaultSlashRegistry() {
	if i.slashRegistry != nil {
		return
	}
	reg := slashcommand.NewRegistry()
	slashcommand.RegisterBuiltins(reg)
	i.slashRegistry = reg
}

// slashCommandContext composes the CommandContext exposed to handlers
// from the chat intent's wired collaborators. nil-safe for partial
// configurations (test harnesses, embedded shells).
//
// Returns:
//   - A populated CommandContext referencing this Intent's collaborators.
//
// Side effects:
//   - None.
func (i *Intent) slashCommandContext() slashcommand.CommandContext {
	return slashcommand.CommandContext{
		MessageWiper:        slashWiper{intent: i},
		SystemMessageWriter: slashWriter{intent: i},
		SessionResumer:      slashResumer{intent: i},
		SessionLister:       i.sessionStore,
		PlanLister:          i.planStore,
		PlanFetcher:         i.planStore,
		AgentRegistry:       i.agentRegistry,
		AgentSwitcher:       i.engine,
		ProviderLister:      i.app,
		ModelSwitcher:       i.engine,
		Registry:            i.slashRegistry,
	}
}

// inputStartsSlash reports whether the live input buffer begins with
// "/" — used by the slash dispatcher to gate picker behaviour.
//
// Returns:
//   - true when the input buffer's first rune is "/".
//
// Side effects:
//   - None.
func (i *Intent) inputStartsSlash() bool {
	return strings.HasPrefix(i.input, "/")
}

// handleSlashKey routes a key event through the active picker, or
// opens a fresh command picker when the user has just typed "/".
//
// Expected:
//   - msg is a tea.KeyMsg from the chat intent's main key dispatcher.
//   - slashTriggered already returned true for this msg.
//
// Returns:
//   - (cmd, true) when consumed; (nil, false) when the picker yields
//     to the legacy text-input pipeline.
//
// Side effects:
//   - May mutate i.input and i.slashState.
func (i *Intent) handleSlashKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if i.slashRegistry == nil {
		return nil, false
	}
	if i.slashState.activeSubPicker != nil {
		return i.routeSubPickerKey(msg)
	}
	if i.slashState.activeCommandPicker != nil {
		return i.routeCommandPickerKey(msg)
	}
	if i.inputStartsSlash() {
		return i.openCommandPicker(msg)
	}
	return nil, false
}

// openCommandPicker initialises the command-picker state on the first
// "/" keystroke and lets the active picker absorb the same key when
// applicable.
//
// Expected:
//   - msg is the key event that triggered the picker (typically the
//     keystroke that turned input into "/...").
//
// Returns:
//   - (nil, true) when the picker opens. The caller must keep input as-is
//     so the user can continue typing.
//
// Side effects:
//   - Allocates a new picker, copies input into slashState.savedInput.
func (i *Intent) openCommandPicker(_ tea.KeyMsg) (tea.Cmd, bool) {
	prefix := strings.TrimPrefix(i.input, "/")
	picker := widgets.NewPicker(i.slashRegistry.Items())
	picker.SetFilter(prefix)
	i.slashState.activeCommandPicker = picker
	i.slashState.savedInput = i.input
	return nil, true
}

// routeCommandPickerKey forwards a key event to the command picker and
// updates the picker filter from the chat input buffer when the user
// types or backspaces. When the user types a space the picker quietly
// dismisses so legacy "/cmd arg arg" inputs continue to flow through
// sendMessage's existing slash-command dispatcher.
//
// Expected:
//   - msg is a tea.KeyMsg.
//
// Returns:
//   - (cmd, true) when the slash surface consumed the event.
//   - (nil, false) when the picker dismissed and the caller should
//     re-process the event through the standard input pipeline.
//
// Side effects:
//   - May close the picker or open a sub-picker.
func (i *Intent) routeCommandPickerKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	picker := i.slashState.activeCommandPicker
	switch msg.Type {
	case tea.KeySpace:
		i.input += " "
		i.dismissSlashPicker(false)
		return nil, true
	case tea.KeyRunes:
		i.input += string(msg.Runes)
		picker.SetFilter(strings.TrimPrefix(i.input, "/"))
		return nil, true
	case tea.KeyBackspace:
		if i.input != "" {
			i.input = i.input[:len(i.input)-1]
		}
		if !strings.HasPrefix(i.input, "/") {
			i.dismissSlashPicker(false)
			return nil, true
		}
		picker.SetFilter(strings.TrimPrefix(i.input, "/"))
		return nil, true
	case tea.KeyEnter:
		return i.confirmCommandPicker()
	}
	cmd, event := picker.Update(msg)
	switch event.Type {
	case widgets.EventCancel:
		i.dismissSlashPicker(true)
		return cmd, true
	case widgets.EventSelect:
		return i.runSelectedCommand(event.Item)
	}
	return cmd, true
}

// legacySlashCommandNames lists the slash commands implemented by the
// pre-existing handleSlashCommand dispatcher in sendMessage. The new
// picker yields to the legacy pipeline when the user types one of
// these names exactly so the existing test contracts (rich /help text,
// /agents listing, /agent <id> switching, /models, /model
// <provider>/<model>) survive.
//
// Adding a slash command to handleSlashCommand requires extending this
// set so the picker continues to defer.
var legacySlashCommandNames = map[string]struct{}{
	"models": {},
	"model":  {},
	"agent":  {},
	"agents": {},
	"help":   {},
}

// confirmCommandPicker handles Enter inside the command picker. When
// the user typed an exact legacy slash-command name the picker yields
// to sendMessage's pre-existing dispatcher; otherwise the picker
// invokes its own handler for the highlighted match.
//
// Returns:
//   - (cmd, true) when consumed by the slash surface; (nil, false)
//     when the slash surface defers to the legacy pipeline.
//
// Side effects:
//   - Resets the picker on consumption.
func (i *Intent) confirmCommandPicker() (tea.Cmd, bool) {
	typed := strings.TrimPrefix(i.input, "/")
	if _, isLegacy := legacySlashCommandNames[strings.ToLower(typed)]; isLegacy {
		i.dismissSlashPicker(false)
		return nil, false
	}
	picker := i.slashState.activeCommandPicker
	selected := picker.Selected()
	if selected == nil {
		i.dismissSlashPicker(false)
		return nil, false
	}
	return i.runSelectedCommand(*selected)
}

// routeSubPickerKey forwards a key event to the sub picker and dispatches
// the parent command's handler when the user confirms.
//
// Expected:
//   - msg is a tea.KeyMsg.
//
// Returns:
//   - (cmd, true) reflecting the sub-picker's outcome.
//
// Side effects:
//   - May close the picker and run a command handler.
func (i *Intent) routeSubPickerKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	picker := i.slashState.activeSubPicker
	cmd, event := picker.Update(msg)
	switch event.Type {
	case widgets.EventCancel:
		i.dismissSlashPicker(true)
		return cmd, true
	case widgets.EventSelect:
		pending := i.slashState.pendingCommand
		i.dismissSlashPicker(true)
		if pending == nil {
			return cmd, true
		}
		ctx := i.slashCommandContext()
		handlerCmd := pending.Handler(ctx, &event.Item)
		return mergeSlashCmds(cmd, handlerCmd), true
	}
	return cmd, true
}

// runSelectedCommand dispatches a command picker selection — either
// opening a sub-picker for the argument or invoking the handler
// directly.
//
// Expected:
//   - item.Value is a slashcommand.Command.
//
// Returns:
//   - (cmd, true) reflecting the dispatch outcome.
//
// Side effects:
//   - May open a sub-picker or run a command handler.
func (i *Intent) runSelectedCommand(item widgets.Item) (tea.Cmd, bool) {
	cmd, ok := item.Value.(slashcommand.Command)
	if !ok {
		i.dismissSlashPicker(true)
		return nil, true
	}
	if cmd.ItemsForPicker == nil {
		i.dismissSlashPicker(true)
		return cmd.Handler(i.slashCommandContext(), nil), true
	}
	items := cmd.ItemsForPicker(i.slashCommandContext())
	if len(items) == 0 {
		i.dismissSlashPicker(true)
		return cmd.Handler(i.slashCommandContext(), nil), true
	}
	i.openSubPicker(cmd, items)
	return nil, true
}

// openSubPicker swaps the visible picker for a fresh one populated by
// the given items.
//
// Expected:
//   - cmd is the parent command.
//   - items is non-empty.
//
// Side effects:
//   - Resets the chat input buffer and replaces slashState.
func (i *Intent) openSubPicker(cmd slashcommand.Command, items []widgets.Item) {
	picker := widgets.NewPicker(items)
	cmdCopy := cmd
	i.slashState.activeCommandPicker = nil
	i.slashState.activeSubPicker = picker
	i.slashState.pendingCommand = &cmdCopy
	i.input = ""
}

// dismissSlashPicker closes the picker. When restoreInput is false the
// chat input buffer is left untouched so the user keeps whatever they
// have typed; when true the saved buffer is restored.
//
// Expected:
//   - i.slashState may already be empty (no-op).
//
// Side effects:
//   - Mutates i.input when restoreInput is true; resets slashState.
func (i *Intent) dismissSlashPicker(restoreInput bool) {
	if restoreInput {
		i.input = ""
	}
	i.slashState = slashState{}
}

// mergeSlashCmds returns a tea.Cmd combining the picker's residual cmd
// (currently always nil but reserved by the API) with the handler's cmd.
//
// Expected:
//   - Both arguments may be nil.
//
// Returns:
//   - A tea.Cmd whose execution runs both inputs in registration order;
//     nil when both are nil.
//
// Side effects:
//   - None.
func mergeSlashCmds(picker, handler tea.Cmd) tea.Cmd {
	switch {
	case picker == nil && handler == nil:
		return nil
	case picker == nil:
		return handler
	case handler == nil:
		return picker
	}
	return tea.Batch(picker, handler)
}

// slashWiper adapts the chat view to the MessageWiper interface.
type slashWiper struct {
	intent *Intent
}

// ClearMessages forwards to the underlying chat view.
//
// Side effects:
//   - Wipes the chat view's message buffer.
func (s slashWiper) ClearMessages() {
	if s.intent == nil || s.intent.view == nil {
		return
	}
	s.intent.view.ClearMessages()
	s.intent.refreshViewport()
}

// slashWriter adapts the chat view to the SystemMessageWriter
// interface.
type slashWriter struct {
	intent *Intent
}

// AddSystemMessage appends a system-role message to the chat view.
//
// Side effects:
//   - Mutates the chat view's message slice.
func (s slashWriter) AddSystemMessage(content string) {
	if s.intent == nil || s.intent.view == nil {
		return
	}
	s.intent.view.AddMessage(chatview.Message{Role: "system", Content: content})
	s.intent.refreshViewport()
}

// slashResumer adapts the chat intent's session-switch path to the
// SessionResumer interface consumed by /sessions.
type slashResumer struct {
	intent *Intent
}

// ResumeSession invokes the chat intent's existing switchToSession
// helper, equivalent to selecting a session from the session browser
// modal.
//
// Side effects:
//   - Triggers the same async session-load chain as the modal browser.
func (s slashResumer) ResumeSession(sessionID string) {
	if s.intent == nil {
		return
	}
	cmd := s.intent.switchToSession(sessionID)
	if cmd != nil {
		s.intent.queuedSlashCmds = append(s.intent.queuedSlashCmds, cmd)
	}
}
