package context_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("CompactedMessage and CompactionIndex storage schema", func() {
	Describe("CompactedMessage JSON roundtrip", func() {
		It("preserves every field across marshal → unmarshal", func() {
			original := flowctx.CompactedMessage{
				ID:                 "01HV8N-unit-id",
				OriginalTokenCount: 1500,
				StoragePath:        "/tmp/compacted/sess-1/01HV8N-unit-id.json",
				Checksum:           "deadbeefcafebabe",
				CreatedAt:          time.Date(2026, 4, 14, 12, 0, 0, 0, time.UTC),
				RetrievalCount:     3,
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactedMessage
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip).To(Equal(original))
		})
	})

	Describe("CompactedUnit JSON roundtrip", func() {
		It("preserves a solo message payload", func() {
			original := flowctx.CompactedUnit{
				Kind: flowctx.UnitSolo,
				Messages: []provider.Message{
					{Role: "user", Content: "hello"},
				},
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactedUnit
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip).To(Equal(original))
		})

		It("preserves a parallel fan-out payload with tool calls intact", func() {
			original := flowctx.CompactedUnit{
				Kind: flowctx.UnitToolGroup,
				Messages: []provider.Message{
					{
						Role: "assistant",
						ToolCalls: []provider.ToolCall{
							{ID: "t1", Name: "read", Arguments: map[string]any{"path": "/a"}},
							{ID: "t2", Name: "read", Arguments: map[string]any{"path": "/b"}},
						},
					},
					{Role: "tool", Content: "A", ToolCalls: []provider.ToolCall{{ID: "t1"}}},
					{Role: "tool", Content: "B", ToolCalls: []provider.ToolCall{{ID: "t2"}}},
				},
			}

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactedUnit
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip.Kind).To(Equal(original.Kind))
			Expect(roundtrip.Messages).To(HaveLen(3))
			Expect(roundtrip.Messages[0].Role).To(Equal("assistant"))
			Expect(roundtrip.Messages[0].ToolCalls).To(HaveLen(2))
			Expect(roundtrip.Messages[0].ToolCalls[0].ID).To(Equal("t1"))
			Expect(roundtrip.Messages[0].ToolCalls[0].Arguments["path"]).To(Equal("/a"))
			Expect(roundtrip.Messages[1].ToolCalls[0].ID).To(Equal("t1"))
			Expect(roundtrip.Messages[2].ToolCalls[0].ID).To(Equal("t2"))
		})
	})

	Describe("DefaultMessageCompactor.ShouldCompact (per-unit, ADR atomicity)", func() {
		var (
			compactor *flowctx.DefaultMessageCompactor
			msgs      []provider.Message
		)

		BeforeEach(func() {
			compactor = flowctx.NewDefaultMessageCompactor(10)
			msgs = []provider.Message{
				{Role: "user", Content: "one two three"},                                                                         // 3 tokens (solo)
				{Role: "assistant", Content: "alpha beta gamma delta"},                                                           // 4 tokens (solo)
				{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "t1"}}, Content: "calling"},                              // 1
				{Role: "tool", Content: "result body word word word word word word", ToolCalls: []provider.ToolCall{{ID: "t1"}}}, // 8
			}
		})

		It("returns false when threshold is zero or negative", func() {
			zero := flowctx.NewDefaultMessageCompactor(0)
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			Expect(zero.ShouldCompact(unit, msgs)).To(BeFalse())
		})

		It("returns false for a solo unit at the threshold (strict greater-than)", func() {
			boundary := flowctx.NewDefaultMessageCompactor(3)
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			Expect(boundary.ShouldCompact(unit, msgs)).To(BeFalse())
		})

		It("returns true for a solo unit strictly above threshold", func() {
			boundary := flowctx.NewDefaultMessageCompactor(2)
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			Expect(boundary.ShouldCompact(unit, msgs)).To(BeTrue())
		})

		It("sums tokens across the whole tool-group unit", func() {
			// Tool-group unit covers msgs[2..4]: 1 + 8 = 9 tokens.
			unit := flowctx.Unit{Kind: flowctx.UnitToolGroup, Start: 2, End: 4}
			Expect(compactor.UnitTokenCount(unit, msgs)).To(Equal(9))
			Expect(compactor.ShouldCompact(unit, msgs)).To(BeFalse())

			// Lower the threshold so the *unit* (not any single message)
			// triggers compaction — this is the ADR-mandated per-unit gate.
			low := flowctx.NewDefaultMessageCompactor(8)
			Expect(low.ShouldCompact(unit, msgs)).To(BeTrue())
		})

		It("treats empty content as zero tokens", func() {
			empty := provider.Message{Role: "assistant", Content: ""}
			Expect(compactor.TokenCount(empty)).To(Equal(0))
		})
	})

	Describe("DefaultMessageCompactor.Compact placeholder emission", func() {
		var (
			compactor *flowctx.DefaultMessageCompactor
			msgs      []provider.Message
		)

		BeforeEach(func() {
			compactor = flowctx.NewDefaultMessageCompactor(0)
			msgs = []provider.Message{
				{Role: "user", Content: "u"},
				{Role: "assistant", ToolCalls: []provider.ToolCall{{ID: "t1"}, {ID: "t2"}}},
				{Role: "tool", Content: "A", ToolCalls: []provider.ToolCall{{ID: "t1"}}},
				{Role: "tool", Content: "B", ToolCalls: []provider.ToolCall{{ID: "t2"}}},
			}
		})

		It("emits a single solo Role:user placeholder for a solo unit", func() {
			unit := flowctx.Unit{Kind: flowctx.UnitSolo, Start: 0, End: 1}
			out := compactor.Compact(unit, msgs, "rec-solo")

			Expect(out.Role).To(Equal("user"))
			Expect(out.Content).To(ContainSubstring("rec-solo"))
			Expect(out.Content).To(ContainSubstring("1 message"))
			Expect(out.ToolCalls).To(BeEmpty())
		})

		It("emits a single solo placeholder for a parallel fan-out group, dropping all tool entries", func() {
			unit := flowctx.Unit{Kind: flowctx.UnitToolGroup, Start: 1, End: 4}
			out := compactor.Compact(unit, msgs, "rec-fanout")

			// Per ADR atomicity: the whole (N+1)-message unit becomes a
			// single placeholder. tool_use and tool_result entries are
			// dropped *together*.
			Expect(out.Role).To(Equal("user"))
			Expect(out.ToolCalls).To(BeEmpty())
			Expect(out.Content).To(ContainSubstring("rec-fanout"))
			Expect(out.Content).To(ContainSubstring("3 messages"))
		})

		It("placeholder carries no tool_call_id (ADR view-only / atomicity)", func() {
			unit := flowctx.Unit{Kind: flowctx.UnitToolGroup, Start: 1, End: 4}
			out := compactor.Compact(unit, msgs, "rec")

			Expect(out.Role).NotTo(Equal("tool"))
			for _, tc := range out.ToolCalls {
				Expect(tc.ID).To(BeEmpty())
			}
		})
	})

	Describe("CompactionIndex", func() {
		It("NewCompactionIndex initialises an empty, session-bound index", func() {
			idx := flowctx.NewCompactionIndex("sess-42")

			Expect(idx.SessionID).To(Equal("sess-42"))
			Expect(idx.Entries).NotTo(BeNil())
			Expect(idx.Entries).To(BeEmpty())
			Expect(idx.UpdatedAt.IsZero()).To(BeTrue())
		})

		It("survives JSON roundtrip with entries", func() {
			original := flowctx.NewCompactionIndex("sess-42")
			original.Entries["e1"] = flowctx.CompactedMessage{
				ID:                 "e1",
				OriginalTokenCount: 900,
				StoragePath:        "/tmp/compacted/sess-42/e1.json",
				Checksum:           "00ff",
				CreatedAt:          time.Unix(1700000000, 0).UTC(),
			}
			original.UpdatedAt = time.Unix(1700000001, 0).UTC()

			data, err := json.Marshal(original)
			Expect(err).NotTo(HaveOccurred())

			var roundtrip flowctx.CompactionIndex
			Expect(json.Unmarshal(data, &roundtrip)).To(Succeed())

			Expect(roundtrip.SessionID).To(Equal("sess-42"))
			Expect(roundtrip.Entries).To(HaveKey("e1"))
			Expect(roundtrip.Entries["e1"].OriginalTokenCount).To(Equal(900))
			Expect(roundtrip.UpdatedAt.Equal(original.UpdatedAt)).To(BeTrue())
		})
	})
})
