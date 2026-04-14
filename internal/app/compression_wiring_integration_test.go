package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

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
// caller-supplied summary JSON from Chat. The integration test drives
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
// Matching the technique used by the engine's auto_compaction_trigger
// tests so the integration test can precisely target the threshold with
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
func buildCompressionSummaryJSON(t *testing.T) string {
	t.Helper()
	summary := ctxstore.CompactionSummary{
		Intent:    "wire-compression-integration",
		NextSteps: []string{"continue task"},
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	return string(raw)
}

// wiringScenario groups the live objects a compression wiring test run
// produces, so each focused subtest can assert against one aspect
// without re-initialising the whole bootstrap.
type wiringScenario struct {
	manifest        agent.Manifest
	metricsReg      *prometheus.Registry
	fake            *compressionFakeProvider
	compression     compressionComponents
	messages        []provider.Message
	compactedEvents *atomic.Int64
}

// runWiringScenario builds the compression components, wires them into
// a real engine, drives a threshold-crossing turn, and returns the
// observables. Splitting this out of the test functions keeps the
// assertion sites under the cyclomatic complexity cap while still
// exercising the full live-bootstrap surface for each subtest.
func runWiringScenario(t *testing.T) wiringScenario {
	t.Helper()

	tempDir, err := os.MkdirTemp("", "compression-wiring-*")
	if err != nil {
		t.Fatalf("mkdir temp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tempDir) })

	cfg := &config.AppConfig{
		CategoryRouting: engine.DefaultCategoryRouting(),
		Compression: ctxstore.CompressionConfig{
			AutoCompaction: ctxstore.AutoCompactionConfig{Enabled: true, Threshold: 0.60},
			SessionMemory: ctxstore.SessionMemoryConfig{
				Enabled:    true,
				StorageDir: filepath.Join(tempDir, "session-memory"),
			},
		},
	}

	metricsReg := prometheus.NewRegistry()
	recorder := tracer.NewPrometheusRecorder(metricsReg)
	fake := &compressionFakeProvider{summaryJSON: buildCompressionSummaryJSON(t)}
	compression := buildCompressionComponents(cfg, nil, fake, recorder)

	manifest := agent.Manifest{
		ID:                "compression-agent",
		Name:              "Compression Agent",
		ContextManagement: agent.DefaultContextManagement(),
	}
	if compression.summariserAdapter != nil {
		compression.summariserAdapter.WithManifest(&manifest)
	}

	store, err := recall.NewFileContextStore(filepath.Join(tempDir, "context.json"), "fake-model")
	if err != nil {
		t.Fatalf("new context store: %v", err)
	}

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

	msgs := eng.BuildContextWindowForTesting(context.Background(), "session-wiring", "next turn")

	return wiringScenario{
		manifest:        manifest,
		metricsReg:      metricsReg,
		fake:            fake,
		compression:     compression,
		messages:        msgs,
		compactedEvents: &compactedEvents,
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

// metricSeen walks the gathered metric families and reports whether
// name has at least one sample with the expected agent label. For
// counters the sample must also carry a positive value.
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

// wiringAssertion names one focused check made against a wiringScenario.
// Declaring each assertion as a value rather than a nested closure keeps
// TestCompressionWiring_EndToEnd_ActivatesL2AndL3 under revive's
// cognitive-complexity gate; the branching is now hidden inside the
// per-assertion helpers.
type wiringAssertion struct {
	name  string
	check func(*testing.T, wiringScenario)
}

// wiringAssertions lists the end-to-end checks that collectively close
// the compression wiring gap. Each entry is a focused subtest so a
// single regression (e.g. metric label missing) surfaces as one named
// failure rather than burying the signal inside an omnibus test.
func wiringAssertions() []wiringAssertion {
	return []wiringAssertion{
		{"buildCompressionComponents honours Enabled flags", assertCompressionEnabledFlags},
		{"AutoCompactor was invoked and spliced a summary into the window", assertSummaryMarkerPresent},
		{"Prometheus counters land on the shared metrics registry", assertPrometheusMetricsObserved},
		{"ContextCompactedEvent is published on the engine bus", assertContextCompactedEventPublished},
		{"L3 session-memory store was constructed", assertSessionMemoryStoreConstructed},
	}
}

// TestCompressionWiring_EndToEnd_ActivatesL2AndL3 is the regression
// guard that closes the wiring gap identified on 2026-04-14: a real
// Engine built through the same compressionComponents path used by
// setupEngine must fire L2 compaction across the threshold, publish the
// plugin-events ContextCompactedEvent, increment the Prometheus
// compression-tokens-saved counter, observe the context-window gauge,
// and — when L3 is enabled — persist a session-memory directory.
//
// The test deliberately does NOT call setupEngine wholesale because that
// path reaches out to MCP servers, reads filesystem configs, and
// initialises the entire app container. Instead it exercises the
// smallest bootstrap surface that proves the wiring: the
// buildCompressionComponents helper plus a fresh engine.New configured
// with the helper's output.
func TestCompressionWiring_EndToEnd_ActivatesL2AndL3(t *testing.T) {
	t.Parallel()

	s := runWiringScenario(t)
	for _, a := range wiringAssertions() {
		t.Run(a.name, func(t *testing.T) {
			t.Parallel()
			a.check(t, s)
		})
	}
}

func assertCompressionEnabledFlags(t *testing.T, s wiringScenario) {
	t.Helper()
	if s.compression.autoCompactor == nil {
		t.Error("autoCompactor is nil; AutoCompaction.Enabled=true should produce one")
	}
	if s.compression.sessionMemoryStore == nil {
		t.Error("sessionMemoryStore is nil; SessionMemory.Enabled=true should produce one")
	}
	if s.compression.metrics == nil {
		t.Error("metrics is nil; should always be non-nil")
	}
	if s.compression.recorder == nil {
		t.Error("recorder is nil; should always be threaded through")
	}
}

func assertSummaryMarkerPresent(t *testing.T, s wiringScenario) {
	t.Helper()
	if got := s.fake.chatCalls.Load(); got < 1 {
		t.Errorf("fake provider Chat calls = %d; want >= 1 (L2 summariser not invoked)", got)
	}
	if !containsSummaryMarker(s.messages) {
		t.Error("built context missing [auto-compacted summary] marker; engine did not splice summary back into window")
	}
}

func assertPrometheusMetricsObserved(t *testing.T, s wiringScenario) {
	t.Helper()
	families, err := s.metricsReg.Gather()
	if err != nil {
		t.Fatalf("gather metrics: %v", err)
	}
	if !metricSeen(families, "flowstate_compression_tokens_saved_total", s.manifest.ID) {
		t.Errorf("flowstate_compression_tokens_saved_total was not observed with agent_id=%q; compression recorder is not wired through to the shared registry", s.manifest.ID)
	}
	if !metricSeen(families, "flowstate_context_window_tokens", s.manifest.ID) {
		t.Errorf("flowstate_context_window_tokens was not observed with agent_id=%q; WindowBuilder recorder is not wired", s.manifest.ID)
	}
}

func assertContextCompactedEventPublished(t *testing.T, s wiringScenario) {
	t.Helper()
	if got := s.compactedEvents.Load(); got < 1 {
		t.Errorf("EventContextCompacted publications = %d; want >= 1", got)
	}
}

func assertSessionMemoryStoreConstructed(t *testing.T, s wiringScenario) {
	t.Helper()
	// The store creates its directory lazily on first Save, so the
	// wiring test asserts construction only; end-to-end persistence is
	// covered by internal/recall/session_memory_test.go.
	if s.compression.sessionMemoryStore == nil {
		t.Error("sessionMemoryStore is nil; wiring did not construct the store")
	}
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

// TestCompressionWiring_Disabled_YieldsZeroValueBundle locks in the
// opt-in contract: with cfg.Compression left at defaults (every
// Enabled=false) buildCompressionComponents must produce a bundle where
// AutoCompactor and SessionMemoryStore are nil. This prevents a
// regression where a caller flips defaults and silently activates
// compression on every deployment.
func TestCompressionWiring_Disabled_YieldsZeroValueBundle(t *testing.T) {
	t.Parallel()

	cfg := &config.AppConfig{
		Compression: ctxstore.DefaultCompressionConfig(),
	}

	metricsReg := prometheus.NewRegistry()
	recorder := tracer.NewPrometheusRecorder(metricsReg)
	fake := &compressionFakeProvider{summaryJSON: "{}"}

	compression := buildCompressionComponents(cfg, nil, fake, recorder)

	if compression.autoCompactor != nil {
		t.Error("autoCompactor is non-nil when AutoCompaction.Enabled=false")
	}
	if compression.sessionMemoryStore != nil {
		t.Error("sessionMemoryStore is non-nil when SessionMemory.Enabled=false")
	}
	if compression.metrics == nil {
		t.Error("metrics is nil; must always be populated so the engine hot path can increment without nil checks")
	}
	if compression.recorder != recorder {
		t.Error("recorder was not threaded through when compression is disabled")
	}
	if compression.summariserAdapter != nil {
		t.Error("summariserAdapter is non-nil when AutoCompaction.Enabled=false")
	}
}
