package engine_test

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/context/compaction"
	"github.com/baphled/flowstate/internal/context/factstore"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

const factRecallSentinel = "[recalled facts]"

type phaseBStubExtractor struct {
	facts []factstore.Fact
}

func (s phaseBStubExtractor) Extract(_ context.Context, sessionID string, _ []provider.Message) ([]factstore.Fact, error) {
	out := make([]factstore.Fact, len(s.facts))
	for i, f := range s.facts {
		f.SessionID = sessionID
		out[i] = f
	}
	return out, nil
}

type phaseBTokenCounter struct{}

func (phaseBTokenCounter) Count(text string) int   { return len(strings.Fields(text)) }
func (phaseBTokenCounter) ModelLimit(_ string) int { return 100000 }

func phaseBSeed(store *recall.FileContextStore, userText string) {
	store.Append(provider.Message{Role: "user", Content: userText})
}

func phaseBNewService(root string, facts []factstore.Fact) *factstore.Service {
	store := factstore.NewFileFactStore(root)
	cfg := factstore.DefaultConfig()
	cfg.RecallTopK = 3
	return factstore.NewService(store, phaseBStubExtractor{facts: facts}, cfg)
}

func phaseBFindRecallSection(msgs []provider.Message) (int, provider.Message, bool) {
	for i, m := range msgs {
		if m.Role == "system" && strings.Contains(m.Content, factRecallSentinel) {
			return i, m, true
		}
	}
	return 0, provider.Message{}, false
}

var _ = Describe("Engine RLM Phase B fact recall wiring", func() {
	It("prepends a [recalled facts] system block listing the top-K facts", func() {
		tempDir := GinkgoT().TempDir()
		factRoot := filepath.Join(tempDir, "facts")

		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())
		phaseBSeed(store, "qdrant collection naming question")

		facts := []factstore.Fact{
			{Text: "the qdrant collection is named flowstate-recall", SourceMessageID: "m1"},
			{Text: "user prefers terse responses", SourceMessageID: "m2"},
			{Text: "always run gofmt before committing", SourceMessageID: "m3"},
		}

		manifest := agent.Manifest{
			ID:                "phase-b-agent",
			Name:              "Phase B wiring",
			ContextManagement: agent.DefaultContextManagement(),
		}
		manifest.ContextManagement.SlidingWindowSize = 50

		eng := engine.New(engine.Config{
			Manifest:     manifest,
			Store:        store,
			TokenCounter: phaseBTokenCounter{},
			CompactionConfig: compaction.Config{
				MicroEnabled:          true,
				HotTailMinResults:     3,
				HotTailSizeBudget:     2000,
				FactExtractionEnabled: true,
			},
			FactService: phaseBNewService(factRoot, facts),
		})

		Expect(eng.IngestForFactsForTest(context.Background(), "phase-b-session",
			[]provider.Message{{Role: "user", Content: "seed"}})).To(Succeed())

		messages := eng.BuildContextWindowForTest(context.Background(), "phase-b-session", "qdrant collection question")

		idx, recallMsg, ok := phaseBFindRecallSection(messages)
		Expect(ok).To(BeTrue(), "expected a [recalled facts] system block")
		Expect(idx).To(Equal(1), "recall block must sit immediately after the system prompt")
		Expect(recallMsg.Content).To(ContainSubstring("flowstate-recall"))
	})

	It("does not inject a [recalled facts] block when FactExtractionEnabled=false", func() {
		tempDir := GinkgoT().TempDir()
		factRoot := filepath.Join(tempDir, "facts")
		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())
		phaseBSeed(store, "any user prompt")

		facts := []factstore.Fact{
			{Text: "user prefers terse responses", SourceMessageID: "m1"},
		}

		eng := engine.New(engine.Config{
			Manifest: agent.Manifest{
				ID:                "phase-b-off",
				ContextManagement: agent.DefaultContextManagement(),
			},
			Store:        store,
			TokenCounter: phaseBTokenCounter{},
			CompactionConfig: compaction.Config{
				MicroEnabled:          false,
				FactExtractionEnabled: false,
			},
			FactService: phaseBNewService(factRoot, facts),
		})

		messages := eng.BuildContextWindowForTest(context.Background(), "off-session", "next turn")

		_, _, ok := phaseBFindRecallSection(messages)
		Expect(ok).To(BeFalse())
	})

	It("emits no recall block when no facts have been ingested", func() {
		tempDir := GinkgoT().TempDir()
		factRoot := filepath.Join(tempDir, "facts")
		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())
		phaseBSeed(store, "kick off")

		eng := engine.New(engine.Config{
			Manifest: agent.Manifest{
				ID:                "phase-b-empty",
				ContextManagement: agent.DefaultContextManagement(),
			},
			Store:        store,
			TokenCounter: phaseBTokenCounter{},
			CompactionConfig: compaction.Config{
				FactExtractionEnabled: true,
			},
			FactService: phaseBNewService(factRoot, nil),
		})

		messages := eng.BuildContextWindowForTest(context.Background(), "empty-session", "next turn")

		_, _, ok := phaseBFindRecallSection(messages)
		Expect(ok).To(BeFalse(), "an empty fact set must not emit a recall block")
	})
})
