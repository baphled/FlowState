package swarmactivity_test

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

func TestSwarmActivity(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "SwarmActivity Suite")
}

var _ = Describe("SwarmActivityPane", func() {
	Describe("NewSwarmActivityPane", func() {
		It("returns a non-nil pane", func() {
			Expect(swarmactivity.NewSwarmActivityPane()).NotTo(BeNil())
		})
	})

	Describe("Render", func() {
		var pane *swarmactivity.SwarmActivityPane

		BeforeEach(func() {
			pane = swarmactivity.NewSwarmActivityPane()
		})

		Context("at a comfortable width and height", func() {
			It("renders the header as the first line", func() {
				output := pane.Render(80, 10)
				Expect(output).NotTo(BeEmpty())
				firstLine := strings.Split(output, "\n")[0]
				Expect(firstLine).To(ContainSubstring("Activity Timeline"))
			})

			It("renders between three and five placeholder timeline items", func() {
				output := pane.Render(80, 10)
				lines := strings.Split(output, "\n")
				// Skip the header line, count body lines with the bullet marker.
				var items int
				for _, line := range lines[1:] {
					if strings.Contains(line, "▸") {
						items++
					}
				}
				Expect(items).To(BeNumerically(">=", 3))
				Expect(items).To(BeNumerically("<=", 5))
			})
		})

		Context("with limited width", func() {
			It("truncates long body lines with an ellipsis suffix", func() {
				output := pane.Render(20, 10)
				lines := strings.Split(output, "\n")
				// At least one body line must have been truncated.
				var truncated bool
				for _, line := range lines[1:] {
					if strings.HasSuffix(line, "…") {
						truncated = true
					}
					// No body line exceeds the declared width when measured visually.
					Expect(lipgloss.Width(line)).To(BeNumerically("<=", 20))
				}
				Expect(truncated).To(BeTrue(), "expected at least one line to be truncated with an ellipsis")
			})

			It("truncates CJK content without splitting a rune mid-byte", func() {
				// Japanese body — each character is a multi-byte rune and visually
				// two cells wide. Truncation must measure by visual width, not bytes,
				// and never emit a half-rune.
				events := []streaming.SwarmEvent{
					{
						Type:    streaming.EventToolCall,
						Status:  "started",
						AgentID: "エージェント名",
					},
				}

				output := pane.WithEvents(events).Render(20, 5)

				lines := strings.Split(output, "\n")
				for _, line := range lines {
					Expect(lipgloss.Width(line)).To(BeNumerically("<=", 20),
						"no line may exceed the declared width even with multi-byte runes")
					// Every byte prefix must be valid UTF-8 — if the truncation
					// split a rune, strings.ToValidUTF8 with a replacement would differ.
					Expect(strings.ToValidUTF8(line, "?")).To(Equal(line),
						"truncation must not produce invalid UTF-8")
				}
			})

			It("preserves the ▸ bullet glyph when truncation is required", func() {
				events := []streaming.SwarmEvent{
					{
						Type:    streaming.EventDelegation,
						Status:  "started",
						AgentID: "an-agent-name-that-is-much-longer-than-any-reasonable-width",
					},
				}

				// Width 15 forces aggressive truncation but the bullet should survive.
				output := pane.WithEvents(events).Render(15, 5)

				body := strings.Split(output, "\n")
				found := false
				for _, line := range body[1:] {
					if strings.Contains(line, "▸") {
						found = true
						Expect(lipgloss.Width(line)).To(BeNumerically("<=", 15))
					}
				}
				Expect(found).To(BeTrue(), "the ▸ bullet must survive even the tightest truncation")
			})

			It("handles emoji and wide glyphs in agent IDs without panicking", func() {
				events := []streaming.SwarmEvent{
					{Type: streaming.EventToolCall, Status: "ok", AgentID: "🤖-bot"},
					{Type: streaming.EventPlan, Status: "done", AgentID: "测试"},
				}

				Expect(func() {
					_ = pane.WithEvents(events).Render(30, 10)
				}).NotTo(Panic())
			})
		})

		Context("with limited height", func() {
			It("clamps total rendered lines to the declared height", func() {
				output := pane.Render(80, 2)
				lines := strings.Split(output, "\n")
				Expect(len(lines)).To(BeNumerically("<=", 2))
				Expect(lines[0]).To(ContainSubstring("Activity Timeline"))
			})
		})

		Context("below the minimum usable thresholds", func() {
			It("returns an empty string when width is below the minimum", func() {
				Expect(pane.Render(9, 10)).To(BeEmpty())
			})

			It("returns an empty string when height is below the minimum", func() {
				Expect(pane.Render(80, 1)).To(BeEmpty())
			})

			It("returns empty for negative dimensions without panicking", func() {
				Expect(func() { _ = pane.Render(-5, -5) }).NotTo(Panic())
				Expect(pane.Render(-5, -5)).To(BeEmpty())
			})
		})

		Context("at the exact minimum usable thresholds", func() {
			It("renders at exactly the minimum width (10)", func() {
				output := pane.Render(10, 5)
				Expect(output).NotTo(BeEmpty(), "width 10 is the minimum and must render")
			})
		})
	})

	Describe("default visibleTypes P2 T2 / P3 coalesce (EventToolResult)", func() {
		It("coalesces a matching tool_call + tool_result into the call's line with the result's status", func() {
			pane := swarmactivity.NewSwarmActivityPane()

			// P3 A4: tool_result events no longer render as their own
			// line. Instead, the default visibility must allow the
			// coalesce step to find the tool_result and derive the
			// tool_call's displayed status from it. Verify end-to-end:
			// two events in, one line out, with the result's status.
			events := []streaming.SwarmEvent{
				{
					ID:      "toolu_01TR",
					Type:    streaming.EventToolCall,
					Status:  "started",
					AgentID: "tool-agent",
				},
				{
					ID:      "toolu_01TR",
					Type:    streaming.EventToolResult,
					Status:  "completed",
					AgentID: "tool-agent",
				},
			}
			output := pane.WithEvents(events).Render(80, 10)
			Expect(output).To(ContainSubstring("Tool Call"),
				"the coalesced line must identify itself as a Tool Call (human label)")
			Expect(output).To(ContainSubstring("completed"),
				"the coalesced line's status must reflect the paired tool_result")
			// Count body bullet lines: coalesce yields exactly one.
			lines := strings.Split(output, "\n")
			bullets := 0
			for _, line := range lines {
				if strings.Contains(line, "▸") {
					bullets++
				}
			}
			Expect(bullets).To(Equal(1),
				"tool_call + tool_result with the same ID must collapse into one body line")
		})
	})

	Describe("WithVisibleTypes", func() {
		var pane *swarmactivity.SwarmActivityPane

		BeforeEach(func() {
			pane = swarmactivity.NewSwarmActivityPane()
		})

		It("is chainable and returns the receiver", func() {
			out := pane.WithVisibleTypes(map[streaming.SwarmEventType]bool{
				streaming.EventDelegation: true,
				streaming.EventToolCall:   true,
				streaming.EventPlan:       true,
				streaming.EventReview:     true,
			})
			Expect(out).To(BeIdenticalTo(pane))
		})

		Context("with default (all visible)", func() {
			It("does not render a filter indicator line", func() {
				events := []streaming.SwarmEvent{
					{Type: streaming.EventDelegation, Status: "started", AgentID: "a1"},
					{Type: streaming.EventToolCall, Status: "ok", AgentID: "a2"},
				}
				output := pane.WithEvents(events).Render(80, 10)
				Expect(output).NotTo(ContainSubstring("[D]"))
				Expect(output).NotTo(ContainSubstring("[T]"))
				Expect(output).NotTo(ContainSubstring("[P]"))
				Expect(output).NotTo(ContainSubstring("[R]"))
			})

			It("does not render a count summary in the header", func() {
				events := []streaming.SwarmEvent{
					{Type: streaming.EventDelegation, Status: "started", AgentID: "a1"},
				}
				output := pane.WithEvents(events).Render(80, 10)
				Expect(output).NotTo(ContainSubstring("showing"))
			})
		})

		Context("with one type hidden", func() {
			var events []streaming.SwarmEvent

			BeforeEach(func() {
				events = []streaming.SwarmEvent{
					{Type: streaming.EventDelegation, Status: "started", AgentID: "del-agent"},
					{Type: streaming.EventToolCall, Status: "ok", AgentID: "tool-agent"},
					{Type: streaming.EventPlan, Status: "done", AgentID: "plan-agent"},
				}
				pane.WithEvents(events).WithVisibleTypes(map[streaming.SwarmEventType]bool{
					streaming.EventDelegation: true,
					streaming.EventToolCall:   false,
					streaming.EventPlan:       true,
					streaming.EventReview:     true,
				})
			})

			It("excludes the hidden type from the rendered body", func() {
				output := pane.Render(80, 10)
				Expect(output).To(ContainSubstring("Delegation"))
				Expect(output).To(ContainSubstring("Plan"))
				Expect(output).NotTo(ContainSubstring("Tool Call"))
			})

			It("renders the filter indicator line", func() {
				output := pane.Render(80, 10)
				Expect(output).To(ContainSubstring("[D]"))
				Expect(output).To(ContainSubstring("[T]"))
				Expect(output).To(ContainSubstring("[P]"))
				Expect(output).To(ContainSubstring("[R]"))
			})

			It("renders a count summary showing 2 of 3", func() {
				output := pane.Render(80, 10)
				Expect(output).To(ContainSubstring("showing 2 of 3"))
			})
		})

		Context("with multiple types hidden", func() {
			It("only renders matching event types", func() {
				events := []streaming.SwarmEvent{
					{Type: streaming.EventDelegation, Status: "s", AgentID: "a1"},
					{Type: streaming.EventToolCall, Status: "s", AgentID: "a2"},
					{Type: streaming.EventPlan, Status: "s", AgentID: "a3"},
					{Type: streaming.EventReview, Status: "s", AgentID: "a4"},
				}
				pane.WithEvents(events).WithVisibleTypes(map[streaming.SwarmEventType]bool{
					streaming.EventDelegation: false,
					streaming.EventToolCall:   false,
					streaming.EventPlan:       true,
					streaming.EventReview:     false,
				})

				output := pane.Render(80, 10)
				Expect(output).To(ContainSubstring("Plan"))
				Expect(output).NotTo(ContainSubstring("Delegation"))
				Expect(output).NotTo(ContainSubstring("Tool Call"))
				Expect(output).NotTo(ContainSubstring("Review"))
				Expect(output).To(ContainSubstring("showing 1 of 4"))
			})
		})

		Context("with all types hidden", func() {
			It("renders a helpful 'all hidden' message instead of a bare count (P8 T3)", func() {
				// F17: "showing 0 of 2" is ambiguous when the user has
				// filtered everything out. Replace with actionable guidance
				// that tells them how to recover.
				events := []streaming.SwarmEvent{
					{Type: streaming.EventDelegation, Status: "s", AgentID: "a1"},
					{Type: streaming.EventToolCall, Status: "s", AgentID: "a2"},
				}
				pane.WithEvents(events).WithVisibleTypes(map[streaming.SwarmEventType]bool{
					streaming.EventDelegation: false,
					streaming.EventToolCall:   false,
					streaming.EventPlan:       false,
					streaming.EventReview:     false,
				})

				output := pane.Render(80, 10)
				Expect(output).To(ContainSubstring("Activity Timeline"))
				// The old bare "showing 0 of 2" must be gone — it was the
				// confusing case; keep the actionable message instead.
				Expect(output).NotTo(ContainSubstring("showing 0 of 2"))
				Expect(output).To(ContainSubstring("All events hidden"))
				Expect(output).To(ContainSubstring("[T]"))
				// No event body lines (no bullet markers).
				lines := strings.Split(output, "\n")
				for _, line := range lines {
					Expect(line).NotTo(ContainSubstring("▸"))
				}
			})
		})

		Context("count indicator accuracy", func() {
			It("reports the correct counts for mixed visibility", func() {
				events := make([]streaming.SwarmEvent, 0, 10)
				for range 5 {
					events = append(events, streaming.SwarmEvent{
						Type: streaming.EventDelegation, Status: "s", AgentID: "a",
					})
				}
				for range 5 {
					events = append(events, streaming.SwarmEvent{
						Type: streaming.EventToolCall, Status: "s", AgentID: "b",
					})
				}
				pane.WithEvents(events).WithVisibleTypes(map[streaming.SwarmEventType]bool{
					streaming.EventDelegation: true,
					streaming.EventToolCall:   false,
					streaming.EventPlan:       true,
					streaming.EventReview:     true,
				})

				output := pane.Render(80, 20)
				Expect(output).To(ContainSubstring("showing 5 of 10"))
			})
		})

		Context("filter indicator format", func() {
			It("contains all four type labels when filtering is active", func() {
				events := []streaming.SwarmEvent{
					{Type: streaming.EventDelegation, Status: "s", AgentID: "a"},
				}
				pane.WithEvents(events).WithVisibleTypes(map[streaming.SwarmEventType]bool{
					streaming.EventDelegation: true,
					streaming.EventToolCall:   true,
					streaming.EventPlan:       true,
					streaming.EventReview:     false, // one hidden to activate filter line
				})

				output := pane.Render(80, 10)
				// All four tags must appear in the output.
				Expect(output).To(ContainSubstring("[D]"))
				Expect(output).To(ContainSubstring("[T]"))
				Expect(output).To(ContainSubstring("[P]"))
				Expect(output).To(ContainSubstring("[R]"))
			})
		})

		Context("without events (placeholder mode)", func() {
			It("does not render count summary even when filter is active", func() {
				pane.WithVisibleTypes(map[streaming.SwarmEventType]bool{
					streaming.EventDelegation: false,
					streaming.EventToolCall:   true,
					streaming.EventPlan:       true,
					streaming.EventReview:     true,
				})

				output := pane.Render(80, 10)
				// Placeholder mode: no count summary since events slice is empty.
				Expect(output).NotTo(ContainSubstring("showing"))
				// But filter indicator IS rendered because a type is hidden.
				Expect(output).To(ContainSubstring("[D]"))
			})
		})
	})

	Describe("WithEvents", func() {
		var pane *swarmactivity.SwarmActivityPane

		BeforeEach(func() {
			pane = swarmactivity.NewSwarmActivityPane()
		})

		It("is chainable and returns the receiver", func() {
			out := pane.WithEvents(nil)
			Expect(out).To(BeIdenticalTo(pane))
		})

		It("renders the supplied events in place of the placeholders", func() {
			now := time.Unix(1_700_000_000, 0)
			events := []streaming.SwarmEvent{
				{
					ID:        "evt-1",
					Type:      streaming.EventDelegation,
					Status:    "started",
					Timestamp: now,
					AgentID:   "qa-agent",
				},
				{
					ID:        "evt-2",
					Type:      streaming.EventToolCall,
					Status:    "completed",
					Timestamp: now,
					AgentID:   "senior-engineer",
				},
			}

			output := pane.WithEvents(events).Render(80, 10)

			Expect(output).To(ContainSubstring("Activity Timeline"))
			Expect(output).To(ContainSubstring("Delegation"))
			Expect(output).To(ContainSubstring("qa-agent"))
			Expect(output).To(ContainSubstring("started"))
			Expect(output).To(ContainSubstring("Tool Call"))
			Expect(output).To(ContainSubstring("senior-engineer"))
			Expect(output).To(ContainSubstring("completed"))

			// The placeholder items should not appear when real events are set.
			Expect(output).NotTo(ContainSubstring("Plan: Wave 2"))
		})

		It("shows the empty-state text when supplied an explicit empty slice", func() {
			// P3 A1: an empty-but-non-nil slice is the caller's way of
			// asserting "I've loaded state; the timeline is genuinely
			// empty". The pane must switch to the empty-state text and
			// never revert to placeholder items again.
			output := pane.WithEvents([]streaming.SwarmEvent{}).Render(80, 10)

			Expect(output).To(ContainSubstring("Activity Timeline"))
			Expect(output).To(ContainSubstring("No activity yet"))
			Expect(output).NotTo(ContainSubstring("Plan: Wave 2"))
		})

		It("preserves placeholder mode when supplied a nil slice", func() {
			// A nil slice is not an assertion of loaded state — it signals
			// "no snapshot available yet" (early startup). Placeholder
			// items must remain visible so the pane has useful content
			// before a session is loaded.
			output := pane.WithEvents(nil).Render(80, 10)

			Expect(output).To(ContainSubstring("Activity Timeline"))
			Expect(output).To(ContainSubstring("▸"))
		})

		It("renders a dash placeholder for events with empty AgentID or Status", func() {
			events := []streaming.SwarmEvent{
				{
					ID:   "evt-empty",
					Type: streaming.EventToolCall,
					// AgentID and Status intentionally empty.
				},
			}

			output := pane.WithEvents(events).Render(80, 10)
			Expect(output).To(ContainSubstring("Tool Call"))
			// formatEvent substitutes "-" for empty AgentID/Status so the
			// line is always structurally complete.
			Expect(output).To(ContainSubstring("· - · -"))
		})

		It("renders events oldest first so overflow trims the top of the body", func() {
			events := make([]streaming.SwarmEvent, 0, 3)
			for _, id := range []string{"first", "middle", "last"} {
				events = append(events, streaming.SwarmEvent{
					ID:      id,
					Type:    streaming.EventToolCall,
					Status:  "completed",
					AgentID: id + "-agent",
				})
			}

			output := pane.WithEvents(events).Render(80, 10)
			lines := strings.Split(output, "\n")

			// Header first, then body lines in insertion order.
			Expect(lines[0]).To(ContainSubstring("Activity Timeline"))

			var bodyIdx []int
			for idx, line := range lines {
				if strings.Contains(line, "-agent") {
					bodyIdx = append(bodyIdx, idx)
				}
			}
			Expect(bodyIdx).To(HaveLen(3))
			Expect(lines[bodyIdx[0]]).To(ContainSubstring("first"))
			Expect(lines[bodyIdx[1]]).To(ContainSubstring("middle"))
			Expect(lines[bodyIdx[2]]).To(ContainSubstring("last"))
		})
	})
})
