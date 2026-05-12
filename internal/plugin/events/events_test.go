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

		// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026) §"Schema growth":
		// ToolEventData grows two correlation identifiers to close the schema gap
		// the chunk-driven SwarmEvent path carried today. ToolCallID is the
		// upstream provider wire id (P14b audit trail); InternalToolCallID is the
		// FlowState session-scoped canonical id stable across provider failover.
		// Both are `omitempty` so pre-bridge events.jsonl recordings stay
		// byte-identical when decoded.
		Context("with correlation IDs", func() {
			It("emits tool_call_id and internal_tool_call_id JSON keys when populated", func() {
				data := events.ToolEventData{
					ToolName:           "bash",
					Args:               map[string]any{"cmd": "ls"},
					ToolCallID:         "toolu_01ABC",
					InternalToolCallID: "fs_internal_42",
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				var parsed map[string]any
				Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
				Expect(parsed["tool_call_id"]).To(Equal("toolu_01ABC"))
				Expect(parsed["internal_tool_call_id"]).To(Equal("fs_internal_42"))
			})

			It("omits tool_call_id and internal_tool_call_id when empty", func() {
				data := events.ToolEventData{
					ToolName: "bash",
					Args:     map[string]any{"cmd": "ls"},
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(raw)).NotTo(ContainSubstring("tool_call_id"))
				Expect(string(raw)).NotTo(ContainSubstring("internal_tool_call_id"))
			})

			It("preserves the existing keys when the new fields are empty (round-trip invariance)", func() {
				// Persisted-format invariance: a pre-bridge ToolEventData
				// (no correlation IDs) marshalled today must produce the
				// same JSON shape it produced before the schema grew.
				// `omitempty` is the load-bearing tag — adding new fields
				// must not surface them on the wire when unset.
				data := events.ToolEventData{
					SessionID: "sess-1",
					ToolName:  "bash",
					Args:      map[string]any{"cmd": "ls"},
					Result:    "output",
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				var parsed map[string]any
				Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
				Expect(parsed).To(HaveKey("session_id"))
				Expect(parsed).To(HaveKey("tool_name"))
				Expect(parsed).To(HaveKey("args"))
				Expect(parsed).To(HaveKey("result"))
				Expect(parsed).NotTo(HaveKey("tool_call_id"))
				Expect(parsed).NotTo(HaveKey("internal_tool_call_id"))
			})
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
				ErrorType:    string(provider.ErrorTypeAuthFailure),
				ErrorCode:    "401",
				HTTPStatus:   401,
				IsRetriable:  false,
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
			Expect(evt.Data.ErrorType).To(Equal(string(provider.ErrorTypeAuthFailure)))
			Expect(evt.Data.ErrorCode).To(Equal("401"))
			Expect(evt.Data.HTTPStatus).To(Equal(401))
			Expect(evt.Data.IsRetriable).To(BeFalse())
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
				ErrorType:    string(provider.ErrorTypeRateLimit),
				ErrorCode:    "429",
				HTTPStatus:   429,
				IsRetriable:  true,
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())

			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["error"]).To(Equal("rate limited"))
			Expect(parsed["phase"]).To(Equal("failover"))
			Expect(parsed["provider_name"]).To(Equal("anthropic"))
			Expect(parsed["error_type"]).To(Equal("rate_limit"))
			Expect(parsed["error_code"]).To(Equal("429"))
			Expect(parsed["http_status"]).To(Equal(float64(429)))
			Expect(parsed["is_retriable"]).To(BeTrue())
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
			Expect(parsed).NotTo(HaveKey("error_type"))
			Expect(parsed).NotTo(HaveKey("error_code"))
			Expect(parsed).NotTo(HaveKey("http_status"))
			Expect(parsed).NotTo(HaveKey("is_retriable"))
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

		// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026) §"Schema growth".
		Context("with correlation IDs", func() {
			It("emits tool_call_id and internal_tool_call_id JSON keys when populated", func() {
				data := events.ToolExecuteErrorEventData{
					ToolName:           "bash",
					Args:               map[string]any{"cmd": "false"},
					Error:              errors.New("exit 1"),
					ToolCallID:         "toolu_01ERR",
					InternalToolCallID: "fs_internal_err",
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				var parsed map[string]any
				Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
				Expect(parsed["tool_call_id"]).To(Equal("toolu_01ERR"))
				Expect(parsed["internal_tool_call_id"]).To(Equal("fs_internal_err"))
			})

			It("omits tool_call_id and internal_tool_call_id when empty", func() {
				data := events.ToolExecuteErrorEventData{
					ToolName: "bash",
					Error:    errors.New("exit 1"),
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(raw)).NotTo(ContainSubstring("tool_call_id"))
				Expect(string(raw)).NotTo(ContainSubstring("internal_tool_call_id"))
			})

			It("preserves the pre-bridge JSON shape when the new fields are empty", func() {
				data := events.ToolExecuteErrorEventData{
					SessionID: "sess-1",
					ToolName:  "bash",
					Error:     errors.New("boom"),
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				var parsed map[string]any
				Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
				Expect(parsed).To(HaveKey("tool_name"))
				Expect(parsed).To(HaveKey("error"))
				Expect(parsed).NotTo(HaveKey("tool_call_id"))
				Expect(parsed).NotTo(HaveKey("internal_tool_call_id"))
			})
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

		// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026) §"Schema growth".
		Context("with correlation IDs", func() {
			It("emits tool_call_id and internal_tool_call_id JSON keys when populated", func() {
				data := events.ToolExecuteResultEventData{
					ToolName:           "bash",
					Args:               map[string]any{"cmd": "ls"},
					Result:             "a.txt",
					ToolCallID:         "toolu_01OK",
					InternalToolCallID: "fs_internal_ok",
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				var parsed map[string]any
				Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
				Expect(parsed["tool_call_id"]).To(Equal("toolu_01OK"))
				Expect(parsed["internal_tool_call_id"]).To(Equal("fs_internal_ok"))
			})

			It("omits tool_call_id and internal_tool_call_id when empty", func() {
				data := events.ToolExecuteResultEventData{
					ToolName: "bash",
					Result:   "ok",
				}
				raw, err := json.Marshal(data)
				Expect(err).NotTo(HaveOccurred())
				Expect(string(raw)).NotTo(ContainSubstring("tool_call_id"))
				Expect(string(raw)).NotTo(ContainSubstring("internal_tool_call_id"))
			})

			It("decodes pre-bridge fixtures without the new keys (additive on the wire)", func() {
				// ToolExecuteResultEventData uses plain JSON tags (no
				// custom MarshalJSON), so json.Unmarshal works
				// symmetrically. A pre-bridge fixture (no correlation
				// IDs) must decode cleanly with the new fields
				// zero-valued.
				fixture := []byte(`{"session_id":"sess-1","tool_name":"bash","result":"ok"}`)
				var got events.ToolExecuteResultEventData
				Expect(json.Unmarshal(fixture, &got)).To(Succeed())
				Expect(got.ToolName).To(Equal("bash"))
				Expect(got.ToolCallID).To(BeEmpty())
				Expect(got.InternalToolCallID).To(BeEmpty())
			})
		})
	})

	Describe("DelegationStartedEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.DelegationEventData{
				ChainID:         "chain-123",
				ParentSessionID: "parent-sess",
				ChildSessionID:  "child-sess",
				SourceAgent:     "orchestrator",
				TargetAgent:     "qa-agent",
				Status:          "started",
				Description:     "run all the tests",
				StartedAt:       time.Now().UTC(),
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewDelegationStartedEvent(data, ts)
			Expect(evt.EventType()).To(Equal("delegation.started"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("defaults timestamp to now when not provided", func() {
			data := events.DelegationEventData{
				ChainID:         "chain-456",
				ParentSessionID: "p",
				ChildSessionID:  "c",
				SourceAgent:     "lead",
				TargetAgent:     "worker",
				Status:          "started",
			}
			evt := events.NewDelegationStartedEvent(data)
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})

		It("serialises to JSON with snake_case keys for ChildSessionID", func() {
			data := events.DelegationEventData{
				ChainID:         "chain-json",
				ParentSessionID: "parent-1",
				ChildSessionID:  "child-1",
				SourceAgent:     "lead",
				TargetAgent:     "qa",
				Status:          "started",
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())

			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			// Default Go JSON marshalling uses field names verbatim;
			// downstream projection at the API SSE seam converts to
			// snake_case for the on-the-wire SwarmEvent metadata.
			Expect(parsed).To(HaveKey("ChildSessionID"))
			Expect(parsed).To(HaveKey("ChainID"))
			Expect(parsed["ChildSessionID"]).To(Equal("child-1"))
		})
	})

	Describe("DelegationCompletedEvent", func() {
		It("implements Event interface and sets fields including model and provider", func() {
			completedAt := time.Now().UTC()
			data := events.DelegationEventData{
				ChainID:         "chain-c",
				ParentSessionID: "p",
				ChildSessionID:  "c",
				SourceAgent:     "lead",
				TargetAgent:     "qa",
				Status:          "completed",
				ModelName:       "claude-3",
				ProviderName:    "anthropic",
				ToolCalls:       3,
				LastTool:        "bash",
				StartedAt:       time.Now().UTC().Add(-time.Minute),
				CompletedAt:     &completedAt,
			}
			evt := events.NewDelegationCompletedEvent(data)
			Expect(evt.EventType()).To(Equal("delegation.completed"))
			Expect(evt.Data.ModelName).To(Equal("claude-3"))
			Expect(evt.Data.ProviderName).To(Equal("anthropic"))
			Expect(evt.Data.ToolCalls).To(Equal(3))
			Expect(evt.Data.LastTool).To(Equal("bash"))
			Expect(evt.Data.CompletedAt).NotTo(BeNil())
		})

		It("defaults timestamp to now when not provided", func() {
			evt := events.NewDelegationCompletedEvent(events.DelegationEventData{
				ChainID: "x",
				Status:  "completed",
			})
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("DelegationFailedEvent", func() {
		It("implements Event interface and carries the error message as a string", func() {
			completedAt := time.Now().UTC()
			data := events.DelegationEventData{
				ChainID:         "chain-f",
				ParentSessionID: "p",
				ChildSessionID:  "c",
				SourceAgent:     "lead",
				TargetAgent:     "qa",
				Status:          "failed",
				Error:           "gate rejected output",
				CompletedAt:     &completedAt,
			}
			evt := events.NewDelegationFailedEvent(data)
			Expect(evt.EventType()).To(Equal("delegation.failed"))
			Expect(evt.Data.Error).To(Equal("gate rejected output"))
		})

		It("serialises to JSON with the error message as a plain string", func() {
			data := events.DelegationEventData{
				ChainID: "chain-fj",
				Status:  "failed",
				Error:   "stream error",
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["Error"]).To(Equal("stream error"))
		})
	})

	// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
	// the engine grows three swarm-gate lifecycle events on the bus.
	// GateEventData mirrors DelegationEventData's shape (one struct,
	// three wrapper types, started/completed/failed-style triplet)
	// using `Reason string` and `Cause string` so default Go JSON
	// marshalling suffices without a custom MarshalJSON.
	Describe("GateEvaluatingEvent", func() {
		It("implements Event interface and sets fields", func() {
			data := events.GateEventData{
				SwarmID:   "swarm-99",
				SessionID: "sess-99",
				Lifecycle: "pre",
				GateCount: 2,
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewGateEvaluatingEvent(data, ts)
			Expect(evt.EventType()).To(Equal("gate.evaluating"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
		})

		It("defaults timestamp to now when not provided", func() {
			evt := events.NewGateEvaluatingEvent(events.GateEventData{
				SwarmID:   "swarm-x",
				SessionID: "sess-x",
				Lifecycle: "pre",
				GateCount: 1,
			})
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})

		It("serialises to JSON with default Go field names so subscribers can decode without a custom MarshalJSON", func() {
			data := events.GateEventData{
				SwarmID:   "swarm-j",
				SessionID: "sess-j",
				Lifecycle: "pre",
				GateCount: 3,
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())

			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed).To(HaveKey("SwarmID"))
			Expect(parsed).To(HaveKey("SessionID"))
			Expect(parsed["Lifecycle"]).To(Equal("pre"))
			Expect(parsed["GateCount"]).To(Equal(float64(3)))
		})
	})

	Describe("GatePassedEvent", func() {
		It("implements Event interface and sets fields with batch-level shape", func() {
			data := events.GateEventData{
				SwarmID:   "swarm-c",
				SessionID: "sess-c",
				Lifecycle: "post",
				MemberID:  "",
				GateCount: 4,
			}
			evt := events.NewGatePassedEvent(data)
			Expect(evt.EventType()).To(Equal("gate.passed"))
			Expect(evt.Data.GateCount).To(Equal(4))
			Expect(evt.Data.MemberID).To(BeEmpty(),
				"swarm-level pass events carry no MemberID — set only on member-pre/post lifecycle points")
		})

		It("defaults timestamp to now when not provided", func() {
			evt := events.NewGatePassedEvent(events.GateEventData{Lifecycle: "post"})
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("GateFailedEvent", func() {
		It("implements Event interface and carries the typed *swarm.GateError fields as plain strings", func() {
			data := events.GateEventData{
				SwarmID:   "swarm-f",
				SessionID: "sess-f",
				Lifecycle: "post-member",
				MemberID:  "plan-reviewer",
				GateName:  "post-member-plan-reviewer-result-schema",
				GateKind:  "builtin:result-schema",
				Reason:    "schema validation failed",
				Cause:     "missing required property \"verdict\"",
			}
			evt := events.NewGateFailedEvent(data)
			Expect(evt.EventType()).To(Equal("gate.failed"))
			Expect(evt.Data.GateName).To(Equal("post-member-plan-reviewer-result-schema"))
			Expect(evt.Data.MemberID).To(Equal("plan-reviewer"))
			Expect(evt.Data.Reason).To(ContainSubstring("schema validation failed"))
		})

		It("serialises typed gate-error fields as plain strings", func() {
			data := events.GateEventData{
				SwarmID:        "swarm-fj",
				SessionID:      "sess-fj",
				Lifecycle:      "pre",
				GateName:       "envelope-check",
				GateKind:       "ext:relevance-gate",
				Reason:         "off-topic",
				CoordStoreKeys: []string{"chain/researcher/output", "chain/topic/spec"},
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["GateName"]).To(Equal("envelope-check"))
			Expect(parsed["GateKind"]).To(Equal("ext:relevance-gate"))
			Expect(parsed["Reason"]).To(Equal("off-topic"))
			coords, ok := parsed["CoordStoreKeys"].([]any)
			Expect(ok).To(BeTrue())
			Expect(coords).To(HaveLen(2))
		})

		It("defaults timestamp to now when not provided", func() {
			evt := events.NewGateFailedEvent(events.GateEventData{Lifecycle: "post"})
			Expect(evt.Timestamp()).To(BeTemporally("~", time.Now(), time.Second))
		})
	})

	Describe("Gate event catalog registration", func() {
		// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
		// the catalog gains three new entries with StatusActive so
		// downstream tooling (T11 validation, T13 docs) discovers
		// the new topics without re-deriving the pattern.
		It("registers gate.evaluating, gate.passed and gate.failed with StatusActive", func() {
			topics := map[string]bool{
				events.EventGateEvaluating: false,
				events.EventGatePassed:     false,
				events.EventGateFailed:     false,
			}
			for _, entry := range events.Catalog {
				if _, ok := topics[entry.Topic]; ok {
					Expect(entry.Status).To(Equal(events.StatusActive),
						"%q must be active in the catalog", entry.Topic)
					topics[entry.Topic] = true
				}
			}
			for topic, found := range topics {
				Expect(found).To(BeTrue(), "catalog missing entry for %q", topic)
			}
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

	// Phase-5 Slice δ — Trigger discriminant on ContextCompactedEvent.
	//
	// Closed vocabulary: "ratio" | "gate_proximity" | "model_switch" |
	// "tool_result_wave". The field was added in Slice α; Slice δ surfaces
	// it onto the SSE wire and the Vue chip tooltip. This spec pins the
	// shape at the engine seam — the source of truth — so subsequent
	// bridges cannot silently drop the discriminant.
	Describe("ContextCompactedEvent", func() {
		It("implements Event interface and sets fields including Trigger", func() {
			data := events.ContextCompactedEventData{
				SessionID:      "sess-δ",
				AgentID:        "delta-agent",
				OriginalTokens: 12000,
				SummaryTokens:  3000,
				LatencyMS:      800,
				Trigger:        "model_switch",
			}
			ts := time.Now().Add(-time.Minute)
			evt := events.NewContextCompactedEvent(data, ts)
			Expect(evt.EventType()).To(Equal("context.compacted"))
			Expect(evt.Timestamp()).To(BeTemporally("~", ts, time.Second))
			Expect(evt.Data).To(Equal(data))
			Expect(evt.Data.Trigger).To(Equal("model_switch"),
				"NewContextCompactedEvent must preserve the Trigger discriminant verbatim — Slice δ surfaces this onto the chip tooltip")
		})

		It("tolerates an empty Trigger for forward-compatibility", func() {
			data := events.ContextCompactedEventData{
				SessionID:      "sess-legacy",
				OriginalTokens: 5000,
				SummaryTokens:  1000,
			}
			evt := events.NewContextCompactedEvent(data)
			Expect(evt.Data.Trigger).To(BeEmpty(),
				"empty Trigger is tolerated so historical events that pre-date the field remain decodable")
		})

		It("serialises Trigger to JSON for the wire bridge", func() {
			data := events.ContextCompactedEventData{
				SessionID: "sess-wire",
				Trigger:   "tool_result_wave",
			}
			raw, err := json.Marshal(data)
			Expect(err).NotTo(HaveOccurred())
			var parsed map[string]any
			Expect(json.Unmarshal(raw, &parsed)).To(Succeed())
			Expect(parsed["Trigger"]).To(Equal("tool_result_wave"),
				"the Go field name flows verbatim through json.Marshal — the bridge handler re-keys to snake_case for the SSE wire")
		})
	})

	// UI Parity PR5 — Live token counter (May 2026).
	//
	// StreamingHeartbeatEventData grows a TokenCount field so the chat UI's
	// streaming-affordance chrome can render "1,247 tokens · 42 t/s" next to
	// the working-on label. The engine populates the field from the in-flight
	// turn's UsageDelta accumulator (latest cumulative output_tokens reported
	// by the provider on message_delta), and the SSE bridge forwards it onto
	// the wire so the Vue store can compute tokens-per-second from the
	// delta-vs-prev-tick at the documented heartbeat interval.
	Describe("StreamingHeartbeatEvent", func() {
		It("implements Event interface and sets fields including TokenCount", func() {
			data := events.StreamingHeartbeatEventData{
				SessionID:  "sess-hb-1",
				AgentID:    "Tech-Lead",
				Phase:      "generating",
				TokenCount: 1247,
			}
			evt := events.NewStreamingHeartbeatEvent(data)
			Expect(evt.EventType()).To(Equal(events.EventStreamingHeartbeat))
			Expect(evt.Data.SessionID).To(Equal("sess-hb-1"))
			Expect(evt.Data.AgentID).To(Equal("Tech-Lead"))
			Expect(evt.Data.Phase).To(Equal("generating"))
			Expect(evt.Data.TokenCount).To(Equal(int64(1247)),
				"engine populates TokenCount from the in-flight turn's cumulative output_tokens so the chat UI can render a live counter next to the working-on label")
		})

		It("defaults TokenCount to zero when the heartbeat fires before the first UsageDelta arrives", func() {
			// The first heartbeat of a turn typically fires while the
			// provider is still reasoning — no message_delta yet, no
			// cumulative output_tokens to thread. Zero is the legitimate
			// pre-fire value and the chat UI suppresses the counter
			// until the value transitions positive.
			data := events.StreamingHeartbeatEventData{
				SessionID: "sess-hb-2",
				AgentID:   "planner",
				Phase:     "thinking",
			}
			evt := events.NewStreamingHeartbeatEvent(data)
			Expect(evt.Data.TokenCount).To(BeZero(),
				"zero TokenCount is the legitimate pre-first-UsageDelta value")
		})
	})
})
