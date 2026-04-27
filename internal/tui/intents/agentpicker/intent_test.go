package agentpicker_test

import (
	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/agentpicker"
)

var _ = Describe("AgentPickerIntent", func() {
	var (
		intent *agentpicker.Intent
		agents []agentpicker.AgentEntry
	)

	BeforeEach(func() {
		agents = []agentpicker.AgentEntry{
			{ID: "agent1", Name: "First Agent"},
			{ID: "agent2", Name: "Second Agent"},
			{ID: "agent3", Name: "Third Agent"},
		}
		intent = agentpicker.NewIntent(agentpicker.IntentConfig{
			Agents: agents,
		})
	})

	Describe("NewIntent", func() {
		It("creates an intent with agents loaded from config", func() {
			Expect(intent).NotTo(BeNil())
		})

		It("creates an intent with selection at first agent", func() {
			Expect(intent.SelectedAgent()).To(Equal(0))
		})
	})

	Describe("View", func() {
		It("renders agent list", func() {
			view := intent.View()
			Expect(view).NotTo(BeEmpty())
			Expect(view).To(ContainSubstring("First Agent"))
			Expect(view).To(ContainSubstring("Second Agent"))
			Expect(view).To(ContainSubstring("Third Agent"))
		})

		It("shows cursor indicator on selected agent", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("First Agent"))
		})
	})

	Describe("navigation", func() {
		Context("arrow key navigation", func() {
			It("moves selection down on KeyDown", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedAgent()).To(Equal(1))
			})

			It("does not move beyond last agent on KeyDown", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedAgent()).To(Equal(2))
			})

			It("moves selection up on KeyUp", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedAgent()).To(Equal(1))
			})

			It("does not move before first agent on KeyUp", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.SelectedAgent()).To(Equal(0))
			})
		})
	})

	Describe("agent selection", func() {
		It("returns result with selected agent ID on Enter", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			result := intent.Result()
			Expect(result).NotTo(BeNil())
			Expect(result.Data).To(Equal("agent2"))
		})

		It("returns result with first agent ID when Enter pressed at index 0", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			result := intent.Result()
			Expect(result).NotTo(BeNil())
			Expect(result.Data).To(Equal("agent1"))
		})
	})

	Describe("cancellation", func() {
		It("returns nil result on Escape", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			result := intent.Result()
			Expect(result).To(BeNil())
		})

		It("returns nil result on Ctrl+C", func() {
			intent.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			result := intent.Result()
			Expect(result).To(BeNil())
		})
	})

	Describe("modal dismissal", func() {
		It("dispatches DismissModalMsg on Enter", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
			Expect(cmd).NotTo(BeNil())
			Expect(cmd()).To(Equal(intents.DismissModalMsg{}))
		})

		It("dispatches DismissModalMsg on Escape", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEsc})
			Expect(cmd).NotTo(BeNil())
			Expect(cmd()).To(Equal(intents.DismissModalMsg{}))
		})

		It("dispatches DismissModalMsg on Ctrl+C", func() {
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
			Expect(cmd).NotTo(BeNil())
			Expect(cmd()).To(Equal(intents.DismissModalMsg{}))
		})

		It("does not dispatch DismissModalMsg on arrow navigation", func() {
			Expect(intent.Update(tea.KeyMsg{Type: tea.KeyDown})).To(BeNil())
			Expect(intent.Update(tea.KeyMsg{Type: tea.KeyUp})).To(BeNil())
		})
	})

	Describe("Init", func() {
		It("returns nil cmd", func() {
			cmd := intent.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("Intent interface compliance", func() {
		It("satisfies app.Intent interface", func() {
			var _ interface {
				Init() tea.Cmd
				Update(tea.Msg) tea.Cmd
				View() string
			} = intent
		})
	})

	Describe("integration: multi-agent picker with navigation", func() {
		Context("with two agents", func() {
			BeforeEach(func() {
				agents = []agentpicker.AgentEntry{
					{ID: "planner", Name: "Strategic Planner"},
					{ID: "executor", Name: "Task Executor"},
				}
				intent = agentpicker.NewIntent(agentpicker.IntentConfig{
					Agents: agents,
				})
			})

			It("displays both agents in view", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Strategic Planner"))
				Expect(view).To(ContainSubstring("Task Executor"))
			})

			It("navigates from first to second agent on KeyDown", func() {
				Expect(intent.SelectedAgent()).To(Equal(0))
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.SelectedAgent()).To(Equal(1))
			})

			It("returns correct agent ID on selection of second agent", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyEnter})
				result := intent.Result()
				Expect(result).NotTo(BeNil())
				Expect(result.Data).To(Equal("executor"))
			})

			It("shows cursor on selected agent after navigation", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				view := intent.View()
				Expect(view).To(ContainSubstring("> Task Executor"))
				Expect(view).NotTo(ContainSubstring("> Strategic Planner"))
			})
		})
	})
})
