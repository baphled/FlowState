// Package engine_test covers P13 — the RecallBroker context-assembly
// hook must only fire for agents whose manifest opts in via
// uses_recall:true. Agents that default to false (the P13 default) must
// see zero RecallBroker.Query calls even when a broker is configured
// on the engine. This moves recall from "always on" to "opt-in per
// agent" and is the primary win of P13.
package engine_test

import (
	"bytes"
	stdctx "context"
	"log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/recall"
)

// countingBroker counts Query invocations atomically so the P13
// opt-in gate tests can assert exact call counts.
type countingBroker struct {
	queries atomic.Int64
}

func (b *countingBroker) Query(_ stdctx.Context, _ string, _ int) ([]recall.Observation, error) {
	b.queries.Add(1)
	return []recall.Observation{}, nil
}

var _ = Describe("RecallBroker opt-in gate (P13)", Label("p13", "recall", "opt-in"), func() {
	var (
		broker  *countingBroker
		counter *contextAssemblyTokenCounter
		tmpDir  string
	)

	BeforeEach(func() {
		broker = &countingBroker{}
		counter = &contextAssemblyTokenCounter{
			countFn:      func(text string) int { return len(text) / 4 },
			modelLimitFn: func(model string) int { return 8192 },
		}
		var err error
		tmpDir, err = os.MkdirTemp("", "p13-opt-in-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("skips the RecallBroker hook when manifest.UsesRecall is false", func() {
		manifest := agent.Manifest{
			ID:         "no-recall-agent",
			Name:       "No Recall Agent",
			UsesRecall: false,
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		eng := engine.New(engine.Config{
			ChatProvider: &contextAssemblyProvider{},
			Manifest:     manifest,
			TokenCounter: counter,
			Store:        store,
			RecallBroker: broker,
		})

		msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
		Expect(msgs).NotTo(BeEmpty())
		Expect(broker.queries.Load()).To(BeEquivalentTo(0),
			"broker.Query must not fire when uses_recall is false")
	})

	It("invokes the RecallBroker hook when manifest.UsesRecall is true", func() {
		manifest := agent.Manifest{
			ID:         "recall-agent",
			Name:       "Recall Agent",
			UsesRecall: true,
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		eng := engine.New(engine.Config{
			ChatProvider: &contextAssemblyProvider{},
			Manifest:     manifest,
			TokenCounter: counter,
			Store:        store,
			RecallBroker: broker,
		})

		msgs := eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hello")
		Expect(msgs).NotTo(BeEmpty())
		Expect(broker.queries.Load()).To(BeEquivalentTo(1),
			"broker.Query must fire exactly once when uses_recall is true")
	})

	It("defaults to skipping the broker when manifest.UsesRecall is zero-valued", func() {
		// Zero-value Manifest → UsesRecall defaults to false. This
		// locks in the P13 backwards-compat note: any manifest that
		// does not explicitly opt in loses recall.
		manifest := agent.Manifest{
			ID:   "default-agent",
			Name: "Default Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
		Expect(err).NotTo(HaveOccurred())

		eng := engine.New(engine.Config{
			ChatProvider: &contextAssemblyProvider{},
			Manifest:     manifest,
			TokenCounter: counter,
			Store:        store,
			RecallBroker: broker,
		})

		eng.BuildContextWindowForTest(stdctx.Background(), "ses-1", "Hi")
		Expect(broker.queries.Load()).To(BeEquivalentTo(0),
			"default UsesRecall=false must skip the broker")
	})
})

// Diagnostic for FlowState's silent-zero recall failure mode: when the
// recall pipeline is configured with one embedding model but the active
// session was created against a different one, Qdrant returns an empty
// 200 OK with no error and the operator cannot tell "no relevant data"
// from "embeddings are dimensionally wrong" without inspecting forensic
// state. Delivery G stamped Session.EmbeddingModel onto session
// metadata; this delivery wires the consumer to compare it against the
// recall pipeline's configured model and emit a loud, structured
// diagnostic on mismatch — without refusing the query (degraded
// results beat no results; the fix is observability, not gating).
//
// See:
//   - memory: project_flowstate_recall_silent_zero_failure
//   - vault: Bug Fixes/Recall Diagnostic - Embedding Model Stamp (May 2026).md
var _ = Describe("RecallBroker embedding-model dimension diagnostic", Label("recall", "diagnostic", "embedding-model"), func() {
	var (
		broker  *countingBroker
		counter *contextAssemblyTokenCounter
		tmpDir  string
		manifest agent.Manifest
	)

	BeforeEach(func() {
		broker = &countingBroker{}
		counter = &contextAssemblyTokenCounter{
			countFn:      func(text string) int { return len(text) / 4 },
			modelLimitFn: func(model string) int { return 8192 },
		}
		manifest = agent.Manifest{
			ID:         "recall-agent",
			Name:       "Recall Agent",
			UsesRecall: true,
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
		}
		var err error
		tmpDir, err = os.MkdirTemp("", "recall-embed-diagnostic")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	// captureSlog redirects the default slog logger to an in-memory
	// buffer for the duration of the spec, restoring the prior default
	// on cleanup. Mirrors the pattern in
	// internal/app/recall_collection_wiring_log_test.go so tests share
	// the same observability assertion surface.
	captureSlog := func() *bytes.Buffer {
		prev := slog.Default()
		DeferCleanup(func() { slog.SetDefault(prev) })
		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))
		return &buf
	}

	Context("when the session and recall embedding models match", func() {
		It("does not emit a mismatch diagnostic and still queries the broker", func() {
			buf := captureSlog()
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider:          &contextAssemblyProvider{},
				Manifest:              manifest,
				TokenCounter:          counter,
				Store:                 store,
				RecallBroker:          broker,
				RecallEmbeddingModel:  "nomic-embed-text",
				SessionEmbeddingLookup: func(_ string) (string, bool) {
					return "nomic-embed-text", true
				},
			})

			eng.BuildContextWindowForTest(stdctx.Background(), "ses-match", "Hello")

			Expect(broker.queries.Load()).To(BeEquivalentTo(1),
				"happy path must not gate the query")
			out := buf.String()
			Expect(out).NotTo(ContainSubstring("recall embedding-model mismatch"),
				"matching models must not surface a mismatch warning — log was: %s", out)
			Expect(out).NotTo(ContainSubstring("level=WARN"),
				"matching models must not raise log severity — log was: %s", out)
		})
	})

	Context("when the session embedding model differs from the recall pipeline", func() {
		It("emits a WARN diagnostic naming both models and the session ID, and still queries the broker", func() {
			buf := captureSlog()
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider:          &contextAssemblyProvider{},
				Manifest:              manifest,
				TokenCounter:          counter,
				Store:                 store,
				RecallBroker:          broker,
				RecallEmbeddingModel:  "nomic-embed-text",
				SessionEmbeddingLookup: func(_ string) (string, bool) {
					return "all-minilm", true
				},
			})

			eng.BuildContextWindowForTest(stdctx.Background(), "ses-mismatch", "Hello")

			Expect(broker.queries.Load()).To(BeEquivalentTo(1),
				"mismatch must not gate the query — degraded results beat no results")
			out := buf.String()
			Expect(out).To(ContainSubstring("recall embedding-model mismatch"),
				"mismatch must surface a single greppable phrase — log was: %s", out)
			Expect(out).To(ContainSubstring("level=WARN"),
				"mismatch must raise severity to WARN so it is visible in default operator filters — log was: %s", out)
			Expect(out).To(ContainSubstring("session_id=ses-mismatch"),
				"mismatch must name the session so operators can locate the .meta.json sidecar — log was: %s", out)
			Expect(out).To(ContainSubstring("session_embedding_model=all-minilm"),
				"mismatch must name the session's stamped model — log was: %s", out)
			Expect(out).To(ContainSubstring("recall_embedding_model=nomic-embed-text"),
				"mismatch must name the recall pipeline's configured model — log was: %s", out)
		})
	})

	Context("when the session predates the embedding-model stamp (legacy session)", func() {
		It("emits an INFO diagnostic noting the gap and still queries the broker", func() {
			buf := captureSlog()
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())

			eng := engine.New(engine.Config{
				ChatProvider:          &contextAssemblyProvider{},
				Manifest:              manifest,
				TokenCounter:          counter,
				Store:                 store,
				RecallBroker:          broker,
				RecallEmbeddingModel:  "nomic-embed-text",
				SessionEmbeddingLookup: func(_ string) (string, bool) {
					// Session predates Delivery G — the field exists
					// but is empty on disk because the sidecar was
					// written before the stamp wiring.
					return "", true
				},
			})

			eng.BuildContextWindowForTest(stdctx.Background(), "ses-legacy", "Hello")

			Expect(broker.queries.Load()).To(BeEquivalentTo(1),
				"legacy session must not gate the query")
			out := buf.String()
			Expect(out).To(ContainSubstring("recall embedding-model unverifiable"),
				"legacy gap must surface a distinct phrase from the mismatch case — log was: %s", out)
			Expect(out).To(ContainSubstring("level=INFO"),
				"legacy gap must stay at INFO — operator can't act on missing data, only note it — log was: %s", out)
			Expect(out).To(ContainSubstring("session_id=ses-legacy"),
				"legacy log must name the session so operators can locate the sidecar — log was: %s", out)
			Expect(out).NotTo(ContainSubstring("level=WARN"),
				"legacy gap must NOT escalate to WARN — that's reserved for live mismatch — log was: %s", out)
		})
	})

	Context("when the recall pipeline is not configured with an embedding model", func() {
		It("skips the diagnostic entirely (nothing to compare against)", func() {
			buf := captureSlog()
			store, err := recall.NewFileContextStore(filepath.Join(tmpDir, "store.json"), "test-model")
			Expect(err).NotTo(HaveOccurred())

			// No RecallEmbeddingModel set — the recall pipeline
			// itself is unconfigured for embedding routing. The
			// diagnostic has no reference point and must stay silent
			// rather than inventing a comparison.
			eng := engine.New(engine.Config{
				ChatProvider: &contextAssemblyProvider{},
				Manifest:     manifest,
				TokenCounter: counter,
				Store:        store,
				RecallBroker: broker,
				SessionEmbeddingLookup: func(_ string) (string, bool) {
					return "nomic-embed-text", true
				},
			})

			eng.BuildContextWindowForTest(stdctx.Background(), "ses-nopipeline", "Hello")

			out := buf.String()
			Expect(out).NotTo(ContainSubstring("recall embedding-model mismatch"),
				"unconfigured pipeline must not synthesise a mismatch — log was: %s", out)
			Expect(out).NotTo(ContainSubstring("recall embedding-model unverifiable"),
				"unconfigured pipeline must not synthesise an INFO line either — log was: %s", out)
		})
	})
})
