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

	agentpkg "github.com/baphled/flowstate/internal/agent"
	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// parityOverflowState carries per-scenario state for the parent/child
// engine-parity overflow scenarios: two identically configured engines
// (parent, child) sharing a small word-token limit, one swollen
// transcript seeded into both, and the observed outcomes of dispatching
// the parent directly versus dispatching the child through the delegate
// tool.
type parityOverflowState struct {
	parentEng  *engine.Engine
	childEng   *engine.Engine
	summariser *paritySummariser
	provider   *parityStubProvider
	delegate   *engine.DelegateTool
	sessionMgr *session.Manager
	childCtx   context.Context
	parentOK   bool
	childOK    bool
	childErr   string
	failAll    bool
}

// paritySummariser is a scripted Summariser double; failAll forces the
// naive truncation fallback so the retry can still genuinely overflow.
type paritySummariser struct {
	failAll bool
	calls   int
	mu      sync.Mutex
}

func (s *paritySummariser) Summarise(_ context.Context, _ string, _ string, _ []provider.Message) (string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if s.failAll {
		return "", fmt.Errorf("summariser chain failed")
	}
	return `{"intent":"continue","next_steps":["proceed"]}`, nil
}

// parityStubProvider accepts every gate-skipped dispatch. Overflow is
// injected by the engine's own pre-flight gate via the small token
// limit; refuseOverTokens models a provider that genuinely still
// overflows on the retry.
type parityStubProvider struct {
	refuseOverTokens int
}

func (p *parityStubProvider) Name() string { return "parity-stub" }

func (p *parityStubProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	est := 0
	for _, m := range req.Messages {
		est += len(strings.Fields(m.Content))
	}
	ch := make(chan provider.StreamChunk, 1)
	if p.refuseOverTokens > 0 && est > p.refuseOverTokens {
		ch <- provider.StreamChunk{Error: &provider.Error{ErrorType: provider.ErrorTypeContextWindowExceeded}, Done: true}
		close(ch)
		return ch, nil
	}
	ch <- provider.StreamChunk{Content: "recovered reply", Done: true}
	close(ch)
	return ch, nil
}

func (p *parityStubProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *parityStubProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *parityStubProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "parity-model", Provider: "parity-stub", ContextLength: 512}}, nil
}

func (s *parityOverflowState) buildEngine(id string) (*engine.Engine, error) {
	dir, err := os.MkdirTemp("", "parity-bdd-*")
	if err != nil {
		return nil, err
	}
	store, err := recall.NewFileContextStore(filepath.Join(dir, "ctx.json"), "parity-model")
	if err != nil {
		return nil, err
	}
	cfg := flowctx.DefaultCompressionConfig()
	cfg.AutoCompaction.Enabled = true
	cfg.AutoCompaction.Threshold = 0.01
	cm := agentpkg.DefaultContextManagement()
	cm.CompactionThreshold = 0
	cm.SlidingWindowSize = 500
	eng := engine.New(engine.Config{
		ChatProvider: s.provider,
		Manifest: agentpkg.Manifest{
			ID:                id,
			Instructions:      agentpkg.Instructions{SystemPrompt: "sys"},
			ContextManagement: cm,
		},
		TokenCounter:          wordE2ECounter{limit: 400},
		OutputReserveForTests: 64,
		AutoCompactor:         flowctx.NewAutoCompactor(s.summariser),
		Store:                 store,
		CompressionConfig:     cfg,
	})
	eng.SetModelPreference("parity-stub", "parity-model")
	return eng, nil
}

func (s *parityOverflowState) wire(failAll bool) error {
	s.failAll = failAll
	s.summariser = &paritySummariser{failAll: failAll}
	s.provider = &parityStubProvider{}
	if failAll {
		// The summariser fails, so the compaction falls back to the
		// naive truncation summary; the rebuilt child window can
		// still be small enough to clear the local usable-limit
		// gate (400 - 64 = 336 word-tokens). Model a provider whose
		// real limit is tighter than the local estimate so the
		// re-dispatched (gate-cleared) request genuinely still
		// overflows and the delegation must fail closed with
		// ErrCompactionInsufficient.
		s.provider.refuseOverTokens = 12
	}
	s.ensureSessionManager()
	var err error
	if s.parentEng, err = s.buildEngine("parity-parent"); err != nil {
		return err
	}
	if s.childEng, err = s.buildEngine("parity-child"); err != nil {
		return err
	}
	s.delegate = engine.NewDelegateTool(
		map[string]*engine.Engine{"parity-child": s.childEng},
		agentpkg.Delegation{CanDelegate: true},
		"parity-parent",
	).WithSessionManager(s.sessionMgr)
	return nil
}

// ensureSessionManager wires a session manager that knows the child
// session id, so a session_id resume targets the seeded "child-sess"
// transcript instead of NewDelegateTool minting a synthetic
// delegate session that never sees the swollen history.
func (s *parityOverflowState) ensureSessionManager() {
	s.sessionMgr = session.NewManager(nil)
	s.sessionMgr.RegisterSession("child-sess", "parity-child")
	s.sessionMgr.RegisterSession("parent-sess", "parity-parent")
}

