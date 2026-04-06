//go:build rejectionred
// +build rejectionred

package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("DelegateTool rejection exhaustion", func() {
	const testChainID = "test-chain-001"

	var (
		planWriterEngine *engine.Engine
		store            *coordination.MemoryStore
		bgManager        *engine.BackgroundTaskManager
		delegateTool     *engine.DelegateTool
		delegation       agent.Delegation
	)

	BeforeEach(func() {
		planWriterEngine = engine.New(engine.Config{
			ChatProvider: &harnessAwareMockProvider{
				chunks: []provider.StreamChunk{
					{Content: "plan content"},
					{Done: true},
				},
			},
			Manifest: agent.Manifest{
				ID:                "plan-writer",
				Name:              "Plan Writer",
				ContextManagement: agent.DefaultContextManagement(),
			},
		})

		store = coordination.NewMemoryStore()
		bgManager = engine.NewBackgroundTaskManager()

		delegation = agent.Delegation{
			CanDelegate:         true,
			DelegationAllowlist: []string{"plan-writer"},
		}

		delegateTool = engine.NewDelegateToolWithBackground(
			map[string]*engine.Engine{"plan-writer": planWriterEngine},
			delegation,
			"orchestrator-agent",
			bgManager,
			store,
		)
	})

	Context("when rejection count for the chain has reached the maximum", func() {
		It("returns errMaxRejectionsExhausted without delegating", func() {
			for range 3 {
				_, _ = store.Increment(testChainID + "/rejection-count")
			}

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "plan-writer",
					"message":       "Write a plan for feature X",
					"handoff": map[string]interface{}{
						"chain_id":     testChainID,
						"source_agent": "orchestrator-agent",
						"target_agent": "plan-writer",
					},
				},
			})

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("max rejections exhausted"))
		})
	})
})
