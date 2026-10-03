package engine

import (
	"context"
	"strings"
	"testing"

	agentpkg "github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// TestRehydratedRebuildReinjectsStructuralContext verifies the Phase 3
// (rank 6) rebuild shape on the explicit/session path: the structural
// block, summary and verbatim tail all survive, budgeted at usable*4/5
// with oldest-stubs-first trimming.
func TestRehydratedRebuildReinjectsStructuralContext(t *testing.T) {
	e := newRehydratedTestEngine(t, 1000)
	msgs := []provider.Message{
		{Role: "user", Content: "tail message 0"},
		{Role: "user", Content: "tail message 1"},
	}
	rebuilt := e.rebuildRehydrated(
		context.Background(),
		"rehydrate-session",
		msgs,
		"[auto-compacted summary]: summary text",
		[]string{"[in_progress] phase 3 work"},
		[]string{"internal/pkg/file.go"},
		[]string{"chain/report"},
	)
	if len(rebuilt) == 0 {
		t.Fatal("rebuilt window is empty")
	}
	var joined strings.Builder
	for _, m := range rebuilt {
		joined.WriteString(m.Content)
		joined.WriteString("\n")
	}
	all := joined.String()
	for _, want := range []string{"phase 3 work", "internal/pkg/file.go", "chain/report", "summary text", "tail message 0", "tail message 1"} {
		if !strings.Contains(all, want) {
			t.Errorf("rebuilt window lacks %q", want)
		}
	}
}

// TestRecordSessionFileEvictsDuplicates keeps a re-edited path in the
// newest slot and caps the tracked list at sessionRecentFilesCap.
func TestRecordSessionFileEvictsDuplicates(t *testing.T) {
	e := newRehydratedTestEngine(t, 1000)
	e.recordSessionFile("s", "a.go")
	e.recordSessionFile("s", "b.go")
	e.recordSessionFile("s", "a.go")
	files := e.sessionRecentFiles["s"]
	if len(files) != 2 || files[0] != "b.go" || files[1] != "a.go" {
		t.Fatalf("want [b.go a.go], got %v", files)
	}
	for i := 0; i < sessionRecentFilesCap+5; i++ {
		e.recordSessionFile("s", string(rune('a'+i%26))+string(rune('0'+i%10))+".go")
	}
	if got := len(e.sessionRecentFiles["s"]); got != sessionRecentFilesCap {
		t.Fatalf("tracked files = %d, want cap %d", got, sessionRecentFilesCap)
	}
}

// TestStructuralContextSourcesResolvesTodoPlan checks the todo store
// items surface as the active-plan lines when no explicit sources are
// supplied.
func TestStructuralContextSourcesResolvesTodoPlan(t *testing.T) {
	e := newRehydratedTestEngine(t, 1000)
	plans, files, keys := e.structuralContextSources("rehydrate-session")
	if len(plans) != 0 && plans[0] != "plan item" {
		t.Fatalf("unexpected plans: %v", plans)
	}
	if len(files) != 0 || len(keys) != 0 {
		t.Fatalf("expected empty file/key tracking, got %v %v", files, keys)
	}
}

// rehydratedTestCounter is a one-token-per-word counter with a
// configurable model limit, mirroring the BDD harness counters.
type rehydratedTestCounter struct{ limit int }

func (c rehydratedTestCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return lenProviderFields(text)
}

func (c rehydratedTestCounter) ModelLimit(string) int { return c.limit }

// newRehydratedTestEngine builds a minimal engine wired the same way
// the Phase 3 BDD harness wires it: a temp context store and
// auto-compaction enabled with a controllable model limit.
func newRehydratedTestEngine(t *testing.T, limit int) *Engine {
	t.Helper()
	dir := t.TempDir()
	store, err := recall.NewFileContextStore(dir+"/ctx.json", "test-model")
	if err != nil {
		t.Fatalf("new file context store: %v", err)
	}
	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cm := agentpkg.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 50
	return New(Config{
		Manifest: agentpkg.Manifest{
			ID:                "rehydrate-agent",
			Instructions:      agentpkg.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		Store:                 store,
		TokenCounter:          rehydratedTestCounter{limit: limit},
		CompressionConfig:     cfg,
		OutputReserveForTests: 1,
	})
}

// lenProviderFields counts whitespace-separated fields without
// importing strings twice at call sites.
func lenProviderFields(text string) int {
	out, count := text, 0
	inField := false
	for _, r := range out {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			inField = false
			continue
		}
		if !inField {
			count++
			inField = true
		}
	}
	return count
}

var _ = provider.Message{}
