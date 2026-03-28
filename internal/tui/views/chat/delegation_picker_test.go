package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tui/views/chat"
)

var _ = Describe("DelegationPickerModal", func() {
	Describe("NewDelegationPickerModal", func() {
		It("creates a modal with empty sessions", func() {
			modal := chat.NewDelegationPickerModal(nil, 80, 24)
			Expect(modal).NotTo(BeNil())
		})

		It("creates a modal with given dimensions", func() {
			modal := chat.NewDelegationPickerModal(nil, 100, 30)
			Expect(modal).NotTo(BeNil())
		})
	})

	Describe("rendering", func() {
		It("renders empty state when no sessions", func() {
			modal := chat.NewDelegationPickerModal(nil, 80, 24)
			rendered := modal.Render(80, 24)
			Expect(rendered).To(ContainSubstring("Delegation"))
			Expect(rendered).To(ContainSubstring("No delegations"))
		})

		It("renders sessions with cursor indicator", func() {
			sessions := []*session.Session{
				{ID: "ses-1", AgentID: "qa-agent", Status: "completed"},
				{ID: "ses-2", AgentID: "researcher", Status: "running"},
			}
			modal := chat.NewDelegationPickerModal(sessions, 80, 24)
			rendered := modal.Render(80, 24)
			Expect(rendered).To(ContainSubstring("qa-agent"))
			Expect(rendered).To(ContainSubstring(">"))
		})
	})

	Describe("cursor movement", func() {
		It("MoveUp clamps to zero", func() {
			sessions := []*session.Session{
				{ID: "ses-1", AgentID: "agent-1", Status: "active"},
			}
			modal := chat.NewDelegationPickerModal(sessions, 80, 24)
			modal.MoveUp()
			Expect(modal.Selected().AgentID).To(Equal("agent-1"))
		})

		It("MoveDown increments cursor", func() {
			sessions := []*session.Session{
				{ID: "ses-1", AgentID: "agent-1", Status: "active"},
				{ID: "ses-2", AgentID: "agent-2", Status: "active"},
			}
			modal := chat.NewDelegationPickerModal(sessions, 80, 24)
			modal.MoveDown()
			Expect(modal.Selected().AgentID).To(Equal("agent-2"))
		})

		It("MoveDown clamps to last item", func() {
			sessions := []*session.Session{
				{ID: "ses-1", AgentID: "agent-1", Status: "active"},
				{ID: "ses-2", AgentID: "agent-2", Status: "active"},
			}
			modal := chat.NewDelegationPickerModal(sessions, 80, 24)
			modal.MoveDown()
			modal.MoveDown()
			modal.MoveDown()
			Expect(modal.Selected().AgentID).To(Equal("agent-2"))
		})

		It("MoveUp decrements cursor", func() {
			sessions := []*session.Session{
				{ID: "ses-1", AgentID: "agent-1", Status: "active"},
				{ID: "ses-2", AgentID: "agent-2", Status: "active"},
			}
			modal := chat.NewDelegationPickerModal(sessions, 80, 24)
			modal.MoveDown()
			modal.MoveUp()
			Expect(modal.Selected().AgentID).To(Equal("agent-1"))
		})

		It("does not panic on empty sessions", func() {
			modal := chat.NewDelegationPickerModal(nil, 80, 24)
			Expect(func() {
				modal.MoveUp()
				modal.MoveDown()
			}).NotTo(Panic())
		})
	})

	Describe("Selected", func() {
		It("returns nil when sessions are empty", func() {
			modal := chat.NewDelegationPickerModal(nil, 80, 24)
			Expect(modal.Selected()).To(BeNil())
		})

		It("returns the currently highlighted session", func() {
			sessions := []*session.Session{
				{ID: "ses-1", AgentID: "agent-1", Status: "active"},
				{ID: "ses-2", AgentID: "agent-2", Status: "active"},
			}
			modal := chat.NewDelegationPickerModal(sessions, 80, 24)
			modal.MoveDown()
			sel := modal.Selected()
			Expect(sel).NotTo(BeNil())
			Expect(sel.AgentID).To(Equal("agent-2"))
		})
	})
})
