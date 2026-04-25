package swarmactivity_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
	swarmactivity "github.com/baphled/flowstate/internal/tui/components/swarm_activity"
)

// These specs pin the SwarmActivityPane render-cache invalidation
// contract: cache hits must NOT mask real changes to inputs that
// influence the rendered output. The cache key (event count + first/
// last event ID + width + height + profile + visible-types mask) is
// the entire fingerprint; if any of these inputs changes, the next
// Render must produce fresh output.
var _ = Describe("SwarmActivityPane render cache", func() {
	mkEvent := func(id string, ts time.Time) streaming.SwarmEvent {
		return streaming.SwarmEvent{
			ID:        id,
			Type:      streaming.EventToolCall,
			Status:    "started",
			AgentID:   "agent-a",
			Timestamp: ts,
		}
	}

	Describe("idempotent renders", func() {
		It("returns identical output for repeated identical inputs", func() {
			pane := swarmactivity.NewSwarmActivityPane()
			events := []streaming.SwarmEvent{mkEvent("e1", time.Now())}
			pane.WithEvents(events)

			first := pane.Render(40, 10)
			second := pane.Render(40, 10)

			Expect(second).To(Equal(first))
		})
	})

	Describe("invalidation when a new event arrives", func() {
		It("rerenders so the new event is visible", func() {
			pane := swarmactivity.NewSwarmActivityPane()
			start := time.Now()
			events := []streaming.SwarmEvent{mkEvent("first-event", start)}
			pane.WithEvents(events)

			before := pane.Render(40, 10)

			events = append(events, mkEvent("second-event", start.Add(time.Second)))
			pane.WithEvents(events)
			after := pane.Render(40, 10)

			Expect(after).NotTo(Equal(before),
				"appending a new event must invalidate the cache so the next render reflects it")
		})
	})

	Describe("invalidation when the head event evicts (capacity overflow)", func() {
		It("rerenders with the new tip event visible (and the evicted one gone)", func() {
			pane := swarmactivity.NewSwarmActivityPane()
			start := time.Now()
			// Distinct agent IDs so the rendered output reflects the
			// shift; same slice length to simulate the at-capacity
			// eviction path (oldest dropped, newest appended).
			mk := func(id, agent string, ts time.Time) streaming.SwarmEvent {
				return streaming.SwarmEvent{
					ID:        id,
					Type:      streaming.EventToolCall,
					Status:    "started",
					AgentID:   agent,
					Timestamp: ts,
				}
			}
			eventsBefore := []streaming.SwarmEvent{
				mk("evictee", "evictee-agent", start),
				mk("middle", "middle-agent", start.Add(time.Second)),
				mk("tip-1", "tip1-agent", start.Add(2*time.Second)),
			}
			eventsAfter := []streaming.SwarmEvent{
				mk("middle", "middle-agent", start.Add(time.Second)),
				mk("tip-1", "tip1-agent", start.Add(2*time.Second)),
				mk("tip-2", "tip2-agent", start.Add(3*time.Second)),
			}
			pane.WithEvents(eventsBefore)
			before := pane.Render(60, 12)
			Expect(before).To(ContainSubstring("evictee-agent"))

			pane.WithEvents(eventsAfter)
			after := pane.Render(60, 12)
			Expect(after).To(ContainSubstring("tip2-agent"),
				"capacity-driven eviction shifts first/last event IDs; cache must invalidate")
			Expect(after).NotTo(ContainSubstring("evictee-agent"),
				"the evicted agent must not appear in the post-eviction render")
		})
	})

	Describe("invalidation when the visible-types filter changes", func() {
		It("rerenders so the filter indicator updates", func() {
			pane := swarmactivity.NewSwarmActivityPane()
			pane.WithEvents([]streaming.SwarmEvent{mkEvent("e1", time.Now())})

			allVisible := map[streaming.SwarmEventType]bool{
				streaming.EventDelegation: true,
				streaming.EventToolCall:   true,
				streaming.EventToolResult: true,
				streaming.EventPlan:       true,
				streaming.EventReview:     true,
			}
			pane.WithVisibleTypes(allVisible)
			before := pane.Render(40, 10)

			toolCallsOnly := map[streaming.SwarmEventType]bool{
				streaming.EventDelegation: false,
				streaming.EventToolCall:   true,
				streaming.EventToolResult: true,
				streaming.EventPlan:       false,
				streaming.EventReview:     false,
			}
			pane.WithVisibleTypes(toolCallsOnly)
			after := pane.Render(40, 10)

			Expect(after).NotTo(Equal(before),
				"toggling visible types must invalidate the cache so the filter indicator updates")
		})
	})

	Describe("invalidation when width changes", func() {
		It("rerenders at the new width", func() {
			pane := swarmactivity.NewSwarmActivityPane()
			pane.WithEvents([]streaming.SwarmEvent{mkEvent("e1", time.Now())})

			narrow := pane.Render(20, 10)
			wide := pane.Render(60, 10)

			Expect(wide).NotTo(Equal(narrow),
				"different widths produce different layouts — cache must key on width")
		})
	})
})
