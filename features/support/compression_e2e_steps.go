package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// compressionE2EState holds scenario state for the @e2e scenarios that
// drive the full L1+L2+L3 pipeline end-to-end. State is reset in the
// Before hook so scenarios never share data.
type compressionE2EState struct {
	tempDir  string
	memDir   string
	ctxDir   string
	sessID   string
	seedFact string

	capturedReq *provider.ChatRequest
}

// RegisterCompressionE2ESteps wires the plan T20 @e2e scenarios that
// close the remaining deviations: cross-session recall through the
// engine Stream path and the quantitative ≥40% reduction benchmark.
//
// Expected:
//   - ctx is a non-nil godog.ScenarioContext.
//
// Returns:
//   - None.
//
// Side effects:
//   - Registers Given/When/Then steps and a Before/After hook pair that
//     allocates a per-scenario temp directory and zeroes captured state.
func RegisterCompressionE2ESteps(ctx *godog.ScenarioContext) {
	state := &compressionE2EState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.capturedReq = nil
		state.seedFact = ""
		state.sessID = "e2e-session"

		dir, err := os.MkdirTemp("", "compression-e2e-*")
		if err != nil {
			return c, fmt.Errorf("temp dir: %w", err)
		}
		state.tempDir = dir
		state.memDir = filepath.Join(dir, "session-memory")
		state.ctxDir = filepath.Join(dir, "recall")
		if mkErr := os.MkdirAll(state.memDir, 0o700); mkErr != nil {
			return c, fmt.Errorf("mkdir memdir: %w", mkErr)
		}
		if mkErr := os.MkdirAll(state.ctxDir, 0o700); mkErr != nil {
			return c, fmt.Errorf("mkdir ctxdir: %w", mkErr)
		}
		return c, nil
	})

	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if state.tempDir != "" {
			_ = os.RemoveAll(state.tempDir)
			state.tempDir = ""
		}
		return c, nil
	})

	ctx.Step(`^session A persisted a knowledge entry "([^"]*)" to the session memory store$`, func(content string) error {
		state.seedFact = content
		writer := recall.NewSessionMemoryStore(state.memDir)
		writer.AddEntry(recall.KnowledgeEntry{
			ID:        "e2e-fact-1",
			Type:      "fact",
			Content:   content,
			Relevance: 0.9,
		})
		if err := writer.Save(state.sessID); err != nil {
			return fmt.Errorf("save session A memory: %w", err)
		}
		return nil
	})

	ctx.Step(`^a fresh engine loads the same session memory store and streams one user turn$`, func() error {
		if state.seedFact == "" {
			return errors.New("prior Given did not seed a fact")
		}

		reloaded := recall.NewSessionMemoryStore(state.memDir)
		if err := reloaded.Load(state.sessID); err != nil {
			return fmt.Errorf("reload in session B: %w", err)
		}
		if len(reloaded.Entries()) == 0 {
			return errors.New("reloaded store is empty; seeding did not persist")
		}

		capturing := &capturingStubProvider{}
		store, err := recall.NewFileContextStore(filepath.Join(state.ctxDir, "ctx.json"), "test-model")
		if err != nil {
			return fmt.Errorf("recall store: %w", err)
		}

		cfg := flowctx.DefaultCompressionConfig()
		cfg.SessionMemory.Enabled = true

		eng := engine.New(engine.Config{
			ChatProvider: capturing,
			Manifest: agent.Manifest{
				ID:                "e2e-agent",
				Instructions:      agent.Instructions{SystemPrompt: "sys"},
				ContextManagement: agent.DefaultContextManagement(),
			},
			Store:              store,
			TokenCounter:       flowctx.NewTiktokenCounter(),
			CompressionConfig:  cfg,
			SessionMemoryStore: reloaded,
		})

		chunks, streamErr := eng.Stream(context.Background(), "e2e-agent", "continue")
		if streamErr != nil {
			return fmt.Errorf("stream: %w", streamErr)
		}
		for range chunks {
			// drain
		}

		req := capturing.last()
		if req == nil {
			return errors.New("provider stream was never invoked")
		}
		state.capturedReq = req
		return nil
	})

	ctx.Step(`^the provider request contains a session memory block$`, func() error {
		if state.capturedReq == nil {
			return errors.New("no provider request captured")
		}
		for _, m := range state.capturedReq.Messages {
			if strings.HasPrefix(m.Content, "[session memory]:") {
				return nil
			}
		}
		return fmt.Errorf("no message with [session memory]: prefix; got %d messages", len(state.capturedReq.Messages))
	})

	ctx.Step(`^the session memory block mentions "([^"]*)"$`, func(want string) error {
		if state.capturedReq == nil {
			return errors.New("no provider request captured")
		}
		for _, m := range state.capturedReq.Messages {
			if !strings.HasPrefix(m.Content, "[session memory]:") {
				continue
			}
			if strings.Contains(m.Content, want) {
				return nil
			}
			return fmt.Errorf("session memory block does not mention %q; got %q", want, m.Content)
		}
		return errors.New("session memory block not found; cannot check contents")
	})
}

// capturingStubProvider is a provider.Provider double used by the
// @e2e cross-session recall scenario. It records the ChatRequest passed
// to Stream so the test can assert the assembled window shape.
type capturingStubProvider struct {
	mu      sync.Mutex
	lastReq provider.ChatRequest
	called  bool
}

// Name returns a stable identifier.
//
// Expected:
//   - None.
//
// Returns:
//   - A stable literal string.
//
// Side effects:
//   - None.
func (p *capturingStubProvider) Name() string { return "e2e-capturing" }

// Stream records the inbound request and returns a single-chunk
// closed channel so the engine's stream loop terminates cleanly.
//
// Expected:
//   - ctx is the streaming context.
//   - req is the ChatRequest assembled by the engine.
//
// Returns:
//   - A channel that emits one Done chunk and closes.
//   - A nil error.
//
// Side effects:
//   - Captures req for later inspection via last().
func (p *capturingStubProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	p.lastReq = req
	p.called = true
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Content: "", Done: true}
	close(ch)
	return ch, nil
}

// Chat records the request and returns an empty response.
//
// Expected:
//   - ctx is the chat context.
//   - req is the ChatRequest.
//
// Returns:
//   - A zero-value ChatResponse.
//   - A nil error.
//
// Side effects:
//   - Captures req for later inspection via last().
func (p *capturingStubProvider) Chat(_ context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	p.mu.Lock()
	p.lastReq = req
	p.called = true
	p.mu.Unlock()
	return provider.ChatResponse{}, nil
}

// Embed returns zeros. Unused by the scenarios here but required by
// the interface.
//
// Expected:
//   - None.
//
// Returns:
//   - A nil slice and nil error.
//
// Side effects:
//   - None.
func (p *capturingStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models returns an empty list. Unused by the scenarios here but
// required by the interface.
//
// Expected:
//   - None.
//
// Returns:
//   - A nil slice and nil error.
//
// Side effects:
//   - None.
func (p *capturingStubProvider) Models() ([]provider.Model, error) { return nil, nil }

// last returns a pointer to the most recently captured ChatRequest, or
// nil if the provider was never invoked. Callers must not mutate the
// returned value.
//
// Expected:
//   - None.
//
// Returns:
//   - A pointer to the last captured ChatRequest, or nil.
//
// Side effects:
//   - None.
func (p *capturingStubProvider) last() *provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.called {
		return nil
	}
	req := p.lastReq
	return &req
}
