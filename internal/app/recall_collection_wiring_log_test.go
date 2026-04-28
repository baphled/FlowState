package app

import (
	"bytes"
	"log/slog"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

// Recall broker startup wiring log.
//
// Smoke discovery on 2026-04-27: a recall pipeline configured against
// the wrong collection (or with a chat-model identifier in the
// embedding path) failed silently. Operators had no startup signal
// telling them which collection, URL, and embedding model the broker
// was actually wired to. The architecture document declares
// `flowstate-recall` canonical; the smoke operator's local config
// pointed at `opencode_memory`. Both are valid, but the silent wiring
// gave no way to confirm which one was active.
//
// These specs pin the observability contract: when buildRecallBroker
// successfully wires a Qdrant source, an INFO log line names the
// collection, URL, and embedding model. Operators can grep that line
// at startup and confirm coherence between YAML/env, Qdrant, and the
// architectural canonical name. With the line in place the
// silent-config-drift category from Bug 2's note is closed.
var _ = Describe("buildRecallBroker startup wiring log", func() {
	It("emits an INFO log naming the collection, URL, and embedding model when Qdrant is wired", func() {
		prev := slog.Default()
		DeferCleanup(func() { slog.SetDefault(prev) })

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))

		client := &recordingMCPClient{}
		cfg := &config.AppConfig{}
		cfg.Qdrant.URL = "http://localhost:6333"
		cfg.Qdrant.Collection = "flowstate-recall"
		cfg.EmbeddingModel = "nomic-embed-text"

		broker := buildRecallBroker(recallBrokerParams{
			cfg:       cfg,
			mcpClient: client,
		})
		Expect(broker).NotTo(BeNil())

		out := buf.String()
		Expect(out).To(ContainSubstring("recall broker wired"),
			"startup log must declare the broker is wired with a single greppable phrase — log was: %s", out)
		Expect(out).To(ContainSubstring("collection=flowstate-recall"),
			"startup log must name the collection so operators can confirm it matches Qdrant — log was: %s", out)
		Expect(out).To(ContainSubstring("qdrant_url=http://localhost:6333"),
			"startup log must name the Qdrant URL so operators can confirm the env/YAML resolution — log was: %s", out)
		Expect(out).To(ContainSubstring("embedding_model=nomic-embed-text"),
			"startup log must name the embedding model so operators can confirm Bug 2 stays closed — log was: %s", out)
	})

	It("falls back to the canonical default `flowstate-recall` collection name when YAML omits qdrant.collection", func() {
		prev := slog.Default()
		DeferCleanup(func() { slog.SetDefault(prev) })

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))

		client := &recordingMCPClient{}
		cfg := &config.AppConfig{}
		cfg.Qdrant.URL = "http://localhost:6333"
		// cfg.Qdrant.Collection deliberately empty.

		broker := buildRecallBroker(recallBrokerParams{
			cfg:       cfg,
			mcpClient: client,
		})
		Expect(broker).NotTo(BeNil())

		out := buf.String()
		Expect(out).To(ContainSubstring("collection=flowstate-recall"),
			"the architectural canonical default must surface in the wiring log when YAML omits the collection — log was: %s", out)
	})

	It("does not emit the wiring INFO log when Qdrant is disabled", func() {
		prev := slog.Default()
		DeferCleanup(func() { slog.SetDefault(prev) })

		var buf bytes.Buffer
		handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})
		slog.SetDefault(slog.New(handler))

		client := &recordingMCPClient{}
		cfg := &config.AppConfig{} // No URL, no env var assumed in this test.

		broker := buildRecallBroker(recallBrokerParams{
			cfg:       cfg,
			mcpClient: client,
		})
		Expect(broker).NotTo(BeNil())

		out := buf.String()
		Expect(out).NotTo(ContainSubstring("recall broker wired"),
			"the wiring log must only fire on the Qdrant-attached branch — log was: %s", out)
	})
})
