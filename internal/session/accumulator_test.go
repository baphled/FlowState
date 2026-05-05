package session_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("AccumulateStream", func() {
	var (
		appender *fakeAppender
	)

	BeforeEach(func() {
		appender = &fakeAppender{}
	})

	It("forwards all chunks from rawCh to the returned channel", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "hello"}
		rawCh <- provider.StreamChunk{Content: " world"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)

		var chunks []provider.StreamChunk
		for chunk := range out {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(HaveLen(3))
	})

	It("appends an assistant message to the appender on Done", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "Hello "}
		rawCh <- provider.StreamChunk{Content: "world"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
		drainChannel(out)

		Expect(appender.messages).To(HaveLen(1))
		Expect(appender.messages[0].Role).To(Equal("assistant"))
		Expect(appender.messages[0].Content).To(Equal("Hello world"))
		Expect(appender.messages[0].AgentID).To(Equal("agent-1"))
	})

	Context("when chunks carry engine-stamped ModelID and ProviderID", func() {
		// Track B regression cover: the assistant message must persist the
		// (model, provider) pair the engine stamped on each chunk so per-turn
		// attribution survives reload and the activity-indicator chip can
		// show "produced by glm-4.6 · zai" even when the chunk itself didn't
		// carry the fields (they were stamped on a previous chunk in the same
		// turn). The accumulator records the most recent non-empty pair seen
		// and copies it onto the appended assistant Message at flush time.
		It("stamps the assistant message with ModelName and ProviderName from the latest chunk that carried them", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Content: "Hello ", ModelID: "glm-4.6", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Content: "world"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			Expect(appender.messages).To(HaveLen(1))
			Expect(appender.messages[0].Role).To(Equal("assistant"))
			Expect(appender.messages[0].ModelName).To(Equal("glm-4.6"))
			Expect(appender.messages[0].ProviderName).To(Equal("zai"))
		})

		It("leaves ModelName and ProviderName empty when no chunk in the turn carried them", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{Content: "Hello "}
			rawCh <- provider.StreamChunk{Content: "world"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			Expect(appender.messages).To(HaveLen(1))
			Expect(appender.messages[0].ModelName).To(BeEmpty())
			Expect(appender.messages[0].ProviderName).To(BeEmpty())
		})

		It("uses the LAST non-empty pair seen so a mid-turn failover is reflected on the message", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			// First chunks: anthropic (the failed primary).
			rawCh <- provider.StreamChunk{Content: "I tried", ModelID: "claude-sonnet-4-6", ProviderID: "anthropic"}
			// Mid-turn: failover replays the prefix on a new provider, the
			// engine restamps the chunks with the new pair.
			rawCh <- provider.StreamChunk{Content: " and answered", ModelID: "glm-4.6", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Done: true, ModelID: "glm-4.6", ProviderID: "zai"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			Expect(appender.messages).To(HaveLen(1))
			Expect(appender.messages[0].ModelName).To(Equal("glm-4.6"),
				"the message should reflect the model that actually produced the persisted content")
			Expect(appender.messages[0].ProviderName).To(Equal("zai"))
		})
	})

	It("appends a tool_result message with ToolName and ToolInput", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{
			ToolCall: &provider.ToolCall{
				Name:      "bash",
				Arguments: map[string]any{"command": "ls -la"},
			},
		}
		rawCh <- provider.StreamChunk{
			ToolResult: &provider.ToolResultInfo{Content: "file1.go"},
		}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
		drainChannel(out)

		var toolResults []session.Message
		for _, m := range appender.messages {
			if m.Role == "tool_result" {
				toolResults = append(toolResults, m)
			}
		}
		Expect(toolResults).To(HaveLen(1))
		Expect(toolResults[0].ToolName).To(Equal("bash"))
		Expect(toolResults[0].ToolInput).To(Equal("ls -la"))
		Expect(toolResults[0].Content).To(Equal("file1.go"))
	})

	It("flushes buffered content before a tool call", func() {
		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "thinking..."}
		rawCh <- provider.StreamChunk{
			ToolCall: &provider.ToolCall{
				Name:      "read",
				Arguments: map[string]any{"filePath": "/foo.go"},
			},
		}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
		drainChannel(out)

		var assistantMsgs []session.Message
		for _, m := range appender.messages {
			if m.Role == "assistant" {
				assistantMsgs = append(assistantMsgs, m)
			}
		}
		Expect(assistantMsgs).To(HaveLen(1))
		Expect(assistantMsgs[0].Content).To(Equal("thinking..."))
	})

	It("uses the provided sessionID and agentID for appended messages", func() {
		rawCh := make(chan provider.StreamChunk, 2)
		rawCh <- provider.StreamChunk{Content: "resp"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), appender, "my-session", "my-agent", rawCh)
		drainChannel(out)

		Expect(appender.sessionIDs).To(ContainElement("my-session"))
		Expect(appender.messages[0].AgentID).To(Equal("my-agent"))
	})

	Context("when a tool call chunk arrives", func() {
		It("stores a tool_call message with the tool name", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "bash",
					Arguments: map[string]any{"command": "ls -la"},
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var toolCalls []session.Message
			for _, m := range appender.messages {
				if m.Role == "tool_call" {
					toolCalls = append(toolCalls, m)
				}
			}
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].Content).To(Equal("bash"))
			Expect(toolCalls[0].ToolName).To(Equal("bash"))
			Expect(toolCalls[0].ToolInput).To(Equal("ls -la"))
		})

		It("populates ToolInput for tools outside the hand-coded allowlist via the tiered fallback", func() {
			// Regression: tool_call messages for delegate / search_nodes /
			// coordination_store / MCP tools previously persisted with empty
			// ToolInput because the accumulator only knew the bash/read/write
			// allowlist. The tiered fallback in tooldisplay.PrimaryArgValue
			// must produce a useful display string for these too.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "search_nodes",
					Arguments: map[string]any{"query": "FlowState recall", "limit": 10},
				},
			}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name: "delegate",
					Arguments: map[string]any{
						"subagent_type": "senior-engineer",
						"message":       "implement the fallback",
					},
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var toolCalls []session.Message
			for _, m := range appender.messages {
				if m.Role == "tool_call" {
					toolCalls = append(toolCalls, m)
				}
			}
			Expect(toolCalls).To(HaveLen(2))
			Expect(toolCalls[0].ToolName).To(Equal("search_nodes"))
			Expect(toolCalls[0].ToolInput).To(Equal("FlowState recall"))
			Expect(toolCalls[1].ToolName).To(Equal("delegate"))
			Expect(toolCalls[1].ToolInput).To(Equal("senior-engineer"))
		})

		It("redacts sensitive arg values before persisting them as ToolInput", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name: "external_api",
					Arguments: map[string]any{
						"api_key": "sk-real-key-do-not-leak",
					},
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var toolCalls []session.Message
			for _, m := range appender.messages {
				if m.Role == "tool_call" {
					toolCalls = append(toolCalls, m)
				}
			}
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].ToolInput).NotTo(ContainSubstring("sk-real-key"))
			Expect(toolCalls[0].ToolInput).To(ContainSubstring("[REDACTED]"))
		})

		It("stores tool_call message before the tool_result message", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "bash",
					Arguments: map[string]any{"command": "ls -la"},
				},
			}
			rawCh <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{Content: "file1.go"},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			roles := make([]string, 0, len(appender.messages))
			for _, m := range appender.messages {
				roles = append(roles, m.Role)
			}
			Expect(roles).To(ContainElements("tool_call", "tool_result"))
			toolCallIdx := -1
			toolResultIdx := -1
			for idx, role := range roles {
				if role == "tool_call" {
					toolCallIdx = idx
				}
				if role == "tool_result" {
					toolResultIdx = idx
				}
			}
			Expect(toolCallIdx).To(BeNumerically("<", toolResultIdx))
		})
	})

	Context("when a tool result is an error", func() {
		It("stores a tool_error message when IsError is true", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					Name:      "bash",
					Arguments: map[string]any{"command": "bad-command"},
				},
			}
			rawCh <- provider.StreamChunk{
				ToolResult: &provider.ToolResultInfo{Content: "command not found", IsError: true},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var toolErrors []session.Message
			for _, m := range appender.messages {
				if m.Role == "tool_error" {
					toolErrors = append(toolErrors, m)
				}
			}
			Expect(toolErrors).To(HaveLen(1))
			Expect(toolErrors[0].Content).To(Equal("command not found"))
		})
	})

	Context("when thinking content arrives", func() {
		It("stores a thinking message", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{Thinking: "I need to analyse this..."}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "thinking" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("I need to analyse this..."))
		})

		It("accumulates multiple thinking chunks into one message", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "First thought."}
			rawCh <- provider.StreamChunk{Thinking: " Second thought."}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "thinking" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("First thought. Second thought."))
		})
	})

	Context("when thinking content arrives with a signature", func() {
		// Phase 3 Anthropic round-trip: the assistant message produced by
		// the turn must persist the structured ThinkingBlocks so the next
		// turn can replay them verbatim. Without the signature being
		// pinned to the persisted assistant message, Anthropic silently
		// disables extended thinking continuity on turn 2+.
		It("persists the signature on the assistant message via ThinkingBlocks", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Thinking:  "weighing the request",
				Signature: "sig-encrypted-xyz",
			}
			rawCh <- provider.StreamChunk{Content: "the answer is 42"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
				"signed thinking blocks must round-trip on the persisted assistant "+
					"message — without them, the next turn cannot replay thinking "+
					"continuity and Anthropic disables thinking server-side")
			Expect(assistantMsgs[0].ThinkingBlocks[0].Signature).To(Equal("sig-encrypted-xyz"))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("weighing the request"))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Redacted).To(BeFalse())
		})

		It("persists redacted thinking on the assistant message", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{RedactedThinking: "encrypted-blob-xyz"}
			rawCh <- provider.StreamChunk{Content: "answer follows"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Redacted).To(BeTrue())
			Expect(assistantMsgs[0].ThinkingBlocks[0].Data).To(Equal("encrypted-blob-xyz"))
		})

		It("persists the stop_reason from a message_delta chunk on the assistant message", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Content: "I cannot help with that"}
			rawCh <- provider.StreamChunk{
				EventType:  "stop_reason",
				StopReason: "refusal",
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).To(Equal("refusal"),
				"refusal must surface on the persisted message so consumers can "+
					"distinguish it from a normal end_turn (Claude 4+ addition)")
		})

		It("ignores usage chunks (no message synthesis) but does not break content accumulation", func() {
			rawCh := make(chan provider.StreamChunk, 5)
			rawCh <- provider.StreamChunk{
				EventType: "usage",
				Usage: &provider.UsageDelta{
					InputTokens:          100,
					CacheReadInputTokens: 50,
					RequestID:            "msg_01ABC",
				},
			}
			rawCh <- provider.StreamChunk{Content: "hello"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			Expect(appender.messages).To(HaveLen(1))
			Expect(appender.messages[0].Role).To(Equal("assistant"))
			Expect(appender.messages[0].Content).To(Equal("hello"))
		})

		It("preserves multiple thinking blocks in order on the assistant message", func() {
			rawCh := make(chan provider.StreamChunk, 6)
			rawCh <- provider.StreamChunk{Thinking: "first", Signature: "sig-A"}
			rawCh <- provider.StreamChunk{RedactedThinking: "redacted-B"}
			rawCh <- provider.StreamChunk{Thinking: "third", Signature: "sig-C"}
			rawCh <- provider.StreamChunk{Content: "final"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(3))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("first"))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Signature).To(Equal("sig-A"))
			Expect(assistantMsgs[0].ThinkingBlocks[1].Redacted).To(BeTrue())
			Expect(assistantMsgs[0].ThinkingBlocks[1].Data).To(Equal("redacted-B"))
			Expect(assistantMsgs[0].ThinkingBlocks[2].Thinking).To(Equal("third"))
			Expect(assistantMsgs[0].ThinkingBlocks[2].Signature).To(Equal("sig-C"))
		})
	})

	Context("when delegation info arrives", func() {
		It("stores a delegation message when status is completed", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "build-agent",
					Status:      "completed",
					ModelName:   "claude-3-5-sonnet",
					ToolCalls:   3,
					LastTool:    "bash",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var delegationMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "delegation" {
					delegationMsgs = append(delegationMsgs, m)
				}
			}
			Expect(delegationMsgs).To(HaveLen(1))
			Expect(delegationMsgs[0].Content).To(ContainSubstring("build-agent"))
		})

		It("stores a delegation message when status is failed", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "worker-agent",
					Status:      "failed",
					ModelName:   "gpt-4o",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var delegationMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "delegation" {
					delegationMsgs = append(delegationMsgs, m)
				}
			}
			Expect(delegationMsgs).To(HaveLen(1))
			Expect(delegationMsgs[0].Content).To(ContainSubstring("worker-agent"))
		})

		It("stores a delegation_started message when status is started", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "build-agent",
					Status:      "started",
					ModelName:   "claude-3-5-sonnet",
					ChainID:     "chain-1",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var startedMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "delegation_started" {
					startedMsgs = append(startedMsgs, m)
				}
			}
			Expect(startedMsgs).To(HaveLen(1))
			Expect(startedMsgs[0].Content).To(ContainSubstring("build-agent"))
			Expect(startedMsgs[0].AgentID).To(Equal("agent-1"))
		})

		It("stores a delegation_started message when status is running", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "worker", Status: "running", ChainID: "c-2",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var started []session.Message
			for _, m := range appender.messages {
				if m.Role == "delegation_started" {
					started = append(started, m)
				}
			}
			Expect(started).To(HaveLen(1))
		})

		It("only emits a single delegation_started per chain even when running fires repeatedly", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			info := &provider.DelegationInfo{TargetAgent: "w", Status: "running", ChainID: "c-3"}
			rawCh <- provider.StreamChunk{DelegationInfo: info}
			rawCh <- provider.StreamChunk{DelegationInfo: info}
			rawCh <- provider.StreamChunk{DelegationInfo: info}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			count := 0
			for _, m := range appender.messages {
				if m.Role == "delegation_started" {
					count++
				}
			}
			Expect(count).To(Equal(1))
		})

		It("does not store a delegation message when status is pending", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "build-agent",
					Status:      "pending",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			for _, m := range appender.messages {
				Expect(m.Role).NotTo(Equal("delegation"))
			}
		})

		It("populates structured delegation fields on the appended message", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "build-agent",
					Status:      "started",
					ChainID:     "chain-X",
					ModelName:   "claude-3-5-sonnet",
					ToolCalls:   2,
					LastTool:    "bash",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			Expect(appender.messages).To(HaveLen(1))
			m := appender.messages[0]
			Expect(m.Role).To(Equal("delegation_started"))
			Expect(m.TargetAgent).To(Equal("build-agent"))
			Expect(m.ChainID).To(Equal("chain-X"))
			Expect(m.ModelName).To(Equal("claude-3-5-sonnet"))
			Expect(m.ToolCalls).To(Equal(2))
			Expect(m.LastTool).To(Equal("bash"))
			Expect(m.Status).To(Equal("started"))
		})

		It("updates the in-flight delegation message in place when running chunks arrive", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "w", Status: "started", ChainID: "c-up",
					ToolCalls: 1, LastTool: "read",
				},
			}
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "w", Status: "running", ChainID: "c-up",
					ToolCalls: 5, LastTool: "edit",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			Expect(appender.messages).To(HaveLen(1))
			Expect(appender.updates).To(HaveLen(1))
			Expect(appender.updates[0].chainID).To(Equal("c-up"))
			updated := appender.lastUpdatedFor("c-up")
			Expect(updated.ToolCalls).To(Equal(5))
			Expect(updated.LastTool).To(Equal("edit"))
			Expect(updated.Status).To(Equal("running"))
		})

		It("flips role to delegation and updates fields on terminal status for an in-flight chain", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "w", Status: "started", ChainID: "c-term",
				},
			}
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "w", Status: "completed", ChainID: "c-term",
					ToolCalls: 7, LastTool: "bash",
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			Expect(appender.messages).To(HaveLen(1))
			Expect(appender.updates).NotTo(BeEmpty())
			updated := appender.lastUpdatedFor("c-term")
			Expect(updated.Role).To(Equal("delegation"))
			Expect(updated.Status).To(Equal("completed"))
			Expect(updated.ToolCalls).To(Equal(7))
		})
	})

	Context("when an event chunk arrives (EventType is set)", func() {
		It("does not accumulate Content from event chunks into the assistant message", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				EventType: "harness_attempt_start",
				Content:   `{"attempt":1,"maxRetries":1}`,
			}
			rawCh <- provider.StreamChunk{Content: "Hello! "}
			rawCh <- provider.StreamChunk{Content: "I'm the assistant."}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].Content).To(Equal("Hello! I'm the assistant."))
			Expect(assistantMsgs[0].Content).NotTo(ContainSubstring("attempt"))
			Expect(assistantMsgs[0].Content).NotTo(ContainSubstring("maxRetries"))
		})

		It("forwards event chunks to the returned channel even though they are not accumulated", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				EventType: "harness_attempt_start",
				Content:   `{"attempt":1,"maxRetries":1}`,
			}
			rawCh <- provider.StreamChunk{Content: "ok"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)

			var forwarded []provider.StreamChunk
			for chunk := range out {
				forwarded = append(forwarded, chunk)
			}
			Expect(forwarded).To(HaveLen(3))
			Expect(forwarded[0].EventType).To(Equal("harness_attempt_start"))
		})

		It("does not accumulate Thinking from event chunks", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				EventType: "some_event",
				Thinking:  "noise that should not become a thinking message",
			}
			rawCh <- provider.StreamChunk{Thinking: "real thought"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "thinking" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("real thought"))
		})
	})

	Context("when the raw channel closes without a terminal Done chunk", func() {
		It("flushes accumulated assistant content on channel close", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{Content: "hello"}
			rawCh <- provider.StreamChunk{Content: " world"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].Content).To(Equal("hello world"))
			Expect(assistantMsgs[0].AgentID).To(Equal("agent-1"))
		})

		It("flushes accumulated thinking content on channel close", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{Thinking: "half a"}
			rawCh <- provider.StreamChunk{Thinking: " thought"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "thinking" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].Content).To(Equal("half a thought"))
		})

		It("does not double-flush when a Done chunk is followed by channel close", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{Content: "only once"}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].Content).To(Equal("only once"))
		})
	})
})

