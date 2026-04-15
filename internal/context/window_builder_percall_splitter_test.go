package context_test

// Item 3 — WindowBuilder.splitter per-call refactor.
//
// Before Item 3 the splitter was a shared field on the WindowBuilder
// mutated by WithSplitter, and correctness depended on the engine
// holding buildWindowMu around the attach-then-build pair. Two
// concurrent callers without that lock would swap each other's
// splitter mid-flight, spilling one session's cold messages into
// another session's storage directory.
//
// The fix: pass the splitter per-call via ctxstore.WithSplitterOption.
// This test runs two goroutines calling Build* on the same shared
// builder with different splitters and asserts each goroutine's
// messages land exclusively in its own spillover directory.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// percallSimpleCounter counts one token per character, mirroring the
// simpleCounter used in micro_compaction_integration_test.go.
type percallSimpleCounter struct{}

func (percallSimpleCounter) Count(text string) int   { return len(text) }
func (percallSimpleCounter) ModelLimit(_ string) int { return 1_000_000 }

func buildStoreForPercallTest(t *testing.T, dir, session string, messages []provider.Message) *recall.FileContextStore {
	t.Helper()

	path := filepath.Join(dir, session+"-ctx.json")
	store, err := recall.NewFileContextStore(path, "test-model")
	if err != nil {
		t.Fatalf("NewFileContextStore(%s): %v", session, err)
	}
	for _, m := range messages {
		store.Append(m)
	}
	return store
}

// TestWindowBuilder_Build_NoSplitterOption_SkipsL1 pins the zero-option
// default. Without WithSplitterOption the builder must not attempt L1
// compaction — this is the migration path for pre-Item-3 callers
// that have not been updated to pass the option, and also the
// "micro-compaction disabled" path in production.
func TestWindowBuilder_Build_NoSplitterOption_SkipsL1(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	messages := []provider.Message{
		{Role: "user", Content: strings.Repeat("first turn content ", 40)},
		{Role: "assistant", Content: strings.Repeat("first response content ", 40)},
	}
	store := buildStoreForPercallTest(t, tmpDir, "no-splitter", messages)

	manifest := agent.Manifest{
		ID: "worker",
		Instructions: agent.Instructions{
			SystemPrompt: "system",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}

	builder := flowctx.NewWindowBuilder(percallSimpleCounter{})
	result := builder.BuildContext(&manifest, "next", store, 100_000)

	for _, m := range result {
		if strings.HasPrefix(m.Content, "[compacted: ") {
			t.Fatalf("placeholder emitted without WithSplitterOption: %q", m.Content)
		}
	}
}

// TestWindowBuilder_Build_ConcurrentCallersDoNotContaminate is the
// core Item 3 regression. Two goroutines call Build* on the SAME
// shared WindowBuilder with DIFFERENT splitters pointed at different
// storage directories. Each must see only its own splitter's output;
// no cold unit may land under the wrong session's dir.
//
// Before Item 3 this test would race — the WithSplitter setter
// mutated b.splitter without synchronisation, so goroutine A might
// run Split on goroutine B's splitter and spill into B's dir.
func TestWindowBuilder_Build_ConcurrentCallersDoNotContaminate(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	makeMessages := func(prefix string) []provider.Message {
		return []provider.Message{
			{Role: "user", Content: strings.Repeat(prefix+" user content ", 60)},
			{Role: "assistant", Content: strings.Repeat(prefix+" assistant response ", 60)},
			{Role: "user", Content: strings.Repeat(prefix+" follow-up ", 60)},
		}
	}
	storeA := buildStoreForPercallTest(t, tmpDir, "sess-A", makeMessages("alpha"))
	storeB := buildStoreForPercallTest(t, tmpDir, "sess-B", makeMessages("beta"))

	spillA := filepath.Join(tmpDir, "compacted-A")
	spillB := filepath.Join(tmpDir, "compacted-B")

	splitterA := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
		Compactor:   flowctx.NewDefaultMessageCompactor(20),
		HotTailSize: 1,
		StorageDir:  spillA,
		SessionID:   "sess-A",
	})
	splitterA.StartPersistWorker(context.Background())
	defer splitterA.Stop()

	splitterB := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
		Compactor:   flowctx.NewDefaultMessageCompactor(20),
		HotTailSize: 1,
		StorageDir:  spillB,
		SessionID:   "sess-B",
	})
	splitterB.StartPersistWorker(context.Background())
	defer splitterB.Stop()

	manifest := agent.Manifest{
		ID: "worker",
		Instructions: agent.Instructions{
			SystemPrompt: "system",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}

	// Share one builder between both goroutines — the whole point of
	// the refactor.
	builder := flowctx.NewWindowBuilder(percallSimpleCounter{})

	var wg sync.WaitGroup
	wg.Add(2)

	// Goroutine A — uses splitterA only.
	go func() {
		defer wg.Done()
		for range 20 {
			_ = builder.BuildContext(&manifest, "next A", storeA, 100_000, flowctx.WithSplitterOption(splitterA))
		}
	}()
	// Goroutine B — uses splitterB only.
	go func() {
		defer wg.Done()
		for range 20 {
			_ = builder.BuildContext(&manifest, "next B", storeB, 100_000, flowctx.WithSplitterOption(splitterB))
		}
	}()

	wg.Wait()

	splitterA.Stop()
	splitterB.Stop()

	// Each session's spillover dir must contain only payloads from
	// its own messages. Cross-contamination manifests as "alpha"
	// content inside sess-B's dir or vice versa.
	assertSpillIsolated(t, filepath.Join(spillA, "sess-A"), "alpha", "beta")
	assertSpillIsolated(t, filepath.Join(spillB, "sess-B"), "beta", "alpha")
}

// assertSpillIsolated walks a spillover directory and asserts each
// JSON payload contains only content from the expected prefix, never
// from the forbidden one.
func assertSpillIsolated(t *testing.T, dir, wantPrefix, forbiddenPrefix string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	sawAny := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", path, err)
		}
		body := string(data)
		if strings.Contains(body, forbiddenPrefix) {
			t.Fatalf("cross-contamination: spill file %s contains forbidden prefix %q", path, forbiddenPrefix)
		}
		if !strings.Contains(body, wantPrefix) {
			snippet := body
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			t.Fatalf("spill file %s missing expected prefix %q; body=%q", path, wantPrefix, snippet)
		}
		sawAny = true
	}
	if !sawAny {
		t.Fatalf("no JSON payloads found under %s — the goroutine produced no cold spills, test conditions are not exercising the splitter", dir)
	}
}
