//go:build e2e

package support

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

// coordinationWriteChainID is the deterministic delegation chain the
// coordination-write scenarios dispatch under, supplied via the delegate
// handoff so the fixture and the engine agree on the coordination
// namespace the post-completion judgement inspects.
const coordinationWriteChainID = "chain-bdd-coordination-writes"

// coordinationWriteChildOutput is the substantive child response the
// scripted provider streams; scenarios assert it surfaces in success
// tool results.
const coordinationWriteChildOutput = "member output: analysis complete"

// coordinationWriteProvider is a scripted child-provider whose stream
// performs the coordination_store write before emitting its response,
// modelling a child agent that persists (or skips) its coordination
// deliverable mid-run.
type coordinationWriteProvider struct {
	store    coordination.Store
	writeKey bool
}

// Name returns the provider name.
//
// Returns:
//   - The string "mock-coordination-write".
//
// Side effects:
//   - None.
//
// Expected: parameters for Name.
func (p *coordinationWriteProvider) Name() string { return "mock-coordination-write" }

// Stream persists the scenario's coordination key before emitting the
// scripted substantive response.
//
// Expected:
//   - ctx is a valid context.
//   - req is the chat request the child engine dispatched.
//
// Returns:
//   - A channel carrying the substantive response then Done.
//   - An error when the scripted coordination write fails.
//
// Side effects:
//   - Writes <chainID>/member-findings to the coordination store when the
//     scenario models a compliant child.
func (p *coordinationWriteProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.writeKey {
		if err := p.store.Set(coordinationWriteChainID+"/member-findings", []byte("verified findings")); err != nil {
			return nil, err
		}
	}
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		defer close(ch)
		ch <- provider.StreamChunk{Content: coordinationWriteChildOutput}
		ch <- provider.StreamChunk{Done: true}
	}()
	return ch, nil
}

// Chat returns a mock assistant response carrying the scripted child
// output.
//
// Expected:
//   - ctx is a valid context.
//   - req is a chat request.
//
// Returns:
//   - A ChatResponse with assistant content.
//   - nil error always.
//
// Side effects:
//   - None.
func (p *coordinationWriteProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{
		Message: provider.Message{Role: "assistant", Content: coordinationWriteChildOutput},
	}, nil
}

// Embed returns a nil embedding slice.
//
// Expected:
//   - ctx is a valid context.
//   - req is an EmbedRequest.
//
// Returns:
//   - A nil slice and nil error always.
//
// Side effects:
//   - None.
func (p *coordinationWriteProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

// Models returns a single mock model entry.
//
// Returns:
//   - A slice containing one model entry.
//   - nil error always.
//
// Side effects:
//   - None.
//
// Expected: parameters for Models.
func (p *coordinationWriteProvider) Models() ([]provider.Model, error) {
	return []provider.Model{{ID: "mock-model", Provider: "mock-coordination-write", ContextLength: 8192}}, nil
}

// DelegationCoordinationSteps holds the coordination-write enforcement
// scenario state: the enforcement flag, the child's compliance, the
// captured slog output, and the collected delegate tool result.
type DelegationCoordinationSteps struct {
	enforcement bool
	childWrites bool
	logBuf      *bytes.Buffer
	origLogger  *slog.Logger
	collected   tool.Result
	collectErr  error
}

// RegisterDelegationCoordinationSteps registers the delegation
// coordination-write enforcement step definitions on the scenario
// context.
//
// Expected:
//   - ctx is the godog ScenarioContext receiving the steps.
//
// Side effects:
//   - Registers Before/After hooks and step definitions.
func RegisterDelegationCoordinationSteps(ctx *godog.ScenarioContext) {
	s := &DelegationCoordinationSteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.enforcement = false
		s.childWrites = false
		s.logBuf = &bytes.Buffer{}
		s.origLogger = slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(s.logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})))
		s.collected = tool.Result{}
		s.collectErr = nil
		return bddCtx, nil
	})
	ctx.After(func(bddCtx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		if s.origLogger != nil {
			slog.SetDefault(s.origLogger)
		}
		return bddCtx, nil
	})
	ctx.Step(`^delegation coordination-write enforcement is enabled$`, s.enforcementEnabled)
	ctx.Step(`^delegation coordination-write enforcement is disabled$`, s.enforcementDisabled)
	ctx.Step(`^a delegate child stream that completes substantive output without coordination writes$`, s.childCompletesWithoutWrites)
	ctx.Step(`^a delegate child stream that completes and writes a coordination key$`, s.childCompletesAndWrites)
	ctx.Step(`^the delegation result is collected$`, s.collectResult)
	ctx.Step(`^the parent receives a tool result carrying a coordination-write error$`, s.parentReceivesCoordinationWriteError)
	ctx.Step(`^the log contains "([^"]*)"$`, s.logContains)
	ctx.Step(`^the delegation reports success with the child output$`, s.successWithChildOutput)
}

// enforcementEnabled arms the delegate wiring for coordination-write
// enforcement.
//
// Returns:
//   - nil always.
//
// Side effects:
//   - Records the enforcement intent for the collect step.
func (s *DelegationCoordinationSteps) enforcementEnabled() error {
	s.enforcement = true
	return nil
}

