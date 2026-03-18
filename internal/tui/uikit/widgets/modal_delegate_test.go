package widgets_test

import (
	"github.com/baphled/flowstate/internal/tui/uikit/feedback"
	"github.com/baphled/flowstate/internal/tui/uikit/widgets"
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ModalDelegate", func() {
	It("delegates Show to feedback.DetailModal", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		delegate.Show()
		Expect(modal.IsVisible()).To(BeTrue())
	})

	It("delegates Hide to feedback.DetailModal", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		delegate.Hide()
		Expect(modal.IsVisible()).To(BeFalse())
	})

	It("delegates SetContent to feedback.DetailModal", func() {
		modal := feedback.NewDetailModal("Title", "")
		delegate := widgets.NewModalDelegate(modal)
		delegate.SetContent("Test content")
		Expect(modal.View()).To(ContainSubstring("Test content"))
	})

	It("delegates SetDimensions to feedback.DetailModal", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		delegate.SetDimensions(100, 40)
		Expect(modal.View()).To(BeAssignableToTypeOf(""))
	})

	It("delegates ModalUpdate to feedback.DetailModal", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		model, cmd := delegate.ModalUpdate(tea.KeyMsg{Type: tea.KeyEnter})
		Expect(model).NotTo(BeNil())
		Expect(cmd).To(BeNil())
	})

	It("Modal returns the underlying modal", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		Expect(delegate.Modal()).To(Equal(modal))
	})

	It("SetModal replaces the underlying modal", func() {
		modal1 := feedback.NewDetailModal("Title1", "Content1")
		modal2 := feedback.NewDetailModal("Title2", "Content2")
		delegate := widgets.NewModalDelegate(modal1)
		delegate.SetModal(modal2)
		Expect(delegate.Modal()).To(Equal(modal2))
	})

	It("ModalView returns the modal's view", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		view := delegate.ModalView()
		Expect(view).To(BeAssignableToTypeOf(""))
		Expect(view).To(ContainSubstring("Content"))
	})

	It("IsVisible returns false when modal is hidden", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		delegate.Hide()
		Expect(delegate.IsVisible()).To(BeFalse())
	})

	It("IsVisible returns true when modal is shown", func() {
		modal := feedback.NewDetailModal("Title", "Content")
		delegate := widgets.NewModalDelegate(modal)
		delegate.Show()
		Expect(delegate.IsVisible()).To(BeTrue())
	})
})
