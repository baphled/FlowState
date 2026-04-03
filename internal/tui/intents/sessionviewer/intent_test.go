package sessionviewer_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	tuiintents "github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/sessionviewer"
)

var _ = Describe("Intent", func() {
	var (
		intent    *sessionviewer.Intent
		sessionID string
		content   string
	)

	BeforeEach(func() {
		sessionID = "abcd1234-some-session-id"
		content = "line1\nline2\nline3\nline4\nline5\nline6\nline7\nline8\nline9\nline10"
		intent = sessionviewer.NewIntent(sessionID, content, 80, 24)
	})

	Describe("Init", func() {
		Context("when initialised", func() {
			It("returns nil", func() {
				cmd := intent.Init()
				Expect(cmd).To(BeNil())
			})
		})
	})

	Describe("Update", func() {
		Context("when Esc is pressed", func() {
			It("sets Result action to navigate_parent", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
				Expect(intent.Result()).NotTo(BeNil())
				Expect(intent.Result().Action).To(Equal("navigate_parent"))
			})

			It("returns a NavigateToParentMsg command", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
				Expect(cmd).NotTo(BeNil())
				msg := cmd()
				_, isParent := msg.(tuiintents.NavigateToParentMsg)
				Expect(isParent).To(BeTrue())
			})
		})

		Context("when scroll keys are pressed", func() {
			It("returns nil for KeyUp", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(cmd).To(BeNil())
			})

			It("returns nil for KeyDown", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(cmd).To(BeNil())
			})

			It("returns nil for KeyPgUp", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyPgUp})
				Expect(cmd).To(BeNil())
			})

			It("returns nil for KeyPgDown", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyPgDown})
				Expect(cmd).To(BeNil())
			})

			It("delegates ScrollDown to the viewer and changes view output", func() {
				longContent := "line01\nline02\nline03\nline04\nline05\nline06\nline07\nline08\nline09\nline10\nline11\nline12\nline13\nline14\nline15"
				scrollIntent := sessionviewer.NewIntent(sessionID, longContent, 80, 10)
				viewBefore := scrollIntent.View()
				scrollIntent.Update(tea.KeyMsg{Type: tea.KeyDown})
				viewAfter := scrollIntent.View()
				Expect(viewBefore).NotTo(Equal(viewAfter))
			})

			It("does not change view after ScrollUp when already at top", func() {
				viewBefore := intent.View()
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				viewAfter := intent.View()
				Expect(viewBefore).To(Equal(viewAfter))
			})
		})

		Context("when an unhandled message is received", func() {
			It("returns nil for non-key messages", func() {
				cmd := intent.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
				Expect(cmd).To(BeNil())
			})

			It("returns nil for unrecognised key messages", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
				Expect(cmd).To(BeNil())
			})
		})
	})

	Describe("View", func() {
		Context("when rendered", func() {
			It("contains the breadcrumb from the session ID", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("abcd1234"))
			})

			It("contains the Chat prefix in the breadcrumb", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Chat"))
			})

			It("contains the help text", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Esc back"))
			})
		})
	})

	Describe("Result", func() {
		Context("when no action has been taken", func() {
			It("returns nil initially", func() {
				Expect(intent.Result()).To(BeNil())
			})
		})
	})

	Describe("SetBreadcrumbPath", func() {
		Context("when the breadcrumb path is updated", func() {
			It("changes the breadcrumb shown in View", func() {
				intent.SetBreadcrumbPath("Chat > custom-path")
				view := intent.View()
				Expect(view).To(ContainSubstring("custom-path"))
			})
		})
	})

	Describe("Interface compliance", func() {
		It("implements the tuiintents.Intent interface", func() {
			var _ tuiintents.Intent = (*sessionviewer.Intent)(nil)
		})
	})
})
