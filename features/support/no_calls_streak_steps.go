//go:build e2e

// Package support provides BDD test step definitions and helpers.
package support

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

// noCallsStreakSteps holds per-scenario state for the tool_use_no_calls
// streak-failover feature: the scripted primary and fallback providers,
// their failover manager, and the streamed content.
type noCallsStreakSteps struct {
	primary  *noCallsScriptedProvider
	fallback *noCallsRecoveringProvider
	content  string
	ran      bool
	once     bool
}

// noCallsScriptedProvider answers every stream with stop_reason
// "tool_use" and zero tool calls, modelling the pathological provider
// from the 2026-09-30 Tool Budget Exhaustion RCA.
type noCallsScriptedProvider struct {
	name  string
	calls int
}

// Name identifies the provider to the engine.
func (p *noCallsScriptedProvider) Name() string { return p.name }

// Stream emits a tool_use stop reason with no tool call.
func (p *noCallsScriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.calls++
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "Let me check.", Done: true, StopReason: "tool_use"}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *noCallsScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *noCallsScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies the provider interface with no models.
func (p *noCallsScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// noCallsRecoveringProvider answers a clean end_turn so a failed-over
// turn completes naturally.
type noCallsRecoveringProvider struct {
	name  string
	calls int
}

// Name identifies the provider to the engine.
func (p *noCallsRecoveringProvider) Name() string { return p.name }

// Stream emits a terminal end_turn chunk.
func (p *noCallsRecoveringProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.calls++
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "Recovered.", Done: true, StopReason: "end_turn"}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *noCallsRecoveringProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *noCallsRecoveringProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies the provider interface with no models.
func (p *noCallsRecoveringProvider) Models() ([]provider.Model, error) { return nil, nil }

// noCallsOnceProvider emits one tool_use-without-calls round then a
// clean end_turn, modelling a tolerated single false positive.
type noCallsOnceProvider struct {
	name  string
	calls int
}

// Name identifies the provider to the engine.
func (p *noCallsOnceProvider) Name() string { return p.name }

// Stream emits the pathological round once, then recovers.
func (p *noCallsOnceProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.calls++
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		if p.calls == 1 {
			ch <- provider.StreamChunk{Content: "checking", Done: true, StopReason: "tool_use"}
			return
		}
		ch <- provider.StreamChunk{Content: "Fine.", Done: true, StopReason: "end_turn"}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *noCallsOnceProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *noCallsOnceProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies the provider interface with no models.
func (p *noCallsOnceProvider) Models() ([]provider.Model, error) { return nil, nil }

// noCallsFailoverManager wires a two-candidate failover chain whose head
// is the pathological primary and whose tail is the healthy fallback.
func noCallsFailoverManager(primary, fallback provider.Provider) *failover.Manager {
	registry := provider.NewRegistry()
	registry.Register(primary)
	registry.Register(fallback)
	fm := failover.NewManager(registry, failover.NewHealthManager(), time.Minute)
	fm.SetBasePreferences([]provider.ModelPreference{
		{Provider: primary.Name(), Model: "primary-model"},
		{Provider: fallback.Name(), Model: "fallback-model"},
	})
	return fm
}

// noCallsEngine builds an engine routing through the failover chain.
func noCallsEngine(primary, fallback provider.Provider, testTool tool.Tool) *engine.Engine {
	manifest := agent.Manifest{
		ID:                "nocalls-agent",
		Name:              "NoCalls Agent",
		Instructions:      agent.Instructions{SystemPrompt: "You are a helpful assistant."},
		ContextManagement: agent.DefaultContextManagement(),
		Capabilities:      agent.Capabilities{Tools: []string{"test_tool"}},
	}
	return engine.New(engine.Config{
		ChatProvider:    primary,
		FailoverManager: noCallsFailoverManager(primary, fallback),
		Registry: func() *provider.Registry {
			r := provider.NewRegistry()
			r.Register(primary)
			r.Register(fallback)
			return r
		}(),
		EventBus: eventbus.NewEventBus(),
		Manifest: manifest,
		Tools:    []tool.Tool{testTool},
	})
}

// primaryAnswersToolUseWithoutCalls arms the pathological primary and
// the recovering fallback.
func (s *noCallsStreakSteps) primaryAnswersToolUseWithoutCalls() error {
	s.primary = &noCallsScriptedProvider{name: "nocalls-primary"}
	s.fallback = &noCallsRecoveringProvider{name: "nocalls-fallback"}
	return nil
}

// primaryRecoversAfterOneRound arms the once-pathological primary and
// the fallback that must never be consulted.
func (s *noCallsStreakSteps) primaryRecoversAfterOneRound() error {
	s.once = true
	return nil
}

// turnStreamedThroughFailoverChain streams one turn through the chain.
func (s *noCallsStreakSteps) turnStreamedThroughFailoverChain() error {
	scratch := &invariantEchoTool{name: "test_tool", output: "ok"}
	primary := provider.Provider(s.primary)
	if s.once {
		primary = &noCallsOnceProvider{name: "nocalls-primary"}
	}
	eng := noCallsEngine(primary, s.fallback, scratch)
	ctx := context.Background()
	chunks, err := eng.Stream(ctx, "nocalls-session", "Hello")
	if err != nil {
		return err
	}
	for chunk := range chunks {
		s.content += chunk.Content
	}
	s.ran = true
	return nil
}

// fallbackCompletesTurn asserts the fallback produced the terminal text.
func (s *noCallsStreakSteps) fallbackCompletesTurn() error {
	if !s.ran {
		return fmt.Errorf("no turn was streamed")
	}
	if s.fallback == nil || s.fallback.calls < 1 {
		return fmt.Errorf("failover must route at least one request to the next candidate")
	}
	if !noCallsContains(s.content, "Recovered.") {
		return fmt.Errorf("the fallback provider must complete the turn, got %q", s.content)
	}
	return nil
}

// primaryNotRetriedBeyondThreshold asserts the primary was abandoned at
// the streak threshold instead of burning the iteration budget.
func (s *noCallsStreakSteps) primaryNotRetriedBeyondThreshold() error {
	if s.primary == nil {
		return fmt.Errorf("no primary was armed")
	}
	if s.primary.calls > 2 {
		return fmt.Errorf("primary retried %d times; streak threshold is 2", s.primary.calls)
	}
	return nil
}

// fallbackNeverCalled asserts a single tolerated round never left the
// primary.
func (s *noCallsStreakSteps) fallbackNeverCalled() error {
	if s.fallback == nil || s.fallback.calls != 0 {
		return fmt.Errorf("a single tool_use_no_calls must not trip failover, fallback called %d times", s.fallback.calls)
	}
	if !noCallsContains(s.content, "Fine.") {
		return fmt.Errorf("the same provider's retry must complete the turn, got %q", s.content)
	}
	return nil
}

// noCallsContains reports whether s contains sub.
func noCallsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// RegisterNoCallsStreakSteps wires the tool_use_no_calls streak
// failover feature steps onto the godog scenario context.
func RegisterNoCallsStreakSteps(ctx *godog.ScenarioContext) {
	s := &noCallsStreakSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &noCallsStreakSteps{}
		return bddCtx, nil
	})
	ctx.Step(`^a primary provider that answers tool_use with zero tool calls$`, s.primaryAnswersToolUseWithoutCalls)
	ctx.Step(`^a fallback provider that answers a clean end_turn$`, s.fallbackAnswersCleanEndTurn)
	ctx.Step(`^a primary provider whose first round emits tool_use without calls and whose second round succeeds$`, s.primaryRecoversAfterOneRound)
	ctx.Step(`^the turn is streamed through the failover chain$`, s.streamThroughChain)
	ctx.Step(`^the fallback provider completes the turn$`, s.fallbackCompletesTurn)
	ctx.Step(`^the primary provider is not retried beyond the streak threshold of two$`, s.primaryNotRetriedBeyondThreshold)
	ctx.Step(`^the fallback provider is never called$`, s.fallbackNeverCalled)
}

// fallbackAnswersCleanEndTurn arms the recovering fallback.
func (s *noCallsStreakSteps) fallbackAnswersCleanEndTurn() error {
	s.fallback = &noCallsRecoveringProvider{name: "nocalls-fallback"}
	return nil
}

// streamThroughChain streams the assembled chain scenario.
func (s *noCallsStreakSteps) streamThroughChain() error {
	return s.turnStreamedThroughFailoverChain()
}
