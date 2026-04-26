package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
)

// signalledProvider is a provider.Provider test double that signals a
// channel when Chat is invoked. Tests use the signal to prove the
// extractor fired asynchronously after Stream's chunk channel drained,
// without reaching into engine internals.
type signalledProvider struct {
	streamChunks []provider.StreamChunk
	chatSignal   chan struct{}
	chatResp     provider.ChatResponse
	chatCalls    atomic.Int32
}

func (p *signalledProvider) Name() string { return "signalled" }

func (p *signalledProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(p.streamChunks))
	go func() {
		defer close(ch)
		for i := range p.streamChunks {
			ch <- p.streamChunks[i]
		}
	}()
	return ch, nil
}

func (p *signalledProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	p.chatCalls.Add(1)
	select {
	case p.chatSignal <- struct{}{}:
	default:
	}
	return p.chatResp, nil
}

func (p *signalledProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *signalledProvider) Models() ([]provider.Model, error) { return nil, nil }

// mustMarshalEntries marshals the given entries into a JSON string,
// failing the spec on marshal error. Used to script
// signalledProvider.chatResp bodies without hand-constructing JSON.
func mustMarshalEntries(entries []recall.KnowledgeEntry) string {
	data, err := json.Marshal(entries)
	Expect(err).NotTo(HaveOccurred(), "marshal entries")
	return string(data)
}

// T15 asynchronous knowledge extraction.
//
// These specs assert that Engine.Stream fires the L3
// recall.KnowledgeExtractor in a background goroutine after the stream
// loop terminates, using a context derived from context.Background so the
// extraction is not cancelled when the user-facing stream channel closes.
var _ = Describe("Engine.Stream async knowledge extraction", func() {
	It("fires the extractor after the stream completes and persists memory under sessionID", func() {
		chatProvider := &signalledProvider{
			streamChunks: []provider.StreamChunk{
				{Content: "ok"},
				{Content: "", Done: true},
			},
		}
		extractorProvider := &signalledProvider{
			chatSignal: make(chan struct{}, 1),
			chatResp: provider.ChatResponse{Message: provider.Message{
				Content: mustMarshalEntries([]recall.KnowledgeEntry{
					{ID: "e1", Type: "fact", Content: "T15 async extraction works", Relevance: 0.9},
				}),
			}},
		}

		memDir := GinkgoT().TempDir()
		memStore := recall.NewSessionMemoryStore(memDir)
		extractor := recall.NewKnowledgeExtractor(extractorProvider, memStore, "t15-session")

		storeDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(filepath.Join(storeDir, "ctx.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		cfg := ctxstore.DefaultCompressionConfig()
		cfg.SessionMemory.Enabled = true

		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Manifest: agent.Manifest{
				ID:                "t15-agent",
				Instructions:      agent.Instructions{SystemPrompt: "sys"},
				ContextManagement: agent.DefaultContextManagement(),
			},
			Store:              store,
			TokenCounter:       ctxstore.NewTiktokenCounter(),
			KnowledgeExtractor: extractor,
			CompressionConfig:  cfg,
		})

		chunks, err := eng.Stream(context.Background(), "t15-agent", "hello world")
		Expect(err).NotTo(HaveOccurred())
		for chunk := range chunks {
			_ = chunk
		}

		Eventually(extractorProvider.chatSignal, 2*time.Second).Should(Receive(),
			"KnowledgeExtractor.Extract was not invoked within 2s of stream completion")

		// Wait briefly for the store.Save to land. Poll rather than
		// sleep — the rename is quick but not instantaneous under the
		// race detector.
		target := filepath.Join(memDir, "t15-session", "memory.json")
		Eventually(func() error {
			_, statErr := os.Stat(target)
			return statErr
		}, 2*time.Second, 25*time.Millisecond).Should(Succeed(),
			"session memory file was never written at %s", target)
	})

	It("does not fire the extractor when SessionMemory.Enabled is false", func() {
		chatProvider := &signalledProvider{
			streamChunks: []provider.StreamChunk{
				{Content: "ok"},
				{Content: "", Done: true},
			},
		}
		extractorProvider := &signalledProvider{chatSignal: make(chan struct{}, 1)}

		memStore := recall.NewSessionMemoryStore(GinkgoT().TempDir())
		extractor := recall.NewKnowledgeExtractor(extractorProvider, memStore, "t15-session-off")

		storeDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(filepath.Join(storeDir, "ctx.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		cfg := ctxstore.DefaultCompressionConfig()
		cfg.SessionMemory.Enabled = false

		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Manifest: agent.Manifest{
				Instructions:      agent.Instructions{SystemPrompt: "sys"},
				ContextManagement: agent.DefaultContextManagement(),
			},
			Store:              store,
			TokenCounter:       ctxstore.NewTiktokenCounter(),
			KnowledgeExtractor: extractor,
			CompressionConfig:  cfg,
		})

		chunks, err := eng.Stream(context.Background(), "", "hello")
		Expect(err).NotTo(HaveOccurred())
		for chunk := range chunks {
			_ = chunk
		}

		Consistently(extractorProvider.chatSignal, 300*time.Millisecond).ShouldNot(Receive(),
			"extractor fired but SessionMemory is disabled")
	})

	It("uses the live sessionID via the KnowledgeExtractorFactory path", func() {
		chatProvider := &signalledProvider{
			streamChunks: []provider.StreamChunk{
				{Content: "ok"},
				{Content: "", Done: true},
			},
		}
		extractorProvider := &signalledProvider{
			chatSignal: make(chan struct{}, 1),
			chatResp: provider.ChatResponse{Message: provider.Message{
				Content: mustMarshalEntries([]recall.KnowledgeEntry{
					{ID: "e1", Type: "fact", Content: "factory path wiring works", Relevance: 0.9},
				}),
			}},
		}

		memDir := GinkgoT().TempDir()
		memStore := recall.NewSessionMemoryStore(memDir)

		storeDir := GinkgoT().TempDir()
		store, err := recall.NewFileContextStore(filepath.Join(storeDir, "ctx.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		cfg := ctxstore.DefaultCompressionConfig()
		cfg.SessionMemory.Enabled = true

		const liveSessionID = "live-session-xyz"

		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Manifest: agent.Manifest{
				ID:                "factory-agent",
				Instructions:      agent.Instructions{SystemPrompt: "sys"},
				ContextManagement: agent.DefaultContextManagement(),
			},
			Store:        store,
			TokenCounter: ctxstore.NewTiktokenCounter(),
			KnowledgeExtractorFactory: func(sessionID string) *recall.KnowledgeExtractor {
				return recall.NewKnowledgeExtractor(extractorProvider, memStore, sessionID)
			},
			CompressionConfig: cfg,
		})

		ctx := context.WithValue(context.Background(), session.IDKey{}, liveSessionID)
		chunks, err := eng.Stream(ctx, "factory-agent", "hello")
		Expect(err).NotTo(HaveOccurred())
		for chunk := range chunks {
			_ = chunk
		}

		Eventually(extractorProvider.chatSignal, 2*time.Second).Should(Receive(),
			"factory-path extraction did not fire within 2s of stream completion")

		// The live sessionID must be the directory under which memory.json
		// landed — the factory path's whole point is that each Stream gets
		// its own sessionID rather than a constructor-baked constant.
		target := filepath.Join(memDir, liveSessionID, "memory.json")
		Eventually(func() error {
			_, statErr := os.Stat(target)
			return statErr
		}, 2*time.Second, 25*time.Millisecond).Should(Succeed(),
			"memory.json was never written at %s", target)
	})
})
