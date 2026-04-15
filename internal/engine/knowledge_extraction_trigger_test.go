// Package engine_test — T15 asynchronous knowledge extraction.
//
// These tests assert that Engine.Stream fires the L3
// recall.KnowledgeExtractor in a background goroutine after the stream
// loop terminates, using a context derived from context.Background so
// the extraction is not cancelled when the user-facing stream channel
// closes.
package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

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

// TestStream_AsyncKnowledgeExtraction_FiresAfterStreamCompletes asserts
// the T15 central contract. The extractor provider's Chat is invoked
// from the background goroutine; the test blocks on the signal channel
// with a 2-second deadline to prove the extraction completes after
// the consumer drains the stream channel.
func TestStream_AsyncKnowledgeExtraction_FiresAfterStreamCompletes(t *testing.T) {
	t.Parallel()

	chatProvider := &signalledProvider{
		streamChunks: []provider.StreamChunk{
			{Content: "ok"},
			{Content: "", Done: true},
		},
	}
	extractorProvider := &signalledProvider{
		chatSignal: make(chan struct{}, 1),
		chatResp: provider.ChatResponse{Message: provider.Message{
			Content: mustMarshalEntries(t, []recall.KnowledgeEntry{
				{ID: "e1", Type: "fact", Content: "T15 async extraction works", Relevance: 0.9},
			}),
		}},
	}

	memDir := t.TempDir()
	memStore := recall.NewSessionMemoryStore(memDir)
	extractor := recall.NewKnowledgeExtractor(extractorProvider, memStore, "t15-session")

	storeDir := t.TempDir()
	store, err := recall.NewFileContextStore(filepath.Join(storeDir, "ctx.json"), "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

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
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for chunk := range chunks {
		_ = chunk
	}

	select {
	case <-extractorProvider.chatSignal:
		// Extraction fired; test passes.
	case <-time.After(2 * time.Second):
		t.Fatalf("KnowledgeExtractor.Extract was not invoked within 2s of stream completion")
	}

	// Wait briefly for the store.Save to land, then verify the memory
	// survived. Poll rather than sleep — the rename is quick but not
	// instantaneous under the race detector.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(filepath.Join(memDir, "t15-session", "memory.json")); statErr == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("session memory file was never written at %s", filepath.Join(memDir, "t15-session", "memory.json"))
}

// TestStream_AsyncKnowledgeExtraction_DisabledByConfig_DoesNotFire
// asserts the opt-in invariant: with SessionMemory.Enabled = false the
// goroutine is never launched, regardless of whether a KnowledgeExtractor
// is wired.
func TestStream_AsyncKnowledgeExtraction_DisabledByConfig_DoesNotFire(t *testing.T) {
	t.Parallel()

	chatProvider := &signalledProvider{
		streamChunks: []provider.StreamChunk{
			{Content: "ok"},
			{Content: "", Done: true},
		},
	}
	extractorProvider := &signalledProvider{chatSignal: make(chan struct{}, 1)}

	memStore := recall.NewSessionMemoryStore(t.TempDir())
	extractor := recall.NewKnowledgeExtractor(extractorProvider, memStore, "t15-session-off")

	storeDir := t.TempDir()
	store, err := recall.NewFileContextStore(filepath.Join(storeDir, "ctx.json"), "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

	cfg := ctxstore.DefaultCompressionConfig()
	cfg.SessionMemory.Enabled = false // explicit for readability

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
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for chunk := range chunks {
		_ = chunk
	}

	select {
	case <-extractorProvider.chatSignal:
		t.Fatalf("extractor fired but SessionMemory is disabled")
	case <-time.After(300 * time.Millisecond):
		// Silence as expected.
	}
}

// mustMarshalEntries marshals the given entries into a JSON string,
// failing the test on marshal error. Used to script
// signalledProvider.chatResp bodies without hand-constructing JSON.
func mustMarshalEntries(t *testing.T, entries []recall.KnowledgeEntry) string {
	t.Helper()
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal entries: %v", err)
	}
	return string(data)
}

// TestStream_AsyncKnowledgeExtraction_FactoryPath_WritesUnderLiveSessionID
// is the regression guard for the wiring gap identified on 2026-04-15:
// the App bootstrap builds a per-session extractor factory rather than
// a static extractor, so the live sessionID passed to Stream flows all
// the way through to SessionMemoryStore.Save. Without the factory
// branch in dispatchKnowledgeExtraction the memory would have been
// written under a constant "default" directory regardless of which
// session the stream belongs to.
func TestStream_AsyncKnowledgeExtraction_FactoryPath_WritesUnderLiveSessionID(t *testing.T) {
	t.Parallel()

	chatProvider := &signalledProvider{
		streamChunks: []provider.StreamChunk{
			{Content: "ok"},
			{Content: "", Done: true},
		},
	}
	extractorProvider := &signalledProvider{
		chatSignal: make(chan struct{}, 1),
		chatResp: provider.ChatResponse{Message: provider.Message{
			Content: mustMarshalEntries(t, []recall.KnowledgeEntry{
				{ID: "e1", Type: "fact", Content: "factory path wiring works", Relevance: 0.9},
			}),
		}},
	}

	memDir := t.TempDir()
	memStore := recall.NewSessionMemoryStore(memDir)

	storeDir := t.TempDir()
	store, err := recall.NewFileContextStore(filepath.Join(storeDir, "ctx.json"), "test-model")
	if err != nil {
		t.Fatalf("store: %v", err)
	}

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
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for chunk := range chunks {
		_ = chunk
	}

	select {
	case <-extractorProvider.chatSignal:
		// Extraction fired.
	case <-time.After(2 * time.Second):
		t.Fatalf("factory-path extraction did not fire within 2s of stream completion")
	}

	// The live sessionID must be the directory under which memory.json
	// landed — the factory path's whole point is that each Stream gets
	// its own sessionID rather than a constructor-baked constant.
	deadline := time.Now().Add(2 * time.Second)
	target := filepath.Join(memDir, liveSessionID, "memory.json")
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(target); statErr == nil {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("memory.json was never written at %s", target)
}
