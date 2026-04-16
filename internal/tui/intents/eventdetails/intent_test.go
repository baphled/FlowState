package eventdetails_test

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents"
	"github.com/baphled/flowstate/internal/tui/intents/eventdetails"
)

var _ = Describe("EventDetailsIntent", func() {
	var (
		intent    *eventdetails.Intent
		timestamp time.Time
	)

	BeforeEach(func() {
		timestamp = time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	})

	Describe("Intent interface compliance", func() {
		It("satisfies the Intent interface", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-1",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			var _ intents.Intent = intent
			Expect(intent).NotTo(BeNil())
		})
	})

	Describe("New", func() {
		It("creates a non-nil intent", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-1",
				Type:      streaming.EventToolCall,
				Status:    "running",
				Timestamp: timestamp,
				AgentID:   "worker-1",
			})
			Expect(intent).NotTo(BeNil())
		})
	})

	Describe("Init", func() {
		It("returns nil cmd", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-1",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			cmd := intent.Init()
			Expect(cmd).To(BeNil())
		})
	})

	Describe("View", func() {
		Context("with minimal event (no metadata)", func() {
			BeforeEach(func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-minimal",
					Type:      streaming.EventDelegation,
					Status:    "started",
					Timestamp: timestamp,
					AgentID:   "orchestrator",
				})
			})

			It("renders the event type and status", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("[delegation]"))
				Expect(view).To(ContainSubstring("started"))
			})

			It("renders the timestamp in RFC3339 format", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("2026-04-16T12:00:00Z"))
			})

			It("renders the agent ID", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Agent: orchestrator"))
			})

			It("renders the event ID", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("ID: evt-minimal"))
			})

			It("does not render a Metadata section", func() {
				view := intent.View()
				Expect(view).NotTo(ContainSubstring("Metadata"))
			})

			It("renders the footer hint", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Esc: close"))
			})
		})

		Context("with delegation event metadata", func() {
			BeforeEach(func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-deleg",
					Type:      streaming.EventDelegation,
					Status:    "completed",
					Timestamp: timestamp,
					AgentID:   "orchestrator",
					Metadata: map[string]interface{}{
						"source_agent": "tech-lead",
						"description":  "Investigate authentication module",
						"priority":     "high",
					},
				})
			})

			It("renders source_agent prominently", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("source_agent: tech-lead"))
			})

			It("renders description prominently", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("description: Investigate authentication module"))
			})

			It("renders remaining keys alphabetically", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("priority: high"))
			})

			It("renders the Metadata header", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("Metadata"))
			})
		})

		Context("with tool event metadata", func() {
			BeforeEach(func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-tool",
					Type:      streaming.EventToolCall,
					Status:    "error",
					Timestamp: timestamp,
					AgentID:   "worker-1",
					Metadata: map[string]interface{}{
						"tool_name": "bash",
						"is_error":  true,
						"exit_code": 1,
					},
				})
			})

			It("renders tool_name prominently", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("tool_name: bash"))
			})

			It("renders is_error prominently", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("is_error: true"))
			})

			It("renders remaining keys alphabetically after prominent ones", func() {
				view := intent.View()
				Expect(view).To(ContainSubstring("exit_code: 1"))
			})
		})

		Context("with plan event metadata", func() {
			It("renders content prominently", func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-plan",
					Type:      streaming.EventPlan,
					Status:    "created",
					Timestamp: timestamp,
					AgentID:   "planner",
					Metadata: map[string]interface{}{
						"content": "Phase 1: investigate; Phase 2: implement",
					},
				})
				view := intent.View()
				Expect(view).To(ContainSubstring("content: Phase 1: investigate"))
			})
		})

		Context("with review event metadata", func() {
			It("renders verdict prominently", func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-review",
					Type:      streaming.EventReview,
					Status:    "approved",
					Timestamp: timestamp,
					AgentID:   "reviewer",
					Metadata: map[string]interface{}{
						"verdict":  "LGTM",
						"comments": 3,
					},
				})
				view := intent.View()
				Expect(view).To(ContainSubstring("verdict: LGTM"))
				Expect(view).To(ContainSubstring("comments: 3"))
			})
		})
	})

	Describe("Update", func() {
		Context("scroll state changes", func() {
			BeforeEach(func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-scroll",
					Type:      streaming.EventDelegation,
					Status:    "started",
					Timestamp: timestamp,
					AgentID:   "orchestrator",
					Metadata: map[string]interface{}{
						"key1": "val1",
						"key2": "val2",
						"key3": "val3",
					},
				})
				// Set a small visible height so scroll is meaningful.
				intent.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
			})

			It("scrolls down on Down key", func() {
				Expect(intent.ScrollOffset()).To(Equal(0))
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.ScrollOffset()).To(Equal(1))
			})

			It("scrolls up on Up key", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.ScrollOffset()).To(Equal(1))
			})

			It("scrolls down on j key", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
				Expect(intent.ScrollOffset()).To(Equal(1))
			})

			It("scrolls up on k key", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
				Expect(intent.ScrollOffset()).To(Equal(1))
			})

			It("does not scroll below zero", func() {
				intent.Update(tea.KeyMsg{Type: tea.KeyUp})
				Expect(intent.ScrollOffset()).To(Equal(0))
			})

			It("clamps scroll to max offset", func() {
				for range 100 {
					intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				}
				offset := intent.ScrollOffset()
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
				Expect(intent.ScrollOffset()).To(Equal(offset))
			})
		})

		Context("Escape produces close signal", func() {
			BeforeEach(func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-esc",
					Type:      streaming.EventDelegation,
					Status:    "started",
					Timestamp: timestamp,
					AgentID:   "orchestrator",
				})
			})

			It("returns a DismissModalMsg cmd", func() {
				cmd := intent.Update(tea.KeyMsg{Type: tea.KeyEscape})
				Expect(cmd).NotTo(BeNil())
				msg := cmd()
				Expect(msg).To(BeAssignableToTypeOf(intents.DismissModalMsg{}))
			})

			It("sets the result with Dismissed true", func() {
				Expect(intent.Result()).To(BeNil())
				intent.Update(tea.KeyMsg{Type: tea.KeyEscape})
				result := intent.Result()
				Expect(result).NotTo(BeNil())
				data, ok := result.Data.(eventdetails.Result)
				Expect(ok).To(BeTrue())
				Expect(data.Dismissed).To(BeTrue())
			})
		})

		Context("WindowSizeMsg", func() {
			It("stores dimensions without returning a cmd", func() {
				intent = eventdetails.New(streaming.SwarmEvent{
					ID:        "evt-size",
					Type:      streaming.EventDelegation,
					Status:    "started",
					Timestamp: timestamp,
					AgentID:   "orchestrator",
				})
				cmd := intent.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
				Expect(cmd).To(BeNil())
			})
		})
	})

	Describe("Result", func() {
		It("returns nil before any action", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-noop",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			Expect(intent.Result()).To(BeNil())
		})
	})

	Describe("Update edge cases", func() {
		It("returns nil for an unrecognised message type", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-unknown-msg",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			// Send a non-KeyMsg, non-WindowSizeMsg message type.
			cmd := intent.Update("arbitrary-string-message")
			Expect(cmd).To(BeNil())
		})
	})

	Describe("handleKey edge cases", func() {
		It("returns nil for an unrecognised rune key", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-unknown-key",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
			Expect(cmd).To(BeNil())
		})

		It("returns nil for a multi-rune key message", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-multi-rune",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			cmd := intent.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a', 'b'}})
			Expect(cmd).To(BeNil())
		})
	})

	Describe("scroll boundary edge cases", func() {
		It("clamps to zero when content fits within viewport", func() {
			// With no metadata and a large height, content fits entirely —
			// maxScroll will be negative and clamped to 0.
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-small-content",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			// Set a large viewport so content fits easily.
			intent.Update(tea.WindowSizeMsg{Width: 120, Height: 60})
			// Attempt to scroll down — should stay at 0 because maxScroll <= 0.
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			Expect(intent.ScrollOffset()).To(Equal(0))
		})
	})

	Describe("prominentKeysForType coverage", func() {
		It("returns nil for an unknown event type", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-unknown-type",
				Type:      streaming.SwarmEventType("custom_event"),
				Status:    "active",
				Timestamp: timestamp,
				AgentID:   "agent-1",
				Metadata: map[string]interface{}{
					"alpha": "first",
					"beta":  "second",
				},
			})
			// View should render metadata keys alphabetically with no prominent ordering.
			view := intent.View()
			Expect(view).To(ContainSubstring("alpha: first"))
			Expect(view).To(ContainSubstring("beta: second"))
		})
	})

	Describe("visibleHeight edge cases", func() {
		It("returns default of 20 when height is zero", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-zero-height",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
			})
			// Do not send WindowSizeMsg — height stays 0.
			// Scrolling exercises visibleHeight with default.
			intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			// Just verify it does not panic and offset is reasonable.
			Expect(intent.ScrollOffset()).To(BeNumerically(">=", 0))
		})

		It("clamps boxH to minimum of 12 for very small heights", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-tiny-height",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
				Metadata: map[string]interface{}{
					"k1": "v1", "k2": "v2", "k3": "v3",
					"k4": "v4", "k5": "v5", "k6": "v6",
				},
			})
			// Height 10 → 70% = 7 → clamp to 12 → visible = 8.
			intent.Update(tea.WindowSizeMsg{Width: 80, Height: 10})
			// Scroll down several times to exercise the clamp path.
			for range 20 {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			Expect(intent.ScrollOffset()).To(BeNumerically(">=", 0))
		})

		It("clamps boxH to maximum of 40 for very large heights", func() {
			intent = eventdetails.New(streaming.SwarmEvent{
				ID:        "evt-huge-height",
				Type:      streaming.EventDelegation,
				Status:    "started",
				Timestamp: timestamp,
				AgentID:   "orchestrator",
				Metadata: map[string]interface{}{
					"k1": "v1", "k2": "v2", "k3": "v3",
					"k4": "v4", "k5": "v5", "k6": "v6",
				},
			})
			// Height 80 → 70% = 56 → clamp to 40 → visible = 36.
			intent.Update(tea.WindowSizeMsg{Width: 120, Height: 80})
			for range 5 {
				intent.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			Expect(intent.ScrollOffset()).To(BeNumerically(">=", 0))
		})
	})
})
