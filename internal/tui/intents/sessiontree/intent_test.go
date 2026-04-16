package sessiontree_test

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/intents/sessiontree"
)

var _ = Describe("SessionTreeIntent", func() {
	var intent *sessiontree.Intent

	Describe("Intent interface compliance", func() {
		It("satisfies the Intent interface", func() {
			intent = sessiontree.New("root", []sessiontree.SessionNode{
				{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
			})
			var _ interface {
				Init() tea.Cmd
				Update(tea.Msg) tea.Cmd
				View() string
			} = intent
		})
	})

	Describe("New", func() {
		It("creates a non-nil intent", func() {
			intent = sessiontree.New("root", []sessiontree.SessionNode{
				{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
			})
			Expect(intent).NotTo(BeNil())
		})

		It("sets cursor to currentSessionID", func() {
			intent = sessiontree.New("child-1", []sessiontree.SessionNode{
				{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
				{SessionID: "child-1", AgentID: "researcher", ParentID: "root"},
			})
			Expect(intent.CursorID()).To(Equal("child-1"))
		})
	})

	Describe("Init", func() {
		It("returns nil cmd", func() {
			intent = sessiontree.New("root", []sessiontree.SessionNode{
				{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
			})
			cmd := intent.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("View", func() {
		Context("with a single root node", func() {
			BeforeEach(func() {
				intent = sessiontree.New("root", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
				})
			})

			It("renders the title", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Session Tree"))
			})

			It("renders one node marked as current with bullet", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("● orchestrator (root)"))
			})
		})

		Context("with a 3-level tree", func() {
			BeforeEach(func() {
				intent = sessiontree.New("root", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
					{SessionID: "child-1", AgentID: "researcher", ParentID: "root"},
					{SessionID: "grandchild-1", AgentID: "analyst", ParentID: "child-1"},
				})
			})

			It("renders indented hierarchy with box-drawing connectors", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("└─ researcher (child-1)"))
				Expect(view).To(ContainSubstring("   └─ analyst (grandchild-1)"))
			})
		})

		Context("with a 5-node tree in depth-first order", func() {
			BeforeEach(func() {
				intent = sessiontree.New("root", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
					{SessionID: "child-1", AgentID: "researcher", ParentID: "root"},
					{SessionID: "child-2", AgentID: "writer", ParentID: "root"},
					{SessionID: "grandchild-1", AgentID: "analyst", ParentID: "child-1"},
					{SessionID: "grandchild-2", AgentID: "editor", ParentID: "child-2"},
				})
			})

			It("renders in depth-first order", func() {
				view := intent.View()
				lines := nonEmptyLines(view)

				// Find the order of agent IDs in the output.
				var agents []string
				for _, line := range lines {
					for _, agent := range []string{"orchestrator", "researcher", "analyst", "writer", "editor"} {
						if strings.Contains(line, agent) {
							agents = append(agents, agent)
						}
					}
				}
				Expect(agents).To(Equal([]string{"orchestrator", "researcher", "analyst", "writer", "editor"}))
			})

			It("uses fork connector for non-last children and elbow for last", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("├─ researcher (child-1)"))
				Expect(view).To(ContainSubstring("└─ writer (child-2)"))
			})

			It("uses continuation line for nested children under non-last parent", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("│  └─ analyst (grandchild-1)"))
			})
		})

		Context("with current session marker", func() {
			It("marks the current session with bullet", func() {
				intent = sessiontree.New("child-1", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
					{SessionID: "child-1", AgentID: "researcher", ParentID: "root"},
				})
				view := intent.View()
				Expect(view).To(ContainSubstring("● researcher (child-1)"))
			})

			It("does not mark non-current sessions with bullet", func() {
				intent = sessiontree.New("child-1", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
					{SessionID: "child-1", AgentID: "researcher", ParentID: "root"},
				})
				view := intent.View()
				Expect(view).NotTo(ContainSubstring("● orchestrator"))
			})
		})

		Context("with cursor highlighting", func() {
			It("highlights cursor position with angle bracket when not current session", func() {
				intent = sessiontree.New("root", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
					{SessionID: "child-1", AgentID: "researcher", ParentID: "root"},
				})
				// Cursor defaults to currentSessionID which is root.
				// Root is both current and cursor, so it gets bullet.
				view := intent.View()
				Expect(view).To(ContainSubstring("● orchestrator (root)"))
			})

			It("uses angle bracket for cursor when cursor differs from current", func() {
				intent = sessiontree.New("root", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
					{SessionID: "child-1", AgentID: "researcher", ParentID: "root"},
				})
				// Move cursor to child-1 via SetCursor (used for testing).
				intent.SetCursor("child-1")
				view := intent.View()
				Expect(view).To(ContainSubstring("> researcher (child-1)"))
				// Root is still current session, should still have bullet.
				Expect(view).To(ContainSubstring("● orchestrator (root)"))
			})
		})

		Context("with empty sessions", func() {
			It("shows no sessions message", func() {
				intent = sessiontree.New("", nil)
				view := intent.View()
				Expect(view).To(ContainSubstring("No sessions"))
			})
		})

		Context("with window size", func() {
			It("stores dimensions from WindowSizeMsg", func() {
				intent = sessiontree.New("root", []sessiontree.SessionNode{
					{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
				})
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
				Expect(intent.Width()).To(Equal(80))
				Expect(intent.Height()).To(Equal(24))
			})
		})
	})

	Describe("Update", func() {
		It("handles WindowSizeMsg", func() {
			intent = sessiontree.New("root", []sessiontree.SessionNode{
				{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
			})
			cmd := intent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			Expect(cmd).To(BeNil())
		})

		It("returns nil for unhandled messages", func() {
			intent = sessiontree.New("root", []sessiontree.SessionNode{
				{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
			})
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyUp})
			Expect(cmd).To(BeNil())
		})
	})

	Describe("Result", func() {
		It("returns nil before any selection or cancel", func() {
			intent = sessiontree.New("root", []sessiontree.SessionNode{
				{SessionID: "root", AgentID: "orchestrator", ParentID: ""},
			})
			Expect(intent.Result()).To(BeNil())
		})
	})
})

// nonEmptyLines splits a string by newlines and returns only non-empty lines.
func nonEmptyLines(s string) []string {
	var result []string
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return result
}
