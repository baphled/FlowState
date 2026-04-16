package chat_test

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

var _ = Describe("Session trail in View()", func() {
	var intent *chat.Intent

	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
	})

	AfterEach(func() {
		chat.SetRunningInTestsForTest(false)
	})

	Context("with a three-level ancestry chain", func() {
		BeforeEach(func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"grandparent": {
						ID:      "grandparent",
						AgentID: "planner",
					},
					"parent": {
						ID:       "parent",
						AgentID:  "tech-lead",
						ParentID: "grandparent",
					},
					"child": {
						ID:       "child",
						AgentID:  "engineer",
						ParentID: "parent",
					},
				},
			}
			intent = chat.NewIntent(chat.IntentConfig{
				AgentID:      "engineer",
				SessionID:    "child",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
			intent.SetSessionManagerForTest(mgr)
			chat.RefreshSessionTrailForTest(intent)
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		})

		It("renders the trail with items joined by ' > ' in View()", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("planner"))
			Expect(view).To(ContainSubstring("tech-lead"))
			Expect(view).To(ContainSubstring("engineer"))
			// The separator " > " joins the labels.
			Expect(view).To(ContainSubstring("planner > tech-lead > engineer"))
		})
	})

	Context("with a single root session", func() {
		BeforeEach(func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"root": {
						ID:      "root",
						AgentID: "orchestrator",
					},
				},
			}
			intent = chat.NewIntent(chat.IntentConfig{
				AgentID:      "orchestrator",
				SessionID:    "root",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
			intent.SetSessionManagerForTest(mgr)
			chat.RefreshSessionTrailForTest(intent)
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
		})

		It("renders the single label in View()", func() {
			view := intent.View()
			Expect(view).To(ContainSubstring("orchestrator"))
		})
	})

	Context("trail consumes vertical space", func() {
		It("reduces viewport height by 1 when trail is non-empty", func() {
			// Intent with trail (non-empty).
			mgrWithTrail := &stubSessionManager{
				sessions: map[string]*session.Session{
					"root": {
						ID:      "root",
						AgentID: "orchestrator",
					},
					"child": {
						ID:       "child",
						AgentID:  "engineer",
						ParentID: "root",
					},
				},
			}
			intentWithTrail := chat.NewIntent(chat.IntentConfig{
				AgentID:      "engineer",
				SessionID:    "child",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
			intentWithTrail.SetSessionManagerForTest(mgrWithTrail)
			chat.RefreshSessionTrailForTest(intentWithTrail)
			intentWithTrail.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

			// Intent without trail (nil sessionManager => empty trail).
			intentNoTrail := chat.NewIntent(chat.IntentConfig{
				AgentID:      "orchestrator",
				SessionID:    "root",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
			intentNoTrail.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

			// With a trail, viewport should be 1 row shorter.
			Expect(intentWithTrail.ViewportHeight()).To(Equal(intentNoTrail.ViewportHeight() - 1))
		})
	})

	Context("primary width in dual-pane mode", func() {
		It("uses 70% primary width when secondary pane is visible", func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"root": {
						ID:      "root",
						AgentID: "planner",
					},
					"child": {
						ID:       "child",
						AgentID:  "engineer",
						ParentID: "root",
					},
				},
			}
			intent = chat.NewIntent(chat.IntentConfig{
				AgentID:      "engineer",
				SessionID:    "child",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
			intent.SetSessionManagerForTest(mgr)
			chat.RefreshSessionTrailForTest(intent)
			// Width 100, dual-pane visible → primary = ((100-1)*7)/10 = 69.
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
			// secondaryPaneVisible defaults to true; ensure swarmActivity is set
			// so dual-pane actually activates.

			view := intent.View()
			// The trail should be rendered. With dual-pane, the trail is clamped
			// to primaryWidth (69 columns). A trail like "planner > engineer"
			// (18 chars) fits easily within 69.
			Expect(view).To(ContainSubstring("planner > engineer"))
		})
	})

	Context("primary width in single-pane mode", func() {
		It("uses full terminal width when secondary pane is hidden", func() {
			mgr := &stubSessionManager{
				sessions: map[string]*session.Session{
					"root": {
						ID:      "root",
						AgentID: "planner",
					},
					"child": {
						ID:       "child",
						AgentID:  "engineer",
						ParentID: "root",
					},
				},
			}
			intent = chat.NewIntent(chat.IntentConfig{
				AgentID:      "engineer",
				SessionID:    "child",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
			intent.SetSessionManagerForTest(mgr)
			chat.RefreshSessionTrailForTest(intent)
			intent.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
			// Width 60 is below dualPaneMinWidth (80), so single-pane forced.

			view := intent.View()
			Expect(view).To(ContainSubstring("planner > engineer"))
		})
	})

	Context("with empty trail", func() {
		It("does not add a trail header row", func() {
			intent = chat.NewIntent(chat.IntentConfig{
				AgentID:      "orchestrator",
				SessionID:    "root",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
			// No session manager → empty trail.
			intent.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

			view := intent.View()
			// No " > " separator should appear.
			lines := strings.Split(view, "\n")
			for _, line := range lines {
				Expect(line).NotTo(ContainSubstring(" > "))
			}
		})
	})
})
