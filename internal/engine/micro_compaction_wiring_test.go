package engine_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// wordTokenCounterForWiring attributes one token per whitespace-delimited
// word. Mirrors the helper used in the app-level wiring test so threshold
// sizing stays intuitive.
type wordTokenCounterForWiring struct{}

func (wordTokenCounterForWiring) Count(text string) int {
	count := 0
	inWord := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' {
			if inWord {
				count++
				inWord = false
			}
			continue
		}
		inWord = true
	}
	if inWord {
		count++
	}
	return count
}

func (wordTokenCounterForWiring) ModelLimit(_ string) int { return 10000 }

// L1 micro-compaction wiring tests close the gap identified on
// 2026-04-14: HotColdSplitter was only ever constructed in *_test.go
// files. In production, the engine built its WindowBuilder without
// calling WithSplitter, so `compression.micro_compaction.enabled: true`
// was a silent no-op.
//
// Coverage:
//   - Enabled=true wires the splitter and at least one .json spill file
//     lands under StorageDir/<sessionID>/.
//   - Enabled=false leaves the splitter cache entry unset for that
//     session — opt-in is preserved.
var _ = Describe("Engine micro-compaction wiring through buildContextWindow", func() {
	It("attaches a HotColdSplitter and writes spill files when enabled", func() {
		tempDir := GinkgoT().TempDir()
		storageDir := filepath.Join(tempDir, "compacted")

		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())

		// 10 messages × 7 words each. With HotTailSize=2 and
		// TokenThreshold=5, 8 cold units each ≥ 7 tokens will qualify.
		content := "one two three four five six seven"
		for range 10 {
			store.Append(provider.Message{Role: "assistant", Content: content})
		}

		manifest := agent.Manifest{
			ID:                "micro-wire-agent",
			Name:              "Micro-compaction wiring",
			ContextManagement: agent.DefaultContextManagement(),
		}

		eng := engine.New(engine.Config{
			Manifest:     manifest,
			Store:        store,
			TokenCounter: wordTokenCounterForWiring{},
			CompressionConfig: ctxstore.CompressionConfig{
				MicroCompaction: ctxstore.MicroCompactionConfig{
					Enabled:           true,
					HotTailSize:       2,
					TokenThreshold:    5,
					StorageDir:        storageDir,
					PlaceholderTokens: 10,
				},
			},
		})

		sessionID := "wire-session"
		_ = eng.BuildContextWindowForTest(context.Background(), sessionID, "next turn")

		// Allow the async persist worker to drain. We stop the cached
		// splitter explicitly via the test accessor; Stop blocks until
		// the worker goroutine exits so subsequent filesystem reads are
		// deterministic.
		splitter := eng.SessionSplitterForTest(sessionID)
		Expect(splitter).NotTo(BeNil(),
			"engine did not cache a session splitter; WithSplitter never fired")
		splitter.Stop()

		spillDir := filepath.Join(storageDir, sessionID)
		entries, err := os.ReadDir(spillDir)
		Expect(err).NotTo(HaveOccurred(),
			"ReadDir(%s) failed — engine never wired the splitter so no spill files were written", spillDir)

		jsonCount := 0
		for _, e := range entries {
			if filepath.Ext(e.Name()) == ".json" {
				jsonCount++
			}
		}
		Expect(jsonCount).To(BeNumerically(">", 0),
			"spill dir %s contains no .json payloads; HotColdSplitter did not execute under the engine", spillDir)
	})

	It("does not attach a splitter when MicroCompaction.Enabled=false", func() {
		tempDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())
		store.Append(provider.Message{Role: "user", Content: "hello"})

		eng := engine.New(engine.Config{
			Manifest:     agent.Manifest{ID: "no-micro", ContextManagement: agent.DefaultContextManagement()},
			Store:        store,
			TokenCounter: wordTokenCounterForWiring{},
			CompressionConfig: ctxstore.CompressionConfig{
				MicroCompaction: ctxstore.MicroCompactionConfig{Enabled: false},
			},
		})

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-off", "next turn")

		Expect(eng.SessionSplitterForTest("sess-off")).To(BeNil(),
			"splitter was cached despite MicroCompaction.Enabled=false")
	})
})
