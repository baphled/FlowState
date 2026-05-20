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
			// Delegate must persist both the routing target and the brief —
			// the previous "subagent_type only" rendering silently dropped
			// every parent's delegation intent. See Bug Fixes/Delegation
			// Brief Persistence (May 2026).
			Expect(toolCalls[1].ToolInput).To(ContainSubstring("senior-engineer"))
			Expect(toolCalls[1].ToolInput).To(ContainSubstring("implement the fallback"))
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

		// Production bug 2026-05-11 (req_011Cavnk52Fbsfes8zWumAcm):
		// Whitespace-only thinking from the streaming layer was persisted
		// verbatim onto the assistant message's ThinkingBlocks. On the
		// next turn this fed back into the Anthropic request and the API
		// rejected it with HTTP 400 invalid_request_error. The storage
		// layer must drop whitespace-only thinking at the flushThinking
		// gate so the bad block never becomes part of session history.
		It("does NOT persist a whitespace-only thinking block from the stream", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "   \n\t  ", Signature: "sig-whitespace"}
			rawCh <- provider.StreamChunk{Content: "real answer"}
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
			Expect(assistantMsgs[0].ThinkingBlocks).To(BeEmpty(),
				"whitespace-only thinking must not be persisted — round-tripping "+
					"it on the next turn produces HTTP 400 invalid_request_error "+
					"(production bug req_011Cavnk52Fbsfes8zWumAcm)")
			Expect(assistantMsgs[0].Content).To(Equal("real answer"),
				"the assistant content must still be persisted; only the empty "+
					"thinking block is dropped")
		})

		It("does NOT emit a thinking message for whitespace-only thinking content", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "  \n  ", Signature: "sig-blank"}
			rawCh <- provider.StreamChunk{Content: "answer"}
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
			Expect(thinkingMsgs).To(BeEmpty(),
				"whitespace-only thinking carries no information — emitting a blank "+
					"thinking bubble in the UI is noise, and persisting it feeds the "+
					"Anthropic 400 bug on the next turn")
		})
	})

	Context("when an assistant turn produces only thinking with no content", func() {
		// Bug pin: OpenAI-compat reasoning providers (zai/glm-4.6, DeepSeek-R1)
		// occasionally emit a Done after only reasoning_content tokens — no
		// content tokens, no tool calls. Before this fix, flushContent
		// early-returned on empty contentBuf, leaving the persisted session
		// with a free-floating thinking message and no assistant turn. The
		// chat UI saw nothing of Role: "assistant" and rendered a stalled
		// session. The accumulator must synthesise a placeholder assistant
		// message carrying the thinking blocks so the turn is renderable.
		//
		// Smoking gun: session 3c5374fd-2835-4720-b543-0c3c95b028aa on
		// glm-4.6 — 362 chunks of reasoning_content, zero content, Done,
		// 1492-char thinking block stranded with no enclosing assistant.
		It("synthesises a placeholder assistant message carrying the thinking blocks", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "weighing the request", Signature: "sig-A"}
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
			Expect(assistantMsgs).To(HaveLen(1),
				"a thinking-only turn must still produce an assistant message so "+
					"the UI can render the turn — without it the session appears stalled")
			Expect(assistantMsgs[0].Content).To(BeEmpty(),
				"the placeholder carries no content because the model produced none")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("weighing the request"))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Signature).To(Equal("sig-A"))
			Expect(assistantMsgs[0].AgentID).To(Equal("agent-1"),
				"agent stamping is symmetric with the content-bearing path")
		})

		It("stamps the placeholder with model, provider, and stop_reason from the turn", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Thinking:   "long reasoning chain",
				Signature:  "sig-B",
				ModelID:    "glm-4.6",
				ProviderID: "zai",
			}
			rawCh <- provider.StreamChunk{
				EventType:  "stop_reason",
				StopReason: "end_turn",
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
			Expect(assistantMsgs[0].ModelName).To(Equal("glm-4.6"))
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
			Expect(assistantMsgs[0].StopReason).To(Equal("end_turn"))
		})

		It("does not synthesise on a tool-bearing turn from a unified-assistant provider (Anthropic)", func() {
			// Regression guard for unified-assistant providers: Anthropic's
			// wire format packs `text`, `thinking`, and `tool_use` content
			// blocks into a single assistant message per round. The
			// accumulator's tool_call message is the persisted turn artefact;
			// adding an empty-content placeholder would double-stamp the
			// history for every Anthropic turn that uses tools.
			//
			// The provider-aware predicate keys on the engine-stamped
			// chunk.ProviderID, so this test sets ProviderID="anthropic" on
			// the chunks the accumulator's lastProviderID will latch onto.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Thinking:   "deciding which tool",
				Signature:  "sig-T",
				ProviderID: "anthropic",
			}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "call-1",
					Name:      "search",
					Arguments: map[string]any{"query": "anything"},
				},
				ProviderID: "anthropic",
			}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "anthropic"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(BeEmpty(),
				"thinking-then-tool-call on a unified-assistant provider must NOT "+
					"produce a synthesised placeholder; the tool_call is the "+
					"visible shape of the Anthropic turn")
		})

		It("synthesises a placeholder on a tool-bearing turn from a non-unified provider (zai/openaicompat)", func() {
			// Bug pin (May 2026, follow-up): user-visible "agent returns empty
			// response after tool use" symptom. OpenAI-compat reasoning
			// providers (zai/glm-4.6, DeepSeek-R1) emit `reasoning_content`
			// on its own channel, separate from `content` and `tool_calls`.
			// On a thinking-then-tool-call turn ending with Done, the prior
			// over-aggressive turnHadToolCall gate suppressed synthesis
			// unconditionally — the persisted history was [thinking,
			// tool_call] with no enclosing assistant message, and the
			// chat UI rendered thinking → gap → tool widget with nothing
			// to close the turn.
			//
			// The fix gates the suppression on
			// providerProducesUnifiedAssistant(s.lastProviderID), so on zai
			// (and every other openaicompat-style provider) the placeholder
			// IS synthesised, attaching the accumulated ThinkingBlocks under
			// a single assistant message per turn — matching the
			// one-assistant-message-per-turn pattern other harnesses
			// converge on.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Thinking:   "deciding which tool",
				Signature:  "sig-zai",
				ProviderID: "zai",
				ModelID:    "glm-4.6",
			}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "call-zai",
					Name:      "search",
					Arguments: map[string]any{"query": "anything"},
				},
				ProviderID: "zai",
				ModelID:    "glm-4.6",
			}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai", ModelID: "glm-4.6"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"thinking-then-tool-call on a non-unified provider MUST produce "+
					"a synthesised assistant placeholder so the turn's reasoning "+
					"is not stranded; the chat UI needs an assistant message to "+
					"close the turn after the tool widget renders")
			Expect(assistantMsgs[0].Content).To(BeEmpty(),
				"the placeholder carries no content because the model emitted "+
					"reasoning_content only — no visible content tokens")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
				"the accumulated thinking block must attach to the placeholder "+
					"so persisted history holds the reasoning under an assistant turn")
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("deciding which tool"))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Signature).To(Equal("sig-zai"))
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"),
				"the engine-stamped provider carries onto the placeholder so "+
					"per-turn attribution survives reload")
			Expect(assistantMsgs[0].ModelName).To(Equal("glm-4.6"))
			Expect(assistantMsgs[0].AgentID).To(Equal("agent-1"))
		})

		It("synthesises a placeholder on every round of a multi-round tool loop (zai/openaicompat)", func() {
			// Bug pin: a multi-round tool loop on zai accumulates
			// ThinkingBlocks across every round (round 1 reasoning + tool A,
			// round 2 reasoning + tool B, ..., round N reasoning + Done).
			// flushContent never resets thinkingBlocks because contentBuf is
			// always empty; applyToolCall sets turnHadToolCall=true on
			// round 1 and never clears it. Before this fix, the final Done
			// (after round N's reasoning) hit the gate, the synthesis was
			// suppressed, and every round's reasoning was dropped from the
			// persisted history.
			//
			// With the provider-aware gate, the final synthesis fires for
			// non-unified providers and ALL accumulated ThinkingBlocks
			// attach to the placeholder. The user sees one assistant
			// message per turn carrying the full reasoning chain.
			rawCh := make(chan provider.StreamChunk, 8)
			rawCh <- provider.StreamChunk{Thinking: "round 1 thought", Signature: "sig-r1", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{
				ToolCall:   &provider.ToolCall{ID: "call-r1", Name: "search", Arguments: map[string]any{"q": "a"}},
				ProviderID: "zai",
			}
			rawCh <- provider.StreamChunk{Thinking: "round 2 thought", Signature: "sig-r2", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{
				ToolCall:   &provider.ToolCall{ID: "call-r2", Name: "search", Arguments: map[string]any{"q": "b"}},
				ProviderID: "zai",
			}
			rawCh <- provider.StreamChunk{Thinking: "round 3 thought", Signature: "sig-r3", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"the end-of-turn synthesis must fire exactly once for the whole "+
					"multi-round tool loop, carrying every round's reasoning")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(3),
				"all three rounds' thinking blocks must attach to the placeholder; "+
					"silently dropping them is the user-visible 'empty response after "+
					"tool use' regression this fix closes")
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("round 1 thought"))
			Expect(assistantMsgs[0].ThinkingBlocks[1].Thinking).To(Equal("round 2 thought"))
			Expect(assistantMsgs[0].ThinkingBlocks[2].Thinking).To(Equal("round 3 thought"))
		})

		It("does not regress the content-bearing turn — content and thinking still co-attach to the assistant message", func() {
			// Pair guard: with thinking AND content present, behaviour is
			// unchanged — exactly one assistant message containing the
			// content and the thinking blocks.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "I think therefore", Signature: "sig-C"}
			rawCh <- provider.StreamChunk{Content: "the answer is here"}
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
			Expect(assistantMsgs).To(HaveLen(1),
				"content-bearing turns must still produce exactly one assistant message")
			Expect(assistantMsgs[0].Content).To(Equal("the answer is here"))
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Signature).To(Equal("sig-C"))
		})
	})

	// Streaming Coherence — Slice C (May 2026). True-empty-turn fall-through:
	// when a turn produced no content, no thinking, and no tool calls, the
	// accumulator must still emit a placeholder assistant message stamped
	// with StopReasonEmptyTurn so the chat UI can render a soft-error
	// affordance immediately on Done. Pre-slice such turns left the in-
	// flight bubble running until the 60s watchdog tripped.
	Context("when a turn produces no content, no thinking, and no tool calls (true empty turn — Slice C)", func() {
		It("synthesises a placeholder assistant stamped with StopReasonEmptyTurn", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			// A bare Done with no preceding content / thinking / tool_call —
			// the upstream stream finished with nothing produced.
			rawCh <- provider.StreamChunk{
				ModelID:    "claude-sonnet-4-6",
				ProviderID: "anthropic",
				Done:       true,
			}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"true-empty turn must still produce an assistant placeholder so the UI can "+
					"render an empty_turn affordance instead of leaving the in-flight bubble running")
			Expect(assistantMsgs[0].Content).To(BeEmpty())
			Expect(assistantMsgs[0].ThinkingBlocks).To(BeEmpty())
			Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonEmptyTurn))
			Expect(assistantMsgs[0].ModelName).To(Equal("claude-sonnet-4-6"))
			Expect(assistantMsgs[0].ProviderName).To(Equal("anthropic"))
		})

		It("does NOT synthesise on a tool-bearing turn", func() {
			// A tool-bearing turn's deliverable is the tool result; an
			// empty-turn placeholder would be wrong there. The synthesizer
			// gates on `!turnHadToolCall` to avoid this.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				ModelID:    "claude-sonnet-4-6",
				ProviderID: "anthropic",
				ToolCall: &provider.ToolCall{
					ID:        "tc-1",
					Name:      "read_file",
					Arguments: map[string]any{"path": "/tmp/foo"},
				},
			}
			rawCh <- provider.StreamChunk{Done: true}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			// No empty-turn placeholder among the assistant messages.
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					Expect(m.StopReason).NotTo(Equal(session.StopReasonEmptyTurn),
						"tool-bearing turns must not produce empty_turn placeholders")
				}
			}
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

		// Bug pin (E.1 / F.2): the OpenAI-compat layer only emits a
		// terminal Done chunk when FinishReason != "" (see
		// internal/provider/openaicompat/openaicompat.go:229-232). A
		// thinking-only turn from a reasoning provider (zai/glm-4.6,
		// DeepSeek-R1) can finish with no FinishReason chunk — the
		// goroutine then close()s rawCh without ever emitting Done. The
		// accumulator's `!ok` branch flushed thinking + content and
		// returned without calling synthesizePlaceholderAssistant, so
		// the persisted history again held a stranded Role: "thinking"
		// with no enclosing assistant — the same chat-stalls symptom
		// f918bb9f was meant to fix. The synthesis must fire on this
		// end-of-stream path too.
		It("synthesises a placeholder assistant message when only thinking arrived before channel close (no terminal Done)", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				Thinking:   "weighing the request",
				Signature:  "sig-close-only",
				ModelID:    "glm-4.6",
				ProviderID: "zai",
			}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"a thinking-only turn that ends with rawCh-close (no Done) must "+
					"still produce an assistant placeholder — otherwise the chat UI "+
					"sees no assistant turn to render and the session appears stalled, "+
					"the same symptom f918bb9f closed for the Done-then-close path")
			Expect(assistantMsgs[0].Content).To(BeEmpty(),
				"the placeholder carries no content because the model produced none")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
				"the accumulated thinking blocks must attach to the placeholder so "+
					"the next turn can replay them via WithPriorMessages — without "+
					"this Anthropic disables thinking continuity silently")
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("weighing the request"))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Signature).To(Equal("sig-close-only"))
			Expect(assistantMsgs[0].AgentID).To(Equal("agent-1"),
				"agent stamping is symmetric with the Done-then-close path")
			Expect(assistantMsgs[0].ModelName).To(Equal("glm-4.6"),
				"the engine-stamped model carries onto the placeholder so reload sees the producer")
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
		})

		It("does not synthesise a placeholder when only content (no thinking) arrived before channel close", func() {
			// Regression guard: pure-content close-without-Done turns are
			// already covered by the "flushes accumulated assistant
			// content on channel close" spec above — they produce exactly
			// one assistant message, not two. The synthesis gate
			// (len(thinkingBlocks) > 0) must keep this case unchanged.
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{Content: "no thinking here"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"content-bearing close-without-Done must continue to produce exactly "+
					"one assistant message; the synthesis path must not double-fire")
			Expect(assistantMsgs[0].Content).To(Equal("no thinking here"))
			Expect(assistantMsgs[0].ThinkingBlocks).To(BeEmpty())
		})

		It("does not synthesise a placeholder on close-without-Done for a tool-bearing turn from a unified-assistant provider (Anthropic)", func() {
			// Regression guard mirroring the Done-path Anthropic gate:
			// Anthropic packs assistant content + tool_use into one
			// message per round, so the tool_call is the persisted
			// turn artefact and an extra placeholder would double-stamp
			// the history.
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				Thinking:   "deciding which tool",
				Signature:  "sig-T",
				ProviderID: "anthropic",
			}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "call-close",
					Name:      "search",
					Arguments: map[string]any{"query": "anything"},
				},
				ProviderID: "anthropic",
			}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(BeEmpty(),
				"thinking-then-tool-call ending with rawCh-close on a unified-assistant "+
					"provider must NOT produce a synthesised placeholder; the tool_call "+
					"is the visible shape of the Anthropic turn")
		})

		It("synthesises a placeholder on close-without-Done for a tool-bearing turn from a non-unified provider (zai/openaicompat)", func() {
			// Bug pin (May 2026, follow-up): the close-without-Done path
			// must mirror the Done-path provider-aware gate. A reasoning
			// provider whose stream ends after a tool_call without ever
			// emitting a terminal Done (the openaicompat layer only emits
			// Done when FinishReason != "" — see
			// internal/provider/openaicompat/openaicompat.go:229-238)
			// otherwise drops the turn's reasoning at end-of-stream.
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				Thinking:   "deciding which tool",
				Signature:  "sig-zai-close",
				ProviderID: "zai",
			}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "call-zai-close",
					Name:      "search",
					Arguments: map[string]any{"query": "anything"},
				},
				ProviderID: "zai",
			}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"thinking-then-tool-call ending with rawCh-close on a non-unified "+
					"provider MUST produce a synthesised placeholder; otherwise the "+
					"turn's reasoning is silently dropped at end-of-stream")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("deciding which tool"))
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
		})
	})

	Context("when an assistant turn produced only raw thinking text (no Signature, no stop_reason, no tool_call)", func() {
		// Bug pin (May 7 2026, follow-up to e8e7226f): live reproducer
		// session 718b5d51-f01b-45f0-80bb-31329a9d44e7 message index 9
		// (glm-4.5 via zai) ended with a Thinking-only turn whose
		// reasoning_content tokens never carried a Signature (no
		// content_block_stop boundary on this provider) and whose Done
		// chunk arrived without a preceding `stop_reason` event. After
		// flushThinking + flushContent, ThinkingBlocks did accumulate (one
		// block with empty Signature) and synthesizePlaceholderAssistant
		// fired — but the persisted placeholder carried StopReason="" so
		// the Vue UI predicate from 0f27ac98 (`stopReason !== ""`) saw
		// nothing and rendered a blank bubble. The fix synthesises a
		// non-empty StopReason ("thinking_only") whenever the turn's
		// upstream stop_reason was missing, so the existing UI affordance
		// can locate the placeholder without touching the Vue render
		// branch.
		It("synthesises a placeholder with a non-empty StopReason on raw-thinking Done with no upstream stop_reason", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Thinking:   "\n<think>\n<tool_call>bash\n</tool_call>",
				ProviderID: "zai",
				ModelID:    "glm-4.5",
			}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai", ModelID: "glm-4.5"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"a raw-thinking-only turn must still produce an assistant placeholder "+
					"so the Vue UI affordance from 0f27ac98 has a row to render — "+
					"otherwise the user sees a blank bubble")
			Expect(assistantMsgs[0].Content).To(BeEmpty(),
				"the placeholder carries no content because the model produced none")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
				"the captured raw thinking text must attach as a synthetic ThinkingBlock "+
					"so the Vue predicate (thinkingBlocks.length > 0) is satisfied")
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("\n<think>\n<tool_call>bash\n</tool_call>"),
				"the raw thinking text is preserved verbatim on the synthetic block — "+
					"the malformed-tool-call XML is still readable in the UI")
			Expect(assistantMsgs[0].StopReason).NotTo(BeEmpty(),
				"the placeholder MUST carry a non-empty StopReason so the Vue UI "+
					"predicate from 0f27ac98 (`stopReason !== \"\"`) locates the "+
					"affordance — without this the existing render branch sees "+
					"nothing to anchor on and the bubble stays blank")
			Expect(assistantMsgs[0].AgentID).To(Equal("agent-1"),
				"agent stamping is symmetric with the other synthesis paths")
			Expect(assistantMsgs[0].ModelName).To(Equal("glm-4.5"),
				"the engine-stamped model carries onto the placeholder so reload "+
					"sees the producer that emitted the raw reasoning")
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
		})

		It("synthesises a placeholder with a non-empty StopReason on raw-thinking close-without-Done", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				Thinking:   "raw reasoning, no signature",
				ProviderID: "zai",
				ModelID:    "glm-4.5",
			}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"the close-without-Done end-of-stream path must mirror the Done path "+
					"for raw-thinking turns — otherwise providers whose stream ends "+
					"after reasoning_content with no FinishReason chunk strand the turn")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1))
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(Equal("raw reasoning, no signature"))
			Expect(assistantMsgs[0].StopReason).NotTo(BeEmpty(),
				"the placeholder MUST carry a non-empty StopReason on the close-without-Done "+
					"path too, so the Vue UI affordance is reachable on every end-of-stream "+
					"branch the provider can take")
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
		})

		It("promotes answer-shaped terminal raw thinking from zai into assistant content", func() {
			// Regression pin (May 2026): GLM via Z.ai can emit the final
			// user-facing markdown answer on `reasoning_content` instead of
			// `content`. If the accumulator blindly flushes that as Role:
			// "thinking", the UI shows the real reply inside a THINKING
			// block and then adds "Reply didn't come through" from the
			// empty assistant placeholder. Answer-shaped terminal thinking
			// from non-unified providers should render as the assistant
			// reply instead.
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				Thinking:   "## Your Life Admin Action Plan\n\nStart Here\n\n1. Find the reference number.",
				ProviderID: "zai",
				ModelID:    "glm-4.6",
			}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai", ModelID: "glm-4.6"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var thinkingMsgs []session.Message
			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				switch m.Role {
				case "thinking":
					thinkingMsgs = append(thinkingMsgs, m)
				case "assistant":
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(BeEmpty(),
				"the final user-facing answer must not be stranded in a visible THINKING block")
			Expect(assistantMsgs).To(HaveLen(1),
				"the answer-shaped terminal reasoning should become the assistant reply")
			Expect(assistantMsgs[0].Content).To(ContainSubstring("Your Life Admin Action Plan"))
			Expect(assistantMsgs[0].ThinkingBlocks).To(BeEmpty(),
				"the promoted answer itself is not replayable model thinking")
			Expect(assistantMsgs[0].StopReason).NotTo(Equal(session.StopReasonThinkingOnly),
				"content-bearing promoted answers must not trigger the thinking-only UI affordance")
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
			Expect(assistantMsgs[0].ModelName).To(Equal("glm-4.6"))
		})

		It("promotes answer-shaped terminal raw thinking on close-without-Done", func() {
			rawCh := make(chan provider.StreamChunk, 2)
			rawCh <- provider.StreamChunk{
				Thinking:   "Here is the concise action plan.\n\n- Send the payment-plan email.",
				ProviderID: "zai",
				ModelID:    "glm-4.6",
			}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"the close-without-Done path should use the same terminal-answer promotion")
			Expect(assistantMsgs[0].Content).To(ContainSubstring("concise action plan"))
			Expect(assistantMsgs[0].StopReason).NotTo(Equal(session.StopReasonThinkingOnly))
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
		})

		It("DOES synthesise an empty_turn placeholder when the turn produced no content AND no thinking AND no tools (Slice C — Streaming Coherence May 2026)", func() {
			// Streaming Coherence Slice C — pre-slice this behaviour was
			// "no placeholder; the in-flight bubble is left to the watchdog".
			// That left the user staring at a stuck indicator for 60s on
			// every empty turn (provider returned nothing — billing limit,
			// a model bug, an aborted turn). The new contract: synthesise
			// an `empty_turn` placeholder so the chat UI can render a
			// soft-error affordance immediately on Done. The previous
			// behaviour pin is replaced by this one — the new contract is
			// the better default for the user.
			rawCh := make(chan provider.StreamChunk, 1)
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"true-empty turn must produce exactly one placeholder")
			Expect(assistantMsgs[0].Content).To(BeEmpty())
			Expect(assistantMsgs[0].ThinkingBlocks).To(BeEmpty())
			Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonEmptyTurn))
			Expect(assistantMsgs[0].ProviderName).To(Equal("zai"))
		})

		It("does NOT regress a content-bearing turn when raw Thinking text was also present", func() {
			// Negative: a normal turn with both content and raw thinking
			// text must continue producing exactly one content-bearing
			// assistant message — no extra synthesised placeholder. The
			// fix must not double-fire on content-bearing turns.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "raw reasoning, no sig", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Content: "the answer is 42", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"content-bearing turns must produce exactly one assistant message "+
					"regardless of whether raw thinking text was also captured")
			Expect(assistantMsgs[0].Content).To(Equal("the answer is 42"))
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
				"the raw thinking still attaches to the content-bearing message — "+
					"existing flushContent path covers this")
		})

		It("does NOT regress a tool-bearing turn (existing covered path) — no double-firing on raw thinking + tool_call", func() {
			// Negative: a tool-bearing turn from a non-unified provider
			// already fires synthesis exactly once at end-of-turn carrying
			// the accumulated reasoning. The raw-thinking fix must not
			// produce a second placeholder on the same turn — the existing
			// behaviour from e8e7226f remains: one assistant message per
			// turn, full reasoning chain attached.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "raw reasoning, no sig", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "call-rt",
					Name:      "search",
					Arguments: map[string]any{"q": "anything"},
				},
				ProviderID: "zai",
			}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai"}
			close(rawCh)

			out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
			drainChannel(out)

			var assistantMsgs []session.Message
			for _, m := range appender.messages {
				if m.Role == "assistant" {
					assistantMsgs = append(assistantMsgs, m)
				}
			}
			Expect(assistantMsgs).To(HaveLen(1),
				"a tool-bearing turn with raw thinking still produces exactly one "+
					"assistant placeholder — the e8e7226f synthesis path covers this")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
				"the raw thinking attaches to the placeholder so persisted history "+
					"holds the reasoning under the assistant turn")
			Expect(assistantMsgs[0].StopReason).NotTo(BeEmpty(),
				"every synthesised placeholder MUST carry a non-empty StopReason — "+
					"the tool-bearing path inherits the same fix")
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
