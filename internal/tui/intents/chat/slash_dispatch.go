package chat

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tui/intents/chat/slashcommand"
	chatview "github.com/baphled/flowstate/internal/tui/views/chat"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// slashState tracks the picker visible to the user when they type "/".
//
// Three modes:
//   - command picker: filtered by the user's typed buffer (after the "/").
//   - sub picker: populated by a Command's ItemsForPicker.
//   - wizard: a multi-step builder driven by Command.OpenWizard. The
//     wizard renders either a sub-picker (single or multi-select) or a
//     text-input prompt depending on its current step.
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
	// wizard is the active multi-step builder when non-nil. Mutually
	// exclusive with the legacy single-step pickers above on the
	// happy path; the wizard reuses activeSubPicker for its picker
	// steps and the chat input buffer for its text-input steps.
	wizard slashcommand.Wizard
	// wizardPrompt is the prompt label rendered above the wizard's
	// current surface. Cached so the View loop doesn't have to call
	// Current() on every paint.
	wizardPrompt string
	// wizardInputBuffer holds typed characters during a wizard text
	// step, separate from i.input so the wizard can clear it after
	// each submission without disturbing the main chat input.
	wizardInputBuffer string
	// subPickerFilter holds the runes typed against the active
	// sub-picker (e.g. /agent → "pl"). Tracked separately from
	// i.input — which is torn down when the sub-picker opens — so
	// the filter buffer can grow and shrink without the chat input
	// pipeline observing it.
	subPickerFilter string
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
	ctx := slashcommand.CommandContext{
		MessageWiper:        slashWiper{intent: i},
		SystemMessageWriter: slashWriter{intent: i},
		SessionResumer:      slashResumer{intent: i},
		MessageSender:       slashSender{intent: i},
		AgentRegistry:       i.agentRegistry,
		Registry:            i.slashRegistry,
		SwarmsDir:           i.swarmsDir,
		SchemaNames:         swarm.RegisteredSchemaNames(),
	}
	// Guard each interface assignment against typed-nil: a nil-pointer
	// concrete type boxed in an interface value passes a `== nil` check
	// (because the interface's type slot is non-nil), then panics on
	// method dispatch. Assign only when the concrete pointer is non-nil
	// so handlers see a true-nil interface they can guard against.
	if i.sessionStore != nil {
		ctx.SessionLister = i.sessionStore
	}
	if i.planStore != nil {
		ctx.PlanLister = i.planStore
		ctx.PlanFetcher = i.planStore
	}
	if i.engine != nil {
		ctx.AgentSwitcher = i.engine
		ctx.ModelSwitcher = i.engine
	}
	if i.app != nil {
		ctx.ProviderLister = i.app
	}
	return ctx
}

// SetSwarmsDirForTest overrides the swarms-write directory the /swarm
// wizard targets. Tests use this to redirect the wizard at a tmpdir
// without touching the user-config tree.
//
// Expected:
//   - dir is a writable directory; empty disables the wizard's write
//     path entirely.
//
// Side effects:
//   - Mutates i.swarmsDir.
func (i *Intent) SetSwarmsDirForTest(dir string) {
	i.swarmsDir = dir
}

// SlashStateForTest exposes the slash-state struct for test assertions
// against wizard plumbing without leaking the unexported field name.
//
// Returns:
//   - The current slashState.
//
// Side effects:
//   - None.
func (i *Intent) SlashStateForTest() any {
	return i.slashState
}

// WizardActiveForTest reports whether a wizard is currently running.
//
// Returns:
//   - true when slashState.wizard is non-nil.
//
// Side effects:
//   - None.
func (i *Intent) WizardActiveForTest() bool {
	return i.slashState.wizard != nil
}

// SubPickerVisibleLabelsForTest returns the labels currently visible in
// the active sub-picker after applying the live filter buffer. Used by
// the /agent filter-as-you-type spec to pin the rune-routing contract
// without exporting the picker pointer.
//
// Returns:
//   - The Label of every Item in the sub-picker's filtered slice.
//   - nil when no sub-picker is open.
//
// Side effects:
//   - None.
func (i *Intent) SubPickerVisibleLabelsForTest() []string {
	picker := i.slashState.activeSubPicker
	if picker == nil {
		return nil
	}
	filtered := picker.Filtered()
	out := make([]string, len(filtered))
	for idx, item := range filtered {
		out[idx] = item.Label
	}
	return out
}

