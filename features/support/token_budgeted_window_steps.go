//go:build e2e

package support

import (
	"context"
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
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// tokenBudgetState holds scenario state for the token-budgeted window
// scenarios. The engine, store, summariser, and scripted provider are
// wired by the first Given step; later steps stream turns against the
// same engine and assert on the requests the provider actually received.
type tokenBudgetState struct {
	eng        *engine.Engine
	store      *recall.FileContextStore
	summariser *countingE2ESummariser
	provider   *tokenBudgetStubProvider
	sessID     string
	tempDir    string
}

// tokenBudgetStubProvider is a provider.Provider double that records
// every ChatRequest it receives. The first stream emits a single echo
// tool call when toolFirst is set (driving the mid-tool-loop
// continuation path); every other stream emits one terminal chunk.
type tokenBudgetStubProvider struct {
	mu        sync.Mutex
	reqs      []provider.ChatRequest
	toolFirst bool
	tooled    bool
}

// Name returns a stable identifier.
//
// Returns: A stable literal string.
func (p *tokenBudgetStubProvider) Name() string { return "token-budget-stub" }

// Stream records the inbound request and emits either the scripted tool
// call turn or a terminal chunk.
//
// Returns: A channel that emits the scripted chunks and closes.
func (p *tokenBudgetStubProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	p.reqs = append(p.reqs, req)
	emitTool := p.toolFirst && !p.tooled
	p.tooled = true
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, 2)
	if emitTool {
		ch <- provider.StreamChunk{ToolCall: &provider.ToolCall{ID: "call-1", Name: "echo"}}
		ch <- provider.StreamChunk{Done: true}
	} else {
		ch <- provider.StreamChunk{Content: "ok", Done: true}
	}
	close(ch)
	return ch, nil
}

// Chat returns an empty response.
//
// Returns: A zero-value ChatResponse.
func (p *tokenBudgetStubProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed returns zeros. Required by the interface.
//
// Returns: A nil slice and nil error.
func (p *tokenBudgetStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models returns an empty list. Required by the interface.
//
// Returns: A nil slice and nil error.
func (p *tokenBudgetStubProvider) Models() ([]provider.Model, error) { return nil, nil }

// lastReqs returns a copy of every captured request in call order.
//
// Returns: The captured requests; nil-safe (empty slice when unused).
func (p *tokenBudgetStubProvider) lastReqs() []provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]provider.ChatRequest, len(p.reqs))
	copy(out, p.reqs)
	return out
}

// wordyEchoTool is a tool.Tool stub whose result carries 200 words so
// the post-batch live slice grows by a predictable 200 tokens under
// wordE2ECounter, pushing the mid-loop estimate over the gate-proximity
// boundary while the initial request stays under it.
type wordyEchoTool struct{}

// Name returns the tool identifier the scripted provider calls.
//
// Returns: A stable literal string.
func (t *wordyEchoTool) Name() string { return "echo" }

// Description returns a single-word description.
//
// Returns: A stable literal string.
func (t *wordyEchoTool) Description() string { return "d" }

// Execute returns a 200-word result.
//
// Returns: A tool.Result carrying 200 words of output.
func (t *wordyEchoTool) Execute(_ context.Context, _ tool.Input) (tool.Result, error) {
	words := make([]string, 200)
	for i := range words {
		words[i] = "w"
	}
	return tool.Result{Output: strings.Join(words, " ")}, nil
}

// Schema returns an empty schema.
//
// Returns: A zero-value tool.Schema.
func (t *wordyEchoTool) Schema() tool.Schema { return tool.Schema{} }

