// Package engine — C1 regression coverage for session-close
// eviction of per-session HotColdSplitters.
//
// The sessionSplitters cache is unbounded: every unique sessionID that
// hits buildContextWindow with MicroCompaction enabled lands one
// entry. A long-running `flowstate serve` will leak splitters +
// persist-worker goroutines + their buffered channels forever unless
// entries are evicted on session close.
//
// Before C1 there was no eviction path at all. After C1 the engine
// subscribes to session.ended at construction time and tears down
// the matching cache entry: Stop() drains the worker; delete() frees
// the map slot. Subsequent buildContextWindow for the same sessionID
// reconstructs a fresh splitter.
package engine_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// newMicroCompactionTestEngine builds an Engine with L1 enabled, a
// real FileContextStore, and a temp spillover directory, ready to
// cache splitters via BuildContextWindowForTesting.
func newMicroCompactionTestEngine(t *testing.T) (*engine.Engine, *eventbus.EventBus) {
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

// evictionTokenCounter is a minimal TokenCounter: every message is
// one token. Enough for Engine.New to build a WindowBuilder.
type evictionTokenCounter struct{}

func (evictionTokenCounter) Count(text string) int   { return len(text) }
func (evictionTokenCounter) ModelLimit(_ string) int { return 10000 }

// noopChatProvider is enough to satisfy Engine.New — we never call
// Stream in these tests.
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

// waitForEviction polls SessionSplitterForTest until the cache slot
// is empty, with a small bounded budget. Event delivery through the
// bus is synchronous in the current implementation, but a poll guards
// against any future asynchrony and is cheap.
func waitForEviction(t *testing.T, eng *engine.Engine, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if eng.SessionSplitterForTest(sessionID) == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("session splitter for %q was not evicted within deadline", sessionID)
}

// TestSessionEnded_EvictsCachedSplitter is the core C1 regression:
// after building a window for sessionID S, publishing session.ended
// for S must remove the cache entry and Stop the splitter cleanly.
func TestSessionEnded_EvictsCachedSplitter(t *testing.T) {
	t.Parallel()

	eng, bus := newMicroCompactionTestEngine(t)
	ctx := context.Background()
	sessionID := "session-c1-evict"

	// Build a window so a splitter lands in the cache.
	eng.BuildContextWindowForTesting(ctx, sessionID, "hello")

	if eng.SessionSplitterForTest(sessionID) == nil {
		t.Fatalf("expected cached splitter for %q after Build, got nil", sessionID)
	}

	// Publish session.ended. The subscription registered in New must
	// see this, look up the splitter, and evict it.
	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: sessionID,
		Action:    "ended",
	}))

	waitForEviction(t, eng, sessionID)
}

// TestSessionEnded_OtherSessionsUnaffected pins the specificity of
// the eviction: a session.ended for session A must not remove the
// splitter for session B.
func TestSessionEnded_OtherSessionsUnaffected(t *testing.T) {
	t.Parallel()

	eng, bus := newMicroCompactionTestEngine(t)
	ctx := context.Background()

	sessionA := "session-c1-a"
	sessionB := "session-c1-b"

	eng.BuildContextWindowForTesting(ctx, sessionA, "hello A")
	eng.BuildContextWindowForTesting(ctx, sessionB, "hello B")

	if eng.SessionSplitterForTest(sessionA) == nil || eng.SessionSplitterForTest(sessionB) == nil {
		t.Fatalf("expected both sessions to have cached splitters")
	}

	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: sessionA,
		Action:    "ended",
	}))

	waitForEviction(t, eng, sessionA)

	// B must still be present.
	if eng.SessionSplitterForTest(sessionB) == nil {
		t.Fatalf("session.ended for %q wrongly evicted %q", sessionA, sessionB)
	}
}

// TestSessionEnded_ReBuildConstructsFreshSplitter proves that after
// eviction, the next buildContextWindow for the same sessionID
// constructs a brand-new splitter — not the evicted one. This is the
// invariant that makes the cache safe to use after an end event.
func TestSessionEnded_ReBuildConstructsFreshSplitter(t *testing.T) {
	t.Parallel()

	eng, bus := newMicroCompactionTestEngine(t)
	ctx := context.Background()
	sessionID := "session-c1-rebuild"

	eng.BuildContextWindowForTesting(ctx, sessionID, "hello")
	first := eng.SessionSplitterForTest(sessionID)
	if first == nil {
		t.Fatalf("expected first splitter, got nil")
	}

	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: sessionID,
		Action:    "ended",
	}))

	waitForEviction(t, eng, sessionID)

	eng.BuildContextWindowForTesting(ctx, sessionID, "hello again")
	second := eng.SessionSplitterForTest(sessionID)
	if second == nil {
		t.Fatalf("expected fresh splitter after re-build, got nil")
	}
	if first == second {
		t.Fatalf("re-build returned the evicted splitter (identity %v); expected a fresh instance", fmt.Sprintf("%p", first))
	}
}

// TestSessionEnded_NoSplitterCached_NoOp ensures that publishing
// session.ended for a session that never built a window is a silent
// no-op — no panic, no error.
func TestSessionEnded_NoSplitterCached_NoOp(t *testing.T) {
	t.Parallel()

	_, bus := newMicroCompactionTestEngine(t)

	// Must not panic.
	bus.Publish(pluginevents.EventSessionEnded, pluginevents.NewSessionEvent(pluginevents.SessionEventData{
		SessionID: "never-built",
		Action:    "ended",
	}))
}
