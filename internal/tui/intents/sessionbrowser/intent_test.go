package sessionbrowser_test

import (
	"errors"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
)

// recordingDeleter captures delete calls for assertions and optionally
// returns a configured error.
type recordingDeleter struct {
	calls []string
	err   error
}

func (d *recordingDeleter) Delete(sessionID string) error {
	d.calls = append(d.calls, sessionID)
	return d.err
}

var _ = Describe("SessionBrowserIntent", func() {
	var (
		intent   *sessionbrowser.Intent
		sessions []sessionbrowser.SessionEntry
	)

	BeforeEach(func() {
		sessions = []sessionbrowser.SessionEntry{
			{ID: "session-1", Title: "First Session", MessageCount: 5, LastActive: time.Now().Add(-1 * time.Hour)},
			{ID: "session-2", Title: "Second Session", MessageCount: 10, LastActive: time.Now().Add(-24 * time.Hour)},
		}
		intent = sessionbrowser.NewIntent(sessionbrowser.IntentConfig{Sessions: sessions})
	})

	Describe("NewIntent", func() {
		It("creates a non-nil intent", func() {
			Expect(intent).NotTo(BeNil())
		})

		It("starts with selection at index 0", func() {
			Expect(intent.SelectedSession()).To(Equal(0))
		})
	})

	Describe("Init", func() {
		It("returns nil cmd", func() {
			cmd := intent.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("View", func() {
		It("renders the New Session entry", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("\u271a New Session"))
		})

		It("renders session entries", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("First Session"))
			Expect(view).To(ContainSubstring("Second Session"))
		})

		It("highlights the selected item with cursor indicator", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("> \u271a New Session"))
		})

		It("does not highlight unselected items", func() {
			view := intent.View()
			Expect(view).NotTo(ContainSubstring("> First Session"))
			Expect(view).NotTo(ContainSubstring("> Second Session"))
		})
	})

	Describe("navigation", func() {
		Context("KeyDown", func() {
			It("moves selection down", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedSession()).To(Equal(1))
			})

			It("does not move beyond the last item", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedSession()).To(Equal(2))
			})
		})

		Context("KeyUp", func() {
			It("moves selection up", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedSession()).To(Equal(1))
			})

			It("does not move before the first item", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedSession()).To(Equal(0))
			})
		})

		It("updates the cursor indicator after navigation", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			view := intent.View()
			Expect(view).To(ContainSubstring("> First Session"))
			Expect(view).NotTo(ContainSubstring("> \u271a New Session"))
		})
	})

	Describe("selection", func() {
		Context("when New Session is selected (index 0)", func() {
			It("returns a non-nil command", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(cmd).NotTo(BeNil())
			})

			It("sets result to create action", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				result := intent.Result()
				Expect(result).NotTo(BeNil())
				Expect(result.Action).To(Equal(string(sessionbrowser.ActionCreate)))
				Expect(result.Data).To(Equal(sessionbrowser.Nav{
					Action: sessionbrowser.ActionCreate,
				}))
			})
		})

		Context("when an existing session is selected", func() {
			BeforeEach(func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			})

			It("returns a non-nil command", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(cmd).NotTo(BeNil())
			})

			It("sets result with select action and correct session ID", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				result := intent.Result()
				Expect(result).NotTo(BeNil())
				Expect(result.Action).To(Equal(string(sessionbrowser.ActionSelect)))
				Expect(result.Data).To(Equal(sessionbrowser.Nav{
					Action:    sessionbrowser.ActionSelect,
					SessionID: "session-1",
				}))
			})
		})

		Context("when second existing session is selected", func() {
			It("sets result with correct session ID", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				result := intent.Result()
				Expect(result).NotTo(BeNil())
				Expect(result.Data).To(Equal(sessionbrowser.Nav{
					Action:    sessionbrowser.ActionSelect,
					SessionID: "session-2",
				}))
			})
		})
	})

	Describe("cancellation", func() {
		It("emits DismissModalMsg on Escape", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(cmd).NotTo(BeNil())

			msg := cmd()
			Expect(msg).To(BeAssignableToTypeOf(intents.DismissModalMsg{}))
		})

		It("sets cancel result on Escape", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			result := intent.Result()
			Expect(result).NotTo(BeNil())
			Expect(result.Action).To(Equal(string(sessionbrowser.ActionCancel)))
		})

		It("emits DismissModalMsg on Ctrl+C", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			Expect(cmd).NotTo(BeNil())

			msg := cmd()
			Expect(msg).To(BeAssignableToTypeOf(intents.DismissModalMsg{}))
		})

		It("sets cancel result on Ctrl+C", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			result := intent.Result()
			Expect(result).NotTo(BeNil())
			Expect(result.Action).To(Equal(string(sessionbrowser.ActionCancel)))
		})
	})

	Describe("result helpers", func() {
		Describe("NewSelectResult", func() {
			It("returns non-nil result with select action", func() {
				result := sessionbrowser.NewSelectResult("test-id")
				Expect(result).NotTo(BeNil())
				Expect(result.Action).To(Equal(string(sessionbrowser.ActionSelect)))
				Expect(result.Data).To(Equal(sessionbrowser.Nav{
					Action:    sessionbrowser.ActionSelect,
					SessionID: "test-id",
				}))
			})
		})

		Describe("NewCreateResult", func() {
			It("returns non-nil result with create action", func() {
				result := sessionbrowser.NewCreateResult()
				Expect(result).NotTo(BeNil())
				Expect(result.Action).To(Equal(string(sessionbrowser.ActionCreate)))
				Expect(result.Data).To(Equal(sessionbrowser.Nav{
					Action: sessionbrowser.ActionCreate,
				}))
			})
		})

		Describe("NewCancelResult", func() {
			It("returns non-nil result with cancel action", func() {
				result := sessionbrowser.NewCancelResult()
				Expect(result).NotTo(BeNil())
				Expect(result.Action).To(Equal(string(sessionbrowser.ActionCancel)))
				Expect(result.Data).To(Equal(sessionbrowser.Nav{
					Action: sessionbrowser.ActionCancel,
				}))
			})
		})
	})

	Describe("SelectedSession", func() {
		It("returns correct index after navigation", func() {
			Expect(intent.SelectedSession()).To(Equal(0))
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			Expect(intent.SelectedSession()).To(Equal(1))
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			Expect(intent.SelectedSession()).To(Equal(2))
		})
	})

	Describe("Intent interface compliance", func() {
		It("satisfies the Intent interface", func() {
			var _ interface {
				Init() tea.Cmd
				Update(tea.Msg) tea.Cmd
				View() string
			} = intent
		})
	})

	Describe("delete affordance (P10b)", func() {
		var deleter *recordingDeleter

		BeforeEach(func() {
			deleter = &recordingDeleter{}
			intent = sessionbrowser.NewIntent(sessionbrowser.IntentConfig{
				Sessions:        sessions,
				Deleter:         deleter,
				ActiveSessionID: "",
			})
		})

		Describe("d key opens the confirmation modal", func() {
			It("does not open confirmation when New Session is selected", func() {
				// selection at 0 → New Session row
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				Expect(intent.IsConfirmingDelete()).To(BeFalse())
			})

			It("opens the confirmation modal for the selected session", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				Expect(intent.IsConfirmingDelete()).To(BeTrue())
			})

			It("shows the session name and activity timeline wording in the prompt", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				view := intent.View()
				Expect(view).To(ContainSubstring("First Session"))
				Expect(view).To(ContainSubstring("activity timeline"))
				Expect(view).To(ContainSubstring("(y/N)"))
			})
		})

		Describe("confirming delete invokes the store", func() {
			It("calls Delete with the selected session ID on y", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
				Expect(deleter.calls).To(Equal([]string{"session-1"}))
			})

			It("calls Delete on uppercase Y", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
				Expect(deleter.calls).To(Equal([]string{"session-1"}))
			})

			It("calls Delete on Enter", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				Expect(deleter.calls).To(Equal([]string{"session-1"}))
			})

			It("emits a SessionDeletedMsg after a successful delete", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
				Expect(cmd).NotTo(BeNil())

				msg := cmd()
				deleted, ok := msg.(sessionbrowser.SessionDeletedMsg)
				Expect(ok).To(BeTrue())
				Expect(deleted.SessionID).To(Equal("session-1"))
				Expect(deleted.Err).ToNot(HaveOccurred())
			})
		})

		Describe("cancelling delete", func() {
			It("does not call Delete when user presses n", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
				Expect(deleter.calls).To(BeEmpty())
			})

			It("does not call Delete when user presses Esc", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
				Expect(deleter.calls).To(BeEmpty())
			})

			It("closes the confirmation modal after cancel", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
				Expect(intent.IsConfirmingDelete()).To(BeFalse())
			})
		})

		Describe("after a successful delete", func() {
			BeforeEach(func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			})

			It("removes the session from the in-memory list", func() {
				view := intent.View()
				Expect(view).NotTo(ContainSubstring("First Session"))
				Expect(view).To(ContainSubstring("Second Session"))
			})

			It("keeps selection on the same row (now pointing at the next session)", func() {
				// Was at index 1 (First Session); First Session removed; index 1
				// should now point at Second Session (which moved up).
				Expect(intent.SelectedSession()).To(Equal(1))
			})

			It("clears the confirming state", func() {
				Expect(intent.IsConfirmingDelete()).To(BeFalse())
			})
		})

		Describe("deleting the last session in the list", func() {
			BeforeEach(func() {
				// Navigate to the second (last) session at index 2.
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			})

			It("moves selection to the previous session", func() {
				// Was at index 2 (last); after removal, selection should move
				// up to the remaining session at index 1.
				Expect(intent.SelectedSession()).To(Equal(1))
			})
		})

		Describe("deleting the only remaining session", func() {
			BeforeEach(func() {
				sessions = []sessionbrowser.SessionEntry{
					{ID: "only", Title: "Only Session", MessageCount: 1, LastActive: time.Now()},
				}
				deleter = &recordingDeleter{}
				intent = sessionbrowser.NewIntent(sessionbrowser.IntentConfig{
					Sessions: sessions,
					Deleter:  deleter,
				})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
			})

			It("does not crash and shows an empty-state row", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("No sessions yet"))
			})

			It("snaps selection to the New Session row", func() {
				Expect(intent.SelectedSession()).To(Equal(0))
			})
		})

		Describe("failed delete", func() {
			BeforeEach(func() {
				deleter.err = errors.New("disk full")
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
			})

			It("emits a SessionDeletedMsg carrying the error", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
				Expect(cmd).NotTo(BeNil())

				msg := cmd()
				deleted, ok := msg.(sessionbrowser.SessionDeletedMsg)
				Expect(ok).To(BeTrue())
				Expect(deleted.SessionID).To(Equal("session-1"))
				Expect(deleted.Err).To(HaveOccurred())
				Expect(deleted.Err.Error()).To(ContainSubstring("disk full"))
			})

			It("does not remove the session from the list", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
				view := intent.View()
				Expect(view).To(ContainSubstring("First Session"))
			})
		})

		Describe("cannot delete the currently active session", func() {
			BeforeEach(func() {
				intent = sessionbrowser.NewIntent(sessionbrowser.IntentConfig{
					Sessions:        sessions,
					Deleter:         deleter,
					ActiveSessionID: "session-1",
				})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown}) // select session-1 (active)
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
			})

			It("does not open the confirmation modal", func() {
				Expect(intent.IsConfirmingDelete()).To(BeFalse())
			})

			It("does not call Delete", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
				Expect(deleter.calls).To(BeEmpty())
			})

			It("shows a cannot-delete-active message in the view", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Cannot delete the active session"))
			})
		})

		Describe("no deleter configured", func() {
			BeforeEach(func() {
				intent = sessionbrowser.NewIntent(sessionbrowser.IntentConfig{
					Sessions: sessions,
				})
			})

			It("does not open the confirmation modal when d is pressed", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
				Expect(intent.IsConfirmingDelete()).To(BeFalse())
			})
		})
	})
})
