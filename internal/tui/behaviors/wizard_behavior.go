// Package behaviors provides reusable UI behavior components for the FlowState TUI.
package behaviors

import (
	tea "github.com/charmbracelet/bubbletea"
)

// WizardForm abstracts multi-step form operations without exposing huh.
// Implementations wrap form libraries to provide step tracking,
// state queries, and dimension management.
//
// The interface enables:
//   - Testing with mock implementations
//   - Decoupling behaviors/ from form libraries
//   - Consistent wizard form handling across wizard modals
//
//nolint:interfacebloat // WizardForm requires Init/Update/View lifecycle + state queries + dimension management.
type WizardForm interface {
	Init() tea.Cmd
	Update(msg tea.Msg) tea.Cmd
	View() string
	IsCompleted() bool
	IsAborted() bool
	CurrentStep() int
	TotalSteps() int
	SetDimensions(width, height int)
}

// WizardBehavior[T] provides state management, visibility control, and form
// delegation for multi-step wizard modals. It handles the common wizard lifecycle
// (visible -> completed/cancelled) while delegating form-specific logic to a
// WizardForm implementation.
//
// Usage:
//
//	form := forms.NewWizardFormAdapter(huhForm, 3)
//	data := &MyWizardData{}
//	wizard := behaviors.NewWizardBehavior(form, data)
//
//	// In Update:
//	cmd := wizard.Update(msg)
//	if wizard.IsCompleted() { config := wizard.Data() }
//
//	// In View:
//	return wizard.View()
type WizardBehavior[T any] struct {
	form      WizardForm
	data      *T
	visible   bool
	completed bool
	cancelled bool
	skipped   bool
}

// NewWizardBehavior creates a new wizard behavior wrapping the given form and data.
// The wizard starts visible and in an incomplete state.
//
// Expected:
//   - form must be a non-nil WizardForm implementation.
//   - data must be a non-nil pointer to the wizard's data struct.
//
// Returns:
//   - A fully initialized WizardBehavior ready for use.
//
// Side effects:
//   - None.
func NewWizardBehavior[T any](form WizardForm, data *T) *WizardBehavior[T] {
	return &WizardBehavior[T]{
		form:    form,
		data:    data,
		visible: true,
	}
}

// Init initializes the wizard's form.
//
// Returns:
//   - A tea.Cmd from the form's Init, or nil if not visible.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) Init() tea.Cmd {
	if !w.visible {
		return nil
	}
	return w.form.Init()
}

// Update delegates the message to the form and checks for completion/abort.
//
// Expected:
//   - msg is any tea.Msg to forward to the form.
//
// Returns:
//   - A tea.Cmd from the form's Update, or nil if not visible.
//
// Side effects:
//   - Sets completed/cancelled flags and hides wizard when form finishes.
func (w *WizardBehavior[T]) Update(msg tea.Msg) tea.Cmd {
	if !w.visible {
		return nil
	}

	cmd := w.form.Update(msg)

	if w.form.IsCompleted() {
		w.completed = true
		w.visible = false
	}

	if w.form.IsAborted() {
		w.cancelled = true
		w.visible = false
	}

	return cmd
}

// View returns the form's view, or empty string if the wizard is not visible.
//
// Returns:
//   - The form's view string.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) View() string {
	if !w.visible {
		return ""
	}
	return w.form.View()
}

// Data returns the wizard's data pointer.
//
// Returns:
//   - A pointer to the wizard's data struct.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) Data() *T {
	return w.data
}

// Form returns the current WizardForm.
//
// Returns:
//   - The WizardForm implementation.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) Form() WizardForm {
	return w.form
}

// SetForm replaces the current form with a new one.
//
// Expected:
//   - form must be a valid WizardForm implementation.
//
// Side effects:
//   - Replaces the internal form.
func (w *WizardBehavior[T]) SetForm(form WizardForm) {
	w.form = form
}

// IsVisible returns whether the wizard is currently visible.
//
// Returns:
//   - True if visible.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) IsVisible() bool {
	return w.visible
}

// IsCompleted returns whether the wizard finished successfully.
//
// Returns:
//   - True if completed.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) IsCompleted() bool {
	return w.completed
}

// IsCancelled returns whether the wizard was cancelled.
//
// Returns:
//   - True if cancelled.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) IsCancelled() bool {
	return w.cancelled
}

// IsSkipped returns whether the wizard was skipped.
//
// Returns:
//   - True if skipped.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) IsSkipped() bool {
	return w.skipped
}

// CurrentStep returns the form's current step index (0-based).
//
// Returns:
//   - The current step index.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) CurrentStep() int {
	return w.form.CurrentStep()
}

// TotalSteps returns the form's total number of steps.
//
// Returns:
//   - The total step count.
//
// Side effects:
//   - None.
func (w *WizardBehavior[T]) TotalSteps() int {
	return w.form.TotalSteps()
}

// Show makes the wizard visible.
//
// Side effects:
//   - Sets visible to true.
func (w *WizardBehavior[T]) Show() {
	w.visible = true
}

// Hide makes the wizard invisible.
//
// Side effects:
//   - Sets visible to false.
func (w *WizardBehavior[T]) Hide() {
	w.visible = false
}

// Complete marks the wizard as completed and hides it.
//
// Side effects:
//   - Sets completed to true and visible to false.
func (w *WizardBehavior[T]) Complete() {
	w.completed = true
	w.visible = false
}

// Cancel marks the wizard as cancelled and hides it.
//
// Side effects:
//   - Sets cancelled to true and visible to false.
func (w *WizardBehavior[T]) Cancel() {
	w.cancelled = true
	w.visible = false
}

// Skip marks the wizard as skipped and completed, then hides it.
//
// Side effects:
//   - Sets skipped and completed to true, visible to false.
func (w *WizardBehavior[T]) Skip() {
	w.skipped = true
	w.completed = true
	w.visible = false
}

// Reset clears completion, cancellation, and skip flags, and shows the wizard.
//
// Side effects:
//   - Resets all state flags and sets visible to true.
func (w *WizardBehavior[T]) Reset() {
	w.completed = false
	w.cancelled = false
	w.skipped = false
	w.visible = true
}