var _ = Describe("MessageAppender interface", func() {
	It("Manager implements MessageAppender", func() {
		var _ session.MessageAppender = session.NewManager(newMockStreamer())
	})
})

// fakeAppender is a test double implementing session.MessageAppender.
type fakeAppender struct {
	messages   []session.Message
	sessionIDs []string
	updates    []fakeDelegationUpdate
}

type fakeDelegationUpdate struct {
	sessionID string
	chainID   string
	result    session.Message
}

func (f *fakeAppender) AppendMessage(sessionID string, msg session.Message) {
	f.sessionIDs = append(f.sessionIDs, sessionID)
	f.messages = append(f.messages, msg)
}

func (f *fakeAppender) UpdateDelegation(sessionID, chainID string, mutate func(*session.Message)) {
	for i := range f.messages {
		if f.messages[i].ChainID == chainID {
			mutate(&f.messages[i])
			f.updates = append(f.updates, fakeDelegationUpdate{
				sessionID: sessionID,
				chainID:   chainID,
				result:    f.messages[i],
			})
			return
		}
	}
}

func (f *fakeAppender) lastUpdatedFor(chainID string) session.Message {
	for i := len(f.updates) - 1; i >= 0; i-- {
		if f.updates[i].chainID == chainID {
			return f.updates[i].result
		}
	}
	return session.Message{}
}
