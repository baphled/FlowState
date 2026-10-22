package context_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// stubCounter is a TokenCounter that returns 1 token per character,
// giving tests fine-grained control over whether content fits the
// budget. ModelLimit is arbitrary; WindowBuilder does not consult it
// during Build. Shared across context_test files (compression_metrics
// reuses it).
type stubCounter struct{}

func (stubCounter) Count(s string) int      { return len(s) }
func (stubCounter) ModelLimit(_ string) int { return 1_000_000 }

// newFileStoreWithMessages returns a FileContextStore seeded with the
// supplied messages in a per-spec temp directory. Helper rather than
// duplicated boilerplate; removes one noise level from the specs below.
func newFileStoreWithMessages(msgs []provider.Message) *recall.FileContextStore {
	store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "test-model")
	Expect(err).NotTo(HaveOccurred())
	for _, m := range msgs {
		store.Append(m)
	}
	return store
}

// T16 WindowBuilder session-memory integration.
//
// These specs lock in the contract that when a SessionMemoryStore is
// attached to a WindowBuilder, its entries surface as a "[session
// memory]:" block positioned immediately after the system prompt.
// Ordering matters: a memory block after the recent-message sliding
// window would defeat its purpose, so the specs assert position relative
// to both the system prompt and the first user message.
var _ = Describe("WindowBuilder session-memory integration", func() {
	It("places the [session memory] block immediately after the system prompt with all entry types", func() {
		memStore := recall.NewSessionMemoryStore(GinkgoT().TempDir())
		memStore.AddEntry(recall.KnowledgeEntry{Type: "fact", Content: "API base URL is /v1", Relevance: 0.9})
		memStore.AddEntry(recall.KnowledgeEntry{Type: "convention", Content: "prefer snake_case", Relevance: 0.8})
		memStore.AddEntry(recall.KnowledgeEntry{Type: "preference", Content: "British English", Relevance: 0.7})

		builder := contextpkg.NewWindowBuilder(stubCounter{}).WithSessionMemory(memStore)

		store := newFileStoreWithMessages([]provider.Message{
			{Role: "user", Content: "first user message"},
		})

		manifest := &agent.Manifest{
			ID: "t16-agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are helpful.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		result := builder.Build(manifest, store, 10_000)

		Expect(len(result.Messages)).To(BeNumerically(">=", 2),
			"Build produced %d messages; want at least 2 (system + memory)", len(result.Messages))
		Expect(result.Messages[0].Role).To(Equal("system"))

		Expect(result.Messages[1].Content).To(ContainSubstring("[session memory]:"))
		Expect(result.Messages[1].Content).To(ContainSubstring("API base URL is /v1"))
		Expect(result.Messages[1].Content).To(ContainSubstring("prefer snake_case"))
		Expect(result.Messages[1].Content).To(ContainSubstring("British English"))

		// The block must come BEFORE the first user message so the model
		// weights memory ahead of immediate turn content.
		userIdx := -1
		memIdx := -1
		for i, m := range result.Messages {
			if m.Role == "user" && userIdx == -1 {
				userIdx = i
			}
			if strings.Contains(m.Content, "[session memory]:") && memIdx == -1 {
				memIdx = i
			}
		}
		Expect(memIdx).To(BeNumerically("<", userIdx),
			"session memory at %d must precede first user message at %d", memIdx, userIdx)
	})

	It("does not emit a memory block when no SessionMemoryStore is attached", func() {
		builder := contextpkg.NewWindowBuilder(stubCounter{})

		store := newFileStoreWithMessages([]provider.Message{
			{Role: "user", Content: "hi"},
		})

		manifest := &agent.Manifest{
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: agent.DefaultContextManagement(),
		}
		result := builder.Build(manifest, store, 10_000)

		for _, m := range result.Messages {
			Expect(m.Content).NotTo(ContainSubstring("[session memory]:"),
				"memory block present without WithSessionMemory")
		}
	})

	It("does not emit an empty memory shell when the attached store has no entries", func() {
		memStore := recall.NewSessionMemoryStore(GinkgoT().TempDir())
		builder := contextpkg.NewWindowBuilder(stubCounter{}).WithSessionMemory(memStore)

		store := newFileStoreWithMessages([]provider.Message{
			{Role: "user", Content: "hi"},
		})

		manifest := &agent.Manifest{
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: agent.DefaultContextManagement(),
		}
		result := builder.Build(manifest, store, 10_000)

		for _, m := range result.Messages {
			Expect(m.Content).NotTo(ContainSubstring("[session memory]:"),
				"memory block present with empty store")
		}
	})
})
