package streaming_test

import (
	"bytes"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("SwarmEventType P2 T2 (EventToolResult)", func() {
	It("exposes EventToolResult with the canonical tool_result string value", func() {
		Expect(string(streaming.EventToolResult)).To(Equal("tool_result"),
			"EventToolResult must use the canonical tool_result discriminator so "+
				"it matches the provider-level tool_result chunk EventType")
	})

	It("is distinct from the other four SwarmEventType values", func() {
		Expect(streaming.EventToolResult).NotTo(Equal(streaming.EventDelegation))
		Expect(streaming.EventToolResult).NotTo(Equal(streaming.EventToolCall))
		Expect(streaming.EventToolResult).NotTo(Equal(streaming.EventPlan))
		Expect(streaming.EventToolResult).NotTo(Equal(streaming.EventReview))
	})

	It("round-trips a tool_result event through JSONL persistence", func() {
		refTime := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
		ev := streaming.SwarmEvent{
			ID:        "toolu_01TR",
			Type:      streaming.EventToolResult,
			Status:    "completed",
			Timestamp: refTime,
			AgentID:   "senior-engineer",
			Metadata:  map[string]interface{}{"tool_name": "bash", "is_error": false},
		}

		var buf bytes.Buffer
		err := streaming.WriteEventsJSONL(&buf, []streaming.SwarmEvent{ev})
		Expect(err).NotTo(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring(`"tool_result"`))

		events, err := streaming.ReadEventsJSONL(strings.NewReader(buf.String()))
		Expect(err).NotTo(HaveOccurred())
		Expect(events).To(HaveLen(1))
		Expect(events[0].Type).To(Equal(streaming.EventToolResult))
		Expect(events[0].ID).To(Equal("toolu_01TR"))
		Expect(events[0].AgentID).To(Equal("senior-engineer"))
	})
})
