package events_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
)

var _ = Describe("Events", func() {
	Describe("SessionEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.SessionEventData{
				SessionID: "sess1",
				UserID:    "user1",
				Action:    "start",
				Details:   map[string]any{"foo": "bar"},
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewSessionEvent(data, ts)
			Expect(evt.EventType()).To(Equal("session"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})
	})

	Describe("ToolEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ToolEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "hi"},
				Result:   "hi",
				Error:    nil,
			}
			evt := events.NewToolEvent(data)
			Expect(evt.EventType()).To(Equal("tool"))
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
			Expect(evt.Data).To(Equal(data))
		})
	})

	Describe("ProviderEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ProviderEventData{
				ProviderName: "anthropic",
				Request:      "foo",
				Response:     "bar",
				Error:        nil,
			}
			evt := events.NewProviderEvent(data)
			Expect(evt.EventType()).To(Equal("provider"))
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
			Expect(evt.Data).To(Equal(data))
		})
	})

	Describe("PromptEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.PromptEventData{
				AgentID:    "planner",
				FullPrompt: "You are a planner...",
				TokenCount: 1500,
				Truncated:  false,
				Sources:    []string{"manifest", "agent-files"},
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewPromptEvent(data, ts)
			Expect(evt.EventType()).To(Equal("prompt"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("defaults timestamp to now when not provided", func() {
			data := events.PromptEventData{
				AgentID:    "executor",
				FullPrompt: "You are an executor...",
				TokenCount: 500,
			}
			evt := events.NewPromptEvent(data)
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("ContextWindowEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ContextWindowEventData{
				AgentID:         "planner",
				TokenBudget:     128000,
				TokensUsed:      95000,
				BudgetRemaining: 33000,
				MessageCount:    42,
				Truncated:       true,
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewContextWindowEvent(data, ts)
			Expect(evt.EventType()).To(Equal("context.window"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("defaults timestamp to now when not provided", func() {
			data := events.ContextWindowEventData{
				AgentID:     "executor",
				TokenBudget: 64000,
			}
			evt := events.NewContextWindowEvent(data)
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("ToolReasoningEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ToolReasoningEventData{
				AgentID:          "senior-engineer",
				ToolName:         "bash",
				ReasoningContent: "I need to check the test output before proceeding.",
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewToolReasoningEvent(data, ts)
			Expect(evt.EventType()).To(Equal("tool.reasoning"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("defaults timestamp to now when not provided", func() {
			data := events.ToolReasoningEventData{
				AgentID:          "qa-engineer",
				ToolName:         "read",
				ReasoningContent: "Let me read the file to understand the pattern.",
			}
			evt := events.NewToolReasoningEvent(data)
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("BaseEvent", func() {
		It("returns correct eventType and timestamp via embedding", func() {
			ts := time.Now().Add(-time.Hour)
			data := events.SessionEventData{SessionID: "s", UserID: "u", Action: "act", Details: nil}
			evt := events.NewSessionEvent(data, ts)
			Expect(evt.EventType()).To(Equal("session"))
			Expect(evt.Timestamp()).To(Equal(ts))
		})
	})
})
