package chat_test

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

type fakeChildSessionLister struct {
	sessions []*session.Session
	err      error
}

func (f *fakeChildSessionLister) ChildSessions(_ string) ([]*session.Session, error) {
	return f.sessions, f.err
}

func (f *fakeChildSessionLister) AllSessions() ([]*session.Session, error) {
	return f.sessions, f.err
}

func newDelegationTestIntent(lister chat.SessionChildLister) *chat.Intent {
	return chat.NewIntent(chat.IntentConfig{
		AgentID:            "test-agent",
		SessionID:          "parent-session",
		ProviderName:       "test-provider",
		ModelName:          "test-model",
		TokenBudget:        4096,
		ChildSessionLister: lister,
	})
}

var _ = Describe("Delegation Integration", Label("integration"), func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })
	})

	Describe("background task completion via Update", func() {
		var intent *chat.Intent

		BeforeEach(func() {
			intent = chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "test-provider",
				ModelName:    "test-model",
				TokenBudget:  4096,
			})
		})

		It("adds a system message when BackgroundTaskCompletedMsg is received", func() {
			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:      "task-abc",
				Agent:       "explore",
				Description: "investigate codebase",
				Duration:    "3s",
				Status:      "completed",
			}

			intent.Update(msg)

			messages := intent.AllViewMessagesForTest()
			Expect(messages).NotTo(BeEmpty())
			last := messages[len(messages)-1]
			Expect(last.Role).To(Equal("system"))
			Expect(last.Content).To(ContainSubstring("BACKGROUND TASK COMPLETE"))
			Expect(last.Content).To(ContainSubstring("task-abc"))
		})

		It("accumulates system messages for multiple completions", func() {
			for idx := range 3 {
				msg := chat.BackgroundTaskCompletedMsg{
					TaskID:   "task-" + string(rune('0'+idx)),
					Agent:    "explore",
					Duration: "1s",
					Status:   "completed",
				}
				intent.Update(msg)
			}

			messages := intent.AllViewMessagesForTest()
			systemMsgs := 0
			for _, m := range messages {
				if m.Role == "system" {
					systemMsgs++
				}
			}
			Expect(systemMsgs).To(Equal(3))
		})

		It("starts streaming when all background tasks are done and no manager is set", func() {
			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:   "task-final",
				Agent:    "explore",
				Duration: "2s",
				Status:   "completed",
			}

			intent.Update(msg)

			Expect(intent.IsStreaming()).To(BeTrue())
		})

		It("does not start streaming when background tasks are still active", func() {
			bgMgr := engine.NewBackgroundTaskManager()
			bgCtx := context.Background()
			bgMgr.Launch(bgCtx, "running-task", "some-agent", "still going", func(ctx context.Context) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			})
			intent.SetBackgroundManagerForTest(bgMgr)
			DeferCleanup(func() { bgMgr.CancelAll() })

			msg := chat.BackgroundTaskCompletedMsg{
				TaskID:   "partial-done",
				Agent:    "explore",
				Duration: "1s",
				Status:   "completed",
			}

			intent.Update(msg)

			Expect(intent.IsStreaming()).To(BeFalse())
		})
	})

	Describe("delegation picker", func() {
		Context("when Ctrl+D is pressed with child sessions", func() {
			var (
				intent   *chat.Intent
				sessions []*session.Session
			)

			BeforeEach(func() {
				sessions = []*session.Session{
					{ID: "session-alpha-0001", AgentID: "explore", Status: "completed"},
					{ID: "session-beta-0002", AgentID: "builder", Status: "completed"},
				}
				lister := &fakeChildSessionLister{sessions: sessions}
				intent = newDelegationTestIntent(lister)
			})

			It("opens the delegation picker modal", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

				view := intent.View()
				Expect(view).To(ContainSubstring("Delegations"))
			})

			It("lists agent IDs for each delegated session", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

				view := intent.View()
				Expect(view).To(ContainSubstring("explore"))
				Expect(view).To(ContainSubstring("builder"))
			})
		})

		Context("when Ctrl+D is pressed with no child sessions", func() {
			It("shows empty delegation state", func() {
				lister := &fakeChildSessionLister{sessions: nil}
				intent := newDelegationTestIntent(lister)

				intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

				view := intent.View()
				Expect(view).To(ContainSubstring("No delegations"))
			})
		})

		Context("when Esc is pressed while picker is open", func() {
			It("closes the delegation picker", func() {
				sessions := []*session.Session{
					{ID: "session-xyz-0001", AgentID: "explore", Status: "completed"},
				}
				lister := &fakeChildSessionLister{sessions: sessions}
				intent := newDelegationTestIntent(lister)

				intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
				intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

				view := intent.View()
				Expect(view).NotTo(ContainSubstring("Delegations"))
			})
		})

		Context("when Enter is pressed on a selected session", func() {
			It("opens the session viewer with breadcrumb updated to the session ID prefix", func() {
				sess := &session.Session{
					ID:      "abcdef1234567890",
					AgentID: "explore",
					Status:  "completed",
					Messages: []session.Message{
						{Role: "assistant", Content: "Found the answer"},
					},
				}
				lister := &fakeChildSessionLister{sessions: []*session.Session{sess}}
				intent := newDelegationTestIntent(lister)

				intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})

				Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > " + sess.ID[:8]))
			})
		})
	})

	Describe("session viewer", func() {
		Context("when opened via SimulateDelegationEnterForTest", func() {
			var intent *chat.Intent

			BeforeEach(func() {
				intent = newDelegationTestIntent(nil)
			})

			It("renders session messages in the viewer", func() {
				chat.SimulateDelegationEnterForTest(intent, "session-abc-0001", "Assistant response here")

				view := intent.View()
				Expect(view).To(ContainSubstring("Assistant response here"))
			})

			It("shows breadcrumb for the session", func() {
				chat.SimulateDelegationEnterForTest(intent, "abcdef98765432xx", "content")

				Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > abcdef98"))
			})

			It("handles session with short ID without panicking", func() {
				chat.SimulateDelegationEnterForTest(intent, "short", "content")

				Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > short"))
			})

			It("handles empty session content gracefully", func() {
				chat.SimulateDelegationEnterForTest(intent, "session-empty-001", "")

				view := intent.View()
				Expect(view).NotTo(BeEmpty())
			})

			It("returns to chat on Esc from session viewer", func() {
				chat.SimulateDelegationEnterForTest(intent, "session-abc-0001", "some content")

				intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

				Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat"))
			})
		})

		Context("tool call results in session viewer", func() {
			It("renders tool result messages from the child session", func() {
				sess := &session.Session{
					ID:      "tool-session-abc01",
					AgentID: "explore",
					Messages: []session.Message{
						{Role: "tool_result", Content: "file contents here", ToolName: "read"},
					},
				}
				lister := &fakeChildSessionLister{sessions: []*session.Session{sess}}
				intent := newDelegationTestIntent(lister)

				content := intent.RenderSessionContentForTest(sess)
				Expect(content).NotTo(BeEmpty())
			})
		})
	})

	Describe("intent stack and breadcrumb navigation", func() {
		var intent *chat.Intent

		BeforeEach(func() {
			intent = newDelegationTestIntent(nil)
		})

		It("starts with breadcrumb path 'Chat'", func() {
			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat"))
		})

		It("updates breadcrumb when delegation entry is simulated", func() {
			chat.SimulateDelegationEnterForTest(intent, "abcdef12345678ab", "content")

			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > abcdef12"))
		})

		It("truncates session ID to 8 characters in breadcrumb", func() {
			chat.SimulateDelegationEnterForTest(intent, "thisisalongid999", "content")

			path := chat.BreadcrumbPathForTest(intent)
			Expect(path).To(Equal("Chat > thisisal"))
		})

		It("restores breadcrumb to 'Chat' after Esc from session viewer", func() {
			chat.SimulateDelegationEnterForTest(intent, "abcdef12345678ab", "content")
			Expect(chat.BreadcrumbPathForTest(intent)).NotTo(Equal("Chat"))

			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})

			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat"))
		})

		It("can update breadcrumb manually via SetBreadcrumbPathForTest", func() {
			chat.SetBreadcrumbPathForTest(intent, "Chat > custom-path")

			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > custom-path"))
		})

		It("supports deep navigation by direct breadcrumb update", func() {
			chat.SetBreadcrumbPathForTest(intent, "Chat > level1")
			chat.SetBreadcrumbPathForTest(intent, "Chat > level1 > level2")

			Expect(chat.BreadcrumbPathForTest(intent)).To(Equal("Chat > level1 > level2"))
		})
	})

	Describe("child session lister", func() {
		It("returns the correct sessions from the lister", func() {
			sessions := []*session.Session{
				{ID: "child-session-0001", AgentID: "planner"},
				{ID: "child-session-0002", AgentID: "executor"},
			}
			lister := &fakeChildSessionLister{sessions: sessions}
			intent := newDelegationTestIntent(lister)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			view := intent.View()
			Expect(view).To(ContainSubstring("planner"))
			Expect(view).To(ContainSubstring("executor"))
		})

		It("returns an empty slice when no sessions exist", func() {
			lister := &fakeChildSessionLister{sessions: []*session.Session{}}
			intent := newDelegationTestIntent(lister)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			view := intent.View()
			Expect(view).To(ContainSubstring("No delegations"))
		})

		It("supports cursor navigation with down key", func() {
			sessions := []*session.Session{
				{ID: "child-session-aaaa", AgentID: "planner", Status: "completed"},
				{ID: "child-session-bbbb", AgentID: "executor", Status: "completed"},
			}
			lister := &fakeChildSessionLister{sessions: sessions}
			intent := newDelegationTestIntent(lister)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})

			view := intent.View()
			Expect(view).To(ContainSubstring("executor"))
		})

		It("supports cursor navigation with up key after moving down", func() {
			sessions := []*session.Session{
				{ID: "child-session-aaaa", AgentID: "planner", Status: "completed"},
				{ID: "child-session-bbbb", AgentID: "executor", Status: "completed"},
			}
			lister := &fakeChildSessionLister{sessions: sessions}
			intent := newDelegationTestIntent(lister)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyUp})

			view := intent.View()
			Expect(view).To(ContainSubstring("planner"))
		})

		It("shows all child sessions from session manager, not just current session's children", func() {
			sessions := []*session.Session{
				{ID: "child-run1-aaa", AgentID: "librarian", ParentID: "session-run1-parent", Status: "completed"},
				{ID: "child-run2-bbb", AgentID: "explorer", ParentID: "session-run2-parent", Status: "completed"},
				{ID: "child-run3-ccc", AgentID: "oracle", ParentID: "session-run3-parent", Status: "completed"},
			}
			lister := &fakeChildSessionLister{sessions: sessions}
			intent := newDelegationTestIntent(lister)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			view := intent.View()
			Expect(view).To(ContainSubstring("librarian"))
			Expect(view).To(ContainSubstring("explorer"))
			Expect(view).To(ContainSubstring("oracle"))
		})

		It("shows restored sessions from previous runs alongside new delegations", func() {
			sessions := []*session.Session{
				{ID: "child-prev-run-aaa", AgentID: "librarian", ParentID: "session-old-run-111", Status: "completed"},
				{ID: "child-prev-run-bbb", AgentID: "explorer", ParentID: "session-old-run-222", Status: "completed"},
				{ID: "child-current-ccc", AgentID: "oracle", ParentID: "parent-session", Status: "active"},
			}
			lister := &fakeChildSessionLister{sessions: sessions}
			intent := newDelegationTestIntent(lister)

			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlD})

			view := intent.View()
			Expect(view).To(ContainSubstring("librarian"))
			Expect(view).To(ContainSubstring("explorer"))
			Expect(view).To(ContainSubstring("oracle"))
		})
	})
})
