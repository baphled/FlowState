//go:build e2e

package support

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
)

// usageFeedbackState holds scenario state for the auto-compaction
// usage-feedback scenarios. The engine, store, and summariser are wired
// by the first Given step; later steps stream turns against the same
// engine so the gate observes the provider-reported input-token figure
// captured by the production chunk-processing path.
type usageFeedbackState struct {
	eng        *engine.Engine
	store      *recall.FileContextStore
	summariser *countingE2ESummariser
	provider   *usageReportingStubProvider
	sessID     string
	tempDir    string
}

// usageReportingStubProvider is a provider.Provider double whose stream
// emits one terminal chunk. When inputTokens > 0 the chunk carries a
// UsageDelta with that cumulative input-token figure — mirroring the
// Anthropic message_delta / openaicompat trailing-chunk usage shape the
// engine's chunk loop records per turn. When inputTokens == 0 the chunk
// carries no Usage pointer at all, matching providers that report no
// usage data.
type usageReportingStubProvider struct {
	inputTokens int64
}

// Name returns a stable identifier.
//
// Returns: A stable literal string.
func (p *usageReportingStubProvider) Name() string { return "usage-reporting-stub" }

// Stream emits a single terminal chunk, optionally carrying usage.
//
// Returns: A channel that emits one Done chunk and closes.
func (p *usageReportingStubProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 1)
	chunk := provider.StreamChunk{Content: "ok", Done: true}
	if p.inputTokens > 0 {
		chunk.Usage = &provider.UsageDelta{InputTokens: p.inputTokens, OutputTokens: 10}
	}
	ch <- chunk
	close(ch)
	return ch, nil
}

// Chat returns an empty response.
//
// Returns: A zero-value ChatResponse.
func (p *usageReportingStubProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed returns zeros. Required by the interface.
//
// Returns: A nil slice and nil error.
func (p *usageReportingStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models returns an empty list. Required by the interface.
//
// Returns: A nil slice and nil error.
func (p *usageReportingStubProvider) Models() ([]provider.Model, error) { return nil, nil }

// RegisterCompactionUsageFeedbackSteps wires the Phase 5b scenarios that
// pin the feedback-aware auto-compaction gate: the provider-reported
// input-token figure from the most recent turn must be able to fire the
// gate earlier than the pure estimate would, and sessions without a
// reported figure must keep the estimate-only trigger points.
//
// Side effects: registers Given/When/Then steps and a Before/After hook
// pair that allocates a per-scenario temp directory and zeroes state.
func RegisterCompactionUsageFeedbackSteps(ctx *godog.ScenarioContext) {
	state := &usageFeedbackState{sessID: "usage-feedback-session"}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.eng = nil
		state.store = nil
		state.summariser = nil
		state.provider = nil

		dir, err := os.MkdirTemp("", "compaction-usage-feedback-*")
		if err != nil {
			return c, fmt.Errorf("temp dir: %w", err)
		}
		state.tempDir = dir
		return c, nil
	})

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.tempDir != "" {
			_ = os.RemoveAll(state.tempDir)
			state.tempDir = ""
		}
		return c, nil
	})

	ctx.Step(`^a usage-feedback engine is wired with a 0\.50 auto-compaction threshold$`, func() error {
		return state.wireEngine()
	})

	ctx.Step(`^(\d+) messages of (\d+) words each are seeded into the usage-feedback session store$`, func(msgCount, wordCount int) error {
		if state.store == nil {
			return fmt.Errorf("usage-feedback engine not wired by a prior Given step")
		}
		words := make([]string, wordCount)
		for i := range words {
			words[i] = "w"
		}
		content := strings.Join(words, " ")
		for range msgCount {
			state.store.Append(provider.Message{Role: "assistant", Content: content})
		}
		return nil
	})

	ctx.Step(`^the usage-feedback session streams a turn whose provider reports (\d+) input tokens$`, func(reported int) error {
		if state.provider == nil {
			return fmt.Errorf("usage-feedback engine not wired by a prior Given step")
		}
		state.provider.inputTokens = int64(reported)
		return state.streamTurn()
	})

	ctx.Step(`^the usage-feedback session streams a turn whose provider reports no usage$`, func() error {
		if state.provider == nil {
			return fmt.Errorf("usage-feedback engine not wired by a prior Given step")
		}
		state.provider.inputTokens = 0
		return state.streamTurn()
	})

	ctx.Step(`^the usage-feedback session streams another user turn$`, func() error {
		if state.provider == nil {
			return fmt.Errorf("usage-feedback engine not wired by a prior Given step")
		}
		return state.streamTurn()
	})

	ctx.Step(`^auto-compaction fires on the provider-reported input-token figure$`, func() error {
		if state.summariser == nil {
			return fmt.Errorf("usage-feedback summariser not configured")
		}
		if state.summariser.calls.Load() < 1 {
			return fmt.Errorf(
				"summariser was not called; compaction did not fire — the gate must weigh "+
					"the provider-reported 6_000 input tokens (ratio 0.60 > 0.50) over the "+
					"~3_000-token estimate (ratio 0.30) from the seeded store (got calls = %d)",
				state.summariser.calls.Load(),
			)
		}
		return nil
	})

	ctx.Step(`^auto-compaction stays quiet because the estimate is below the threshold$`, func() error {
		if state.summariser == nil {
			return fmt.Errorf("usage-feedback summariser not configured")
		}
		if state.summariser.calls.Load() != 0 {
			return fmt.Errorf(
				"summariser was called %d times; with no reported figure the gate must stay "+
					"quiet at a ~3_000-token estimate (ratio 0.30) under the 0.50 threshold",
				state.summariser.calls.Load(),
			)
		}
		return nil
	})

	ctx.Step(`^auto-compaction fires at the estimate trigger point$`, func() error {
		if state.summariser == nil {
			return fmt.Errorf("usage-feedback summariser not configured")
		}
		if state.summariser.calls.Load() < 1 {
			return fmt.Errorf(
				"summariser was not called; compaction did not fire — with no reported figure "+
					"the gate must keep today's estimate-only trigger: 51 msgs × 100 tokens = "+
					"5_100 exceeds the 0.50 × 10_000 = 5_000 boundary while staying under the "+
					"gate-proximity boundary at 5_404 (got calls = %d)",
				state.summariser.calls.Load(),
			)
		}
		return nil
	})
}

