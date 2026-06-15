//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
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

	// benchmark scenario state
	benchMessages         []provider.Message
	benchUncompressedToks int
	benchCompressedToks   int

	// tool-schema compaction scenario state
	toolSchemaEng        *engine.Engine
	toolSchemaStore      *recall.FileContextStore
	toolSchemaSummariser *countingE2ESummariser
}

// countingE2ESummariser counts how many times Summarise is called and
// returns a canned valid compaction summary. Used by the tool-schema
// compaction BDD scenarios to verify that maybeAutoCompact fired.
type countingE2ESummariser struct {
	calls atomic.Int32
	resp  string
}

func (s *countingE2ESummariser) Summarise(_ context.Context, _, _ string, _ []provider.Message) (string, error) {
	s.calls.Add(1)
	return s.resp, nil
}

// wordE2ECounter is a deterministic one-token-per-word counter for the
// tool-schema BDD scenarios. It advertises a fixed model limit so the
// token-boundary arithmetic in the scenarios is predictable.
type wordE2ECounter struct{ limit int }

func (w wordE2ECounter) Count(text string) int {
	if text == "" {
		return 0
	}
	return len(strings.Fields(text))
}

func (w wordE2ECounter) ModelLimit(_ string) int { return w.limit }

// singleWordToolE2E is a tool.Tool stub whose Name and Description are
// single words. With wordE2ECounter (one token per word) each stub
// contributes Name(1) + Description(1) + overhead(32) = 34 tokens to
// estimateRequestTokens, making the per-tool contribution predictable.
type singleWordToolE2E struct{ n string }

func (t *singleWordToolE2E) Name() string        { return t.n }
func (t *singleWordToolE2E) Description() string { return "d" }
func (t *singleWordToolE2E) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	return tool.Result{}, nil
}
func (t *singleWordToolE2E) Schema() tool.Schema { return tool.Schema{} }

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
		state.benchMessages = nil
		state.benchUncompressedToks = 0
		state.benchCompressedToks = 0
		state.toolSchemaEng = nil
		state.toolSchemaStore = nil
		state.toolSchemaSummariser = nil

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

	ctx.Step(`^a transcript of (\d+) large assistant messages$`, func(n int) error {
		state.benchMessages = make([]provider.Message, 0, n)
		for i := range n {
			state.benchMessages = append(state.benchMessages, provider.Message{
				Role:    "assistant",
				Content: fmt.Sprintf("msg-%d %s", i, strings.Repeat("lorem ipsum dolor sit amet ", 40)),
			})
		}
		return nil
	})

	ctx.Step(`^the window is built with and without the L1 splitter$`, func() error {
		if len(state.benchMessages) == 0 {
			return errors.New("benchmark transcript not seeded")
		}
		counter := flowctx.NewTiktokenCounter()

		// Uncompressed baseline: sum every message as-is.
		for _, m := range state.benchMessages {
			state.benchUncompressedToks += counter.Count(m.Content)
		}

		// Compressed: drive the real splitter with the plan's default
		// threshold and a small hot tail to exercise the reduction path.
		spillDir := filepath.Join(state.tempDir, "bench-spill")
		if err := os.MkdirAll(spillDir, 0o700); err != nil {
			return fmt.Errorf("mkdir spill: %w", err)
		}
		compactor := flowctx.NewDefaultMessageCompactor(20)
		splitter := flowctx.NewHotColdSplitter(flowctx.HotColdSplitterOptions{
			Compactor:   compactor,
			HotTailSize: 2,
			StorageDir:  spillDir,
			SessionID:   "bench",
		})
		if splitter == nil {
			return errors.New("splitter construction failed")
		}
		splitter.StartPersistWorker(context.Background())
		defer splitter.Stop()

		result := splitter.Split(state.benchMessages)
		for _, m := range result.HotMessages {
			state.benchCompressedToks += counter.Count(m.Content)
		}
		return nil
	})

	ctx.Step(`^the compressed window tokens are at most (\d+) percent of the uncompressed window tokens$`, func(pct int) error {
		if state.benchUncompressedToks == 0 {
			return errors.New("uncompressed token count is zero; scenario setup failed")
		}
		if state.benchCompressedToks == 0 {
			return errors.New("compressed token count is zero; splitter produced nothing")
		}
		limit := (state.benchUncompressedToks * pct) / 100
		if state.benchCompressedToks > limit {
			return fmt.Errorf("compressed tokens = %d; uncompressed = %d; limit = %d (%d%%); reduction did not meet target",
				state.benchCompressedToks, state.benchUncompressedToks, limit, pct)
		}
		return nil
	})

	ctx.Step(`^an engine is wired with (\d+) single-word tools and a 0\.50 auto-compaction threshold$`, func(n int) error {
		return state.buildToolSchemaEngine(n, 0.50, 10_000)
	})

	ctx.Step(`^an engine is wired with (\d+) single-word tools and an inert auto-compaction threshold$`, func(n int) error {
		return state.buildToolSchemaEngine(n, 0.99, 100_000)
	})

	ctx.Step(`^(\d+) messages of (\d+) words each are seeded into the tool-schema session store$`, func(msgCount, wordCount int) error {
		if state.toolSchemaStore == nil {
			return errors.New("tool-schema engine not wired by a prior Given step")
		}
		words := make([]string, wordCount)
		for i := range words {
			words[i] = "w"
		}
		content := strings.Join(words, " ")
		for range msgCount {
			state.toolSchemaStore.Append(provider.Message{Role: "assistant", Content: content})
		}
		return nil
	})

	ctx.Step(`^the context window is built with the next user turn for the tool-schema engine$`, func() error {
		if state.toolSchemaEng == nil {
			return errors.New("tool-schema engine not wired by a prior Given step")
		}
		chunks, err := state.toolSchemaEng.Stream(context.Background(), "tool-schema-session", "next user turn")
		if err != nil {
			return fmt.Errorf("stream: %w", err)
		}
		for range chunks {
		}
		return nil
	})

	ctx.Step(`^auto-compaction fires because the tool schema tokens push the ratio above the threshold$`, func() error {
		if state.toolSchemaSummariser == nil {
			return errors.New("tool-schema summariser not configured")
		}
		if state.toolSchemaSummariser.calls.Load() < 1 {
			return fmt.Errorf(
				"summariser was not called; compaction did not fire — "+
					"tool schema tokens must be included in the full-window ratio "+
					"so 49 msgs×100 tokens + 3 tools×34 tokens = 5_002 exceeds "+
					"the 0.50×10_000 = 5_000 boundary (got calls = %d)",
				state.toolSchemaSummariser.calls.Load(),
			)
		}
		return nil
	})

	ctx.Step(`^auto-compaction fires because the tool schema tokens push the request above the gate-proximity boundary$`, func() error {
		if state.toolSchemaSummariser == nil {
			return errors.New("tool-schema summariser not configured")
		}
		if state.toolSchemaSummariser.calls.Load() < 1 {
			return fmt.Errorf(
				"summariser was not called; compaction did not fire — "+
					"tool schema tokens must be included in the gate-proximity estimate "+
					"so 90 msgs×1_000 tokens + 3 user tokens + 30 tools×34 tokens = 91_023 "+
					"exceeds the gate boundary at 90_904 (got calls = %d)",
				state.toolSchemaSummariser.calls.Load(),
			)
		}
		return nil
	})
}

