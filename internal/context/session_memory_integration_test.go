// Package context_test — T16 WindowBuilder session-memory integration.
//
// These tests lock in the contract that when a SessionMemoryStore is
// attached to a WindowBuilder, its entries surface as a
// "[session memory]:" block positioned immediately after the system
// prompt. Ordering matters: a memory block after the recent-message
// sliding window would defeat its purpose, so the tests assert position
// relative to both the system prompt and the first user message.
package context_test

import (
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// stubCounter is a TokenCounter that returns 1 token per character,
// giving tests fine-grained control over whether content fits the
// budget. ModelLimit is arbitrary; WindowBuilder does not consult it
// during Build.
type stubCounter struct{}

func (stubCounter) Count(s string) int      { return len(s) }
func (stubCounter) ModelLimit(_ string) int { return 1_000_000 }

// newFileStoreWithMessages returns a FileContextStore seeded with the
// supplied messages in a per-test temp directory. Helper rather than
// duplicated boilerplate; removes one noise level from the tests below.
func newFileStoreWithMessages(t *testing.T, msgs []provider.Message) *recall.FileContextStore {
	t.Helper()
	store, err := recall.NewFileContextStore(t.TempDir()+"/ctx.json", "test-model")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	for _, m := range msgs {
		store.Append(m)
	}
	return store
}

// TestWindowBuilder_SessionMemoryBlock_AppearsAfterSystemPrompt asserts
// the position invariant: the "[session memory]:" block is the first
// non-system message in the built window, and it carries the expected
// fact/convention/preference bullet list.
func TestWindowBuilder_SessionMemoryBlock_AppearsAfterSystemPrompt(t *testing.T) {
	t.Parallel()

	memStore := recall.NewSessionMemoryStore(t.TempDir())
	memStore.AddEntry(recall.KnowledgeEntry{Type: "fact", Content: "API base URL is /v1", Relevance: 0.9})
	memStore.AddEntry(recall.KnowledgeEntry{Type: "convention", Content: "prefer snake_case", Relevance: 0.8})
	memStore.AddEntry(recall.KnowledgeEntry{Type: "preference", Content: "British English", Relevance: 0.7})

	builder := contextpkg.NewWindowBuilder(stubCounter{}).WithSessionMemory(memStore)

	store := newFileStoreWithMessages(t, []provider.Message{
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

	if len(result.Messages) < 2 {
		t.Fatalf("Build produced %d messages; want at least 2 (system + memory)", len(result.Messages))
	}
	if result.Messages[0].Role != "system" {
		t.Fatalf("messages[0].Role = %q; want system", result.Messages[0].Role)
	}

	if !strings.Contains(result.Messages[1].Content, "[session memory]:") {
		t.Fatalf("messages[1] does not contain [session memory] block; got %q", result.Messages[1].Content)
	}
	if !strings.Contains(result.Messages[1].Content, "API base URL is /v1") {
		t.Fatalf("session memory block missing fact content: %q", result.Messages[1].Content)
	}
	if !strings.Contains(result.Messages[1].Content, "prefer snake_case") {
		t.Fatalf("session memory block missing convention content: %q", result.Messages[1].Content)
	}
	if !strings.Contains(result.Messages[1].Content, "British English") {
		t.Fatalf("session memory block missing preference content: %q", result.Messages[1].Content)
	}

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
	if memIdx >= userIdx {
		t.Fatalf("session memory at %d must precede first user message at %d", memIdx, userIdx)
	}
}

// TestWindowBuilder_NoSessionMemoryAttached_BlockAbsent asserts that
// the session-memory feature is opt-in. A builder constructed without
// WithSessionMemory must produce a window with no memory block.
func TestWindowBuilder_NoSessionMemoryAttached_BlockAbsent(t *testing.T) {
	t.Parallel()

	builder := contextpkg.NewWindowBuilder(stubCounter{})

	store := newFileStoreWithMessages(t, []provider.Message{
		{Role: "user", Content: "hi"},
	})

	manifest := &agent.Manifest{
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: agent.DefaultContextManagement(),
	}
	result := builder.Build(manifest, store, 10_000)

	for _, m := range result.Messages {
		if strings.Contains(m.Content, "[session memory]:") {
			t.Fatalf("memory block present without WithSessionMemory: %q", m.Content)
		}
	}
}

// TestWindowBuilder_SessionMemoryEmpty_BlockAbsent asserts that an
// attached but empty store does not emit an empty shell block. This
// matters because an empty "[session memory]:" header would waste
// tokens and confuse the model.
func TestWindowBuilder_SessionMemoryEmpty_BlockAbsent(t *testing.T) {
	t.Parallel()

	memStore := recall.NewSessionMemoryStore(t.TempDir())
	builder := contextpkg.NewWindowBuilder(stubCounter{}).WithSessionMemory(memStore)

	store := newFileStoreWithMessages(t, []provider.Message{
		{Role: "user", Content: "hi"},
	})

	manifest := &agent.Manifest{
		Instructions:      agent.Instructions{SystemPrompt: "sys"},
		ContextManagement: agent.DefaultContextManagement(),
	}
	result := builder.Build(manifest, store, 10_000)

	for _, m := range result.Messages {
		if strings.Contains(m.Content, "[session memory]:") {
			t.Fatalf("memory block present with empty store: %q", m.Content)
		}
	}
}
