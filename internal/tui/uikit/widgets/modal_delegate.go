package widgets

import (
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	tea "github.com/charmbracelet/bubbletea"
)

// ModalDelegate delegates modal operations to feedback.DetailModal.
type ModalDelegate struct {
	modal *feedback.DetailModal
}

// NewModalDelegate creates a new ModalDelegate wrapping a DetailModal.
//
// Expected:
//   - modal must be a valid DetailModal.
//
// Returns:
//   - A new ModalDelegate instance.
//
// Side effects:
//   - None.
func NewModalDelegate(modal *feedback.DetailModal) *ModalDelegate {
	return &ModalDelegate{modal: modal}
}

// ModalUpdate delegates Update to the underlying DetailModal.
//
// Expected:
//   - msg must be a valid tea.Msg.
//
// Returns:
//   - A tea.Model and tea.Cmd from the underlying modal.
//
// Side effects:
//   - Updates the underlying DetailModal state.
func (d *ModalDelegate) ModalUpdate(msg tea.Msg) (tea.Model, tea.Cmd) {
	return d.modal.Update(msg)
}

// ModalView delegates View to the underlying DetailModal.
//
// Returns:
//   - A string value.
//
// Side effects:
//   - None.
func (d *ModalDelegate) ModalView() string {
	return d.modal.View()
}

// IsVisible returns whether the modal is visible.
//
// Returns:
//   - A bool value.
//
// Side effects:
//   - None.
func (d *ModalDelegate) IsVisible() bool {
	return d.modal.IsVisible()
}

// Show makes the modal visible.
//
// Side effects:
//   - None.
func (d *ModalDelegate) Show() {
	d.modal.Show()
}

// Hide hides the modal.
//
// Side effects:
//   - None.
func (d *ModalDelegate) Hide() {
	d.modal.Hide()
}

// SetDimensions sets terminal dimensions.
//
// Expected:
//   - int must be valid.
//
// Side effects:
//   - None.
func (d *ModalDelegate) SetDimensions(width, height int) {
	d.modal.SetDimensions(width, height)
}

// SetContent sets modal content text.
//
// Expected:
//   - Must be a valid string.
//
// Side effects:
//   - None.
func (d *ModalDelegate) SetContent(content string) {
	d.modal.SetContent(content)
}

// Modal returns the underlying DetailModal.
//
// Returns:
//   - A fully initialized feedback.DetailModal ready for use.
//
// Side effects:
//   - None.
func (d *ModalDelegate) Modal() *feedback.DetailModal {
	return d.modal
}

// SetModal replaces the underlying DetailModal.
//
// Expected:
//   - detailmodal must be valid.
//
// Side effects:
//   - None.
func (d *ModalDelegate) SetModal(modal *feedback.DetailModal) {
	d.modal = modal
}
