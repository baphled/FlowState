package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/prometheus/client_golang/prometheus"
	clientmodel "github.com/prometheus/client_model/go"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	pluginevents "github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tracer"
)

// compressionFakeProvider is a minimal provider.Provider that returns a
// caller-supplied summary JSON from Chat. The integration spec drives
// the L2 summariser through this fake so no real LLM or Ollama instance
// is required in CI. Only Chat is exercised; Stream, Embed, and Models
// return trivial stubs.
type compressionFakeProvider struct {
	summaryJSON string
	chatCalls   atomic.Int64
}

func (f *compressionFakeProvider) Name() string { return "compression-fake" }
func (f *compressionFakeProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}
func (f *compressionFakeProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	f.chatCalls.Add(1)
	return provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: f.summaryJSON},
	}, nil
}
func (f *compressionFakeProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (f *compressionFakeProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "fake-model", Provider: "compression-fake", ContextLength: 100}}, nil
}

// wordTokenCounter attributes one token per whitespace-delimited word.
// Matches the technique used by the engine's auto_compaction_trigger
// specs so the integration spec can precisely target the threshold with
// a modest token budget (100) and a handful of messages.
type wordTokenCounter struct{}

func (wordTokenCounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (wordTokenCounter) ModelLimit(_ string) int { return 100 }

// buildCompressionSummaryJSON produces a valid CompactionSummary JSON
// payload for the fake provider to return. Intent and NextSteps are both
// populated so the summariser's semantic validation passes.
func buildCompressionSummaryJSON() string {
	summary := ctxstore.CompactionSummary{
		Intent:    "wire-compression-integration",
		NextSteps: []string{"continue task"},
	}
	raw, err := json.Marshal(summary)
	Expect(err).NotTo(HaveOccurred(), "marshal summary")
	return string(raw)
}

// wiringScenario groups the live objects a compression wiring scenario
// produces, so each focused It can assert against one aspect without
// re-initialising the whole bootstrap.
type wiringScenario struct {
	manifest        agent.Manifest
	metricsReg      *prometheus.Registry
	fake            *compressionFakeProvider
	compression     compressionComponents
	messages        []provider.Message
	compactedEvents *atomic.Int64
	microStorageDir string
	sessionID       string
	eng             *engine.Engine
}

// runWiringScenario builds the compression components, wires them into a
// real engine, drives a threshold-crossing turn, and returns the
// observables.
func runWiringScenario() wiringScenario {
	tempDir, err := os.MkdirTemp("", "compression-wiring-*")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(tempDir) })

	cfg := &config.AppConfig{
		CategoryRouting: engine.DefaultCategoryRouting(),
		Compression: ctxstore.CompressionConfig{
			MicroCompaction: ctxstore.MicroCompactionConfig{
				Enabled:           true,
				HotTailSize:       2,
				TokenThreshold:    5,
				StorageDir:        filepath.Join(tempDir, "compacted"),
				PlaceholderTokens: 10,
			},
			AutoCompaction: ctxstore.AutoCompactionConfig{Enabled: true, Threshold: 0.60},
			SessionMemory: ctxstore.SessionMemoryConfig{
				Enabled:    true,
				StorageDir: filepath.Join(tempDir, "session-memory"),
			},
		},
	}

	metricsReg := prometheus.NewRegistry()
	recorder := tracer.NewPrometheusRecorder(metricsReg)
	fake := &compressionFakeProvider{summaryJSON: buildCompressionSummaryJSON()}
	compression := buildCompressionComponents(cfg, nil, fake, recorder)

	// Explicitly inherit the global AutoCompaction.Threshold (0.60 in
	// cfg above) by zeroing the per-agent override. The H3 precedence
	// rule in engine.autoCompactionThreshold treats
	// manifest.ContextManagement.CompactionThreshold > 0 as an
	// agent-specific override of the global; DefaultContextManagement
	// sets it to 0.75, which would mask the global the test's token
	// budget is configured against (ratio 0.70 would sit below 0.75 and
	// L2 would never fire). Zero means "use the global", which is what
	// this wiring spec — which exists to prove the global-config path
	// activates L2 end-to-end — actually wants.
	ctxMgmt := agent.DefaultContextManagement()
	ctxMgmt.CompactionThreshold = 0
	manifest := agent.Manifest{
		ID:                "compression-agent",
		Name:              "Compression Agent",
		ContextManagement: ctxMgmt,
	}
	if compression.summariserAdapter != nil {
		compression.summariserAdapter.WithManifest(&manifest)
	}

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "context.json"), "fake-model")
	Expect(err).NotTo(HaveOccurred())

	eng := engine.New(engine.Config{
		ChatProvider:       fake,
		Manifest:           manifest,
		Store:              store,
		TokenCounter:       wordTokenCounter{},
		AutoCompactor:      compression.autoCompactor,
		CompressionConfig:  compression.config,
		CompressionMetrics: compression.metrics,
		SessionMemoryStore: compression.sessionMemoryStore,
		Recorder:           compression.recorder,
	})

	var compactedEvents atomic.Int64
	eng.EventBus().Subscribe(pluginevents.EventContextCompacted, func(evt any) {
		if _, ok := evt.(*pluginevents.ContextCompactedEvent); ok {
			compactedEvents.Add(1)
		}
	})

	// 10 messages × 7 words = 70 tokens against a 100-token budget →
	// ratio 0.70 > threshold 0.60 → compaction must fire.
	const wordsPerMessage = 7
	content := strings.TrimSpace(strings.Repeat("w ", wordsPerMessage))
	for range 10 {
		store.Append(provider.Message{Role: "assistant", Content: content})
	}

	const sessionID = "session-wiring"
	msgs := eng.BuildContextWindowForTesting(context.Background(), sessionID, "next turn")

	return wiringScenario{
		manifest:        manifest,
		metricsReg:      metricsReg,
		fake:            fake,
		compression:     compression,
		messages:        msgs,
		compactedEvents: &compactedEvents,
		microStorageDir: filepath.Join(tempDir, "compacted"),
		sessionID:       sessionID,
		eng:             eng,
	}
}

