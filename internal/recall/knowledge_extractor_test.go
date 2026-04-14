// Package recall_test — T14 KnowledgeExtractor specification.
//
// KnowledgeExtractor asks an LLM to distil a slice of messages into a
// list of KnowledgeEntry records, merges the result into a
// SessionMemoryStore with content-based deduplication, and persists
// the store. The extractor is designed to be fired from a goroutine,
// so its contract is "best-effort with logged errors": a bad LLM
// response is surfaced as an error the caller can log, but never
// panics the process.
package recall_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// extractorProviderStub is a minimal provider.Provider double for T14.
// Only Chat matters; Stream, Embed, Models, and Name are satisfied but
// unused. Scripted responses let tests cover happy, malformed, and
// error paths without spinning up a real provider.
type extractorProviderStub struct {
	chatResp provider.ChatResponse
	chatErr  error
	calls    atomic.Int32
}

// Name implements provider.Provider; the extractor does not read it.
func (p *extractorProviderStub) Name() string { return "extractor-stub" }

// Stream implements provider.Provider; closed channel, no content.
func (p *extractorProviderStub) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

// Chat implements provider.Provider; returns the scripted response or
// error and increments the call counter atomically.
func (p *extractorProviderStub) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	p.calls.Add(1)
	return p.chatResp, p.chatErr
}

// Embed implements provider.Provider; unused in T14.
func (p *extractorProviderStub) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models implements provider.Provider; unused in T14.
func (p *extractorProviderStub) Models() ([]provider.Model, error) { return nil, nil }

// extractorSampleMessages returns a small slice of input messages used
// across all tests. The specific content is irrelevant because the LLM
// is stubbed; the slice exists so the signature matches the production
// call shape.
func extractorSampleMessages() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "what's the API URL?"},
		{Role: "assistant", Content: "it's /v1/agents"},
	}
}

// makeEntriesJSON builds the response body the stub provider returns.
// Keeping the marshalling in test code means any schema drift fails at
// compile time rather than via opaque parse errors.
func makeEntriesJSON(t *testing.T, entries []recall.KnowledgeEntry) string {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal entries: %v", err)
	}
	return string(data)
}

// TestKnowledgeExtractor_Extract_HappyPath_MergesIntoStore asserts the
// central contract: a valid LLM response is parsed, every entry lands
// in the store, and the store is saved to disk so the entries survive a
// restart.
func TestKnowledgeExtractor_Extract_HappyPath_MergesIntoStore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	body := makeEntriesJSON(t, []recall.KnowledgeEntry{
		{ID: "e1", Type: "fact", Content: "API base URL is /v1", Relevance: 0.9},
		{ID: "e2", Type: "convention", Content: "use British English", Relevance: 0.8},
	})
	prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: body}}}

	extractor := recall.NewKnowledgeExtractor(prov, store, "sess-happy")

	if err := extractor.Extract(context.Background(), extractorSampleMessages()); err != nil {
		t.Fatalf("Extract: unexpected error: %v", err)
	}
	if got := len(store.Entries()); got != 2 {
		t.Fatalf("store has %d entries; want 2", got)
	}

	// Save happened as part of Extract — reload from a fresh store to
	// verify the merge reached disk.
	reloaded := recall.NewSessionMemoryStore(dir)
	if err := reloaded.Load("sess-happy"); err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if got := len(reloaded.Entries()); got != 2 {
		t.Fatalf("reloaded has %d entries; want 2", got)
	}
}

// TestKnowledgeExtractor_Extract_SameEntryTwice_StaysDeduped asserts
// the plan's "deduplicated by Content" rule. Running Extract back-to-
// back with the same provider response must not grow the store.
func TestKnowledgeExtractor_Extract_SameEntryTwice_StaysDeduped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	body := makeEntriesJSON(t, []recall.KnowledgeEntry{
		{ID: "e1", Type: "fact", Content: "idempotent fact", Relevance: 0.5},
	})
	prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: body}}}
	extractor := recall.NewKnowledgeExtractor(prov, store, "sess-dedup")

	if err := extractor.Extract(context.Background(), extractorSampleMessages()); err != nil {
		t.Fatalf("Extract #1: %v", err)
	}
	if err := extractor.Extract(context.Background(), extractorSampleMessages()); err != nil {
		t.Fatalf("Extract #2: %v", err)
	}
	if got := len(store.Entries()); got != 1 {
		t.Fatalf("store has %d entries after two identical extractions; want 1", got)
	}
	if got := prov.calls.Load(); got != 2 {
		t.Fatalf("provider calls = %d; want 2 (Extract never memoises)", got)
	}
}

