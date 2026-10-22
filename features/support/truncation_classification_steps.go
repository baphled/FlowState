//go:build e2e

package support

import (
	"context"
	"fmt"
	"sync"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// truncationSteps holds per-scenario state for the truncation-aware
// stop-reason classification feature: a scripted provider emitting a
// chosen upstream stop_reason, plus the messages recorded by the
// in-memory appender the accumulator flushed into.
type truncationSteps struct {
	provider *truncationScriptedProvider
	appender *truncationAppender
	ran      bool
}

// truncationScriptedProvider emits content plus a Done chunk carrying
// the scripted upstream stop reason. The provider name is deliberately
// NOT anthropic/openai so the tool_use finish is treated as
// untrustworthy, exercising the classification branch under test.
type truncationScriptedProvider struct {
	name       string
	stopReason string
	calls      int
}

// Name identifies the provider to the engine.
func (p *truncationScriptedProvider) Name() string { return p.name }

// Stream emits the scripted stop reason on the terminal chunk.
func (p *truncationScriptedProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.calls++
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "Working."}
		ch <- provider.StreamChunk{EventType: "stop_reason", StopReason: p.stopReason, ProviderID: p.name}
		ch <- provider.StreamChunk{Done: true, ProviderID: p.name}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *truncationScriptedProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *truncationScriptedProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies the provider interface with no models.
func (p *truncationScriptedProvider) Models() ([]provider.Model, error) { return nil, nil }

// truncationAppender captures every message the accumulator flushes so
// scenarios can assert the final stamped stop reason.
type truncationAppender struct {
	mu       sync.Mutex
	messages []session.Message
}

// AppendMessage records the flushed message.
func (a *truncationAppender) AppendMessage(_ string, m session.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.messages = append(a.messages, m)
}

// UpdateDelegation is a no-op for this harness.
func (a *truncationAppender) UpdateDelegation(_, _ string, _ func(*session.Message)) {}

// lastAssistantStopReason reports the most recently flushed assistant
// message's stop reason.
func (a *truncationAppender) lastAssistantStopReason() (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.messages) - 1; i >= 0; i-- {
		if a.messages[i].Role == "assistant" {
			return a.messages[i].StopReason, true
		}
	}
	return "", false
}

// turnOnUntrustedProviderWithStopReason arms the scripted provider.
//
// Expected:
//   - reason is the upstream stop_reason to emit.
func (s *truncationSteps) turnOnUntrustedProviderWithStopReason(reason string) error {
	s.provider = &truncationScriptedProvider{name: "zai-untrusted", stopReason: reason}
	return nil
}

// turnEmittedNoToolCallOrDelegation asserts the scripted shape holds.
func (s *truncationSteps) turnEmittedNoToolCallOrDelegation() error {
	if s.provider == nil {
		return fmt.Errorf("no provider was armed")
	}
	return nil
}

// assistantMessageFlushed streams one turn through the accumulator.
func (s *truncationSteps) assistantMessageFlushed() error {
	if s.provider == nil {
		return fmt.Errorf("no provider was armed")
	}
	s.appender = &truncationAppender{}
	rawCh, err := s.provider.Stream(context.Background(), provider.ChatRequest{Provider: s.provider.Name()})
	if err != nil {
		return err
	}
	for range session.AccumulateStream(context.Background(), s.appender, "truncation-session", "truncation-agent", rawCh) {
	}
	s.ran = true
	return nil
}

// messageStopReasonIs asserts the flushed stop reason.
//
// Expected:
//   - want is the expected stamped stop reason.
//
// Returns:
//   - An error when no message was flushed or the reason differs.
func (s *truncationSteps) messageStopReasonIs(want string) error {
	if !s.ran || s.appender == nil {
		return fmt.Errorf("no assistant message was flushed")
	}
	got, ok := s.appender.lastAssistantStopReason()
	if !ok {
		return fmt.Errorf("no assistant message was recorded")
	}
	if got != want {
		return fmt.Errorf("stop reason = %q, want %q", got, want)
	}
	return nil
}

// RegisterTruncationClassificationSteps wires the truncation
// classification feature steps onto the scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers the truncation classification scenario steps.
func RegisterTruncationClassificationSteps(ctx *godog.ScenarioContext) {
	s := &truncationSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &truncationSteps{}
		return bddCtx, nil
	})
	ctx.Step(`^a turn on an untrusted provider that ended with upstream stop_reason "([^"]*)"$`, s.turnOnUntrustedProviderWithStopReason)
	ctx.Step(`^the turn emitted no tool call and no delegation$`, s.turnEmittedNoToolCallOrDelegation)
	ctx.Step(`^the assistant message is flushed$`, s.assistantMessageFlushed)
	ctx.Step(`^the message stop reason is "([^"]*)"$`, s.messageStopReasonIs)
}
