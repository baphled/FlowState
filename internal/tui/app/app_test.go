package app_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/app"
)

type mockIntent struct {
	initCalled   bool
	updateCalled bool
	viewCalled   bool
	lastMsg      tea.Msg
	cmdToReturn  tea.Cmd
	viewToReturn string
}

func (m *mockIntent) Init() tea.Cmd {
	m.initCalled = true
	return m.cmdToReturn
}

func (m *mockIntent) Update(msg tea.Msg) tea.Cmd {
	m.updateCalled = true
	m.lastMsg = msg
	return m.cmdToReturn
}

func (m *mockIntent) View() string {
	m.viewCalled = true
	return m.viewToReturn
}

var _ = Describe("App", func() {
	var (
		subject *app.App
		intent  *mockIntent
	)

	BeforeEach(func() {
		intent = &mockIntent{
			viewToReturn: "test view",
		}
		subject = app.New(intent)
	})

	Describe("New", func() {
		It("creates an App with the given intent", func() {
			Expect(subject).NotTo(BeNil())
		})
	})

	Describe("Init", func() {
		It("delegates to the active intent", func() {
			subject.Init()
			Expect(intent.initCalled).To(BeTrue())
		})

		It("returns the command from the intent", func() {
			expectedCmd := func() tea.Msg { return nil }
			intent.cmdToReturn = expectedCmd

			cmd := subject.Init()
			Expect(cmd).NotTo(BeNil())
		})
	})

	Describe("Update", func() {
		It("delegates messages to the active intent", func() {
			msg := tea.KeyMsg{Type: tea.KeyEnter}
			subject.Update(msg)
			Expect(intent.updateCalled).To(BeTrue())
			Expect(intent.lastMsg).To(Equal(msg))
		})

		It("returns the model and command", func() {
			msg := tea.KeyMsg{Type: tea.KeyEnter}
			model, cmd := subject.Update(msg)
			Expect(model).To(Equal(subject))
			Expect(cmd).To(BeNil())
		})

		Context("with window size message", func() {
			It("stores the dimensions", func() {
				msg := tea.WindowSizeMsg{Width: 100, Height: 50}
				subject.Update(msg)
				Expect(subject.Width()).To(Equal(100))
				Expect(subject.Height()).To(Equal(50))
			})

			It("still delegates to the intent", func() {
				msg := tea.WindowSizeMsg{Width: 100, Height: 50}
				subject.Update(msg)
				Expect(intent.updateCalled).To(BeTrue())
			})
		})
	})

	Describe("View", func() {
		It("delegates to the active intent", func() {
			subject.View()
			Expect(intent.viewCalled).To(BeTrue())
		})

		It("returns the view from the intent", func() {
			intent.viewToReturn = "expected view"
			view := subject.View()
			Expect(view).To(Equal("expected view"))
		})
	})

	Describe("SetIntent", func() {
		It("switches to a new intent", func() {
			newIntent := &mockIntent{viewToReturn: "new view"}
			subject.SetIntent(newIntent)
			view := subject.View()
			Expect(view).To(Equal("new view"))
		})
	})
})
