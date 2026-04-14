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
