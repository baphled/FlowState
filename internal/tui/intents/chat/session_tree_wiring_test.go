package chat_test

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/intents/sessiontree"
	"github.com/baphled/flowstate/internal/tui/uikit/navigation"
)

// stubTreeChildSessionLister implements chat.SessionChildLister for testing the
// session tree overlay. AllSessions returns a configurable list; ChildSessions
// delegates to AllSessions filtered by parent.
type stubTreeChildSessionLister struct {
	sessions []*session.Session
}

func (s *stubTreeChildSessionLister) ChildSessions(parentID string) ([]*session.Session, error) {
	var result []*session.Session
	for _, sess := range s.sessions {
		if sess.ParentID == parentID {
			result = append(result, sess)
		}
	}
	return result, nil
}

func (s *stubTreeChildSessionLister) AllSessions() ([]*session.Session, error) {
	return s.sessions, nil
}

// stubTreeSessionManager implements chat.SessionManager for tree wiring tests.
type stubTreeSessionManager struct {
	sessions map[string]*session.Session
}

func (s *stubTreeSessionManager) EnsureSession(_ string, _ string) {}

func (s *stubTreeSessionManager) SendMessage(
	_ context.Context, _ string, _ string,
) (<-chan provider.StreamChunk, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *stubTreeSessionManager) GetSession(id string) (*session.Session, error) {
	sess, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("session %q not found", id)
	}
	return sess, nil
}

var _ = Describe("Session tree wiring", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		intent = chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "sess-001",
			ProviderName: "openai",
			ModelName:    "gpt-4o",
			TokenBudget:  4096,
		})
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Describe("Ctrl+G opens session tree modal", func() {
		It("emits ShowModalMsg containing a sessiontree.Intent", func() {
			lister := &stubTreeChildSessionLister{
				sessions: []*session.Session{
					{ID: "sess-001", AgentID: "orchestrator"},
					{ID: "sess-002", AgentID: "engineer", ParentID: "sess-001"},
				},
			}
			chat.SetChildSessionListerForTest(intent, lister)

			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			Expect(cmd).NotTo(BeNil())

			msg := cmd()
			showModal, ok := msg.(tuiintents.ShowModalMsg)
			Expect(ok).To(BeTrue(), "expected ShowModalMsg, got %T", msg)
			Expect(showModal.Modal).NotTo(BeNil())

			// Verify the modal is a sessiontree.Intent by checking its View
			// output contains "Session Tree".
			view := showModal.Modal.View()
			Expect(view).To(ContainSubstring("Session Tree"))
		})

		It("does not panic when childSessionLister is nil", func() {
			// Default intent has nil childSessionLister.
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			// Should return nil cmd (no-op) without panic.
			Expect(cmd).To(BeNil())
		})

		It("returns nil when AllSessions returns an error", func() {
			lister := &stubErrorChildSessionLister{}
			chat.SetChildSessionListerForTest(intent, lister)

			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
			Expect(cmd).To(BeNil())
		})
	})

	Describe("SelectedMsg navigates to chosen session", func() {
		var mgr *stubTreeSessionManager

		BeforeEach(func() {
			mgr = &stubTreeSessionManager{
				sessions: map[string]*session.Session{
					"sess-001": {ID: "sess-001", AgentID: "orchestrator"},
					"sess-002": {ID: "sess-002", AgentID: "engineer", ParentID: "sess-001"},
				},
			}
			intent.SetSessionManagerForTest(mgr)
		})

		It("switches session ID to the selected session", func() {
			intent.Update(sessiontree.SelectedMsg{SessionID: "sess-002"})

			Expect(intent.SessionIDForTest()).To(Equal("sess-002"))
		})

		It("refreshes the session trail after selection", func() {
			intent.SetSessionManagerForTest(mgr)
			intent.Update(sessiontree.SelectedMsg{SessionID: "sess-002"})

			items := chat.SessionTrailForTest(intent).Items()
			Expect(items).To(HaveLen(2))
			Expect(items[0]).To(Equal(navigation.SessionTrailItem{
				SessionID: "sess-001",
				AgentID:   "orchestrator",
				Label:     "orchestrator",
			}))
			Expect(items[1]).To(Equal(navigation.SessionTrailItem{
				SessionID: "sess-002",
				AgentID:   "engineer",
				Label:     "engineer",
			}))
		})
	})

	Describe("/help output", func() {
		It("contains Ctrl+G session tree keybinding via the picker Overview entry", func() {
			for _, r := range "/help" {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			messages := intent.MessagesForTest()
			Expect(messages).NotTo(BeEmpty())
			lastMsg := messages[len(messages)-1]
			Expect(lastMsg.Content).To(ContainSubstring("Ctrl+G"))
			Expect(lastMsg.Content).To(ContainSubstring("Open session tree"))
		})
	})
})

// stubErrorChildSessionLister always returns an error from AllSessions.
type stubErrorChildSessionLister struct{}

func (s *stubErrorChildSessionLister) ChildSessions(_ string) ([]*session.Session, error) {
	return nil, fmt.Errorf("list error")
}

func (s *stubErrorChildSessionLister) AllSessions() ([]*session.Session, error) {
	return nil, fmt.Errorf("list error")
}
