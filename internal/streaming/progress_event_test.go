package streaming_test

import (
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/streaming"
)

func TestProgressEvent(t *testing.T) {
	t.Run("round-trips progress events", func(t *testing.T) {
		original := streaming.ProgressEvent{
			TaskID:            "task-1",
			ToolCallCount:     7,
			LastTool:          "grep",
			ActiveDelegations: 2,
			ElapsedTime:       9 * time.Second,
			AgentID:           "agent-1",
		}

		data, err := streaming.MarshalEvent(original)
		if err != nil {
			t.Fatalf("MarshalEvent() error = %v", err)
		}

		restored, err := streaming.UnmarshalEvent(data)
		if err != nil {
			t.Fatalf("UnmarshalEvent() error = %v", err)
		}

		if restored != original {
			t.Fatalf("round-trip mismatch: got %#v want %#v", restored, original)
		}
	})

	t.Run("round-trips completion notifications", func(t *testing.T) {
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
		if err != nil {
			t.Fatalf("MarshalEvent() error = %v", err)
		}

		restored, err := streaming.UnmarshalEvent(data)
		if err != nil {
			t.Fatalf("UnmarshalEvent() error = %v", err)
		}

		if restored != original {
			t.Fatalf("round-trip mismatch: got %#v want %#v", restored, original)
		}
	})
}
