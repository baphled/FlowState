package context_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// percallSimpleCounter counts one token per character.
type percallSimpleCounter struct{}

func (percallSimpleCounter) Count(text string) int   { return len(text) }
func (percallSimpleCounter) ModelLimit(_ string) int { return 1_000_000 }

func buildStoreForPercallTest(dir, session string, messages []provider.Message) *recall.FileContextStore {
	path := filepath.Join(dir, session+"-ctx.json")
	store, err := recall.NewFileContextStore(path, "test-model")
	Expect(err).NotTo(HaveOccurred(), "NewFileContextStore(%s)", session)
	for _, m := range messages {
		store.Append(m)
	}
	return store
}

// expectSpillIsolated walks a spillover directory and asserts each JSON
// payload contains only content from the expected prefix, never from the
// forbidden one.
func expectSpillIsolated(dir, wantPrefix, forbiddenPrefix string) {
	entries, err := os.ReadDir(dir)
	Expect(err).NotTo(HaveOccurred(), "ReadDir(%s)", dir)
	sawAny := false
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred(), "ReadFile(%s)", path)
		body := string(data)
		Expect(body).NotTo(ContainSubstring(forbiddenPrefix),
			"cross-contamination: spill file %s contains forbidden prefix %q", path, forbiddenPrefix)
		Expect(body).To(ContainSubstring(wantPrefix),
			"spill file %s missing expected prefix %q", path, wantPrefix)
		sawAny = true
	}
	Expect(sawAny).To(BeTrue(),
		"no JSON payloads found under %s — the goroutine produced no cold spills", dir)
}

// Item 3 — WindowBuilder.splitter per-call refactor.
//
// Before Item 3 the splitter was a shared field on the WindowBuilder
// mutated by WithSplitter, and correctness depended on the engine holding
// buildWindowMu around the attach-then-build pair. Two concurrent callers
// without that lock would swap each other's splitter mid-flight, spilling
// one session's cold messages into another session's storage directory.
//
// The fix: pass the splitter per-call via ctxstore.WithSplitterOption.
// These specs prove (a) that without the option L1 stays inert and
// (b) that two goroutines on the same shared builder with different
// splitters do NOT contaminate each other's spill output.
var _ = Describe("WindowBuilder.BuildContext per-call splitter", func() {
	It("does not run L1 when no WithSplitterOption is supplied", func() {
		tmpDir := GinkgoT().TempDir()
		messages := []provider.Message{
			{Role: "user", Content: strings.Repeat("first turn content ", 40)},
			{Role: "assistant", Content: strings.Repeat("first response content ", 40)},
		}
		store := buildStoreForPercallTest(tmpDir, "no-splitter", messages)

		manifest := agent.Manifest{
			ID:                "worker",
			Instructions:      agent.Instructions{SystemPrompt: "system"},
			ContextManagement: agent.DefaultContextManagement(),
		}

		builder := flowctx.NewWindowBuilder(percallSimpleCounter{})
		result := builder.BuildContext(&manifest, "next", store, 100_000)

		for _, m := range result {
			Expect(strings.HasPrefix(m.Content, "[compacted: ")).To(BeFalse(),
				"placeholder emitted without WithSplitterOption: %q", m.Content)
		}
	})

	It("isolates concurrent callers' spillover output via per-call splitter option", func() {
		tmpDir := GinkgoT().TempDir()

		makeMessages := func(prefix string) []provider.Message {
			return []provider.Message{
				{Role: "user", Content: strings.Repeat(prefix+" user content ", 60)},
				{Role: "assistant", Content: strings.Repeat(prefix+" assistant response ", 60)},
				{Role: "user", Content: strings.Repeat(prefix+" follow-up ", 60)},
			}
		}
		storeA := buildStoreForPercallTest(tmpDir, "sess-A", makeMessages("alpha"))
		storeB := buildStoreForPercallTest(tmpDir, "sess-B", makeMessages("beta"))

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
			ID:                "worker",
			Instructions:      agent.Instructions{SystemPrompt: "system"},
			ContextManagement: agent.DefaultContextManagement(),
		}

		builder := flowctx.NewWindowBuilder(percallSimpleCounter{})

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			for range 20 {
				_ = builder.BuildContext(&manifest, "next A", storeA, 100_000, flowctx.WithSplitterOption(splitterA))
			}
		}()
		go func() {
			defer wg.Done()
			for range 20 {
				_ = builder.BuildContext(&manifest, "next B", storeB, 100_000, flowctx.WithSplitterOption(splitterB))
			}
		}()

		wg.Wait()

		splitterA.Stop()
		splitterB.Stop()

		expectSpillIsolated(filepath.Join(spillA, "sess-A"), "alpha", "beta")
		expectSpillIsolated(filepath.Join(spillB, "sess-B"), "beta", "alpha")
	})
})
