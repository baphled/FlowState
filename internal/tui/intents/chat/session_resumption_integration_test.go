package chat_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

func makeChildSession(id, agentID, status string) *session.Session {
	return &session.Session{
		ID:      id,
		AgentID: agentID,
		Status:  status,
	}
}

type stubChildSessionLister struct {
	sessions []*session.Session
	err      error
}

func (s *stubChildSessionLister) ChildSessions(_ string) ([]*session.Session, error) {
	return s.sessions, s.err
}

func (s *stubChildSessionLister) AllSessions() ([]*session.Session, error) {
	return s.sessions, s.err
}

func newResumptionIntent(parentID string, children []*session.Session) *chat.Intent {
	lister := &stubChildSessionLister{sessions: children}
	return chat.NewIntent(chat.IntentConfig{
		AgentID:            "test-agent",
		SessionID:          parentID,
		ProviderName:       "test-provider",
		ModelName:          "test-model",
		TokenBudget:        4096,
		ChildSessionLister: lister,
	})
}

var _ = Describe("Session Resumption Integration", Label("integration"), func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })
	})

	Describe("resumed session delegation picker shows delegated agents", func() {
		It("lists all delegated agent sessions when picker is opened", func() {
			children := []*session.Session{
				makeChildSession("child-session-0001", "planner", "completed"),
				makeChildSession("child-session-0002", "executor", "completed"),
				makeChildSession("child-session-0003", "researcher", "completed"),
			}
			intent := newResumptionIntent("parent-session-abc1", children)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			view := intent.View()
			Expect(view).To(ContainSubstring("planner"))
			Expect(view).To(ContainSubstring("executor"))
			Expect(view).To(ContainSubstring("researcher"))
		})

		It("shows the same sessions whether the intent is fresh or resumed", func() {
			children := []*session.Session{
				makeChildSession("child-session-aaaa", "planner", "completed"),
			}

			freshIntent := newResumptionIntent("parent-fresh-0001", children)
			freshIntent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			freshView := freshIntent.View()

			resumedIntent := newResumptionIntent("parent-resume-0001", children)
			resumedIntent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			resumedView := resumedIntent.View()

			Expect(freshView).To(ContainSubstring("planner"))
			Expect(resumedView).To(ContainSubstring("planner"))
		})
	})

	Describe("ChildSessions returns same agents for active and resumed sessions", func() {
		It("returns the same agent IDs regardless of session state", func() {
			children := []*session.Session{
				makeChildSession("child-active-0001", "planner", "active"),
				makeChildSession("child-active-0002", "executor", "active"),
			}
			intent := newResumptionIntent("parent-session-xyz1", children)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			view := intent.View()
			Expect(view).To(ContainSubstring("planner"))
			Expect(view).To(ContainSubstring("executor"))
		})

		It("shows completed child sessions in the delegation picker", func() {
			children := []*session.Session{
				makeChildSession("child-done-aaaa", "qa-engineer", "completed"),
				makeChildSession("child-done-bbbb", "writer", "completed"),
			}
			intent := newResumptionIntent("parent-session-abc2", children)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			view := intent.View()
			Expect(view).To(ContainSubstring("qa-engineer"))
			Expect(view).To(ContainSubstring("writer"))
		})
	})

	Describe("agent IDs are selectable in delegation picker", func() {
		It("allows navigating to the second agent and selecting it", func() {
			children := []*session.Session{
				makeChildSession("child-nav-0001", "planner", "completed"),
				makeChildSession("child-nav-0002", "executor", "completed"),
			}
			intent := newResumptionIntent("parent-session-nav1", children)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(chat.BreadcrumbPathForTest(intent)).To(ContainSubstring("Chat >"))
		})

		It("selects first agent by default on Enter without navigation", func() {
			children := []*session.Session{
				makeChildSession("firstsessionid01", "planner", "completed"),
			}
			intent := newResumptionIntent("parent-session-sel1", children)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > " + "firstses"))
		})
	})

	Describe("navigate into child session from resumed parent", func() {
		It("transitions to session viewer with the child's content", func() {
			child := &session.Session{
				ID:      "child-with-msgs-001",
				AgentID: "explore",
				Status:  "completed",
				Messages: []session.Message{
					{Role: "assistant", Content: "exploration result"},
				},
			}
			intent := newResumptionIntent("parent-with-msgs-001", []*session.Session{child})

			rendered := intent.RenderSessionContentForTest(child)
			Expect(rendered).To(ContainSubstring("exploration"))
			Expect(rendered).To(ContainSubstring("result"))

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(chat.BreadcrumbPathForTest(intent)).To(ContainSubstring("Chat >"))
		})

		It("shows the correct session ID truncated in the breadcrumb after navigation", func() {
			children := []*session.Session{
				makeChildSession("abcdefghijklmno1", "explore", "completed"),
			}
			intent := newResumptionIntent("parent-session-br01", children)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > abcdefgh"))
		})
	})

	Describe("parent-child linkage preserved on resume", func() {
		It("preserves the parent session ID on the intent across delegation picker interactions", func() {
			children := []*session.Session{
				makeChildSession("child-link-0001", "planner", "completed"),
			}
			intent := newResumptionIntent("parent-id-preserved", children)

			Expect(intent.SessionIDForTest()).To(Equal("parent-id-preserved"))

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			Expect(intent.SessionIDForTest()).To(Equal("parent-id-preserved"))
		})

		It("preserves the parent session ID even after navigating into a child session viewer", func() {
			children := []*session.Session{
				makeChildSession("child-link-abc1", "executor", "completed"),
			}
			intent := newResumptionIntent("parent-id-stays-same", children)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

			Expect(intent.SessionIDForTest()).To(Equal("parent-id-stays-same"))
		})

		It("restores parent session context when returning from child session viewer", func() {
			children := []*session.Session{
				makeChildSession("child-return-0001", "planner", "completed"),
			}
			intent := newResumptionIntent("parent-return-check", children)

			chat.SimulateDelegationEnterForTest(intent, "child-return-0001", "child content")
			Expect(chat.BreadcrumbPathForTest(intent)).NotTo(Equal("Chat"))

			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat"))
			Expect(intent.SessionIDForTest()).To(Equal("parent-return-check"))
		})
	})
})