// RegisterTokenBudgetedWindowSteps wires the Phase 5a scenarios that
// pin token-bounded window recovery: the overflow fallback must fit the
// usable*4/5 token target while keeping the newest message, the
// mid-loop compaction rebuild must respect the same bound, and the
// token-bounded rebuild must count the prepended system-prompt prefix
// so the prefixed window does not overshoot the target.
//
// Side effects: registers Given/When/Then steps and a Before/After hook
// pair that allocates a per-scenario temp directory and zeroes state.
func RegisterTokenBudgetedWindowSteps(ctx *godog.ScenarioContext) {
	state := &tokenBudgetState{sessID: "token-budget-session"}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		state.eng = nil
		state.store = nil
		state.summariser = nil
		state.provider = nil

		dir, err := os.MkdirTemp("", "token-budget-window-*")
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

	ctx.Step(`^a token-budget engine is wired with auto-compaction disabled and a (\d+)-token limit$`, func(limit int) error {
		return state.wireEngine(limit, false, 0.99, 1, nil)
	})

	ctx.Step(`^a token-budget engine is wired with auto-compaction enabled, a 0\.99 threshold and a (\d+)-token limit$`, func(limit int) error {
		return state.wireEngine(limit, true, 0.99, 1, nil)
	})

	ctx.Step(`^a token-budget engine is wired with the wordy echo tool, a 0\.99 threshold and a (\d+)-token limit$`, func(limit int) error {
		tools := []tool.Tool{&wordyEchoTool{}}
		if err := state.wireEngine(limit, true, 0.99, 1, tools); err != nil {
			return err
		}
		state.provider.toolFirst = true
		return nil
	})

	ctx.Step(`^a token-budget engine is wired with an (\d+)-word system prompt, a 0\.99 threshold and a (\d+)-token limit$`, func(systemWords, limit int) error {
		return state.wireEngine(limit, true, 0.99, systemWords, nil)
	})

	ctx.Step(`^(\d+) messages of (\d+) words each are seeded into the token-budget session store$`, func(msgCount, wordCount int) error {
		if state.store == nil {
			return fmt.Errorf("token-budget engine not wired by a prior Given step")
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

	ctx.Step(`^the token-budget session streams a turn that overflows the usable window$`, func() error {
		return state.streamTurn()
	})

	ctx.Step(`^the token-budget session streams a turn whose provider calls the echo tool$`, func() error {
		return state.streamTurn()
	})

	ctx.Step(`^the surviving overflow-fallback request fits the (\d+)-token target$`, func(target int) error {
		return state.assertLastRequestFits(target)
	})

	ctx.Step(`^the surviving overflow-fallback request keeps the newest message$`, func() error {
		return state.assertLastRequestKeepsNewest()
	})

	ctx.Step(`^the rebuilt mid-loop continuation request fits the (\d+)-token target$`, func(target int) error {
		return state.assertLastRequestFits(target)
	})

	ctx.Step(`^the rebuilt mid-loop continuation request keeps the newest message$`, func() error {
		return state.assertLastRequestKeepsNewest()
	})

	ctx.Step(`^the compacted retry request fits the (\d+)-token target$`, func(target int) error {
		return state.assertLastRequestFits(target)
	})

	ctx.Step(`^the compacted retry request keeps the newest message$`, func() error {
		return state.assertLastRequestKeepsNewest()
	})
}

// wireEngine assembles the deterministic token-budget Engine: the
// wordE2ECounter (one token per word) with the scenario's model limit, a
// counting summariser returning a valid canned summary, an optional
// auto-compaction enablement/threshold pair, a system prompt of
// systemWords words, and the request-recording stub provider whose
// captured ChatRequests the Then steps inspect.
//
// Returns: An error when store construction or summary scripting fails.
//
// Side effects: sets s.eng, s.store, s.summariser, s.provider.
func (s *tokenBudgetState) wireEngine(limit int, enabled bool, threshold float64, systemWords int, tools []tool.Tool) error {
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
	s.provider = &tokenBudgetStubProvider{}

	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = enabled
	cfg.AutoCompaction.Threshold = threshold

	cm := agent.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 200

	promptWords := make([]string, systemWords)
	for i := range promptWords {
		promptWords[i] = "s"
	}

	manifest := agent.Manifest{
		ID:                "token-budget-agent",
		Instructions:      agent.Instructions{SystemPrompt: strings.Join(promptWords, " ")},
		ContextManagement: cm,
	}
	if len(tools) > 0 {
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			names = append(names, t.Name())
		}
		manifest.Capabilities = agent.Capabilities{Tools: names}
	}

	s.eng = engine.New(engine.Config{
		ChatProvider:      s.provider,
		Manifest:          manifest,
		Store:             s.store,
		TokenCounter:      wordE2ECounter{limit: limit},
		AutoCompactor:     flowctx.NewAutoCompactor(s.summariser),
		CompressionConfig: cfg,
		Tools:             tools,
	})
	return nil
}

// streamTurn drives one full Stream turn against the wired engine with
// the scenario's session ID bound into the context, mirroring the
// session manager's IDKey stamp, then drains the chunk channel.
//
// Returns: An error when the stream call itself fails.
func (s *tokenBudgetState) streamTurn() error {
	if s.eng == nil {
		return fmt.Errorf("token-budget engine not wired by a prior Given step")
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

// assertLastRequestFits estimates the last captured provider request
// with the same per-word counter the engine wiring uses (message
// content, tool-call names and string arguments, plus the 32-token
// per-tool schema overhead) and asserts the figure sits at or under the
// scenario's target.
//
// Returns: An error when no request was captured or the estimate
// exceeds target.
func (s *tokenBudgetState) assertLastRequestFits(target int) error {
	reqs := s.provider.lastReqs()
	if len(reqs) == 0 {
		return fmt.Errorf("provider received no request; the recovery path never dispatched")
	}
	req := reqs[len(reqs)-1]
	counter := wordE2ECounter{}
	estimated := 0
	for _, m := range req.Messages {
		estimated += counter.Count(m.Content)
		for _, tc := range m.ToolCalls {
			estimated += counter.Count(tc.Name)
			for k, v := range tc.Arguments {
				estimated += counter.Count(k)
				if str, ok := v.(string); ok {
					estimated += counter.Count(str)
				}
			}
		}
	}
	for _, t := range req.Tools {
		estimated += counter.Count(t.Name) + counter.Count(t.Description) + 32
	}
	if estimated > target {
		return fmt.Errorf(
			"recovered request estimate %d exceeds the %d-token target; the truncation path must bound the surviving window by usable*4/5",
			estimated, target,
		)
	}
	return nil
}

// assertLastRequestKeepsNewest asserts the last captured provider
// request still carries the turn's newest message verbatim — the
// hot-tail floor that forbids dropping the newest message even when the
// bound cannot be met.
//
// Returns: An error when no request was captured or the newest message
// was dropped.
func (s *tokenBudgetState) assertLastRequestKeepsNewest() error {
	reqs := s.provider.lastReqs()
	if len(reqs) == 0 {
		return fmt.Errorf("provider received no request; the recovery path never dispatched")
	}
	msgs := reqs[len(reqs)-1].Messages
	if len(msgs) == 0 {
		return fmt.Errorf("recovered request carries no messages")
	}
	newest := msgs[len(msgs)-1]
	if newest.Role != "user" || newest.Content != "next user turn" {
		return fmt.Errorf(
			"newest message was dropped; got tail role=%q content=%q, want the verbatim user turn",
			newest.Role, newest.Content,
		)
	}
	return nil
}
