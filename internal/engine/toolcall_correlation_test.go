package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// Phase 14 — the engine stamps InternalToolCallID on every tool-related
// chunk on its way to consumers, even when the upstream provider only
// populated the native ToolCallID. This is the integration point that
// makes downstream coalesce work across a provider failover.
//
// The test drives a single-provider tool loop because the StreamChunk
// contract is consumer-agnostic — the failover-specific flip is covered
// by the P5 regression gate test in internal/plugin/failover.
func TestEngineStream_StampsInternalToolCallID_OnToolCallChunk(t *testing.T) {
	const callID = "toolu_01TESTabc"

	chatProvider := &streamSequenceProvider{
		name: "chat",
		sequences: [][]provider.StreamChunk{
			{
				{
					ToolCallID: callID,
					ToolCall: &provider.ToolCall{
						ID:        callID,
						Name:      "echo_tool",
						Arguments: map[string]any{"msg": "hi"},
					},
				},
			},
			{
				{Content: "done", Done: true},
			},
		},
	}

	echo := &executableMockTool{
		name:        "echo_tool",
		description: "echo",
		execResult:  tool.Result{Output: "hi"},
	}

	eng := engine.New(engine.Config{
		ChatProvider: chatProvider,
		Manifest: agent.Manifest{
			ID:   "test-agent",
			Name: "test-agent",
		},
		Tools:         []tool.Tool{echo},
		TokenCounter:  &pipelineTokenCounter{},
		StreamTimeout: 5 * time.Second,
		// ToolCallCorrelator left nil: the engine must lazy-construct one.
	})

	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, "session-P14-engine"))
	defer cancel()

	ch, err := eng.Stream(ctx, "test-agent", "hello")
	if err != nil {
		t.Fatalf("Stream returned error: %v", err)
	}

	onCall, onResult := collectInternalIDs(t, ch)

	if onCall == "" {
		t.Fatalf("tool_call chunk must carry a non-empty InternalToolCallID; got empty")
	}
	if onResult == "" {
		t.Fatalf("tool_result chunk must carry a non-empty InternalToolCallID; got empty")
	}
	if onCall != onResult {
		t.Fatalf("tool_call and tool_result must share the same InternalToolCallID; call=%q result=%q", onCall, onResult)
	}
}

// collectInternalIDs drains ch, capturing the InternalToolCallID observed
// on the tool_call chunk and on the tool_result chunk. Extracted from the
// test body to keep the test's cognitive complexity inside the repo's
// revive gate.
//
// Expected:
//   - t is the active *testing.T; a channel drain timeout fails it.
//   - ch is a provider stream channel produced by Engine.Stream.
//
// Returns:
//   - The InternalToolCallID observed on the tool_call chunk and the
//     tool_result chunk, respectively. Empty when the chunk was absent.
//
// Side effects:
//   - Calls t.Fatalf on a 5-second drain timeout.
func collectInternalIDs(t *testing.T, ch <-chan provider.StreamChunk) (string, string) {
	t.Helper()
	var onCall, onResult string
	timeout := time.After(5 * time.Second)
	for {
		select {
		case chunk, ok := <-ch:
			if !ok {
				return onCall, onResult
			}
			if chunk.ToolCall != nil {
				onCall = chunk.InternalToolCallID
			}
			if chunk.ToolResult != nil {
				onResult = chunk.InternalToolCallID
			}
			if chunk.Done {
				return onCall, onResult
			}
		case <-timeout:
			t.Fatalf("timed out waiting for stream to complete")
			return onCall, onResult
		}
	}
}
