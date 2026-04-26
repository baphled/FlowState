package streaming_test

import (
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

// T10b EventTypeContextCompacted contract.
//
// These tests pin the streaming-layer half of [[ADR - Tool-Call Atomicity in
// Context Compaction]]: the streaming package must expose a dedicated
// EventTypeContextCompacted constant and ContextCompactedEvent struct
// distinct from EventTypeRecallSummarized. Overloading the recall-summarised
// event would conflate recall summarisation (emitted by
// internal/recall/query_tools.go) with context compaction (emitted by
// internal/engine buildContextWindow), making downstream subscribers unable
// to tell which fired.
var _ = Describe("EventTypeContextCompacted contract", func() {
	It("uses the stable wire name 'context_compacted'", func() {
		Expect(streaming.EventTypeContextCompacted).To(Equal("context_compacted"))
	})

	It("differs from EventTypeRecallSummarized per the ADR", func() {
		Expect(streaming.EventTypeContextCompacted).NotTo(Equal(streaming.EventTypeRecallSummarized))
	})

	It("ContextCompactedEvent.Type() returns the matching discriminator", func() {
		var e streaming.Event = streaming.ContextCompactedEvent{}
		Expect(e.Type()).To(Equal(streaming.EventTypeContextCompacted))
	})

	It("survives JSON marshal/unmarshal via the discriminator-aware envelope", func() {
		original := streaming.ContextCompactedEvent{
			OriginalTokens: 4200,
			SummaryTokens:  560,
			LatencyMS:      1234,
			AgentID:        "t10b-agent",
		}

		data, err := streaming.MarshalEvent(original)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring(`"type":"context_compacted"`))

		decoded, err := streaming.UnmarshalEvent(data)
		Expect(err).NotTo(HaveOccurred())
		got, ok := decoded.(streaming.ContextCompactedEvent)
		Expect(ok).To(BeTrue(), "UnmarshalEvent returned %T; want ContextCompactedEvent", decoded)
		Expect(got).To(Equal(original))
	})

	It("marshals using the JSON field names from the T10b payload contract", func() {
		e := streaming.ContextCompactedEvent{
			OriginalTokens: 1,
			SummaryTokens:  2,
			LatencyMS:      3,
			AgentID:        "x",
		}
		data, err := json.Marshal(e)
		Expect(err).NotTo(HaveOccurred())
		body := string(data)
		for _, key := range []string{`"originalTokens"`, `"summaryTokens"`, `"latencyMs"`, `"agentId"`} {
			Expect(strings.Contains(body, key)).To(BeTrue(),
				"marshalled body %s missing expected key %s", body, key)
		}
	})
})
