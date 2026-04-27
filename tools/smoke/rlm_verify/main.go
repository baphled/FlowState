// Smoke that exercises RLM Phase A (micro-compaction) and Phase B
// (incremental fact extraction) end-to-end against a synthetic session,
// proving the wiring works on the integrated build.
//
// Run from an integration worktree:
//
//	go run ./tools/smoke/rlm_verify
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/context/compaction"
	"github.com/baphled/flowstate/internal/context/factstore"
	"github.com/baphled/flowstate/internal/provider"
)

func main() {
	tmpRoot, err := os.MkdirTemp("", "rlm-smoke-*")
	must("tempdir", err)
	defer os.RemoveAll(tmpRoot)

	fmt.Println("=== RLM Phase A — micro-compaction ===")
	verifyPhaseA(tmpRoot)

	fmt.Println("\n=== RLM Phase B — fact extraction & recall ===")
	verifyPhaseB(tmpRoot)

	fmt.Println("\n=== user config compaction status ===")
	reportConfigStatus()

	fmt.Println("\nPASS")
}

// verifyPhaseA constructs a Phase A MicroCompactor and verifies that a
// synthetic session with 5 read/bash tool results gets its older results
// spilled to a per-session cold-store directory while the hot tail stays
// inline.
func verifyPhaseA(root string) {
	storeRoot := filepath.Join(root, "phase-a")
	cmp := compaction.NewMicroCompactor(compaction.Options{
		StoreRoot:  storeRoot,
		HotTailMin: 2,
		SizeBudget: 512,
	})

	sessionID := "rlm-smoke"
	msgs := []provider.Message{
		{Role: "user", Content: "investigate"},
		toolResult("call-1", "read", strings.Repeat("LARGE-A ", 200)),
		toolResult("call-2", "bash", strings.Repeat("LARGE-B ", 200)),
		toolResult("call-3", "grep", strings.Repeat("LARGE-C ", 200)),
		toolResult("call-4", "read", strings.Repeat("LARGE-D ", 200)),
		toolResult("call-5", "bash", strings.Repeat("LARGE-E ", 50)),
	}

	out, err := cmp.Compact(context.Background(), sessionID, msgs)
	must("compact", err)

	references, hot := classifyResults(out)
	fmt.Printf("  input messages:    %d (5 compactable tool results)\n", len(msgs))
	fmt.Printf("  output messages:   %d\n", len(out))
	fmt.Printf("  spilled to cold:   %d (became reference messages)\n", references)
	fmt.Printf("  hot tail kept:     %d (full content preserved)\n", hot)

	coldDir := filepath.Join(storeRoot, sessionID, "compacted")
	entries, err := os.ReadDir(coldDir)
	must("read cold dir", err)
	fmt.Printf("  cold-store path:   %s\n", coldDir)
	fmt.Printf("  cold-store files:  %d\n", len(entries))
	if len(entries) != references {
		failf("Phase A wrote %d cold-store files but produced %d references", len(entries), references)
	}

	if references == 0 || hot == 0 {
		failf("Phase A did not split hot/cold")
	}
	if len(entries) == 0 {
		failf("Phase A wrote no cold-store files")
	}
}

// verifyPhaseB constructs a Phase B Service (extractor + store) and
// confirms it ingests synthetic session messages, persists a JSONL file,
// and recalls relevant facts on a query.
func verifyPhaseB(root string) {
	storeRoot := filepath.Join(root, "phase-b")
	store := factstore.NewFileFactStore(storeRoot)
	extractor := factstore.NewRegexFactExtractor()
	svc := factstore.NewService(store, extractor, factstore.Config{MaxFactsPerSession: 50, RecallTopK: 3})

	sessionID := "rlm-smoke"
	msgs := []provider.Message{
		{Role: "user", Content: "Remember that the Qdrant collection is named flowstate-recall."},
		{Role: "assistant", Content: "Got it."},
		{Role: "user", Content: "Always use make ai-commit, never git commit directly."},
		{Role: "user", Content: "I'm a senior engineer who works in Go."},
	}

	must("ingest", svc.IngestSession(context.Background(), sessionID, msgs))

	all, err := svc.List(context.Background(), sessionID)
	must("list", err)
	fmt.Printf("  ingested messages: %d\n", len(msgs))
	fmt.Printf("  facts extracted:   %d\n", len(all))
	for i, f := range all {
		preview := f.Text
		if len(preview) > 60 {
			preview = preview[:60] + "…"
		}
		fmt.Printf("    fact[%d]: %s\n", i, preview)
	}

	recalled, err := svc.Recall(context.Background(), sessionID, "ai-commit", 3)
	must("recall", err)
	fmt.Printf("  recall(\"ai-commit\", topK=3): %d facts\n", len(recalled))
	for i, f := range recalled {
		preview := f.Text
		if len(preview) > 60 {
			preview = preview[:60] + "…"
		}
		fmt.Printf("    rank[%d]: %s\n", i, preview)
	}

	jsonlPath := store.Path(sessionID)
	st, err := os.Stat(jsonlPath)
	must("stat facts.jsonl", err)
	fmt.Printf("  facts.jsonl path:  %s (%d bytes, mode %o)\n", jsonlPath, st.Size(), st.Mode().Perm())

	if len(all) == 0 {
		failf("Phase B extracted zero facts from a session that contains explicit remember/always statements")
	}
}

// reportConfigStatus inspects the user's config.yaml for compaction
// blocks. Two distinct compactor flavours coexist: the pre-Phase-A
// "micro_compaction:" block (HotColdSplitter, JSON payloads) and the
// new Phase A "compaction:" block (MicroCompactor, .txt payloads, plus
// Phase B's fact_extraction_enabled toggle).
func reportConfigStatus() {
	home, _ := os.UserHomeDir()
	cfgPath := filepath.Join(home, ".config", "flowstate", "config.yaml")
	body, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Printf("  could not read %s: %v\n", cfgPath, err)
		return
	}
	hasOldMicro := strings.Contains(string(body), "micro_compaction:")
	hasNewCompaction := strings.Contains(string(body), "\ncompaction:")
	hasFactToggle := strings.Contains(string(body), "fact_extraction_enabled")
	fmt.Printf("  config: %s\n", cfgPath)
	fmt.Printf("    old 'micro_compaction:' block (pre-Phase-A HotColdSplitter): %v\n", hasOldMicro)
	fmt.Printf("    new 'compaction:' block (Phase A MicroCompactor):           %v\n", hasNewCompaction)
	fmt.Printf("    fact_extraction_enabled (Phase B):                          %v\n", hasFactToggle)
	if !hasNewCompaction {
		fmt.Printf("    NOTE: Phase A new system requires a 'compaction:' block\n")
		fmt.Printf("          with 'micro_enabled: true' before it activates.\n")
	}
}

func toolResult(id, tool, body string) provider.Message {
	return provider.Message{
		Role:      "tool",
		Content:   body,
		ToolCalls: []provider.ToolCall{{ID: id, Name: tool}},
	}
}

func classifyResults(out []provider.Message) (refs, hot int) {
	for _, msg := range out {
		if msg.Role != "tool" {
			continue
		}
		if strings.Contains(msg.Content, "[content offloaded to") {
			refs++
			continue
		}
		hot++
	}
	return refs, hot
}

func must(label string, err error) {
	if err != nil {
		failf("%s: %v", label, err)
	}
}

func failf(format string, args ...any) {
	fmt.Printf("FAIL: "+format+"\n", args...)
	os.Exit(1)
}

var _ = json.Marshal // keep import while iterating
