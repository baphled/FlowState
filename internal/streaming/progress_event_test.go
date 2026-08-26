package streaming_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("ProgressEvent + CompletionNotificationEvent", func() {
	// round-trips progress events: MarshalEvent + UnmarshalEvent
	// preserve every ProgressEvent field byte-identically.
	It("round-trips ProgressEvent through Marshal/Unmarshal", func() {
		original := streaming.ProgressEvent{
			TaskID:            "task-1",
			ToolCallCount:     7,
			LastTool:          "grep",
			ActiveDelegations: 2,
			ElapsedTime:       9 * time.Second,
			AgentID:           "agent-1",
		}

		data, err := streaming.MarshalEvent(original)
		Expect(err).NotTo(HaveOccurred(), "MarshalEvent")

		restored, err := streaming.UnmarshalEvent(data)
		Expect(err).NotTo(HaveOccurred(), "UnmarshalEvent")
		Expect(restored).To(Equal(original), "round-trip mismatch")
	})

	// round-trips completion notifications: same contract for the
	// completion variant.
	It("round-trips CompletionNotificationEvent through Marshal/Unmarshal", func() {
		original := streaming.CompletionNotificationEvent{
			TaskID:      "task-1",
			Description: "delegation complete",
			Agent:       "worker",
			Duration:    9 * time.Second,
			Status:      "completed",
			Result:      "ok",
			AgentID:     "agent-1",
		}

		data, err := streaming.MarshalEvent(original)
		Expect(err).NotTo(HaveOccurred(), "MarshalEvent")

		restored, err := streaming.UnmarshalEvent(data)
		Expect(err).NotTo(HaveOccurred(), "UnmarshalEvent")
		Expect(restored).To(Equal(original), "round-trip mismatch")
	})
})
