package context_test

import (
	"sync"

	gocontext "context"
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("WindowBuilder chain context race protection", func() {
	It("assembles chain context concurrently without racing", func() {
		builder := context.NewWindowBuilder(context.NewApproximateCounter())
		chainStore := recall.NewInMemoryChainStore(nil)
		tempDir, err := os.MkdirTemp("", "window-builder-chain-race")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tempDir)

		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "store.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		for i := 0; i < 4; i++ {
			Expect(chainStore.Append("agent-a", provider.Message{Role: "assistant", Content: "chain message"})).To(Succeed())
		}

		manifest := &agent.Manifest{
			Instructions:      agent.Instructions{SystemPrompt: "System prompt"},
			ContextManagement: agent.ContextManagement{SlidingWindowSize: 2},
		}

		const workers = 32
		results := make(chan context.BuildResult, workers)
		var wg sync.WaitGroup

		for range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- builder.BuildContextWithChainResult(gocontext.Background(), manifest, "hello", store, chainStore, 128)
			}()
		}

		wg.Wait()
		close(results)

		collected := make([]context.BuildResult, 0, workers)
		for result := range results {
			collected = append(collected, result)
		}

		Expect(collected).To(HaveLen(workers))
		for _, result := range collected {
			Expect(result.Messages).To(HaveLen(6))
			Expect(result.Messages[0].Role).To(Equal("system"))
			Expect(result.Messages[1].Content).To(Equal("chain message"))
			Expect(result.Messages[4].Content).To(Equal("chain message"))
			Expect(result.Messages[5].Role).To(Equal("user"))
		}
	})
})
