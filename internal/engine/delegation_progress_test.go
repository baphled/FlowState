package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("DelegationProgress", func() {
	Context("when streaming delegation progress", func() {
		It("emits ProgressEvent to output channel during execution", func() {
			outChan := make(chan provider.StreamChunk, 100)
			ctx := engine.WithStreamOutput(context.Background(), outChan)

			chatProvider := &mockProvider{
				name: "test-chat-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "response chunk 1", Done: false},
					{Content: "response chunk 2", Done: false},
					{Content: "response chunk 3", Done: false},
					{Content: "response chunk 4", Done: false},
					{Content: "response chunk 5", Done: false},
					{Content: "response chunk 6", Done: true},
				},
			}

			qaManifest := agent.Manifest{
				ID:                "qa-agent",
				Name:              "QA Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			qaEngine := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     qaManifest,
			})

			orchestratorManifest := agent.Manifest{
				ID:   "orchestrator-agent",
				Name: "Orchestrator Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are an orchestrator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			engines := map[string]*engine.Engine{
				"qa-agent": qaEngine,
			}

			delegateTool := engine.NewDelegateTool(engines, orchestratorManifest.Delegation, "orchestrator-agent")

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}

			_, _ = delegateTool.Execute(ctx, input)

			close(outChan)

			var foundProgressEvent bool
			for chunk := range outChan {
				if pev, ok := chunk.Event.(streaming.ProgressEvent); ok && pev.ToolCallCount > 0 {
					foundProgressEvent = true
					break
				}
			}

			Expect(foundProgressEvent).To(BeTrue(), "expected to find at least one ProgressEvent with tool calls > 0")
		})

		It("emits progress every 5 tool calls", func() {
			outChan := make(chan provider.StreamChunk, 100)
			ctx := engine.WithStreamOutput(context.Background(), outChan)

			chunks := make([]provider.StreamChunk, 15)
			for i := range 15 {
				chunks[i] = provider.StreamChunk{Content: "chunk", Done: i == 14}
			}

			chatProvider := &mockProvider{
				name:         "test-chat-provider",
				streamChunks: chunks,
			}

			qaManifest := agent.Manifest{
				ID:                "qa-agent",
				Name:              "QA Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
				ContextManagement: agent.DefaultContextManagement(),
			}

			qaEngine := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     qaManifest,
			})

			orchestratorManifest := agent.Manifest{
				ID:   "orchestrator-agent",
				Name: "Orchestrator Agent",
				Instructions: agent.Instructions{
					SystemPrompt: "You are an orchestrator.",
				},
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
				ContextManagement: agent.DefaultContextManagement(),
			}

			engines := map[string]*engine.Engine{
				"qa-agent": qaEngine,
			}

			delegateTool := engine.NewDelegateTool(engines, orchestratorManifest.Delegation, "orchestrator-agent")

			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}

			_, _ = delegateTool.Execute(ctx, input)

			close(outChan)

			var progressEvents []streaming.ProgressEvent
			for chunk := range outChan {
				if pev, ok := chunk.Event.(streaming.ProgressEvent); ok {
					progressEvents = append(progressEvents, pev)
				}
			}

			Expect(progressEvents).NotTo(BeEmpty(), "expected at least one progress event from 15 tool calls")
		})
	})
})
