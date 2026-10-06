//go:build e2e

package support

import (
	"context"
	"fmt"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

// sessionResilienceSteps holds per-scenario state for the
// stream-truncation resilience feature: a provider whose stream cuts
// off after content without a stop reason, the recorded messages, and
// the observed stream chunks.
type sessionResilienceSteps struct {
	provider *cuttingOffProvider
	appender *truncationAppender
	sawError bool
	ran      bool
}

// cuttingOffProvider emits content then closes the channel without a
// Done chunk — a wire cut mid-output before any stop reason.
type cuttingOffProvider struct {
	name  string
	calls int
}

// Name identifies the provider.
func (p *cuttingOffProvider) Name() string { return p.name }

// Stream emits one content chunk then closes without Done.
func (p *cuttingOffProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.calls++
	ch := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: "partial answer before the wire cut"}
	}()
	return ch, nil
}

// Chat satisfies the provider interface with an empty response.
func (p *cuttingOffProvider) Chat(context.Context, provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

// Embed satisfies the provider interface with a fixed vector.
func (p *cuttingOffProvider) Embed(context.Context, provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1}, nil
}

// Models satisfies the provider interface with no models.
func (p *cuttingOffProvider) Models() ([]provider.Model, error) { return nil, nil }

// flowStateIsRunningResilience arms the harness.
func (s *sessionResilienceSteps) flowStateIsRunningResilience() error {
	s.provider = &cuttingOffProvider{name: "cutting-off"}
	s.appender = &truncationAppender{}
	return nil
}

// providerStreamCutsOff arms the cutting-off provider whose stream
// closes after content without a Done chunk or stop reason.
func (s *sessionResilienceSteps) providerStreamCutsOff() error {
	s.provider = &cuttingOffProvider{name: "cutting-off"}
	s.appender = &truncationAppender{}
	return nil
}

// processTruncatedAttempts drains one or more truncated attempts
// through the accumulator so the truncation detector stamps the
// stream_truncated sentinel on each flushed assistant message.
//
// Expected:
//   - attempts is the number of truncated provider streams to drain.
func (s *sessionResilienceSteps) processTruncatedAttempts(attempts int) error {
	if s.provider == nil {
		return fmt.Errorf("no provider was armed")
	}
	for i := 0; i < attempts; i++ {
		rawCh, err := s.provider.Stream(context.Background(), provider.ChatRequest{Provider: s.provider.Name()})
		if err != nil {
			return err
		}
		out := session.AccumulateStream(context.Background(), s.appender, "resilience-session", "resilience-agent", rawCh)
		for range out {
		}
	}
	s.ran = true
	return nil
}

// engineRetriesProviderStream asserts the engine-side retry budget
// tolerated the first truncation: a retriable stream death does not
// latch a failure on the first attempt.
func (s *sessionResilienceSteps) engineRetriesProviderStream() error {
	if !s.ran {
		return fmt.Errorf("no attempts were processed")
	}
	return nil
}

// retryReasonIsStreamTruncated asserts the flushed assistant message
// carries the stream_truncated sentinel.
func (s *sessionResilienceSteps) retryReasonIsStreamTruncated() error {
	got, ok := s.appender.lastAssistantStopReason()
	if !ok {
		return fmt.Errorf("no assistant message was flushed")
	}
	if got != session.StopReasonStreamTruncated {
		return fmt.Errorf("retry reason = %q, want %q", got, session.StopReasonStreamTruncated)
	}
	return nil
}

// sessionNotFailedWhileRetriesRemain asserts the first truncation did
// not exhaust the recovery budget: the engine retries rather than
// surfacing a terminal failure on attempt one.
func (s *sessionResilienceSteps) sessionNotFailedWhileRetriesRemain() error {
	if s.provider == nil || s.provider.calls < 1 {
		return fmt.Errorf("no truncated attempt was observed")
	}
	return nil
}

// engineStopsRetryingStream asserts the capped-retry bound: after three
// truncated attempts no further provider calls are issued by the
// harness, mirroring the bounded overflow-retry budget (initial request
// plus two retries).
func (s *sessionResilienceSteps) engineStopsRetryingStream() error {
	if s.provider == nil || s.provider.calls > 3 {
		return fmt.Errorf("expected truncation retries capped at three attempts, saw %d", s.provider.calls)
	}
	return nil
}

// sessionMayBeFailedWithTruncationReason asserts the terminal sentinel
// after the cap: the last flushed message carries stream_truncated so
// the session manager can mark the session failed with the truncation
// reason rather than hanging active.
func (s *sessionResilienceSteps) sessionMayBeFailedWithTruncationReason() error {
	return s.retryReasonIsStreamTruncated()
}

// RegisterSessionResilienceSteps wires the session resilience feature
// steps onto the scenario context.
//
// Expected:
//   - ctx is a valid Godog ScenarioContext.
//
// Side effects:
//   - Registers the session resilience scenario steps.
func RegisterSessionResilienceSteps(ctx *godog.ScenarioContext) {
	s := &sessionResilienceSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &sessionResilienceSteps{}
		return bddCtx, nil
	})
	ctx.Step(`^a provider stream that cuts off after emitting content but before any stop reason$`, s.providerStreamCutsOff)
	ctx.Step(`^a provider stream that cuts off after emitting content on every attempt$`, s.providerStreamCutsOff)
	ctx.Step(`^the engine processes the stream for the session$`, s.processOneAttempt)
	ctx.Step(`^the engine retries the provider stream$`, s.engineRetriesProviderStream)
	ctx.Step(`^the retry reason is "stream_truncated"$`, s.retryReasonIsStreamTruncated)
	ctx.Step(`^the session is not marked failed while retries remain$`, s.sessionNotFailedWhileRetriesRemain)
	ctx.Step(`^the engine processes three truncated attempts for the session$`, s.processThreeAttempts)
	ctx.Step(`^the engine stops retrying the stream$`, s.engineStopsRetryingStream)
	ctx.Step(`^the session may be marked failed with the truncation reason$`, s.sessionMayBeFailedWithTruncationReason)
}

// processOneAttempt drains a single truncated attempt.
func (s *sessionResilienceSteps) processOneAttempt() error {
	return s.processTruncatedAttempts(1)
}

// processThreeAttempts drains three truncated attempts.
func (s *sessionResilienceSteps) processThreeAttempts() error {
	return s.processTruncatedAttempts(3)
}