// enforcementDisabled disarms coordination-write enforcement, modelling
// the operator escape hatch.
//
// Returns:
//   - nil always.
//
// Side effects:
//   - Records the enforcement intent for the collect step.
func (s *DelegationCoordinationSteps) enforcementDisabled() error {
	s.enforcement = false
	return nil
}

// childCompletesWithoutWrites arms a child that emits substantive output
// but performs no coordination_store write before completing.
//
// Returns:
//   - nil always.
//
// Side effects:
//   - Records the child's compliance for the collect step.
func (s *DelegationCoordinationSteps) childCompletesWithoutWrites() error {
	s.childWrites = false
	return nil
}

// childCompletesAndWrites arms a child that persists its coordination key
// before completing.
//
// Returns:
//   - nil always.
//
// Side effects:
//   - Records the child's compliance for the collect step.
func (s *DelegationCoordinationSteps) childCompletesAndWrites() error {
	s.childWrites = true
	return nil
}

// collectResult drives a synchronous delegate dispatch end-to-end the way
// the parent engine's tool loop does, capturing the tool result and any
// outer execution error.
//
// Returns:
//   - nil on dispatch completion; execution failures surface via the
//     captured result for the Then steps to judge.
//
// Side effects:
//   - Creates a parent session, child engine, and delegate tool.
//   - Swaps nothing; runs the delegate Execute call.
func (s *DelegationCoordinationSteps) collectResult() error {
	mgr := session.NewManager(nil)
	mgr.RegisterSession("coordination-parent", "coordinator")

	store := coordination.NewMemoryStore()
	child := engine.New(engine.Config{
		ChatProvider: &coordinationWriteProvider{store: store, writeKey: s.childWrites},
		Manifest: agent.Manifest{
			ID:                "coordination-worker",
			Name:              "Coordination Worker",
			Instructions:      agent.Instructions{SystemPrompt: "You perform delegated work."},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})

	delegateTool := engine.NewDelegateToolWithBackground(
		map[string]*engine.Engine{"coordination-worker": child},
		agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"coordination-worker"}},
		"coordinator",
		nil,
		store,
	).WithSessionManager(mgr).
		WithRequireCoordinationWrites(s.enforcement)

	ctx := context.WithValue(context.Background(), session.IDKey{}, "coordination-parent")
	result, err := delegateTool.Execute(ctx, tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "coordination-worker",
			"message":       "Research the coordination seam",
			"handoff":       map[string]interface{}{"chain_id": coordinationWriteChainID},
		},
	})
	s.collected = result
	s.collectErr = err
	return nil
}

// parentReceivesCoordinationWriteError pins the fail-closed contract: the
// parent's delegate tool result carries the coordination-write error while
// the delegation itself does not fail the turn.
//
// Returns:
//   - nil when the result is an error-flagged tool result naming the
//     missing writes and the escape hatch.
//   - An error describing the first violated expectation otherwise.
//
// Side effects:
//   - None.
func (s *DelegationCoordinationSteps) parentReceivesCoordinationWriteError() error {
	if s.collectErr != nil {
		return fmt.Errorf("coordination-write enforcement must surface as a tool result, not a turn failure: %v", s.collectErr)
	}
	if !s.collected.IsError {
		return fmt.Errorf("expected the delegate tool result to be flagged as an error, got output: %s", s.collected.Output)
	}
	if s.collected.Error == nil {
		return fmt.Errorf("expected the delegate tool result to carry an error value")
	}
	if !strings.Contains(s.collected.Error.Error(), "without writing any coordination_store keys") {
		return fmt.Errorf("expected the coordination-write error to name the missing writes, got: %v", s.collected.Error)
	}
	if !strings.Contains(s.collected.Output, "delegation.require_coordination_writes: false") {
		return fmt.Errorf("expected the tool result output to name the escape hatch, got: %s", s.collected.Output)
	}
	return nil
}

// logContains asserts the captured slog output contains the quoted text.
//
// Expected:
//   - text is the substring the scenario expects in the log.
//
// Returns:
//   - nil when the text is present; a descriptive error otherwise.
//
// Side effects:
//   - None.
func (s *DelegationCoordinationSteps) logContains(text string) error {
	if !strings.Contains(s.logBuf.String(), text) {
		return fmt.Errorf("expected the log to contain %q, got:\n%s", text, s.logBuf.String())
	}
	return nil
}

// successWithChildOutput pins the pass-through contract: a delegate that
// satisfies its coordination duties (or runs with enforcement disabled)
// reports success carrying the child output.
//
// Returns:
//   - nil when the result is a success carrying the child output.
//   - A descriptive error otherwise.
//
// Side effects:
//   - None.
func (s *DelegationCoordinationSteps) successWithChildOutput() error {
	if s.collectErr != nil {
		return fmt.Errorf("expected delegation success, got error: %v", s.collectErr)
	}
	if s.collected.IsError {
		return fmt.Errorf("expected a successful tool result, got an error result: %s", s.collected.Output)
	}
	if !strings.Contains(s.collected.Output, coordinationWriteChildOutput) {
		return fmt.Errorf("expected the child output in the tool result, got: %s", s.collected.Output)
	}
	return nil
}