// containsSummaryMarker reports whether msgs carries the
// [auto-compacted summary] marker spliced in by the engine after a
// successful compaction.
func containsSummaryMarker(msgs []provider.Message) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, "[auto-compacted summary]") {
			return true
		}
	}
	return false
}

// metricSeen walks the gathered metric families and reports whether name
// has at least one sample with the expected agent label. For counters
// the sample must also carry a positive value.
func metricSeen(families []*clientmodel.MetricFamily, name, agentID string) bool {
	for _, fam := range families {
		if fam.GetName() != name {
			continue
		}
		for _, m := range fam.GetMetric() {
			if !hasAgentLabel(m, agentID) {
				continue
			}
			if c := m.GetCounter(); c != nil && c.GetValue() > 0 {
				return true
			}
			if m.GetGauge() != nil {
				return true
			}
		}
	}
	return false
}

// hasAgentLabel reports whether m carries an agent_id label matching want.
func hasAgentLabel(m *clientmodel.Metric, want string) bool {
	for _, lbl := range m.GetLabel() {
		if lbl.GetName() == "agent_id" && lbl.GetValue() == want {
			return true
		}
	}
	return false
}

// Compression wiring end-to-end specs.
//
// These specs are the regression guard that closes the wiring gap
// identified on 2026-04-14: a real Engine built through the same
// compressionComponents path used by setupEngine must fire L2 compaction
// across the threshold, publish the plugin-events
// ContextCompactedEvent, increment the Prometheus
// compression-tokens-saved counter, observe the context-window gauge,
// and — when L3 is enabled — persist a session-memory directory.
//
// The specs deliberately do NOT call setupEngine wholesale because that
// path reaches out to MCP servers, reads filesystem configs, and
// initialises the entire app container. Instead they exercise the
// smallest bootstrap surface that proves the wiring: the
// buildCompressionComponents helper plus a fresh engine.New configured
// with the helper's output.
var _ = Describe("Compression wiring end-to-end activation", func() {
	var s wiringScenario

	BeforeEach(func() {
		s = runWiringScenario()
	})

	It("buildCompressionComponents honours Enabled flags", func() {
		Expect(s.compression.autoCompactor).NotTo(BeNil(),
			"autoCompactor is nil; AutoCompaction.Enabled=true should produce one")
		Expect(s.compression.sessionMemoryStore).NotTo(BeNil(),
			"sessionMemoryStore is nil; SessionMemory.Enabled=true should produce one")
		Expect(s.compression.metrics).NotTo(BeNil())
		Expect(s.compression.recorder).NotTo(BeNil())
	})

	It("AutoCompactor was invoked and spliced a summary into the window", func() {
		Expect(s.fake.chatCalls.Load()).To(BeNumerically(">=", int64(1)),
			"L2 summariser was not invoked")
		Expect(containsSummaryMarker(s.messages)).To(BeTrue(),
			"built context missing [auto-compacted summary] marker")
	})

	It("Prometheus counters land on the shared metrics registry", func() {
		families, err := s.metricsReg.Gather()
		Expect(err).NotTo(HaveOccurred())
		Expect(metricSeen(families, "flowstate_compression_tokens_saved_total", s.manifest.ID)).To(BeTrue(),
			"flowstate_compression_tokens_saved_total not observed for agent_id=%q", s.manifest.ID)
		Expect(metricSeen(families, "flowstate_context_window_tokens", s.manifest.ID)).To(BeTrue(),
			"flowstate_context_window_tokens not observed for agent_id=%q", s.manifest.ID)
	})

	It("ContextCompactedEvent is published on the engine bus", func() {
		Expect(s.compactedEvents.Load()).To(BeNumerically(">=", int64(1)))
	})

	It("L3 session-memory store was constructed", func() {
		Expect(s.compression.sessionMemoryStore).NotTo(BeNil())
	})

	It("L1 HotColdSplitter spills cold units to disk", func() {
		Expect(s.eng.StopSessionSplitterForTesting(s.sessionID)).To(BeTrue(),
			"engine cached no splitter for session %q; MicroCompaction wiring is disconnected", s.sessionID)

		spillDir := filepath.Join(s.microStorageDir, s.sessionID)
		entries, err := os.ReadDir(spillDir)
		Expect(err).NotTo(HaveOccurred(),
			"ReadDir(%s) failed; splitter never created the session spill directory", spillDir)

		jsonCount := 0
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				jsonCount++
			}
		}
		Expect(jsonCount).To(BeNumerically(">", 0),
			"spill dir %s contains no .json payloads; HotColdSplitter never executed against the recall store", spillDir)
	})
})

