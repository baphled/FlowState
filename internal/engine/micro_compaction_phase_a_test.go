package engine_test

import (
	"context"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/context/compaction"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

const phaseAReferencePrefix = "[content offloaded to "

func phaseAIsReference(content string) bool {
	return strings.HasPrefix(content, phaseAReferencePrefix)
}

func phaseAStoreSeed(store *recall.FileContextStore, calls []phaseAToolCall) {
	for _, c := range calls {
		store.Append(provider.Message{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{{
				ID:        c.ID,
				Name:      c.Name,
				Arguments: map[string]any{},
			}},
		})
		store.Append(provider.Message{
			Role:      "tool",
			Content:   c.Body,
			ToolCalls: []provider.ToolCall{{ID: c.ID, Name: c.Name}},
		})
	}
}

type phaseAToolCall struct {
	ID   string
	Name string
	Body string
}

type phaseATokenCounter struct{}

func (phaseATokenCounter) Count(text string) int {
	return len(strings.Fields(text))
}

func (phaseATokenCounter) ModelLimit(_ string) int { return 100000 }

var _ = Describe("Engine RLM Phase A micro-compaction wiring", func() {
	It("compacts the oldest five reads + bash and keeps the last three visible", func() {
		tempDir := GinkgoT().TempDir()
		storeDir := filepath.Join(tempDir, "sessions")

		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())

		calls := []phaseAToolCall{
			{ID: "r1", Name: "read", Body: strings.Repeat("a", 600)},
			{ID: "r2", Name: "read", Body: strings.Repeat("b", 600)},
			{ID: "r3", Name: "read", Body: strings.Repeat("c", 600)},
			{ID: "r4", Name: "read", Body: strings.Repeat("d", 600)},
			{ID: "r5", Name: "read", Body: strings.Repeat("e", 600)},
			{ID: "b1", Name: "bash", Body: strings.Repeat("f", 600)},
			{ID: "b2", Name: "bash", Body: strings.Repeat("g", 600)},
			{ID: "b3", Name: "bash", Body: strings.Repeat("h", 600)},
		}
		phaseAStoreSeed(store, calls)

		manifest := agent.Manifest{
			ID:                "phase-a-agent",
			Name:              "Phase A wiring",
			ContextManagement: agent.DefaultContextManagement(),
		}
		manifest.ContextManagement.SlidingWindowSize = 50

		eng := engine.New(engine.Config{
			Manifest:     manifest,
			Store:        store,
			TokenCounter: phaseATokenCounter{},
			CompactionConfig: compaction.Config{
				MicroEnabled:      true,
				HotTailMinResults: 3,
				HotTailSizeBudget: 2000,
			},
			CompactionStoreDir: storeDir,
		})

		messages := eng.BuildContextWindowForTest(context.Background(), "phase-a-session", "next turn")

		toolResultMsgs := make([]provider.Message, 0, len(messages))
		for _, m := range messages {
			if m.Role == "tool" {
				toolResultMsgs = append(toolResultMsgs, m)
			}
		}
		Expect(len(toolResultMsgs)).To(BeNumerically(">=", 8),
			"expected all 8 tool-result messages to be present (compacted entries are still in-slice)")

		coldCount := 0
		hotCount := 0
		for _, m := range toolResultMsgs {
			if phaseAIsReference(m.Content) {
				coldCount++
			} else {
				hotCount++
			}
		}
		Expect(coldCount).To(Equal(5),
			"oldest five tool-results should be compacted to references")
		Expect(hotCount).To(Equal(3),
			"last three tool-results should stay visible")

		lastThree := toolResultMsgs[len(toolResultMsgs)-3:]
		for _, m := range lastThree {
			Expect(phaseAIsReference(m.Content)).To(BeFalse(),
				"final three tool results must remain verbatim")
		}
	})

	It("does not touch tool results when MicroEnabled=false", func() {
		tempDir := GinkgoT().TempDir()
		storeDir := filepath.Join(tempDir, "sessions")
		store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
		Expect(err).NotTo(HaveOccurred())

		calls := []phaseAToolCall{
			{ID: "r1", Name: "read", Body: strings.Repeat("x", 4000)},
			{ID: "r2", Name: "read", Body: strings.Repeat("y", 4000)},
		}
		phaseAStoreSeed(store, calls)

		eng := engine.New(engine.Config{
			Manifest: agent.Manifest{
				ID:                "phase-a-off",
				ContextManagement: agent.DefaultContextManagement(),
			},
			Store:              store,
			TokenCounter:       phaseATokenCounter{},
			CompactionConfig:   compaction.Config{MicroEnabled: false},
			CompactionStoreDir: storeDir,
		})

		messages := eng.BuildContextWindowForTest(context.Background(), "off-session", "next turn")

		for _, m := range messages {
			Expect(phaseAIsReference(m.Content)).To(BeFalse(),
				"no compaction should occur when MicroEnabled=false")
		}
	})
})
