// Package engine — tests for L1 micro-compaction wiring through
// buildWindowBuilder / buildContextWindow.
//
// These tests close the gap identified on 2026-04-14: HotColdSplitter
// was only ever constructed in *_test.go files. In production, the
// engine built its WindowBuilder without calling WithSplitter, so
// `compression.micro_compaction.enabled: true` was a silent no-op.
package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// wordTokenCounterForWiring attributes one token per whitespace-
// delimited word. Mirrors the helper used in the app-level wiring
// test so threshold sizing stays intuitive.
type wordTokenCounterForWiring struct{}

func (wordTokenCounterForWiring) Count(text string) int {
	count := 0
	inWord := false
	for _, r := range text {
		if r == ' ' || r == '\t' || r == '\n' {
			if inWord {
				count++
				inWord = false
			}
			continue
		}
		inWord = true
	}
	if inWord {
		count++
	}
	return count
}

func (wordTokenCounterForWiring) ModelLimit(_ string) int { return 10000 }

// TestEngine_MicroCompaction_Enabled_AttachesSplitterAndSpillsFiles
// drives a real engine (via BuildContextWindowForTest) with
// MicroCompaction.Enabled = true and a threshold low enough to force
// spillover. After the build, the configured StorageDir must contain
// at least one JSON payload under the session subdirectory.
//
// On the HEAD before the fix, this test fails because
// buildWindowBuilder never attached a HotColdSplitter regardless of
// configuration — the spillover directory stays empty.
func TestEngine_MicroCompaction_Enabled_AttachesSplitterAndSpillsFiles(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	storageDir := filepath.Join(tempDir, "compacted")

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	if err != nil {
		t.Fatalf("NewFileContextStore: %v", err)
	}

	// 10 messages × 7 words each. With HotTailSize=2 and
	// TokenThreshold=5, 8 cold units each ≥ 7 tokens will qualify.
	content := "one two three four five six seven"
	for range 10 {
		store.Append(provider.Message{Role: "assistant", Content: content})
	}

	manifest := agent.Manifest{
		ID:                "micro-wire-agent",
		Name:              "Micro-compaction wiring",
		ContextManagement: agent.DefaultContextManagement(),
	}

	eng := New(Config{
		Manifest:     manifest,
		Store:        store,
		TokenCounter: wordTokenCounterForWiring{},
		CompressionConfig: ctxstore.CompressionConfig{
			MicroCompaction: ctxstore.MicroCompactionConfig{
				Enabled:           true,
				HotTailSize:       2,
				TokenThreshold:    5,
				StorageDir:        storageDir,
				PlaceholderTokens: 10,
			},
		},
	})

	sessionID := "wire-session"
	_ = eng.BuildContextWindowForTest(context.Background(), sessionID, "next turn")

	// Allow the async persist worker to drain. We stop the cached
	// splitter explicitly via the test accessor; Stop blocks until
	// the worker goroutine exits so subsequent filesystem reads are
	// deterministic.
	if splitter := eng.SessionSplitterForTest(sessionID); splitter != nil {
		splitter.Stop()
	} else {
		t.Fatalf("engine did not cache a session splitter; WithSplitter never fired")
	}

	spillDir := filepath.Join(storageDir, sessionID)
	entries, err := os.ReadDir(spillDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v — engine never wired the splitter so no spill files were written", spillDir, err)
	}

	jsonCount := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			jsonCount++
		}
	}
	if jsonCount == 0 {
		t.Fatalf("spill dir %s contains no .json payloads; HotColdSplitter did not execute under the engine", spillDir)
	}
}

// TestEngine_MicroCompaction_Disabled_DoesNotAttachSplitter pins the
// opt-in contract: with MicroCompaction.Enabled = false the engine
// must not attach a splitter. This prevents a regression where the
// wiring is always on and existing deployments silently gain L1.
func TestEngine_MicroCompaction_Disabled_DoesNotAttachSplitter(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "ctx.json"), "fake-model")
	if err != nil {
		t.Fatalf("NewFileContextStore: %v", err)
	}
	store.Append(provider.Message{Role: "user", Content: "hello"})

	eng := New(Config{
		Manifest:     agent.Manifest{ID: "no-micro", ContextManagement: agent.DefaultContextManagement()},
		Store:        store,
		TokenCounter: wordTokenCounterForWiring{},
		CompressionConfig: ctxstore.CompressionConfig{
			MicroCompaction: ctxstore.MicroCompactionConfig{Enabled: false},
		},
	})

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-off", "next turn")

	if splitter := eng.SessionSplitterForTest("sess-off"); splitter != nil {
		t.Errorf("splitter was cached for session %q despite MicroCompaction.Enabled=false", "sess-off")
	}
}
