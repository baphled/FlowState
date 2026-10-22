//go:build harnessred
// +build harnessred

package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

type harnessAwareMockProvider struct {
	chunks []provider.StreamChunk
}

func (p *harnessAwareMockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(p.chunks))
	for i := range p.chunks {
		ch <- p.chunks[i]
	}
	close(ch)
	return ch, nil
}

func (p *harnessAwareMockProvider) Name() string {
	return "harness-test-provider"
}

func (p *harnessAwareMockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *harnessAwareMockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *harnessAwareMockProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

var _ = Describe("DelegateTool harness event propagation", func() {
	var (
		planWriterProvider *harnessAwareMockProvider
		planWriterEngine   *engine.Engine
		engines            map[string]*engine.Engine
		delegation         agent.Delegation
	)

	BeforeEach(func() {
		planWriterProvider = &harnessAwareMockProvider{
			chunks: []provider.StreamChunk{
				{Content: "---\nid: test\ntitle: Test Plan\nstatus: draft\ncreated: 2026-01-01\n---\n\n# Test Plan\n\n## TL;DR\n- Summary: test\n"},
				{Done: true},
			},
		}

		planWriterEngine = engine.New(engine.Config{
			ChatProvider: planWriterProvider,
			Manifest: agent.Manifest{
				ID:                "plan-writer",
				Name:              "Plan Writer",
				HarnessEnabled:    true,
				Instructions:      agent.Instructions{SystemPrompt: "You write plans."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})

		engines = map[string]*engine.Engine{
			"plan-writer": planWriterEngine,
		}

		delegation = agent.Delegation{
			CanDelegate:         true,
			DelegationAllowlist: []string{"plan-writer"},
		}
	})

	Context("when target agent has HarnessEnabled: true", func() {
		It("emits a harness_attempt_start event in the output stream", func() {
			outChan := make(chan provider.StreamChunk, 200)
			ctx := engine.WithStreamOutput(context.Background(), outChan)

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator-agent")

			_, _ = delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "plan-writer",
					"message":       "Write a plan for feature X",
				},
			})

			close(outChan)

			foundHarnessAttemptStart := false
			for chunk := range outChan {
				if chunk.EventType == "harness_attempt_start" {
					foundHarnessAttemptStart = true
					break
				}
			}

			Expect(foundHarnessAttemptStart).To(BeTrue(), "expected to find harness_attempt_start event in output stream, but none received")
		})
	})
})