// Compression wiring opt-in contract.
//
// With cfg.Compression left at defaults (every Enabled=false)
// buildCompressionComponents must produce a bundle where AutoCompactor
// and SessionMemoryStore are nil. This prevents a regression where a
// caller flips defaults and silently activates compression on every
// deployment.
var _ = Describe("Compression wiring zero-value bundle", func() {
	It("produces nil AutoCompactor and SessionMemoryStore when compression is disabled", func() {
		cfg := &config.AppConfig{
			Compression: ctxstore.DefaultCompressionConfig(),
		}

		metricsReg := prometheus.NewRegistry()
		recorder := tracer.NewPrometheusRecorder(metricsReg)
		fake := &compressionFakeProvider{summaryJSON: "{}"}

		compression := buildCompressionComponents(cfg, nil, fake, recorder)

		Expect(compression.autoCompactor).To(BeNil(),
			"autoCompactor must be nil when AutoCompaction.Enabled=false")
		Expect(compression.sessionMemoryStore).To(BeNil(),
			"sessionMemoryStore must be nil when SessionMemory.Enabled=false")
		Expect(compression.metrics).NotTo(BeNil(),
			"metrics must always be populated so the engine hot path can increment without nil checks")
		Expect(compression.recorder).To(BeIdenticalTo(recorder),
			"recorder was not threaded through when compression is disabled")
		Expect(compression.summariserAdapter).To(BeNil(),
			"summariserAdapter must be nil when AutoCompaction.Enabled=false")
	})
})