// wireEngine assembles the deterministic usage-feedback Engine: the
// wordE2ECounter (one token per word, 10_000-token limit), a counting
// summariser returning a valid canned summary, a 0.50 auto-compaction
// threshold, and the usage-reporting stub provider whose per-turn Usage
// figure the production chunk loop records.
//
// Returns: An error when store construction or summary scripting fails.
//
// Side effects: sets s.eng, s.store, s.summariser, s.provider.
func (s *usageFeedbackState) wireEngine() error {
	resp, err := bddSummaryJSON(nil)
	if err != nil {
		return fmt.Errorf("bdd summary json: %w", err)
	}
	s.summariser = &countingE2ESummariser{resp: resp}

	store, err := recall.NewFileContextStore(filepath.Join(s.tempDir, "ctx.json"), "test-model")
	if err != nil {
		return fmt.Errorf("new file context store: %w", err)
	}
	s.store = store
	s.provider = &usageReportingStubProvider{}

	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.50

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 200

	s.eng = engine.New(engine.Config{
		ChatProvider: s.provider,
		Manifest: agent.Manifest{
			ID:                "usage-feedback-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		Store:             s.store,
		TokenCounter:      wordE2ECounter{limit: 10_000},
		AutoCompactor:     flowctx.NewAutoCompactor(s.summariser),
		CompressionConfig: cfg,
	})
	return nil
}

// streamTurn drives one full Stream turn against the wired engine with
// the scenario's session ID bound into the context, mirroring the
// session manager's IDKey stamp, then drains the chunk channel.
//
// Returns: An error when the stream call itself fails.
func (s *usageFeedbackState) streamTurn() error {
	if s.eng == nil {
		return fmt.Errorf("usage-feedback engine not wired by a prior Given step")
	}
	ctx := context.WithValue(context.Background(), session.IDKey{}, s.sessID)
	chunks, err := s.eng.Stream(ctx, "", "next user turn")
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}
	for range chunks {
	}
	return nil
}
