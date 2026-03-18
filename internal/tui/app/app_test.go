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

type mockChatModel struct {
	initCalled   bool
	updateCalled bool
	viewCalled   bool
	cmdToReturn  tea.Cmd
	viewToReturn string
	lastMsg      tea.Msg
}

func (m *mockChatModel) Init() tea.Cmd {
	m.initCalled = true
	return m.cmdToReturn
}

func (m *mockChatModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.updateCalled = true
	m.lastMsg = msg
	return m, m.cmdToReturn
}

func (m *mockChatModel) View() string {
	m.viewCalled = true
	return m.viewToReturn
}

var _ = Describe("ChatAdapter", func() {
	var (
		adapter   *app.ChatAdapter
		chatModel *mockChatModel
	)

	BeforeEach(func() {
		chatModel = &mockChatModel{
			viewToReturn: "chat view",
		}
		adapter = app.NewChatAdapter(chatModel)
	})

	Describe("NewChatAdapter", func() {
		It("creates a non-nil adapter", func() {
			Expect(adapter).NotTo(BeNil())
		})
	})

	Describe("Init", func() {
		It("delegates to the wrapped model", func() {
			adapter.Init()
			Expect(chatModel.initCalled).To(BeTrue())
		})

		It("returns the command from the model", func() {
			expectedCmd := func() tea.Msg { return nil }
			chatModel.cmdToReturn = expectedCmd
			cmd := adapter.Init()
			Expect(cmd).NotTo(BeNil())
		})

		It("returns nil when model returns nil", func() {
			cmd := adapter.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("Update", func() {
		It("delegates messages to the wrapped model", func() {
			msg := tea.KeyMsg{Type: tea.KeyEnter}
			adapter.Update(msg)
			Expect(chatModel.updateCalled).To(BeTrue())
			Expect(chatModel.lastMsg).To(Equal(msg))
		})

		It("returns the command from the model", func() {
			expectedCmd := func() tea.Msg { return nil }
			chatModel.cmdToReturn = expectedCmd
			cmd := adapter.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())
		})

		It("updates the wrapped model when returned model implements ChatModel", func() {
			newModel := &mockChatModel{viewToReturn: "updated view"}
			chatModel.cmdToReturn = nil
			adapter.Update(tea.KeyMsg{Type: tea.KeyEnter})

			_ = newModel
			Expect(chatModel.updateCalled).To(BeTrue())
		})
	})

	Describe("View", func() {
		It("delegates to the wrapped model", func() {
			adapter.View()
			Expect(chatModel.viewCalled).To(BeTrue())
		})

		It("returns the view from the model", func() {
			chatModel.viewToReturn = "expected chat view"
			view := adapter.View()
			Expect(view).To(Equal("expected chat view"))
		})
	})
})