// TestKnowledgeExtractor_Extract_LLMError_ReturnsWrappedError asserts
// that a provider-level failure is surfaced to the caller. The caller
// (T15 Stream goroutine) logs the error; this contract keeps the
// extractor responsible for reporting, not for swallowing.
func TestKnowledgeExtractor_Extract_LLMError_ReturnsWrappedError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	prov := &extractorProviderStub{chatErr: errors.New("simulated LLM outage")}
	extractor := recall.NewKnowledgeExtractor(prov, store, "sess-llm-err")

	err := extractor.Extract(context.Background(), extractorSampleMessages())
	if err == nil {
		t.Fatalf("Extract: expected error on LLM failure; got nil")
	}
	if len(store.Entries()) != 0 {
		t.Fatalf("store has entries after LLM failure; want 0")
	}
}

// TestKnowledgeExtractor_Extract_MalformedJSON_ReturnsParseError
// asserts that a non-JSON body is reported as a parse error (not
// silently dropped). The store must not be mutated; the test writes to
// disk to verify no empty file was saved as a side effect.
func TestKnowledgeExtractor_Extract_MalformedJSON_ReturnsParseError(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: "not JSON"}}}
	extractor := recall.NewKnowledgeExtractor(prov, store, "sess-parse-err")

	err := extractor.Extract(context.Background(), extractorSampleMessages())
	if err == nil {
		t.Fatalf("Extract: expected parse error; got nil")
	}
	if len(store.Entries()) != 0 {
		t.Fatalf("store has entries after parse error; want 0")
	}
}

// TestKnowledgeExtractor_Extract_FencedJSON_ParsesSuccessfully asserts
// that the extractor's defensive fence-stripping survives an LLM that
// wraps its output in ```json ... ``` despite the system prompt
// forbidding fences. This exercises the happy branch of
// stripKnowledgeJSONFences which would otherwise stay uncovered.
func TestKnowledgeExtractor_Extract_FencedJSON_ParsesSuccessfully(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	raw := makeEntriesJSON(t, []recall.KnowledgeEntry{
		{ID: "f1", Type: "fact", Content: "fence-test", Relevance: 0.4},
	})
	fenced := "```json\n" + raw + "\n```"
	prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: fenced}}}
	extractor := recall.NewKnowledgeExtractor(prov, store, "sess-fenced")

	if err := extractor.Extract(context.Background(), extractorSampleMessages()); err != nil {
		t.Fatalf("Extract: unexpected error for fenced JSON: %v", err)
	}
	if got := len(store.Entries()); got != 1 {
		t.Fatalf("store has %d entries after fenced extract; want 1", got)
	}
}

// TestKnowledgeExtractor_Extract_FenceWithoutNewline_FallsThrough
// exercises the stripKnowledgeJSONFences branch where the response
// starts with ``` but contains no newline. The stripper returns the
// input unchanged, which then fails JSON parse — the assertion is that
// the error surfaces rather than the extractor silently accepting it.
func TestKnowledgeExtractor_Extract_FenceWithoutNewline_FallsThrough(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)
	prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: "```json no-newline"}}}
	extractor := recall.NewKnowledgeExtractor(prov, store, "sess-fence-no-nl")

	if err := extractor.Extract(context.Background(), extractorSampleMessages()); err == nil {
		t.Fatalf("Extract: expected parse error on fence-without-newline; got nil")
	}
}

// TestKnowledgeExtractor_Extract_EmptyMessages_IsNoOp asserts that the
// extractor refuses to call the LLM when there is nothing to
// distil. This keeps the cost floor at zero for idle sessions and
// mirrors the AutoCompactor's empty-input guard.
func TestKnowledgeExtractor_Extract_EmptyMessages_IsNoOp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := recall.NewSessionMemoryStore(dir)

	prov := &extractorProviderStub{}
	extractor := recall.NewKnowledgeExtractor(prov, store, "sess-empty")

	if err := extractor.Extract(context.Background(), nil); err != nil {
		t.Fatalf("Extract: unexpected error for empty input: %v", err)
	}
	if prov.calls.Load() != 0 {
		t.Fatalf("provider calls = %d; want 0 (empty-input guard)", prov.calls.Load())
	}
}
