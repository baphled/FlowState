package sessionbrowser_test

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
)

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
})