// buildToolSchemaEngine wires a deterministic Engine for the tool-schema
// compaction scenarios. It uses wordE2ECounter (one token per word) with
// the given limit and installs toolCount single-word tools so the
// per-tool token contribution is predictable: each tool adds 34 tokens
// (1 name + 1 description + 32 overhead via estimateRequestTokens).
//
// Expected:
//   - toolCount is the number of single-word tool stubs to install.
//   - threshold is the auto-compaction ratio threshold.
//   - limit is the model context window size advertised by wordE2ECounter.
//
// Returns:
//   - An error when store or engine construction fails.
//
// Side effects:
//   - Sets s.toolSchemaEng, s.toolSchemaStore, s.toolSchemaSummariser.
func (s *compressionE2EState) buildToolSchemaEngine(toolCount int, threshold float64, limit int) error {
	resp, err := bddSummaryJSON(nil)
	if err != nil {
		return fmt.Errorf("bdd summary json: %w", err)
	}
	s.toolSchemaSummariser = &countingE2ESummariser{resp: resp}

	tools := make([]tool.Tool, toolCount)
	for i := range tools {
		tools[i] = &singleWordToolE2E{n: fmt.Sprintf("t%d", i)}
	}

	store, err := recall.NewFileContextStore(filepath.Join(s.tempDir, "ctx.json"), "test-model")
	if err != nil {
		return fmt.Errorf("new file context store: %w", err)
	}
	s.toolSchemaStore = store

	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = threshold

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 200

	s.toolSchemaEng = engine.New(engine.Config{
		ChatProvider: &capturingStubProvider{},
		Manifest: agent.Manifest{
			ID:                "tool-schema-agent",
			Instructions:      agent.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		Store:             s.toolSchemaStore,
		TokenCounter:      wordE2ECounter{limit: limit},
		AutoCompactor:     flowctx.NewAutoCompactor(s.toolSchemaSummariser),
		CompressionConfig: cfg,
		Tools:             tools,
	})
	return nil
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
