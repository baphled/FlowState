package slashcommand

import (
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
)

// WizardStepKind discriminates the variants of WizardStep. The chat
// intent inspects Kind to choose between rendering a text-input prompt,
// opening a (possibly multi-select) picker, or finishing the wizard.
type WizardStepKind int

const (
	// StepInput asks the user to type a string. The chat intent reuses
	// its existing input buffer; SubmitText is called on Enter and
	// Cancel is called on Esc.
	StepInput WizardStepKind = iota
	// StepPicker opens a single-select picker over Items. SubmitItem is
	// called with the chosen item; Cancel on Esc.
	StepPicker
	// StepMultiPicker opens a multi-select picker over Items. The chat
	// intent constructs the picker with widgets.WithMultiSelect();
	// SubmitMulti is called on Enter; Cancel on Esc.
	StepMultiPicker
	// StepConfirm renders the wizard's preview as a system message and
	// asks the user to confirm before finishing. Behaves as a binary
	// picker over a yes / no option set so the chat intent can reuse
	// its existing single-select pipeline.
	StepConfirm
	// StepDone signals the wizard has nothing more to ask. The chat
	// intent dismisses the picker and runs any final SystemMessage the
	// wizard wants surfaced through CompleteMessage().
	StepDone
)

// WizardStep describes the state the chat intent should render. Only
// the fields relevant to the current Kind are populated.
type WizardStep struct {
	// Kind discriminates the variant.
	Kind WizardStepKind
	// Prompt is the human-facing label rendered above the picker / input.
	// Used for every non-Done variant.
	Prompt string
	// Items populates the picker variants.
	Items []widgets.Item
	// PreviewMessage is rendered as a system message before the wizard
	// asks for confirmation. Used for StepConfirm only.
	PreviewMessage string
}

// Wizard is the multi-step driver returned by Command.OpenWizard. The
// chat intent calls Current() to learn the next prompt and Submit*()
// to advance. Implementations stay stateful — Current() reflects the
// post-submission state on the next call.
//
// Method-set design:
//   - Submit* methods return an error so a wizard can reject invalid
//     input (e.g. empty name) without dismissing the surface; the chat
//     intent surfaces the error as a system message and keeps the
//     current step open for a retry.
//   - Cancel() lets the wizard roll back any partially-written
//     filesystem state (e.g. an unconfirmed manifest file). Always
//     called on Esc.
//   - CompleteMessage returns the final dump rendered as a system
//     message when Current().Kind is StepDone, or empty when no final
//     surface is appropriate.
type Wizard interface {
	// Current returns the wizard's current step.
	Current() WizardStep
	// SubmitText advances a StepInput step with the typed value.
	SubmitText(value string) error
	// SubmitItem advances a StepPicker / StepConfirm step with the
	// chosen item.
	SubmitItem(item widgets.Item) error
	// SubmitMulti advances a StepMultiPicker step with the committed
	// multi-set. An empty slice is a legal "skip / no members" path.
	SubmitMulti(items []widgets.Item) error
	// Cancel rolls back any partial state. Idempotent.
	Cancel()
	// CompleteMessage returns the final system-message dump, or empty.
	CompleteMessage() string
}