func (s *parityOverflowState) seedSwollen() error {
	msgs := make([]provider.Message, 0, 60)
	for i := range 25 {
		msgs = append(msgs,
			provider.Message{Role: "user", Content: fmt.Sprintf("turn %d with a handful of words", i)},
			provider.Message{Role: "assistant", Content: fmt.Sprintf("reply %d with a handful of words back", i)},
		)
	}
	oversized := strings.Repeat("swollen tool transcript entry ", 60)
	msgs = append(msgs,
		provider.Message{Role: "user", Content: "final request"},
		provider.Message{Role: "assistant", Content: oversized},
	)
	s.parentEng.SeedHistory("parent-sess", msgs)
	s.childEng.SeedHistory("child-sess", msgs)
	for _, eng := range []*engine.Engine{s.parentEng, s.childEng} {
		req := &provider.ChatRequest{Provider: "parity-stub", Model: "parity-model", Messages: msgs}
		if eng.CheckContextWindowOverflowForTestingE2E(req) == nil {
			return fmt.Errorf("seeded transcript does not overflow the local gate for %s", eng.Manifest().ID)
		}
	}
	return nil
}

func swollenParityMessage() string {
	return strings.Repeat("child turn carrying a swollen tool-result payload data row with several more words of output ", 40)
}

func (s *parityOverflowState) dispatchParent() error {
	ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess")
	window := s.parentEng.BuildContextWindowForTesting(ctx, "parent-sess", swollenParityMessage())
	req := &provider.ChatRequest{Provider: "parity-stub", Model: "parity-model", Messages: window}
	if s.parentEng.CheckContextWindowOverflowForTestingE2E(req) == nil {
		return fmt.Errorf("parent window does not overflow the gate; seeding broken")
	}
	chunks, err := s.parentEng.Stream(ctx, "", swollenParityMessage())
	if err != nil {
		return err
	}
	for chunk := range chunks {
		if chunk.Error != nil {
			return fmt.Errorf("parent stream terminal error: %v", chunk.Error)
		}
		if chunk.Content == "recovered reply" {
			s.parentOK = true
		}
	}
	return nil
}

func (s *parityOverflowState) dispatchChild() error {
	ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess")
	s.childCtx = ctx
	input := tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "parity-child",
			"session_id":    "child-sess",
			"message":       swollenParityMessage(),
		},
	}
	res, err := s.delegate.Execute(ctx, input)
	if err != nil {
		s.childErr = err.Error()
		return nil
	}
	if strings.Contains(res.Output, "recovered reply") {
		s.childOK = true
	} else {
		s.childErr = res.Output
	}
	return nil
}

// RegisterParentChildOverflowParitySteps wires the parent/child
// engine-parity overflow scenarios to the production dispatch paths.
func RegisterParentChildOverflowParitySteps(ctx *godog.ScenarioContext) {
	state := &parityOverflowState{}

	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*state = parityOverflowState{}
		return c, nil
	})

	ctx.Step(`^a parent engine and an identical delegate child engine sharing a small context limit$`, func() error {
		return state.wire(false)
	})

	ctx.Step(`^a parent engine and a delegate child engine whose summariser always fails$`, func() error {
		return state.wire(true)
	})

	ctx.Step(`^both the parent transcript and the child transcript are swollen past the context gate$`, func() error {
		return state.seedSwollen()
	})

	ctx.Step(`^the parent stream is dispatched$`, func() error {
		return state.dispatchParent()
	})

	ctx.Step(`^the parent recovers via overflow compaction and completes$`, func() error {
		if !state.parentOK {
			return fmt.Errorf("parent did not recover via overflow compaction")
		}
		return nil
	})

	ctx.Step(`^the delegated child stream is dispatched through the delegate tool$`, func() error {
		return state.dispatchChild()
	})

	ctx.Step(`^the child also recovers via overflow compaction and the delegation completes$`, func() error {
		if !state.childOK {
			return fmt.Errorf("child did not recover; childErr=%q", state.childErr)
		}
		if got := state.childEng.OverflowRecoveryCompactionsForTestingE2E("child-sess"); got < 1 {
			return fmt.Errorf("child overflow-recovery compactions = %d, want >= 1", got)
		}
		return nil
	})

	ctx.Step(`^the child retry still overflows after compaction$`, func() error {
		if state.childOK {
			return fmt.Errorf("child unexpectedly completed despite failing summariser")
		}
		return nil
	})

	ctx.Step(`^the delegation fails with a terminal compaction-insufficient error$`, func() error {
		if state.childOK {
			return fmt.Errorf("delegation unexpectedly succeeded")
		}
		if !strings.Contains(state.childErr, "compaction insufficient") {
			return fmt.Errorf("child error = %q, want compaction-insufficient error", state.childErr)
		}
		return nil
	})
}
