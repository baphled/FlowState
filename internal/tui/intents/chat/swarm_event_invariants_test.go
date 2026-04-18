package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
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

	cases := []chunkCase{
		{
			name: "delegation",
			chunk: chat.StreamChunkMsg{
				DelegationInfo: &provider.DelegationInfo{
					ChainID:     "chain-1",
					SourceAgent: "orchestrator",
					TargetAgent: "engineer",
					Status:      "started",
					Description: "do the thing",
				},
			},
			fallback: "orchestrator",
			wantType: streaming.EventDelegation,
		},
		{
			name: "tool_call",
			chunk: chat.StreamChunkMsg{
				ToolCallID:   "toolu_01",
				ToolCallName: "bash",
				ToolStatus:   "started",
			},
			fallback: "engineer",
			wantType: streaming.EventToolCall,
		},
		{
			name: "tool_result",
			chunk: chat.StreamChunkMsg{
				EventType:  "tool_result",
				ToolCallID: "toolu_01",
				ToolResult: "ok",
			},
			fallback: "engineer",
			wantType: streaming.EventToolResult,
		},
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
})
