package engine_test

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// engineTestT is the minimal subset of testing.TB the helper uses.
// Accepting an interface lets Ginkgo's GinkgoT() drive the same helper
// without depending on the full TB surface (which gained methods like
// ArtifactDir in Go 1.24).
type engineTestT interface {
	Helper()
	Fatalf(format string, args ...any)
	TempDir() string
}

// newMicroCompactionTestEngine builds an Engine with L1 enabled, a real
// FileContextStore, and a temp spillover directory, ready to cache
// splitters via BuildContextWindowForTesting.
func newMicroCompactionTestEngine(t engineTestT) (*engine.Engine, *eventbus.EventBus) {
	t.Helper()

	bus := eventbus.NewEventBus()
	tempDir := t.TempDir()
	storageDir := filepath.Join(tempDir, "compacted")

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	if err != nil {
		t.Fatalf("NewFileContextStore: %v", err)
	}

	chat := &noopChatProvider{}
	eng := engine.New(engine.Config{
		ChatProvider: chat,
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "You are helpful."},
			ContextManagement: agent.DefaultContextManagement(),
		},
		EventBus:     bus,
		Store:        store,
		TokenCounter: evictionTokenCounter{},
		CompressionConfig: ctxstore.CompressionConfig{
			MicroCompaction: ctxstore.MicroCompactionConfig{
				Enabled:           true,
				HotTailSize:       5,
				TokenThreshold:    1000,
				StorageDir:        storageDir,
				PlaceholderTokens: 50,
			},
		},
	})
	return eng, bus
}

// evictionTokenCounter is a minimal TokenCounter: every message is one
// token. Enough for Engine.New to build a WindowBuilder.
type evictionTokenCounter struct{}

func (evictionTokenCounter) Count(text string) int   { return len(text) }
func (evictionTokenCounter) ModelLimit(_ string) int { return 10000 }

// noopChatProvider is enough to satisfy Engine.New — Stream is never
// called in these tests.
type noopChatProvider struct{}

func (noopChatProvider) Name() string { return "noop" }
func (noopChatProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (noopChatProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (noopChatProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (noopChatProvider) Models() ([]provider.Model, error) { return nil, nil }

// expectSplitterEvicted polls SessionSplitterForTest until the cache slot
// is empty, with a small bounded budget. Event delivery through the bus
// is synchronous in the current implementation, but a poll guards against
// any future asynchrony and is cheap.
func expectSplitterEvicted(eng *engine.Engine, sessionID string) {
	Eventually(func() any {
		return eng.SessionSplitterForTest(sessionID)
	}, 2*time.Second, 5*time.Millisecond).Should(BeNil(),
		"session splitter for %q was not evicted within deadline", sessionID)
}

// C1 regression coverage for session-close eviction of per-session
// HotColdSplitters.
//
// The sessionSplitters cache is unbounded: every unique sessionID that
// hits buildContextWindow with MicroCompaction enabled lands one entry. A
// long-running `flowstate serve` will leak splitters + persist-worker
// goroutines + their buffered channels forever unless entries are evicted
// on session close.
//
// Before C1 there was no eviction path at all. After C1 the engine
// subscribes to session.ended at construction time and tears down the
// matching cache entry: Stop() drains the worker; delete() frees the map
// slot. Subsequent buildContextWindow for the same sessionID reconstructs
// a fresh splitter.
var _ = Describe("Engine session.ended eviction of HotColdSplitter cache", func() {
	It("evicts the cached splitter on session.ended", func() {
		eng, bus := newMicroCompactionTestEngine(GinkgoT())
		ctx := context.Background()
		sessionID := "session-c1-evict"

		eng.BuildContextWindowForTesting(ctx, sessionID, "hello")

		Expect(eng.SessionSplitterForTest(sessionID)).NotTo(BeNil(),
			"expected cached splitter for %q after Build", sessionID)

		bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
			SessionID: sessionID,
			Action:    "ended",
		}))

		expectSplitterEvicted(eng, sessionID)
	})

	It("does not evict a sibling session's splitter on session.ended", func() {
		eng, bus := newMicroCompactionTestEngine(GinkgoT())
		ctx := context.Background()

		sessionA := "session-c1-a"
		sessionB := "session-c1-b"

		eng.BuildContextWindowForTesting(ctx, sessionA, "hello A")
		eng.BuildContextWindowForTesting(ctx, sessionB, "hello B")

		Expect(eng.SessionSplitterForTest(sessionA)).NotTo(BeNil())
		Expect(eng.SessionSplitterForTest(sessionB)).NotTo(BeNil())

		bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
			SessionID: sessionA,
			Action:    "ended",
		}))

		expectSplitterEvicted(eng, sessionA)
		Expect(eng.SessionSplitterForTest(sessionB)).NotTo(BeNil(),
			"session.ended for %q wrongly evicted %q", sessionA, sessionB)
	})

	It("constructs a fresh splitter on rebuild after eviction", func() {
		eng, bus := newMicroCompactionTestEngine(GinkgoT())
		ctx := context.Background()
		sessionID := "session-c1-rebuild"

		eng.BuildContextWindowForTesting(ctx, sessionID, "hello")
		first := eng.SessionSplitterForTest(sessionID)
		Expect(first).NotTo(BeNil())

		bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
			SessionID: sessionID,
			Action:    "ended",
		}))

		expectSplitterEvicted(eng, sessionID)

		eng.BuildContextWindowForTesting(ctx, sessionID, "hello again")
		second := eng.SessionSplitterForTest(sessionID)
		Expect(second).NotTo(BeNil())
		Expect(first).NotTo(BeIdenticalTo(second),
			"re-build returned the evicted splitter (identity %v); expected a fresh instance", fmt.Sprintf("%p", first))
	})

	It("treats session.ended for a never-built session as a silent no-op (no panic)", func() {
		_, bus := newMicroCompactionTestEngine(GinkgoT())

		Expect(func() {
			bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
				SessionID: "never-built",
				Action:    "ended",
			}))
		}).NotTo(Panic())
	})
})
