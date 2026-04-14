package context_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// Phase 1 integration test: end-to-end micro-compaction flow.
//
// This suite exercises T1+T1a+T2+T3+T4+T5 wired together against a real
// recall.FileContextStore + WindowBuilder, and asserts the gate criteria
// from the team-lead's brief:
//
//   - session grows past threshold
//   - cold units spilled to ~/.flowstate/compacted/{id}/
//   - window contains placeholders
//   - raw session.Messages-equivalent (recall store) untouched
//   - parallel fan-out group compacted atomically (no orphan tool_call_id)
//   - spilled directory populated atomically (no leftover .tmp)
var _ = Describe("L1 micro-compaction end-to-end", Label("integration"), func() {
	var (
		tmpDir    string
		store     *recall.FileContextStore
		splitter  *flowctx.HotColdSplitter
		manifest  *agent.Manifest
		sessionID string
		sentinel  []provider.Message
	)

	bigContent := func(prefix string) string {
		return prefix + " " + strings.Repeat("token ", 60)
	}

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		sessionID = "phase1-int"

		var err error
		store, err = recall.NewFileContextStore(filepath.Join(tmpDir, "session.json"), "")
		Expect(err).NotTo(HaveOccurred())

		// 1 user solo + parallel fan-out (3 messages) + 4 user/asst solos.
		store.Append(provider.Message{Role: "user", Content: bigContent("u-old-1")})
		store.Append(provider.Message{
			Role: "assistant",
			ToolCalls: []provider.ToolCall{
				{ID: "call-A", Name: "read"},
				{ID: "call-B", Name: "read"},
			},
			Content: bigContent("calling tools in parallel"),
		})
		store.Append(provider.Message{
			Role:      "tool",
			Content:   bigContent("result-A"),
			ToolCalls: []provider.ToolCall{{ID: "call-A"}},
		})
		store.Append(provider.Message{
			Role:      "tool",
			Content:   bigContent("result-B"),
			ToolCalls: []provider.ToolCall{{ID: "call-B"}},
		})
		store.Append(provider.Message{Role: "user", Content: bigContent("u-mid")})
		store.Append(provider.Message{Role: "assistant", Content: bigContent("a-mid")})
		store.Append(provider.Message{Role: "user", Content: bigContent("u-recent")})
		store.Append(provider.Message{Role: "assistant", Content: bigContent("a-recent")})

		// Snapshot the canonical recall view BEFORE any compaction so we
		// can prove view-only later.
		sentinel = make([]provider.Message, len(store.AllMessages()))
		copy(sentinel, store.AllMessages())

		compactor := flowctx.NewDefaultMessageCompactor(20)
		splitter = flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
			Compactor:   compactor,
			HotTailSize: 2, // aggressive — pushes the fan-out group into cold
			StorageDir:  filepath.Join(tmpDir, "compacted"),
			SessionID:   sessionID,
		})
		splitter.StartPersistWorker(context.Background())

		manifest = &agent.Manifest{
			Instructions:      agent.Instructions{SystemPrompt: "you are an integration test"},
			ContextManagement: agent.ContextManagement{SlidingWindowSize: 16},
		}
	})

	AfterEach(func() {
		splitter.Stop()
	})

	It("compacts old units, leaves canonical history untouched, and spills atomically", func() {
		builder := flowctx.NewWindowBuilder(simpleCounter{}).WithSplitter(splitter)
		result := builder.BuildContext(context.Background(), manifest, "next user message", store, 100000)

		splitter.Stop() // ensure persist worker has drained before we inspect disk

		// 1. The recall store (canonical view) is untouched.
		Expect(store.AllMessages()).To(Equal(sentinel))

		// 2. The window contains at least one placeholder.
		placeholderCount := 0
		for _, m := range result {
			if strings.HasPrefix(m.Content, "[compacted: ") {
				placeholderCount++
			}
		}
		Expect(placeholderCount).To(BeNumerically(">=", 1))

		// 3. Tool atomicity: there is no `Role:"tool"` message in the
		//    output without an immediately preceding assistant tool_use
		//    message that declares the same id.
		assertNoOrphanToolMessages(result)

		// 4. The spillover directory was created and contains JSON
		//    payloads (one per cold unit). No leftover .tmp files.
		spillDir := filepath.Join(tmpDir, "compacted", sessionID)
		entries, err := os.ReadDir(spillDir)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).NotTo(BeEmpty())

		var jsonCount, tmpCount int
		for _, e := range entries {
			switch {
			case strings.HasSuffix(e.Name(), ".json"):
				jsonCount++
				data, readErr := os.ReadFile(filepath.Join(spillDir, e.Name()))
				Expect(readErr).NotTo(HaveOccurred())
				var payload flowctx.CompactedUnit
				Expect(json.Unmarshal(data, &payload)).To(Succeed())
				Expect(payload.Messages).NotTo(BeEmpty())
			case strings.HasSuffix(e.Name(), ".tmp"):
				tmpCount++
			}
		}
		Expect(jsonCount).To(BeNumerically(">=", 1))
		Expect(tmpCount).To(Equal(0))
	})

	It("leaves the recall store identical when compaction is disabled (no splitter attached)", func() {
		builder := flowctx.NewWindowBuilder(simpleCounter{})
		result := builder.BuildContext(context.Background(), manifest, "next", store, 100000)

		Expect(store.AllMessages()).To(Equal(sentinel))
		// Without a splitter, no placeholder strings appear.
		for _, m := range result {
			Expect(m.Content).NotTo(HavePrefix("[compacted: "))
		}
	})

	It("preserves an entire parallel fan-out group atomically when it lands in cold", func() {
		// Aggressively-small hot tail so the fan-out group is in cold.
		small := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
			Compactor:   flowctx.NewDefaultMessageCompactor(20),
			HotTailSize: 1,
			StorageDir:  filepath.Join(tmpDir, "compacted"),
			SessionID:   "atomic-test",
		})
		small.StartPersistWorker(context.Background())
		defer small.Stop()

		builder := flowctx.NewWindowBuilder(simpleCounter{}).WithSplitter(small)
		result := builder.BuildContext(context.Background(), manifest, "next", store, 100000)
		small.Stop()

		assertNoOrphanToolMessages(result)

		// Verify the spilled fan-out payload contains all three messages
		// of the group (assistant + 2 tool results) when it was indeed
		// the unit that got spilled.
		spillDir := filepath.Join(tmpDir, "compacted", "atomic-test")
		entries, _ := os.ReadDir(spillDir)
		foundFanOut := false
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			data, _ := os.ReadFile(filepath.Join(spillDir, e.Name()))
			var payload flowctx.CompactedUnit
			Expect(json.Unmarshal(data, &payload)).To(Succeed())
			if payload.Kind == flowctx.UnitToolGroup {
				foundFanOut = true
				Expect(payload.Messages).To(HaveLen(3))
				Expect(payload.Messages[0].Role).To(Equal("assistant"))
				Expect(payload.Messages[0].ToolCalls).To(HaveLen(2))
				Expect(payload.Messages[1].Role).To(Equal("tool"))
				Expect(payload.Messages[2].Role).To(Equal("tool"))
			}
		}
		Expect(foundFanOut).To(BeTrue(), "expected at least one spilled UnitToolGroup payload")
	})
})

// simpleCounter is a deterministic whitespace-split TokenCounter for tests.
type simpleCounter struct{}

func (simpleCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (simpleCounter) ModelLimit(string) int { return 1 << 20 }

// assertNoOrphanToolMessages walks the message slice and verifies every
// Role:"tool" message has been preceded (within the same turn) by an
// assistant message whose ToolCalls slice contains the matching id. A
// failure here means compaction has split a tool group, violating the
// Compaction Atomicity Invariant.
func assertNoOrphanToolMessages(msgs []provider.Message) {
	declared := make(map[string]bool)
	for i, m := range msgs {
		if m.Role == "assistant" {
			declared = make(map[string]bool, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				declared[tc.ID] = true
			}
			continue
		}
		if m.Role == "tool" {
			Expect(m.ToolCalls).To(HaveLen(1), "tool message %d carries unexpected number of ids", i)
			id := m.ToolCalls[0].ID
			Expect(declared).To(HaveKey(id), "orphan tool message %d (id=%s) — atomicity invariant violated", i, id)
			delete(declared, id)
		}
	}
}
