package engine_test

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// EngineStream_StampsInternalToolCallID_OnToolCallChunk — Phase 14:
// the engine stamps InternalToolCallID on every tool-related chunk on
// its way to consumers, even when the upstream provider only populated
// the native ToolCallID. This is the integration point that makes
// downstream coalesce work across a provider failover.
//
// The spec drives a single-provider tool loop because the StreamChunk
// contract is consumer-agnostic — the failover-specific flip is
// covered by the P5 regression gate test in internal/plugin/failover.
var _ = Describe("Engine.Stream InternalToolCallID stamping (P14)", func() {
	It("stamps the same non-empty InternalToolCallID on tool_call AND tool_result chunks", func() {
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
		Expect(err).NotTo(HaveOccurred(), "Stream returned error")

		onCall, onResult := collectInternalIDs(ch)

		Expect(onCall).NotTo(BeEmpty(),
			"tool_call chunk must carry a non-empty InternalToolCallID")
		Expect(onResult).NotTo(BeEmpty(),
			"tool_result chunk must carry a non-empty InternalToolCallID")
		Expect(onCall).To(Equal(onResult),
			"tool_call and tool_result must share the same InternalToolCallID")
	})
})

// collectInternalIDs drains ch, capturing the InternalToolCallID
// observed on the tool_call chunk and on the tool_result chunk.
// Extracted so the spec body stays focused on the assertion contract.
//
// Expected:
//   - ch is a provider stream channel produced by Engine.Stream.
//
// Returns:
//   - The InternalToolCallID observed on the tool_call chunk and the
//     tool_result chunk, respectively. Empty when the chunk was absent.
//
// Side effects:
//   - Calls Fail on a 5-second drain timeout.
func collectInternalIDs(ch <-chan provider.StreamChunk) (string, string) {
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
			Fail("timed out waiting for stream to complete")
			return onCall, onResult
		}
	}
}
