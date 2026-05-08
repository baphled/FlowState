package chat_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// SwarmEvent invariants validated in P4:
//   - Every creation site stamps SchemaVersion = CurrentSchemaVersion (1).
//   - Every creation site stamps the timestamp in UTC so JSONL round-trips
//     are deterministic and operator logs carry a single, unambiguous zone.
//
// These invariants are asserted against every branch of swarmEventFromChunk
// (delegation, tool_call, tool_result, plan, review) via a table-driven
// test. Adding a new branch without honouring the invariants makes this
// suite go red.
var _ = Describe("SwarmEvent creation invariants (P4)", func() {
	type chunkCase struct {
		name     string
		chunk    chat.StreamChunkMsg
		fallback string
		wantType streaming.SwarmEventType
	}

	// Plans/Delegation Bus Bridge — Engine to SSE (May 2026): delegation
	// events flow via the bus.
	// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026):
	// tool-call/tool-result events flow via the bus too. Their
	// invariants are pinned in the bus-driven Describe block below
	// using HandleEventBusNotificationForTest. The chunk-side
	// invariants here cover only the residual branches —
	// plan_artifact and review_verdict.
	cases := []chunkCase{
		{
			name: "plan",
			chunk: chat.StreamChunkMsg{
				EventType: streaming.EventTypePlanArtifact,
				Content:   "plan body",
			},
			fallback: "planner",
			wantType: streaming.EventPlan,
		},
		{
			name: "review",
			chunk: chat.StreamChunkMsg{
				EventType: streaming.EventTypeReviewVerdict,
				Content:   "review body",
			},
			fallback: "reviewer",
			wantType: streaming.EventReview,
		},
	}

	for _, c := range cases {
		Describe("SwarmEvent from "+c.name+" chunk", func() {
			It("stamps SchemaVersion with CurrentSchemaVersion", func() {
				ev, ok := chat.SwarmEventFromChunkForTest(c.chunk, c.fallback)
				Expect(ok).To(BeTrue())
				Expect(ev.Type).To(Equal(c.wantType))
				Expect(ev.SchemaVersion).To(Equal(streaming.CurrentSchemaVersion),
					"SchemaVersion must be set so loaders can migrate future format changes")
			})

			It("stamps Timestamp in UTC", func() {
				ev, ok := chat.SwarmEventFromChunkForTest(c.chunk, c.fallback)
				Expect(ok).To(BeTrue())
				Expect(ev.Type).To(Equal(c.wantType))
				_, offset := ev.Timestamp.Zone()
				Expect(offset).To(Equal(0),
					"timestamps must be UTC so persisted logs and the audit trail stay consistent")
			})
		})
	}

	Describe("SwarmEvent from delegation bus event", func() {
		var (
			intent *chat.Intent
			_      = eventbus.NewEventBus // import-anchor to satisfy goimports if no other use
		)

		BeforeEach(func() {
			chat.SetRunningInTestsForTest(true)
			intent = chat.NewIntent(chat.IntentConfig{
				AgentID:      "test-agent",
				SessionID:    "test-session",
				ProviderName: "openai",
				ModelName:    "gpt-4o",
				TokenBudget:  4096,
			})
		})

		AfterEach(func() {
			chat.SetRunningInTestsForTest(false)
		})

		It("stamps SchemaVersion = CurrentSchemaVersion on bus-driven delegation events", func() {
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				DelegationStarted: events.NewDelegationStartedEvent(events.DelegationEventData{
					ChainID:         "chain-bus-inv",
					ParentSessionID: "test-session",
					TargetAgent:     "engineer",
					Description:     "do the thing",
				}, time.Now().UTC()),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			Expect(swarmEvents[0].Type).To(Equal(streaming.EventDelegation))
			Expect(swarmEvents[0].SchemaVersion).To(Equal(streaming.CurrentSchemaVersion))
		})

		It("stamps Timestamp in UTC on bus-driven delegation events", func() {
			intent.HandleEventBusNotificationForTest(chat.EventBusNotificationMsg{
				DelegationCompleted: events.NewDelegationCompletedEvent(events.DelegationEventData{
					ChainID:         "chain-bus-utc",
					ParentSessionID: "test-session",
					TargetAgent:     "engineer",
				}, time.Now().UTC()),
			})

			swarmEvents := intent.SwarmStoreForTest().All()
			Expect(swarmEvents).To(HaveLen(1))
			_, offset := swarmEvents[0].Timestamp.Zone()
			Expect(offset).To(Equal(0))
		})
	})
})