// SubPickerFilterForTest returns the live sub-picker filter buffer.
// Used by the /agent filter-as-you-type spec to pin that rune events
// land on the dedicated sub-picker buffer rather than the chat input.
//
// Returns:
//   - The current sub-picker filter string (empty when none typed).
//
// Side effects:
//   - None.
func (i *Intent) SubPickerFilterForTest() string {
	return i.slashState.subPickerFilter
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
	if i.slashState.wizard != nil {
		return i.routeWizardKey(msg)
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

// routeWizardKey dispatches a key event to the active wizard. The
// dispatch branch depends on the wizard's current step kind: text
// steps consume the key as input, picker steps forward to the
// sub-picker.
//
// Expected:
//   - msg is a tea.KeyMsg.
//
// Returns:
//   - (cmd, true) reflecting the wizard-step outcome.
//
// Side effects:
//   - May advance the wizard, open a follow-up picker, or finish the
//     wizard.
func (i *Intent) routeWizardKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	wizard := i.slashState.wizard
	step := wizard.Current()
	switch step.Kind {
	case slashcommand.StepInput:
		return i.routeWizardInputKey(msg)
	case slashcommand.StepPicker, slashcommand.StepMultiPicker, slashcommand.StepConfirm:
		return i.routeWizardPickerKey(msg)
	}
	i.finishWizard()
	return nil, true
}

// routeWizardInputKey consumes a key for a wizard text-input step,
// committing the buffer on Enter and rolling back on Esc.
//
// Returns:
//   - (cmd, true).
//
// Side effects:
//   - Mutates the wizard input buffer or advances the wizard.
func (i *Intent) routeWizardInputKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	switch msg.Type {
	case tea.KeyEsc:
		i.slashState.wizard.Cancel()
		i.dismissSlashPicker(true)
		return nil, true
	case tea.KeyBackspace:
		if i.slashState.wizardInputBuffer != "" {
			buf := i.slashState.wizardInputBuffer
			i.slashState.wizardInputBuffer = buf[:len(buf)-1]
		}
		return nil, true
	case tea.KeyEnter:
		return i.submitWizardText()
	case tea.KeySpace:
		i.slashState.wizardInputBuffer += " "
		return nil, true
	case tea.KeyRunes:
		i.slashState.wizardInputBuffer += string(msg.Runes)
		return nil, true
	}
	return nil, true
}

// submitWizardText commits the wizard input buffer to the active
// wizard, surfacing any validation error as a system message.
//
// Returns:
//   - (cmd, true).
//
// Side effects:
//   - Advances the wizard or surfaces an error.
func (i *Intent) submitWizardText() (tea.Cmd, bool) {
	wizard := i.slashState.wizard
	if err := wizard.SubmitText(i.slashState.wizardInputBuffer); err != nil {
		i.viewWriteSystem("swarm builder: " + err.Error())
		return nil, true
	}
	i.applyWizardStep(wizard.Current())
	return nil, true
}

// routeWizardPickerKey consumes a key for a wizard picker step,
// forwarding to the underlying picker and advancing the wizard on
// commit.
//
// Returns:
//   - (cmd, true).
//
// Side effects:
//   - May advance the wizard or roll it back on cancel.
func (i *Intent) routeWizardPickerKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	picker := i.slashState.activeSubPicker
	if picker == nil {
		return nil, true
	}
	cmd, event := picker.Update(msg)
	switch event.Type {
	case widgets.EventCancel:
		i.slashState.wizard.Cancel()
		i.dismissSlashPicker(true)
		return cmd, true
	case widgets.EventSelect:
		return i.submitWizardItem(event.Item, cmd)
	case widgets.EventMultiSelect:
		return i.submitWizardMulti(event.Items, cmd)
	}
	return cmd, true
}

// submitWizardItem advances the wizard with a single picker selection.
//
// Returns:
//   - (mergedCmd, true).
//
// Side effects:
//   - Advances the wizard or surfaces an error.
func (i *Intent) submitWizardItem(item widgets.Item, picker tea.Cmd) (tea.Cmd, bool) {
	wizard := i.slashState.wizard
	if err := wizard.SubmitItem(item); err != nil {
		i.viewWriteSystem("swarm builder: " + err.Error())
		return picker, true
	}
	i.applyWizardStep(wizard.Current())
	return picker, true
}

