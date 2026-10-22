package recall_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// extractorProviderStub is a minimal provider.Provider double for T14.
// Only Chat matters; Stream, Embed, Models, and Name are satisfied but
// unused. Scripted responses let tests cover happy, malformed, and error
// paths without spinning up a real provider.
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

// Chat implements provider.Provider; returns the scripted response or error
// and increments the call counter atomically.
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
// across all tests. The specific content is irrelevant because the LLM is
// stubbed; the slice exists so the signature matches the production call
// shape.
func extractorSampleMessages() []provider.Message {
	return []provider.Message{
		{Role: "user", Content: "what's the API URL?"},
		{Role: "assistant", Content: "it's /v1/agents"},
	}
}

// makeEntriesJSON builds the response body the stub provider returns.
// Keeping the marshalling in test code means any schema drift fails at
// compile time rather than via opaque parse errors.
func makeEntriesJSON(entries []recall.KnowledgeEntry) string {
	data, err := json.Marshal(entries)
	Expect(err).NotTo(HaveOccurred(), "marshal entries")
	return string(data)
}

// T14 KnowledgeExtractor specification.
//
// KnowledgeExtractor asks an LLM to distil a slice of messages into a list
// of KnowledgeEntry records, merges the result into a SessionMemoryStore
// with content-based deduplication, and persists the store. The extractor
// is designed to be fired from a goroutine, so its contract is "best-effort
// with logged errors": a bad LLM response is surfaced as an error the
// caller can log, but never panics the process.
var _ = Describe("KnowledgeExtractor.Extract", func() {
	It("happy path: parses the LLM response, merges every entry into the store, and persists", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)

		body := makeEntriesJSON([]recall.KnowledgeEntry{
			{ID: "e1", Type: "fact", Content: "API base URL is /v1", Relevance: 0.9},
			{ID: "e2", Type: "convention", Content: "use British English", Relevance: 0.8},
		})
		prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: body}}}
		extractor := recall.NewKnowledgeExtractor(prov, store, "sess-happy")

		Expect(extractor.Extract(context.Background(), extractorSampleMessages())).To(Succeed())
		Expect(store.Entries()).To(HaveLen(2))

		// Save happened as part of Extract — reload from a fresh store to
		// verify the merge reached disk.
		reloaded := recall.NewSessionMemoryStore(dir)
		Expect(reloaded.Load("sess-happy")).To(Succeed())
		Expect(reloaded.Entries()).To(HaveLen(2))
	})

	It("dedupes by content across repeated extractions", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)

		body := makeEntriesJSON([]recall.KnowledgeEntry{
			{ID: "e1", Type: "fact", Content: "idempotent fact", Relevance: 0.5},
		})
		prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: body}}}
		extractor := recall.NewKnowledgeExtractor(prov, store, "sess-dedup")

		Expect(extractor.Extract(context.Background(), extractorSampleMessages())).To(Succeed())
		Expect(extractor.Extract(context.Background(), extractorSampleMessages())).To(Succeed())
		Expect(store.Entries()).To(HaveLen(1),
			"two identical extractions must collapse to a single store entry")
		Expect(prov.calls.Load()).To(Equal(int32(2)),
			"provider must be called once per Extract — Extract never memoises")
	})

	It("returns an error and leaves the store untouched when the provider fails", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)

		prov := &extractorProviderStub{chatErr: errors.New("simulated LLM outage")}
		extractor := recall.NewKnowledgeExtractor(prov, store, "sess-llm-err")

		Expect(extractor.Extract(context.Background(), extractorSampleMessages())).To(HaveOccurred())
		Expect(store.Entries()).To(BeEmpty())
	})

	It("returns a parse error and leaves the store untouched for malformed JSON", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)

		prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: "not JSON"}}}
		extractor := recall.NewKnowledgeExtractor(prov, store, "sess-parse-err")

		Expect(extractor.Extract(context.Background(), extractorSampleMessages())).To(HaveOccurred())
		Expect(store.Entries()).To(BeEmpty())
	})

	It("survives a defensive ```json fence in the response body", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)

		raw := makeEntriesJSON([]recall.KnowledgeEntry{
			{ID: "f1", Type: "fact", Content: "fence-test", Relevance: 0.4},
		})
		fenced := "```json\n" + raw + "\n```"
		prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: fenced}}}
		extractor := recall.NewKnowledgeExtractor(prov, store, "sess-fenced")

		Expect(extractor.Extract(context.Background(), extractorSampleMessages())).To(Succeed())
		Expect(store.Entries()).To(HaveLen(1))
	})

	It("surfaces a parse error when the response starts with ``` but has no newline", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)
		prov := &extractorProviderStub{chatResp: provider.ChatResponse{Message: provider.Message{Content: "```json no-newline"}}}
		extractor := recall.NewKnowledgeExtractor(prov, store, "sess-fence-no-nl")

		Expect(extractor.Extract(context.Background(), extractorSampleMessages())).To(HaveOccurred())
	})

	It("is a no-op for empty input messages and never calls the provider", func() {
		dir := GinkgoT().TempDir()
		store := recall.NewSessionMemoryStore(dir)
		prov := &extractorProviderStub{}
		extractor := recall.NewKnowledgeExtractor(prov, store, "sess-empty")

		Expect(extractor.Extract(context.Background(), nil)).To(Succeed())
		Expect(prov.calls.Load()).To(Equal(int32(0)),
			"empty-input guard: provider must not be called")
	})
})
