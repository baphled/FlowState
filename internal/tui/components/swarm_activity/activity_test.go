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
			Expect(output).To(ContainSubstring("delegation"))
			Expect(output).To(ContainSubstring("qa-agent"))
			Expect(output).To(ContainSubstring("started"))
			Expect(output).To(ContainSubstring("tool_call"))
			Expect(output).To(ContainSubstring("senior-engineer"))
			Expect(output).To(ContainSubstring("completed"))

			// The placeholder items should not appear when real events are set.
			Expect(output).NotTo(ContainSubstring("Plan: Wave 2"))
		})

		It("falls back to placeholder items when supplied an empty slice", func() {
			output := pane.WithEvents([]streaming.SwarmEvent{}).Render(80, 10)

			// Placeholder fallback preserves T5 behaviour during Wave 1 transition.
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
			Expect(output).To(ContainSubstring("tool_call"))
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