// submitWizardMulti advances the wizard with a multi-picker commit.
//
// Returns:
//   - (mergedCmd, true).
//
// Side effects:
//   - Advances the wizard or surfaces an error.
func (i *Intent) submitWizardMulti(items []widgets.Item, picker tea.Cmd) (tea.Cmd, bool) {
	wizard := i.slashState.wizard
	if err := wizard.SubmitMulti(items); err != nil {
		i.viewWriteSystem("swarm builder: " + err.Error())
		return picker, true
	}
	i.applyWizardStep(wizard.Current())
	return picker, true
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
		appendFilterRunes(&i.input, picker, msg.Runes, "/")
		return nil, true
	case tea.KeyBackspace:
		popFilterRune(&i.input, picker, "/")
		if !strings.HasPrefix(i.input, "/") {
			i.dismissSlashPicker(false)
		}
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

// appendFilterRunes appends typed runes to a buffer and re-applies the
// trimmed-prefix view to the picker's filter. Shared by the command
// picker (whose buffer is the chat input with a leading "/") and the
// sub-picker (whose buffer is slashState.subPickerFilter, no prefix).
//
// Expected:
//   - buf is a non-nil string pointer the caller owns.
//   - picker is the picker whose SetFilter is to be re-synced.
//   - runes is the slice from a tea.KeyMsg.Runes payload.
//   - trimPrefix is the prefix to strip from the buffer before passing
//     to SetFilter (e.g. "/" for the command picker, "" for the
//     sub-picker).
//
// Side effects:
//   - Mutates *buf and the picker's filter state.
func appendFilterRunes(buf *string, picker *widgets.Picker, runes []rune, trimPrefix string) {
	*buf += string(runes)
	picker.SetFilter(strings.TrimPrefix(*buf, trimPrefix))
}

// popFilterRune trims the last rune off the buffer and re-applies the
// trimmed-prefix view to the picker's filter. The companion to
// appendFilterRunes for the Backspace path.
//
// Expected:
//   - buf is a non-nil string pointer the caller owns; empty buffers
//     are left untouched.
//   - picker is the picker whose SetFilter is to be re-synced.
//   - trimPrefix is the prefix to strip from the buffer before passing
//     to SetFilter.
//
// Side effects:
//   - May mutate *buf and the picker's filter state.
func popFilterRune(buf *string, picker *widgets.Picker, trimPrefix string) {
	if *buf == "" {
		return
	}
	*buf = (*buf)[:len(*buf)-1]
	picker.SetFilter(strings.TrimPrefix(*buf, trimPrefix))
}

// legacySlashCommandNames lists the slash commands still implemented
// by the pre-existing handleSlashCommand dispatcher in sendMessage.
// The new picker yields to the legacy pipeline when the user types
// one of these names exactly so the existing test contracts
// (/models, /model <provider>/<model> inline-argument forms) survive.
//
// /help, /agent, and /agents have been absorbed into the new picker.
// Bare "/agent" or "/agents" + Enter opens the agent sub-picker; the
// inline "/agent <id>" form still flows through the legacy dispatcher
// because the Space keystroke dismisses the picker before the user
// types the id, surrendering the buffer to sendMessage.
//
// TODO(slash-unification): finish absorbing /models and /model into
// picker builtins. /model <p>/<m> with inline arguments needs an
// arg-parser hook on Command before it can retire from this set. When
// the set is empty, delete it and the early-return block in
// confirmCommandPicker.
//
// Adding a slash command to handleSlashCommand requires extending this
// set so the picker continues to defer.
var legacySlashCommandNames = map[string]struct{}{
	"models": {},
	"model":  {},
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
// the parent command's handler when the user confirms. Rune and Backspace
// keys are absorbed into slashState.subPickerFilter so the sub-picker
// narrows live as the user types (e.g. /agent → "p" leaves only Planner).
//
// Expected:
//   - msg is a tea.KeyMsg.
//
// Returns:
//   - (cmd, true) reflecting the sub-picker's outcome.
//
// Side effects:
//   - May close the picker, mutate slashState.subPickerFilter, or run a
//     command handler.
func (i *Intent) routeSubPickerKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	picker := i.slashState.activeSubPicker
	switch msg.Type {
	case tea.KeyRunes:
		appendFilterRunes(&i.slashState.subPickerFilter, picker, msg.Runes, "")
		return nil, true
	case tea.KeyBackspace:
		popFilterRune(&i.slashState.subPickerFilter, picker, "")
		return nil, true
	}
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
// opening a wizard, opening a sub-picker for the argument, or invoking
// the handler directly.
//
// Expected:
//   - item.Value is a slashcommand.Command.
//
// Returns:
//   - (cmd, true) reflecting the dispatch outcome.
//
// Side effects:
//   - May open a wizard, sub-picker, or run a command handler.
func (i *Intent) runSelectedCommand(item widgets.Item) (tea.Cmd, bool) {
	cmd, ok := item.Value.(slashcommand.Command)
	if !ok {
		i.dismissSlashPicker(true)
		return nil, true
	}
	if cmd.OpenWizard != nil {
		return i.openWizard(cmd)
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

// openWizard activates the wizard returned by cmd.OpenWizard and
// renders its first step.
//
// Expected:
//   - cmd.OpenWizard is non-nil.
//
// Returns:
//   - (nil, true) — the wizard owns the slash surface from this point
//     until SubmitMulti / Cancel returns the wizard to StepDone.
//
// Side effects:
//   - Replaces slashState with the wizard variant and may open a
//     fresh sub-picker.
func (i *Intent) openWizard(cmd slashcommand.Command) (tea.Cmd, bool) {
	wizard := cmd.OpenWizard(i.slashCommandContext())
	if wizard == nil {
		i.dismissSlashPicker(true)
		return nil, true
	}
	i.slashState = slashState{wizard: wizard}
	i.input = ""
	i.applyWizardStep(wizard.Current())
	return nil, true
}

// applyWizardStep configures the slash surface for a fresh WizardStep,
// rendering pickers for picker variants and clearing the input buffer
// for input variants. Pulled out so each Submit* path can reuse the
// step-application pipeline without duplicating picker construction.
//
// Expected:
//   - step is the wizard's current step.
//
// Side effects:
//   - May open a sub-picker, render a system message, or clear the
//     wizard input buffer.
func (i *Intent) applyWizardStep(step slashcommand.WizardStep) {
	i.slashState.wizardPrompt = step.Prompt
	i.slashState.wizardInputBuffer = ""
	switch step.Kind {
	case slashcommand.StepInput:
		i.slashState.activeSubPicker = nil
	case slashcommand.StepPicker:
		i.slashState.activeSubPicker = widgets.NewPicker(step.Items)
	case slashcommand.StepMultiPicker:
		i.slashState.activeSubPicker = widgets.NewPicker(step.Items, widgets.WithMultiSelect())
	case slashcommand.StepConfirm:
		if step.PreviewMessage != "" {
			i.viewWriteSystem(step.PreviewMessage)
		}
		i.slashState.activeSubPicker = widgets.NewPicker(step.Items)
	case slashcommand.StepDone:
		i.finishWizard()
	}
}

// finishWizard tears down the wizard's slash state, surfacing the
// completion message as a system message when the wizard provides one.
//
// Side effects:
//   - Resets slashState; may write a system message.
func (i *Intent) finishWizard() {
	if i.slashState.wizard == nil {
		return
	}
	if msg := i.slashState.wizard.CompleteMessage(); msg != "" {
		i.viewWriteSystem(msg)
	}
	i.slashState = slashState{}
}

// viewWriteSystem proxies a system-message write through the chat
// view, mirroring the slashWriter capability so wizard surfaces don't
// have to round-trip through CommandContext.
//
// Side effects:
//   - Mutates the chat view's message slice.
func (i *Intent) viewWriteSystem(content string) {
	slashWriter{intent: i}.AddSystemMessage(content)
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

// slashSender adapts the chat intent's user-message submission path
// to the slashcommand.MessageSender interface so commands like /plans
// can inject a natural-language prompt and let the active agent answer
// via its existing tools (plan_list, etc.) rather than via a local
// picker. Mirrors slashResumer's queueing pattern so the resulting
// tea.Cmd flushes alongside other slash dispatch output.
type slashSender struct {
	intent *Intent
}

// SendUserMessage seeds the chat input with text and triggers the
// same submission path the user's Enter keypress uses. The resulting
// tea.Cmd is queued onto the intent's slash-cmd buffer so the next
// Update tick flushes it.
//
// Side effects:
//   - Mutates intent.input briefly (then sendMessage clears it).
//   - Appends to intent.queuedSlashCmds.
func (s slashSender) SendUserMessage(text string) {
	if s.intent == nil || strings.TrimSpace(text) == "" {
		return
	}
	s.intent.input = text
	cmd := s.intent.sendMessage()
	if cmd != nil {
		s.intent.queuedSlashCmds = append(s.intent.queuedSlashCmds, cmd)
	}
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

// renderSlashPickerOverlay returns the View() of whichever slash
// picker is currently active (sub-picker takes precedence over the
// command picker because they are mutually exclusive but the sub
// shadows the command surface visually). Returns the empty string
// when no picker is open so the caller can chain with `+ "\n"`
// without leaving a dangling blank line.
//
// Expected:
//   - Receiver may have nil slashState; safe.
//
// Returns:
//   - The active picker's rendered view, or "".
//
// Side effects:
//   - None.
func (i *Intent) renderSlashPickerOverlay() string {
	if i == nil {
		return ""
	}
	if p := i.slashState.activeSubPicker; p != nil {
		return p.View()
	}
	if p := i.slashState.activeCommandPicker; p != nil {
		return p.View()
	}
	return ""
}
