// Package engine_test covers P13 — the RecallBroker context-assembly
// hook must only fire for agents whose manifest opts in via
// uses_recall:true. Agents that default to false (the P13 default) must
// see zero RecallBroker.Query calls even when a broker is configured
// on the engine. This moves recall from "always on" to "opt-in per
// agent" and is the primary win of P13.
package engine_test

import (
	stdctx "context"
	"os"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/recall"
)

// countingBroker counts Query invocations atomically so the P13
// opt-in gate tests can assert exact call counts.
type countingBroker struct {
	queries atomic.Int64
}

func (b *countingBroker) Query(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
	b.queries.Add(1)
	return []recall.Observation{}, nil
}

var _ = Describe("RecallBroker opt-in gate (P13)", Label("p13", "recall", "opt-in"), func() {
	var (
		broker  *countingBroker
		counter *contextAssemblyTokenCounter
		tmpDir  string
	)

	BeforeEach(func() {
		broker = &countingBroker{}
		counter = &contextAssemblyTokenCounter{
			countFn:      func(text string) int { return len(text) / 4 },
			modelLimitFn: func(model string) int { return 8192 },
		}
		var err error
		tmpDir, err = os.MkdirTemp("", "p13-opt-in-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("skips the RecallBroker hook when manifest.UsesRecall is false", func() {
		manifest := agent.Manifest{
			ID:         "no-recall-agent",
			Name:       "No Recall Agent",
			UsesRecall: false,
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		eng := engine.New(engine.Config{
			ChatProvider: &contextAssemblyProvider{},
			Manifest:     manifest,
			TokenCounter: counter,
			Store:        store,
			RecallBroker: broker,
		})

		msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
		Expect(msgs).NotTo(BeEmpty())
		Expect(broker.queries.Load()).To(BeEquivalentTo(0),
			"broker.Query must not fire when uses_recall is false")
	})

	It("invokes the RecallBroker hook when manifest.UsesRecall is true", func() {
		manifest := agent.Manifest{
			ID:         "recall-agent",
			Name:       "Recall Agent",
			UsesRecall: true,
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		eng := engine.New(engine.Config{
			ChatProvider: &contextAssemblyProvider{},
			Manifest:     manifest,
			TokenCounter: counter,
			Store:        store,
			RecallBroker: broker,
		})

		msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
		Expect(msgs).NotTo(BeEmpty())
		Expect(broker.queries.Load()).To(BeEquivalentTo(1),
			"broker.Query must fire exactly once when uses_recall is true")
	})

	It("defaults to skipping the broker when manifest.UsesRecall is zero-valued", func() {
		// Zero-value Manifest → UsesRecall defaults to false. This
		// locks in the P13 backwards-compat note: any manifest that
		// does not explicitly opt in loses recall.
		manifest := agent.Manifest{
			ID:   "default-agent",
			Name: "Default Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		eng := engine.New(engine.Config{
			ChatProvider: &contextAssemblyProvider{},
			Manifest:     manifest,
			TokenCounter: counter,
			Store:        store,
			RecallBroker: broker,
		})

		eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hi")
		Expect(broker.queries.Load()).To(BeEquivalentTo(0),
			"default UsesRecall=false must skip the broker")
	})
})
