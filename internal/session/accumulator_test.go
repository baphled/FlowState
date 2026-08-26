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

	// Streaming Coherence — Fabricated-completion guard (May 2026).
	// Live reproducer: a glm-5.1 coordinator session emitted assistant turns
	// containing fabricated completion language
	// (`✅ Master bug report written to: ~/vaults/...`) twice in a row with
	// ZERO preceding `write`/`bash`/`delegate` tool calls. Nothing was saved
	// — the model self-reported a file operation it never performed. The
	// engine must annotate such turns so downstream consumers (chat UI,
	// audit log, future blocking logic) can distinguish a fabricated
	// completion claim from a real one.
	//
	// Guard contract: content-bearing assistant turn + completion-language
	// signature + NO tool call AND NO delegation in this turn →
	// Message.StopReason = StopReasonFabricatedCompletion. Content flows
	// verbatim; only the StopReason is set.
	Context("when a content-bearing turn matches the fabricated-completion signature", func() {
		It("stamps StopReasonFabricatedCompletion when the turn has no tool_call and no delegation", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				Content:    "✅ Master bug report written to: ~/vaults/baphled/bugs/master.md",
				ProviderID: "zai",
				ModelID:    "glm-5.1",
			}
			rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai", ModelID: "glm-5.1"}
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
				"content-bearing turn produces exactly one assistant message — the "+
					"guard sets StopReason but does NOT suppress or duplicate the message")
			Expect(assistantMsgs[0].Content).To(Equal(
				"✅ Master bug report written to: ~/vaults/baphled/bugs/master.md"),
				"content MUST flow verbatim — the guard annotates, never mutates the body")
			Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonFabricatedCompletion),
				"a self-reported file-write with no qualifying tool evidence in the turn "+
					"MUST be stamped as fabricated so the UI / audit layer can flag it")
		})

		It("matches the bare check-mark glyph as a fabrication signature", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{Content: "✅ done", ProviderID: "zai"}
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
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonFabricatedCompletion),
				"the literal ✅ glyph is part of the documented fabrication signature — "+
					"glm-5.1 emits it as a completion claim in self-reported file-write turns")
		})

		It("matches case-insensitively on the phrase 'persisted to'", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{Content: "Persisted to /tmp/output.md.", ProviderID: "zai"}
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
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonFabricatedCompletion),
				"the signature MUST be case-insensitive — 'Persisted to' and "+
					"'persisted to' are both completion claims")
		})

		It("does NOT stamp the turn when a real tool_call accompanied the content", func() {
			// A turn where the model says "saved to /tmp/x" and then issues
			// an actual tool_call is an announcement of work it is doing
			// in-band — that is NOT fabrication. The guard reads the
			// per-turn flags (turnHadToolCall) so a real tool round
			// suppresses the stamp.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Content:    "✅ Master bug report written to: /tmp/master.md",
				ProviderID: "zai",
			}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "tc-write",
					Name:      "write",
					Arguments: map[string]any{"path": "/tmp/master.md", "content": "..."},
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
			// The flushed content message — produced before the tool_call —
			// must NOT carry the fabrication stamp because the same turn
			// also produced a real tool_call.
			for _, m := range assistantMsgs {
				Expect(m.StopReason).NotTo(Equal(session.StopReasonFabricatedCompletion),
					"a turn carrying a real tool_call is announcement-then-action — "+
						"the guard MUST NOT false-flag legitimate in-band tool use")
			}
		})

		It("does NOT stamp the turn when a delegation accompanied the content", func() {
			// Mirror of the tool_call gate: a coordinator turn that says
			// "delegated to worker" while also producing a delegation
			// chunk is legitimate. The guard reads turnHadDelegation.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Content:    "✅ Saved to plan.md — delegating now",
				ProviderID: "zai",
			}
			rawCh <- provider.StreamChunk{
				DelegationInfo: &provider.DelegationInfo{
					TargetAgent: "worker",
					Status:      "started",
					ChainID:     "c-1",
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
			for _, m := range assistantMsgs {
				Expect(m.StopReason).NotTo(Equal(session.StopReasonFabricatedCompletion),
					"a turn carrying a real delegation is legitimate — "+
						"the guard MUST NOT false-flag delegation-bearing turns")
			}
		})

		It("does NOT stamp genuine content with no completion phrases", func() {
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{
				Content:    "Here is my plan: I will read the file first.",
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
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).NotTo(Equal(session.StopReasonFabricatedCompletion),
				"genuine content with no completion signature MUST flow through unstamped — "+
					"the guard is a precise detector, not a blanket filter")
		})

		It("preserves the upstream stop_reason precedence on content that does not match the signature", func() {
			// When the upstream provider emits a real stop_reason and the
			// content does NOT match the fabrication signature, the upstream
			// value MUST survive — the guard does not overwrite a real
			// stop_reason on legitimate content.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Content: "All looks good.", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{EventType: "stop_reason", StopReason: "end_turn"}
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
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).To(Equal("end_turn"),
				"the upstream stop_reason MUST be preserved on non-fabricated content — "+
					"the guard only stamps when the content matches the signature AND "+
					"no tool/delegation evidence exists this turn")
		})

		It("preserves the empty-turn sentinel and routes empty-stop-reason thinking-only through the stream-truncation detector", func() {
			// Regression coverage: the fabrication guard fires only on the
			// content-bearing flushContent path. The synthesizePlaceholder
			// empty-turn branch MUST still produce StopReasonEmptyTurn.
			//
			// The thinking-only branch's behaviour pin migrated (May 2026
			// stream-truncation detector): a turn that produced thinking,
			// no content, no tool, no delegation AND no upstream stop_reason
			// IS the openaicompat truncation signature (live reproducer
			// session 8779c2ae-69a4-... glm-4.6/zai — Content="",
			// Thinking=30382, ToolCalls=0, StopReason=""). The synthesised
			// placeholder now stamps StopReasonStreamTruncated so the
			// session manager can flip status active -> failed and the
			// boot-time orphan reap is no longer the only recovery path.

			// Empty-turn path — unchanged.
			emptyAppender := &fakeAppender{}
			emptyCh := make(chan provider.StreamChunk, 1)
			emptyCh <- provider.StreamChunk{Done: true, ProviderID: "zai"}
			close(emptyCh)
			emptyOut := session.AccumulateStream(context.Background(), emptyAppender, "sess-1", "agent-1", emptyCh)
			drainChannel(emptyOut)

			var emptyMsgs []session.Message
			for _, m := range emptyAppender.messages {
				if m.Role == "assistant" {
					emptyMsgs = append(emptyMsgs, m)
				}
			}
			Expect(emptyMsgs).To(HaveLen(1))
			Expect(emptyMsgs[0].StopReason).To(Equal(session.StopReasonEmptyTurn),
				"empty-turn synthesis path is independent of the fabrication guard")

			// Thinking-only path with no upstream stop_reason that DID reach
			// a terminal Done — re-pinned to StopReasonThinkingOnly under the
			// Bug F Done-awareness refinement (June 2026). A clean close
			// (turnSawDone) is NOT a wire-level truncation: the placeholder
			// keeps the non-empty thinking_only sentinel for the UI affordance
			// without flipping the session to failed. The genuine wire-cut
			// signature (close WITHOUT Done) still stamps stream_truncated —
			// see the "stream-truncation detection (Bug F)" specs below.
			thinkingAppender := &fakeAppender{}
			thinkingCh := make(chan provider.StreamChunk, 2)
			thinkingCh <- provider.StreamChunk{Thinking: "reasoning only", ProviderID: "zai"}
			thinkingCh <- provider.StreamChunk{Done: true, ProviderID: "zai"}
			close(thinkingCh)
			thinkingOut := session.AccumulateStream(context.Background(), thinkingAppender, "sess-1", "agent-1", thinkingCh)
			drainChannel(thinkingOut)

			var thinkingMsgs []session.Message
			for _, m := range thinkingAppender.messages {
				if m.Role == "assistant" {
					thinkingMsgs = append(thinkingMsgs, m)
				}
			}
			Expect(thinkingMsgs).To(HaveLen(1))
			Expect(thinkingMsgs[0].StopReason).To(Equal(session.StopReasonThinkingOnly),
				"thinking-only that reached a terminal Done with no upstream stop_reason is a "+
					"clean close, not a wire-cut — the placeholder stamps thinking_only, not "+
					"stream_truncated; truncation fires only on the close-without-Done path")
		})
	})

	// Bug E (May 2026) — abandoned-tool turn detection.
	//
	// Live reproducer: child session 3fcb56df-8224-485c-9187-2aaad9ed5879,
	// glm-4.5 executor under coordinator on zai. Sequence:
	//   1. user → "Writer: Write the master bug report ..."
	//   2. thinking → model plans a `write` tool call in reasoning_content
	//   3. assistant → Content="\n", ToolCalls=null, ThinkingBlocks={plan}
	//
	// The model committed to writing a file in the reasoning channel but
	// never emitted the tool_call. The persisted assistant carried a stray
	// newline as content, no tool_call, and an empty StopReason — the chat
	// UI sees a "completed" turn with no work, no soft-error affordance,
	// and the user's request is silently dropped.
	//
	// Guard contract: a turn whose content is whitespace-only AND whose
	// thinking is non-empty AND that produced NO tool_call AND NO
	// delegation must be stamped with StopReasonAbandonedTool. The
	// accumulated thinking blocks MUST survive on the persisted message so
	// the chat UI can render the abandoned plan as evidence and the user
	// can compose a follow-up. Upstream stop_reason precedence does NOT
	// apply on this path — the guard fires regardless of what the upstream
	// provider claimed about the finish reason, because the wire-level
	// completion signal is the actual fault we are correcting.
	Context("when content is whitespace-only with thinking and no tool/delegation", func() {
		It("stamps StopReasonAbandonedTool when content is a bare newline and thinking is non-empty", func() {
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{
				Thinking:   "The user is asking me to write a master bug report to a specific file path in the vault. I need to write the content they provided to the exact file path they specified. Let me use the write tool to create this file.",
				ProviderID: "zai",
				ModelID:    "glm-4.5",
			}
			rawCh <- provider.StreamChunk{
				Content:    "\n",
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
				"a whitespace-only-content turn with abandoned thinking still produces "+
					"exactly one persisted assistant message — the guard annotates, never "+
					"duplicates or suppresses")
			Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonAbandonedTool),
				"the model committed to a tool call in thinking but never emitted it — "+
					"the turn MUST be stamped as abandoned so the chat UI surfaces "+
					"the soft-error affordance instead of pretending the turn completed")
			Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
				"the planning evidence MUST survive on the persisted message so the "+
					"user can see what the model intended to do and re-issue")
			Expect(assistantMsgs[0].ThinkingBlocks[0].Thinking).To(ContainSubstring("write tool"),
				"the original thinking content MUST flow through verbatim — the guard "+
					"only annotates StopReason, never mutates the thinking body")
		})

		It("stamps StopReasonAbandonedTool when content is multiple whitespace characters", func() {
			// Defensive: glm variants have been observed emitting " \n", "\t\n",
			// and other whitespace-only sequences after a fully reasoned plan.
			// All of them are the same wire fault — a non-empty contentBuf
			// that carries no signal — and must be detected uniformly.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "Planning the next call...", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Content: " \n\t", ProviderID: "zai"}
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
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonAbandonedTool),
				"any whitespace-only content trip the guard — the only distinguishing "+
					"feature is the absence of any meaningful payload alongside reasoning")
		})

		It("does NOT stamp the turn when a real tool_call accompanied the whitespace content", func() {
			// A turn with real tool evidence is legitimate, even if its
			// content body is whitespace-only — some providers separate
			// content and tool channels. The guard reads turnHadToolCall.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "planning", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Content: "\n", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{
				ToolCall: &provider.ToolCall{
					ID:        "tc-write",
					Name:      "write",
					Arguments: map[string]any{"path": "/tmp/x"},
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
			for _, m := range assistantMsgs {
				Expect(m.StopReason).NotTo(Equal(session.StopReasonAbandonedTool),
					"a turn with a real tool_call is legitimate — the guard MUST NOT "+
						"false-flag tool-bearing turns whose content channel is empty")
			}
		})

		It("does NOT stamp the turn when content is meaningful even with no tool_call", func() {
			// A turn that produces real prose (no tools, no delegation) is
			// a normal answer. The guard fires only on whitespace-only
			// content with abandoned thinking.
			rawCh := make(chan provider.StreamChunk, 4)
			rawCh <- provider.StreamChunk{Thinking: "thinking about it", ProviderID: "zai"}
			rawCh <- provider.StreamChunk{Content: "Here is my answer.", ProviderID: "zai"}
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
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).NotTo(Equal(session.StopReasonAbandonedTool),
				"meaningful prose content is a real answer — the guard fires only on "+
					"whitespace-only content that carries no signal")
		})

		It("does NOT stamp the turn when whitespace content arrives without any thinking", func() {
			// A whitespace-only-content turn with no thinking is just the
			// pre-existing empty-ish-content case (the model produced
			// nothing). The empty-turn synthesizer handles that path; the
			// abandoned-tool guard requires evidence of planning.
			rawCh := make(chan provider.StreamChunk, 3)
			rawCh <- provider.StreamChunk{Content: "\n", ProviderID: "zai"}
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
			Expect(assistantMsgs).To(HaveLen(1))
			Expect(assistantMsgs[0].StopReason).NotTo(Equal(session.StopReasonAbandonedTool),
				"abandoned-tool requires non-empty thinking — without planning evidence "+
					"the turn is not an abandoned tool call, it is a different fault class")
		})
	})

	// Stream-truncation detector (May 2026, follow-up to Bug E).
	//
	// Live reproducers (read-only — referenced for forensic context):
	//   - Session 8779c2ae-69a4-... — plan-writer on glm-4.6/zai.
	//     Final assistant: Content="", Thinking=30382, ToolCalls=0,
	//     StopReason="". Model announced intent to write the plan in
	//     reasoning_content, then the upstream wire cut before any
	//     tool_use payload landed. Pre-detector this stamped
	//     StopReasonThinkingOnly which made the session indistinguishable
	//     from a clean reasoning-only turn — boot-time orphan reap (30 min
	//     grace) was the only recovery path.
	//   - Session 5e37d947-c049-4d5c-a209-658d9c6a5186 — plan-writer on
	//     glm-5/zai (post-PR7 manifest tightening). Final assistant:
	//     Content=154 ("Let me generate the comprehensive revision:"),
	//     Thinking=2074, ToolCalls=0, StopReason="". Same fault shape,
	//     but content-bearing — flushContent persisted the message with
	//     StopReason="" and the chat UI rendered a silent "completed"
	//     bubble carrying only the model's announce-intent prose.
	//
	// Detector contract: when the assistant turn produced ANY model
	// activity (content OR thinking) AND produced NO tool_call AND NO
	// delegation AND the upstream provider gave NO stop_reason chunk,
	// stamp StopReasonStreamTruncated. The signature is distinct from
	// StopReasonAbandonedTool (which requires WHITESPACE-only content
	// with a thinking block — the model self-cancelled by emitting a
	// stray newline) — truncation is a wire-level cut mid-output where
	// the model never got to its terminal payload.
	//
	// False-positive surface: the openaicompat provider only emits Done
	// when it observed an upstream finish_reason (openaicompat.go:540-547
	// `if sawFinish`). A real provider stream that ends with the channel
	// closing AND no finish_reason on the last chunk IS the truncation
	// pattern in production; synthetic test fixtures that send raw Done
	// without preceding stop_reason are also routed through this branch
	// (see the "preserves the empty-turn sentinel and routes empty-stop-
	// reason thinking-only through the stream-truncation detector" pin
	// above — that synthetic fixture is treated as truncation by design).
	Describe("stream-truncation detection (Bug F, May 2026)", func() {
		Context("content-bearing flushContent path", func() {
			It("stamps StopReasonStreamTruncated when content is non-empty and no upstream stop_reason fired", func() {
				// Reproducer 5e37d947 shape: announce-intent prose then
				// channel close, no finish_reason ever observed by
				// openaicompat. The accumulator's chunk.Done path was
				// never reached because the provider never emitted Done;
				// the channel-close branch ran flushThinking + flushContent.
				rawCh := make(chan provider.StreamChunk, 4)
				rawCh <- provider.StreamChunk{
					Thinking:   "Let me think about how to structure this plan...",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				rawCh <- provider.StreamChunk{
					Content:    "Let me generate the comprehensive revision:",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				// No stop_reason chunk, no Done — channel just closes.
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
					"a truncated content-bearing turn still produces exactly one assistant message — "+
						"the detector annotates, never duplicates or suppresses")
				Expect(assistantMsgs[0].Content).To(ContainSubstring("Let me generate"),
					"the announce-intent prose flows through verbatim — the detector only annotates StopReason")
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonStreamTruncated),
					"content + no-tool + no-delegation + empty-stop-reason IS the truncation signature — "+
						"the persisted message must carry stream_truncated so the session manager can "+
						"flip status active -> failed and downstream consumers (UI banner, audit) can flag it")
			})

			It("stamps StopReasonStreamTruncated even when thinking is absent (pure content truncation)", func() {
				// Some glm variants emit straight to content without
				// reasoning_content. Truncation is keyed on the absence
				// of an upstream stop_reason — thinking is incidental.
				rawCh := make(chan provider.StreamChunk, 3)
				rawCh <- provider.StreamChunk{
					Content:    "Step one: I will fetch the file. Step two:",
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
				Expect(assistantMsgs).To(HaveLen(1))
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonStreamTruncated),
					"a content-bearing turn cut mid-output must be flagged regardless of whether the "+
						"reasoning channel produced anything")
			})
		})

		Context("synthesizePlaceholderAssistant thinking-only path", func() {
			It("stamps StopReasonStreamTruncated when thinking is non-empty, content is empty, and no upstream stop_reason fired", func() {
				// Reproducer 8779c2ae shape: glm-4.6/zai emitted 30382 chars
				// of reasoning, never produced content, never produced a
				// tool_call, never produced a finish_reason. Pre-detector
				// the placeholder stamped StopReasonThinkingOnly which
				// masked the truncation as a clean reasoning-only turn.
				rawCh := make(chan provider.StreamChunk, 3)
				rawCh <- provider.StreamChunk{
					Thinking: "I need to plan the file write. The user wants " +
						"a comprehensive plan document. Let me draft the sections " +
						"in my head before emitting the write tool call...",
					ProviderID: "zai",
					ModelID:    "glm-4.6",
				}
				// No content, no stop_reason, no Done — channel closes.
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
					"the placeholder synthesiser still produces exactly one assistant artefact — "+
						"the persisted history holds the reasoning evidence under an assistant turn "+
						"so the chat UI can render the abandoned plan and the user can re-issue")
				Expect(assistantMsgs[0].Content).To(BeEmpty())
				Expect(assistantMsgs[0].ThinkingBlocks).To(HaveLen(1),
					"the reasoning content survives on the placeholder as forensic evidence — "+
						"the operator can read what the model intended to do")
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonStreamTruncated),
					"thinking + no-content + no-tool + no-delegation + empty-stop-reason IS the "+
						"openaicompat truncation signature — the placeholder must stamp stream_truncated "+
						"so the session manager can flip status active -> failed (replacing the old "+
						"StopReasonThinkingOnly fallback which silently masked the fault)")
			})

			It("does NOT stamp StopReasonStreamTruncated when an upstream stop_reason was captured", func() {
				// A turn that produced only thinking but DID emit a clean
				// finish_reason (e.g. some reasoning provider routes
				// stop_reason but skips content) is a legitimate
				// reasoning-only turn — the upstream stop_reason
				// precedence applies.
				rawCh := make(chan provider.StreamChunk, 3)
				rawCh <- provider.StreamChunk{Thinking: "reasoning text", ProviderID: "zai"}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "end_turn",
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
				Expect(assistantMsgs).To(HaveLen(1))
				Expect(assistantMsgs[0].StopReason).To(Equal("end_turn"),
					"upstream stop_reason precedence — when the provider gave a real terminal "+
						"reason, the detector MUST NOT override it; truncation fires only on "+
						"empty stop_reason")
			})
		})

		Context("negatives — the detector must not fire on legitimate or differently-shaped turns", func() {
			It("does NOT stamp StopReasonStreamTruncated when an upstream stop_reason chunk fired (content-bearing path)", func() {
				rawCh := make(chan provider.StreamChunk, 4)
				rawCh <- provider.StreamChunk{Content: "the answer", ProviderID: "zai"}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "end_turn",
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
				Expect(assistantMsgs).To(HaveLen(1))
				Expect(assistantMsgs[0].StopReason).To(Equal("end_turn"),
					"a clean end_turn path is unchanged — the detector only fires on the empty-stop-reason signature")
			})

			It("does NOT stamp StopReasonStreamTruncated when a tool_call accompanied the turn", func() {
				// A tool-bearing turn whose finish_reason chunk failed to
				// land is not a truncation in the sense the detector
				// flags: the tool_call is the turn's deliverable. The
				// existing turn-interrupted/end-of-stream synthesis
				// already handles this shape via other paths.
				rawCh := make(chan provider.StreamChunk, 4)
				rawCh <- provider.StreamChunk{
					Content:    "Looking up the file...",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					ToolCall: &provider.ToolCall{
						ID:        "tc-1",
						Name:      "read",
						Arguments: map[string]any{"path": "/tmp/x"},
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
				for _, m := range assistantMsgs {
					Expect(m.StopReason).NotTo(Equal(session.StopReasonStreamTruncated),
						"a tool_call this turn proves the model reached its deliverable — "+
							"the truncation detector MUST NOT false-flag a real tool-bearing turn "+
							"just because the upstream finish_reason was missing")
				}
			})

			It("does NOT clobber StopReasonAbandonedTool when content is whitespace-only with thinking", func() {
				// Bug E sentinel: content = "\n", thinking non-empty, no
				// tool/delegation. AbandonedTool fires first because the
				// content IS whitespace-only — the model self-cancelled.
				// The truncation detector must NOT override this.
				rawCh := make(chan provider.StreamChunk, 3)
				rawCh <- provider.StreamChunk{Thinking: "Let me use the write tool", ProviderID: "zai"}
				rawCh <- provider.StreamChunk{Content: "\n", ProviderID: "zai"}
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
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonAbandonedTool),
					"the abandoned-tool sentinel keys on whitespace-only content with thinking — "+
						"its semantics (model self-cancelled by emitting a stray newline) differ "+
						"from truncation (wire-level cut mid-output); the existing sentinel must win")
			})

			It("does NOT stamp StopReasonStreamTruncated when the turn is a true empty turn (no content, no thinking, no tools)", func() {
				// Existing empty-turn synthesis covers this path. The
				// truncation detector requires evidence of model activity.
				rawCh := make(chan provider.StreamChunk, 2)
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
				Expect(assistantMsgs).To(HaveLen(1))
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonEmptyTurn),
					"empty-turn path is unchanged — truncation requires content OR thinking evidence")
			})

			It("does NOT stamp StopReasonStreamTruncated when the turn produced a delegation", func() {
				// A delegation is the turn's deliverable — the detector
				// must not false-flag.
				rawCh := make(chan provider.StreamChunk, 4)
				rawCh <- provider.StreamChunk{
					Content:    "Delegating to the worker.",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-1",
						TargetAgent: "Worker",
						Status:      "started",
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
				for _, m := range assistantMsgs {
					Expect(m.StopReason).NotTo(Equal(session.StopReasonStreamTruncated),
						"a delegation this turn proves the model reached its deliverable — "+
							"truncation must not flag delegation turns")
				}
			})
		})
	})

	// Tool-use-no-calls detection (Bug G, May 2026).
	//
	// Live reproducer: session 8169ca2d-5536-41af-b947-ba3fd7514416 —
	// plan-writer agent on glm-5/zai. Final assistant message
	// (msg[263]) carried Content="Now I'll generate the full plan. Let
	// me compose the Expanded OMO plan document:", ToolCalls=0, and the
	// upstream stop_reason was "tool_use". The provider claimed
	// tool_use as the wire-contract finish reason but emitted zero
	// tool_call blocks; the plan was never written and the session
	// completed silently with no signal to the user that the plan-
	// writer dispatch failed.
	//
	// Detector contract: when the upstream stop_reason is the literal
	// "tool_use" AND the turn produced NO tool_call AND NO delegation,
	// stamp StopReasonToolUseNoCalls. The signature is distinct from
	// the three sibling detectors:
	//   - StopReasonAbandonedTool requires WHITESPACE-only content with
	//     thinking; this detector fires on non-empty content (announce-
	//     intent prose).
	//   - StopReasonStreamTruncated requires EMPTY stop_reason; this
	//     detector fires when stop_reason IS present and IS specifically
	//     "tool_use".
	//   - StopReasonFabricatedCompletion requires self-reported-file-op
	//     phrasing in content; this detector keys on the upstream
	//     stop_reason value, not on content text.
	Describe("tool-use-no-calls detection (Bug G, May 2026)", func() {
		Context("content-bearing flushContent path", func() {
			It("stamps StopReasonToolUseNoCalls when stop_reason is tool_use and zero tool_call blocks landed", func() {
				// Reproducer 8169ca2d shape: announce-intent prose, then
				// the upstream provider emits stop_reason="tool_use",
				// then Done — but no tool_call ever lands. The wire
				// contract says tool_use requires tool_call blocks; the
				// provider violated it.
				rawCh := make(chan provider.StreamChunk, 4)
				rawCh <- provider.StreamChunk{
					Content:    "Now I'll generate the full plan. Let me compose the Expanded OMO plan document:",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				rawCh <- provider.StreamChunk{Done: true, ProviderID: "zai", ModelID: "glm-5"}
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
					"a contract-violating turn still produces exactly one persisted assistant message — "+
						"the detector annotates, never duplicates or suppresses")
				Expect(assistantMsgs[0].Content).To(ContainSubstring("Expanded OMO plan"),
					"the announce-intent prose flows through verbatim — the detector only annotates StopReason")
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonToolUseNoCalls),
					"upstream stop_reason \"tool_use\" with zero tool_call blocks IS the contract-"+
						"violation signature — the persisted message must carry tool_use_no_calls so the "+
						"session manager flips status active -> failed and the chat UI / audit surface the fault")
			})

			It("stamps StopReasonToolUseNoCalls even when thinking is absent", func() {
				// Some glm variants emit straight to content. The
				// detector keys on stop_reason + tool-call absence,
				// not on reasoning state.
				rawCh := make(chan provider.StreamChunk, 4)
				rawCh <- provider.StreamChunk{
					Content:    "Let me invoke the write tool to create the file:",
					ProviderID: "zai",
					ModelID:    "glm-4.6",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
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
				Expect(assistantMsgs).To(HaveLen(1))
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonToolUseNoCalls),
					"the detector fires on stop_reason+tool-absence regardless of whether the "+
						"reasoning channel produced anything")
			})
		})

		// Predicate-widen positives (May 2026, dogfood session
		// 32ab2e60-a69d-4bfc-9f64-1d5ae0734b39). The original Bug G
		// predicate also required `!s.turnHadToolCall &&
		// !s.turnHadDelegation`, which silently excused a contract
		// violation on the FINAL response of any turn that had earlier
		// tool-call or delegation activity. The wire contract is per-
		// message: the assistant message carrying stop_reason="tool_use"
		// must itself be accompanied by a tool_call block. Earlier
		// in-turn tool work does not honour the contract on a later
		// content-only response.
		Context("predicate is message-scoped, not turn-scoped", func() {
			It("stamps StopReasonToolUseNoCalls when an EARLIER delegation succeeded but the FINAL response has stop_reason=tool_use and zero tool_call blocks", func() {
				// Dogfood reproducer session 32ab2e60 — planner agent
				// on glm-5/zai delegated to plan-writer earlier in the
				// turn, then its wrap-up assistant message emitted
				// stop_reason="tool_use" with toolCalls=None. The pre-
				// widen predicate read turnHadDelegation=true and
				// skipped the detector; the user saw status=active on
				// a malformed final response.
				rawCh := make(chan provider.StreamChunk, 8)
				// Earlier in the turn: announce + dispatch a delegation.
				rawCh <- provider.StreamChunk{
					Content:    "Delegating to plan-writer to apply the nits.",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-32ab2e60",
						TargetAgent: "plan-writer",
						Status:      "started",
					},
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-32ab2e60",
						TargetAgent: "plan-writer",
						Status:      "completed",
					},
					ProviderID: "zai",
				}
				// Final assistant message — wrap-up prose, no tool_call,
				// upstream still emits stop_reason="tool_use".
				rawCh <- provider.StreamChunk{
					Content:    "Good, I have the full plan. Now I'll apply all five nits systematically...",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
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
				// The final assistant message — the wrap-up "Good, I
				// have the full plan..." prose — must carry the
				// contract-violation stamp. The earlier announce-prose
				// message (flushed at applyDelegation time) flushed
				// before turnStopReason was set, so it carries
				// StopReason="" and is unaffected.
				Expect(assistantMsgs).NotTo(BeEmpty(),
					"the wrap-up assistant message must be persisted so the detector has a "+
						"message to annotate")
				final := assistantMsgs[len(assistantMsgs)-1]
				Expect(final.Content).To(ContainSubstring("Good, I have the full plan"),
					"the final assistant content message is the one carrying stop_reason='tool_use'")
				Expect(final.StopReason).To(Equal(session.StopReasonToolUseNoCalls),
					"the wire contract is per-message: an earlier successful delegation does NOT "+
						"excuse a final response that claims stop_reason='tool_use' with zero "+
						"accompanying tool_call blocks — dogfood session 32ab2e60 lived because the "+
						"old predicate read turnHadDelegation=true and skipped this case")
			})

			It("does NOT stamp StopReasonToolUseNoCalls on a unified-assistant provider (anthropic) when an EARLIER delegation succeeded and the FINAL response has stop_reason=tool_use", func() {
				// Inverse of the zai/glm delegation case above. On a
				// UNIFIED-ASSISTANT provider (Anthropic), a healthy
				// content+delegate turn legitimately carries
				// stop_reason="tool_use" while the actual tool_use blocks
				// are split into separate Role:"tool_call" rows — so the
				// flushed text row ALWAYS has zero inline ToolCalls by
				// construction. The detector must NOT mislabel this healthy
				// parallel-delegation wrap-up as a wire-contract violation,
				// otherwise a fully-successful Anthropic swarm lead session
				// (plan published, final message end_turn) is falsely
				// latched to status=failed. Live evidence: session 271080ed
				// (anthropic/claude-sonnet-4-6).
				rawCh := make(chan provider.StreamChunk, 8)
				// Earlier in the turn: announce + dispatch a delegation.
				rawCh <- provider.StreamChunk{
					Content:    "Delegating to plan-writer to apply the nits.",
					ProviderID: "anthropic",
					ModelID:    "claude-sonnet-4-6",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-271080ed",
						TargetAgent: "plan-writer",
						Status:      "started",
					},
					ProviderID: "anthropic",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-271080ed",
						TargetAgent: "plan-writer",
						Status:      "completed",
					},
					ProviderID: "anthropic",
				}
				// Final assistant message — wrap-up prose. On Anthropic the
				// upstream stop_reason for a content+tool_use turn is
				// "tool_use", but the tool_use blocks land as separate
				// tool_call rows; this flushed content row has none inline.
				rawCh <- provider.StreamChunk{
					Content:    "Good, I have the full plan. Now I'll apply all five nits systematically...",
					ProviderID: "anthropic",
					ModelID:    "claude-sonnet-4-6",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
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
				Expect(assistantMsgs).NotTo(BeEmpty(),
					"the wrap-up assistant message must be persisted")
				final := assistantMsgs[len(assistantMsgs)-1]
				Expect(final.Content).To(ContainSubstring("Good, I have the full plan"),
					"the final assistant content message is the one carrying stop_reason='tool_use'")
				Expect(final.StopReason).NotTo(Equal(session.StopReasonToolUseNoCalls),
					"on a unified-assistant provider the flushed content row ALWAYS has zero "+
						"inline tool_call blocks by construction (tool_use lands as separate "+
						"tool_call rows) — the detector MUST NOT misclassify this healthy "+
						"Anthropic delegation wrap-up as a wire-contract violation, otherwise a "+
						"successful swarm lead session is falsely latched to status=failed")
			})

			It("does NOT stamp StopReasonToolUseNoCalls on openai (gpt-4o) when an EARLIER delegation succeeded and the FINAL response has stop_reason=tool_use", func() {
				// OpenAI-family extension of the anthropic anti-stamp case
				// above. The openaicompat RunStream loop emits the
				// `tool_call` chunk(s) and `flushAccumulatedToolCalls`
				// BEFORE it mirrors finish_reason into the `stop_reason`
				// chunk (internal/provider/openaicompat/openaicompat.go:
				// 548-588). So on a HEALTHY gpt-4o content+tool turn the
				// accumulator's applyToolCall flushes the content row while
				// turnStopReason is still "" — the persisted content row
				// carries StopReason="" by construction, exactly as on
				// Anthropic. OpenAI maps finish_reason="tool_calls" ->
				// stop_reason="tool_use" (mapFinishReason:842-843); on a
				// trustworthy provider that finish ALWAYS accompanies a real
				// tool_call. A swarm lead that delegated, then emitted a
				// final wrap-up message under that finish_reason is the same
				// healthy shape as the Anthropic case — it MUST NOT be
				// stamped. Live evidence: a planning-loop run whose lead
				// failed over to openai/gpt-4o was falsely stamped
				// tool_use_no_calls and latched to status=failed.
				rawCh := make(chan provider.StreamChunk, 8)
				rawCh <- provider.StreamChunk{
					Content:    "Delegating to plan-writer to apply the nits.",
					ProviderID: "openai",
					ModelID:    "gpt-4o",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-gpt4o-failover",
						TargetAgent: "plan-writer",
						Status:      "started",
					},
					ProviderID: "openai",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-gpt4o-failover",
						TargetAgent: "plan-writer",
						Status:      "completed",
					},
					ProviderID: "openai",
				}
				rawCh <- provider.StreamChunk{
					Content:    "Good, I have the full plan. Now I'll apply all five nits systematically...",
					ProviderID: "openai",
					ModelID:    "gpt-4o",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
					ProviderID: "openai",
				}
				rawCh <- provider.StreamChunk{Done: true, ProviderID: "openai"}
				close(rawCh)

				out := session.AccumulateStream(context.Background(), appender, "sess-1", "agent-1", rawCh)
				drainChannel(out)

				var assistantMsgs []session.Message
				for _, m := range appender.messages {
					if m.Role == "assistant" {
						assistantMsgs = append(assistantMsgs, m)
					}
				}
				Expect(assistantMsgs).NotTo(BeEmpty(),
					"the wrap-up assistant message must be persisted")
				final := assistantMsgs[len(assistantMsgs)-1]
				Expect(final.Content).To(ContainSubstring("Good, I have the full plan"),
					"the final assistant content message is the one carrying stop_reason='tool_use'")
				Expect(final.StopReason).NotTo(Equal(session.StopReasonToolUseNoCalls),
					"openai is a trustworthy unified-assistant provider: a healthy gpt-4o swarm "+
						"lead wrap-up that reports finish_reason='tool_calls' MUST NOT be "+
						"misclassified as a wire-contract violation — otherwise a successful "+
						"failover-to-gpt-4o swarm lead is falsely latched to status=failed")
			})

			It("STILL stamps StopReasonToolUseNoCalls on zai (glm) when the FINAL response has stop_reason=tool_use and zero tool_call blocks", func() {
				// Anti-regression guard for the openai extension: widening
				// the trusted set to include openai must NOT excuse the
				// genuine glm/zai model-defect. glm-5 announces a tool,
				// the provider reports finish_reason='tool_calls', and glm
				// emits ZERO tool_calls anywhere (live dogfood session
				// 32ab2e60). zai stays OUT of the trusted set so this
				// genuine contract violation is still caught.
				rawCh := make(chan provider.StreamChunk, 8)
				rawCh <- provider.StreamChunk{
					Content:    "Delegating to plan-writer to apply the nits.",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-zai-regression",
						TargetAgent: "plan-writer",
						Status:      "started",
					},
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					DelegationInfo: &provider.DelegationInfo{
						ChainID:     "chain-zai-regression",
						TargetAgent: "plan-writer",
						Status:      "completed",
					},
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					Content:    "Good, I have the full plan. Now I'll apply all five nits systematically...",
					ProviderID: "zai",
					ModelID:    "glm-5",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
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
				final := assistantMsgs[len(assistantMsgs)-1]
				Expect(final.StopReason).To(Equal(session.StopReasonToolUseNoCalls),
					"extending the trusted set to openai must NOT excuse the genuine glm/zai "+
						"defect — zai stays non-unified so the real Bug-G contract violation is "+
						"still caught (dogfood session 32ab2e60)")
			})

			It("stamps StopReasonToolUseNoCalls when an EARLIER tool_call fired but the FINAL response has stop_reason=tool_use and zero tool_call blocks", func() {
				// Same shape as the delegation case but with a real
				// tool_call earlier in the turn rather than a
				// delegation. The pre-widen predicate read
				// turnHadToolCall=true and skipped the detector.
				rawCh := make(chan provider.StreamChunk, 8)
				// Earlier in the turn: announce + execute a real tool_call.
				rawCh <- provider.StreamChunk{
					Content:    "Let me check the file first.",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					ToolCall: &provider.ToolCall{
						ID:        "tc-read-earlier",
						Name:      "read",
						Arguments: map[string]any{"path": "/tmp/x"},
					},
					ProviderID: "zai",
				}
				// Final assistant message — wrap-up prose, no further
				// tool_call, upstream still emits stop_reason="tool_use".
				rawCh <- provider.StreamChunk{
					Content:    "Now I'll write the result. Let me compose the output:",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
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
				Expect(assistantMsgs).NotTo(BeEmpty())
				final := assistantMsgs[len(assistantMsgs)-1]
				Expect(final.Content).To(ContainSubstring("Now I'll write the result"),
					"the final assistant content message is the one carrying stop_reason='tool_use'")
				Expect(final.StopReason).To(Equal(session.StopReasonToolUseNoCalls),
					"the wire contract is per-message: an earlier successful tool_call does NOT "+
						"excuse a final response that claims stop_reason='tool_use' with zero "+
						"accompanying tool_call blocks; the predicate must be message-scoped")
			})
		})

		Context("negatives — the detector must not fire on legitimate or differently-shaped turns", func() {
			It("does NOT stamp StopReasonToolUseNoCalls when a real tool_call accompanied the tool_use stop_reason", func() {
				// The happy path: stop_reason="tool_use" alongside a real
				// tool_call. The wire contract is honoured; the detector
				// MUST NOT false-flag.
				rawCh := make(chan provider.StreamChunk, 5)
				rawCh <- provider.StreamChunk{
					Content:    "I'll write the file now.",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					ToolCall: &provider.ToolCall{
						ID:        "tc-write",
						Name:      "write",
						Arguments: map[string]any{"path": "/tmp/x"},
					},
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
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
				for _, m := range assistantMsgs {
					Expect(m.StopReason).NotTo(Equal(session.StopReasonToolUseNoCalls),
						"a real tool_call honoured the wire contract — the detector MUST NOT "+
							"false-flag the happy path just because stop_reason is tool_use")
				}
			})

			It("does NOT stamp StopReasonToolUseNoCalls when stop_reason is end_turn (normal text response)", func() {
				// A clean text-only response with stop_reason=end_turn
				// and no tool_call is a normal answer — the detector
				// MUST NOT fire on the end_turn signature.
				rawCh := make(chan provider.StreamChunk, 4)
				rawCh <- provider.StreamChunk{
					Content:    "The answer is 42.",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "end_turn",
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
				Expect(assistantMsgs).To(HaveLen(1))
				Expect(assistantMsgs[0].StopReason).To(Equal("end_turn"),
					"a clean end_turn path is unchanged — the detector keys on the literal "+
						"tool_use stop_reason, not on the absence of tool_call alone")
				Expect(assistantMsgs[0].StopReason).NotTo(Equal(session.StopReasonToolUseNoCalls),
					"end_turn with no tool_call is the normal happy path for a text answer — "+
						"the detector MUST NOT fire here")
			})

			It("does NOT clobber StopReasonAbandonedTool when content is whitespace-only with thinking and stop_reason is tool_use", func() {
				// Ordering pin: AbandonedTool (whitespace + thinking)
				// claims ownership over a tool_use stop_reason on its
				// specific signature. The new detector must fire only
				// when AbandonedTool's predicate did not match.
				rawCh := make(chan provider.StreamChunk, 5)
				rawCh <- provider.StreamChunk{
					Thinking:   "I should use the write tool to create the file.",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					Content:    "\n",
					ProviderID: "zai",
				}
				rawCh <- provider.StreamChunk{
					EventType:  "stop_reason",
					StopReason: "tool_use",
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
				Expect(assistantMsgs).To(HaveLen(1))
				Expect(assistantMsgs[0].StopReason).To(Equal(session.StopReasonAbandonedTool),
					"the AbandonedTool signature (whitespace-only content + thinking) is more "+
						"specific and runs first; the tool-use-no-calls detector must respect "+
						"the existing ordering and not overwrite the more specific sentinel")
			})
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
