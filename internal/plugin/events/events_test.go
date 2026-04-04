package events_test

import (
	"encoding/json"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
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

	Describe("ProviderRequestEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ProviderRequestEventData{
				SessionID:    "sess1",
				AgentID:      "test-agent",
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Request: provider.ChatRequest{
					Provider: "anthropic",
					Model:    "claude-3",
					Messages: []provider.Message{{Role: "user", Content: "hello"}},
					Tools:    []provider.Tool{{Name: "bash", Description: "run commands"}},
				},
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewProviderRequestEvent(data, ts)
			Expect(evt.EventType()).To(Equal("provider.request"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("defaults timestamp to now when not provided", func() {
			data := events.ProviderRequestEventData{
				AgentID:      "executor",
				ProviderName: "openai",
				ModelName:    "gpt-4",
				Request: provider.ChatRequest{
					Provider: "openai",
					Model:    "gpt-4",
				},
			}
			evt := events.NewProviderRequestEvent(data)
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

	Describe("ProviderResponseEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ProviderResponseEventData{
				SessionID:       "sess1",
				AgentID:         "test-agent",
				ProviderName:    "anthropic",
				ModelName:       "claude-3",
				ResponseContent: "Hello, world!",
				ToolCalls:       2,
				DurationMS:      1500,
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewProviderResponseEvent(data, ts)
			Expect(evt.EventType()).To(Equal("provider.response"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("defaults timestamp to now when not provided", func() {
			data := events.ProviderResponseEventData{
				AgentID:      "executor",
				ProviderName: "openai",
				ModelName:    "gpt-4",
			}
			evt := events.NewProviderResponseEvent(data)
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("ProviderErrorEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ProviderErrorEventData{
				SessionID:    "sess1",
				AgentID:      "test-agent",
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Error:        errors.New("auth failed"),
				Phase:        "stream_init",
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewProviderErrorEvent(data, ts)
			Expect(evt.EventType()).To(Equal("provider.error"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data.SessionID).To(Equal("sess1"))
			Expect(evt.Data.AgentID).To(Equal("test-agent"))
			Expect(evt.Data.ProviderName).To(Equal("anthropic"))
			Expect(evt.Data.ModelName).To(Equal("claude-3"))
			Expect(evt.Data.Error).To(MatchError("auth failed"))
			Expect(evt.Data.Phase).To(Equal("stream_init"))
		})

		It("defaults timestamp to now when not provided", func() {
			data := events.ProviderErrorEventData{
				AgentID:      "executor",
				ProviderName: "openai",
				ModelName:    "gpt-4",
				Error:        errors.New("timeout"),
				Phase:        "failover",
			}
			evt := events.NewProviderErrorEvent(data)
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})

		It("serialises to JSON with error as string", func() {
			data := events.ProviderErrorEventData{
				SessionID:    "sess1",
				AgentID:      "test-agent",
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Error:        errors.New("rate limited"),
				Phase:        "failover",
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())

			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["error"]).To(Equal("rate limited"))
			Expect(parsed["phase"]).To(Equal("failover"))
			Expect(parsed["provider_name"]).To(Equal("anthropic"))
		})

		It("serialises to JSON with empty error when nil", func() {
			data := events.ProviderErrorEventData{
				ProviderName: "ollama",
				Phase:        "stream_init",
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())

			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed).NotTo(HaveKey("error"))
		})
	})

	Describe("SessionResumedEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.SessionResumedEventData{
				SessionID: "sess42",
				UserID:    "user42",
				Details:   map[string]any{"foo": "bar"},
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewSessionResumedEvent(data, ts)
			Expect(evt.EventType()).To(Equal("session.resumed"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("serialises to JSON", func() {
			data := events.SessionResumedEventData{
				SessionID: "sess42",
				UserID:    "user42",
				Details:   map[string]any{"foo": "bar"},
			}
			evt := events.NewSessionResumedEvent(data, time.Now())
			raw, err := json.Marshal(evt)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["data"]).NotTo(BeNil())
		})
	})

	Describe("ToolExecuteErrorEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ToolExecuteErrorEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "fail"},
				Error:    errors.New("tool failed"),
			}
			evt := events.NewToolExecuteErrorEvent(data)
			Expect(evt.EventType()).To(Equal("tool.execute.error"))
			Expect(evt.Data.ToolName).To(Equal("echo"))
			Expect(evt.Data.Error).To(MatchError("tool failed"))
		})

		It("serialises to JSON with error as string", func() {
			data := events.ToolExecuteErrorEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "fail"},
				Error:    errors.New("tool failed"),
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["error"]).To(Equal("tool failed"))
		})
	})

	Describe("ToolExecuteResultEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ToolExecuteResultEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "ok"},
				Result:   "ok",
			}
			evt := events.NewToolExecuteResultEvent(data)
			Expect(evt.EventType()).To(Equal("tool.execute.result"))
			Expect(evt.Data.Result).To(Equal("ok"))
		})

		It("serialises to JSON", func() {
			data := events.ToolExecuteResultEventData{
				ToolName: "echo",
				Args:     map[string]any{"msg": "ok"},
				Result:   "ok",
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["result"]).To(Equal("ok"))
		})
	})

	Describe("ProviderRequestRetryEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.ProviderRequestRetryEventData{
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Reason:       "rate limit",
				Attempt:      2,
			}
			evt := events.NewProviderRequestRetryEvent(data)
			Expect(evt.EventType()).To(Equal("provider.request.retry"))
			Expect(evt.Data.ProviderName).To(Equal("anthropic"))
			Expect(evt.Data.Attempt).To(Equal(2))
		})

		It("serialises to JSON", func() {
			data := events.ProviderRequestRetryEventData{
				ProviderName: "anthropic",
				ModelName:    "claude-3",
				Reason:       "rate limit",
				Attempt:      2,
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["reason"]).To(Equal("rate limit"))
			Expect(parsed["attempt"]).To(Equal(float64(2)))
		})
	})
})
