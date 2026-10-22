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

var _ = Describe("DelegateTool tool-capability allowlist", func() {
	var (
		qaProvider *mockProvider
		qaEngine   *engine.Engine
		engines    map[string]*engine.Engine
		delegation agent.Delegation
		input      tool.Input
	)

	BeforeEach(func() {
		qaProvider = &mockProvider{
			name: "ollama",
			streamChunks: []provider.StreamChunk{
				{Content: "should never be streamed", Done: true},
			},
		}

		qaManifest := agent.Manifest{
			ID:                "qa-agent",
			Name:              "QA Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
			ContextManagement: agent.DefaultContextManagement(),
		}

		qaEngine = engine.New(engine.Config{
			ChatProvider: qaProvider,
			Manifest:     qaManifest,
		})

		engines = map[string]*engine.Engine{
			"qa-agent": qaEngine,
		}

		delegation = agent.Delegation{CanDelegate: true}

		input = tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "qa-agent",
				"message":       "Run tests",
			},
		}
	})

	It("returns a structured error and skips streaming when the resolved model is on the deny list", func() {
		qaEngine.SetModelPreference("ollama", "llama3.2:latest")
		delegateTool := newDelegateToolWithCapability(engines, delegation, []string{"claude-*"}, []string{"llama3.2*"})

		_, err := delegateTool.Execute(context.Background(), input)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("delegate refused"))
		Expect(err.Error()).To(ContainSubstring("qa-agent"))
		Expect(err.Error()).To(ContainSubstring("llama3.2:latest"))
		Expect(qaProvider.capturedRequest).To(BeNil())
	})

	It("returns a structured error when the model is not in the allow list (fail closed)", func() {
		qaEngine.SetModelPreference("ollama", "some-new-model:7b")
		delegateTool := newDelegateToolWithCapability(engines, delegation, []string{"claude-*"}, nil)

		_, err := delegateTool.Execute(context.Background(), input)

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("delegate refused"))
		Expect(err.Error()).To(ContainSubstring("some-new-model:7b"))
		Expect(qaProvider.capturedRequest).To(BeNil())
	})

	It("permits delegation when the resolved model matches an allow-list pattern", func() {
		qaEngine.SetModelPreference("anthropic", "claude-sonnet-4-20250514")
		delegateTool := newDelegateToolWithCapability(engines, delegation, []string{"claude-*"}, []string{"llama3.2*"})

		_, err := delegateTool.Execute(context.Background(), input)

		Expect(err).NotTo(HaveOccurred())
		Expect(qaProvider.capturedRequest).NotTo(BeNil())
	})

	It("skips the gate when no allow/deny lists are configured (back-compat)", func() {
		qaEngine.SetModelPreference("ollama", "llama3.2:latest")
		delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

		_, err := delegateTool.Execute(context.Background(), input)

		Expect(err).NotTo(HaveOccurred())
		Expect(qaProvider.capturedRequest).NotTo(BeNil())
	})
})

func newDelegateToolWithCapability(
	engines map[string]*engine.Engine,
	delegation agent.Delegation,
	allow, deny []string,
) *engine.DelegateTool {
	return engine.NewDelegateTool(engines, delegation, "orchestrator").
		WithToolCapability(allow, deny)
}
