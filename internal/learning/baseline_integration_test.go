package learning_test

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
)

var _ = Describe("JSONFileStore baseline", Label("integration"), func() {
	var (
		tempDir  string
		filePath string
		store    learning.Store
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "learning-baseline-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, tempDir)

		filePath = filepath.Join(tempDir, "learning.json")
		store = learning.NewJSONFileStore(filePath)
	})

	It("round-trips an entry via Capture and Query", func() {
		entry := learning.Entry{
			Timestamp:   time.Now(),
			AgentID:     "test-agent",
			UserMessage: "what is baseline testing",
			Response:    "it establishes a regression baseline",
			ToolsUsed:   []string{"bash"},
			Outcome:     "success",
		}

		err := store.Capture(entry)
		Expect(err).NotTo(HaveOccurred())

		results := store.Query(entry.UserMessage)
		Expect(results).To(ContainElement(entry))
	})
})

var _ = Describe("hook.LearningHook baseline", Label("integration"), func() {
	var (
		tempDir  string
		filePath string
		store    learning.Store
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "learning-hook-baseline-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.RemoveAll, tempDir)

		filePath = filepath.Join(tempDir, "learning.json")
		store = learning.NewJSONFileStore(filePath)
	})

	It("captures the user message and streamed response after completion", func() {
		entry := learning.Entry{
			Timestamp:   time.Now(),
			AgentID:     "test-agent",
			UserMessage: "what is baseline testing",
			Response:    "it establishes a regression baseline",
			ToolsUsed:   []string{"bash"},
			Outcome:     "success",
		}

		handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			chunks := make(chan provider.StreamChunk, 2)
			go func() {
				defer close(chunks)
				chunks <- provider.StreamChunk{Content: "it establishes a "}
				chunks <- provider.StreamChunk{Content: "regression baseline", Done: true}
			}()
			return chunks, nil
		}

		hooked := hook.LearningHook(store)(handler)
		result, err := hooked(context.Background(), &provider.ChatRequest{
			Messages: []provider.Message{{Role: "user", Content: entry.UserMessage}},
		})
		Expect(err).NotTo(HaveOccurred())

		for chunk := range result {
			_ = chunk
		}

		Eventually(func() []learning.Entry {
			return store.Query(entry.UserMessage)
		}).Should(ContainElement(gstruct.MatchFields(gstruct.IgnoreExtras, gstruct.Fields{
			"UserMessage": Equal(entry.UserMessage),
			"Response":    Equal(entry.Response),
		})))
	})
})
