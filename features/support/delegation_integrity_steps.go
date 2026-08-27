//go:build e2e

package support

import (
	"context"
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// DelegationIntegritySteps holds state for the delegation completion
// integrity scenarios: a scripted child chunk channel, a parent
// context, and the collected (result, error) pair.
type DelegationIntegritySteps struct {
	chunks       chan provider.StreamChunk
	cancelWanted bool
	cancel       context.CancelFunc
	result       engine.DelegationResultForTest
	collectErr   error
}

// RegisterDelegationIntegritySteps registers the delegation
// completion integrity step definitions on the scenario context.
func RegisterDelegationIntegritySteps(ctx *godog.ScenarioContext) {
	s := &DelegationIntegritySteps{}
	ctx.Before(func(bddCtx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.chunks = nil
		s.cancelWanted = false
		s.cancel = nil
		s.result = engine.DelegationResultForTest{}
		s.collectErr = nil
		return bddCtx, nil
	})
	ctx.Step(`^a delegate child stream that produces substantive output and closes$`, s.substantiveChildStream)
	ctx.Step(`^a delegate child stream that produces no substantive output and closes$`, s.emptyChildStream)
	ctx.Step(`^the parent stream context is cancelled after the child output arrives$`, s.cancelParent)
	ctx.Step(`^the retry budget is exhausted$`, s.noOp)
	ctx.Step(`^the delegation result is collected$`, s.collectResult)
	ctx.Step(`^the delegation reports success with the child output$`, s.assertSuccessWithOutput)
	ctx.Step(`^the delegation error is nil$`, s.assertErrNil)
	ctx.Step(`^the delegation fails with an empty-response terminal error$`, s.assertEmptyTerminalError)
	ctx.Step(`^the delegation status is not reported as completed$`, s.assertNotCompleted)
}

// substantiveChildStream buffers a substantive child reply and closes
// the channel, modelling a delegate that completed and persisted work.
func (s *DelegationIntegritySteps) substantiveChildStream() error {
	s.chunks = make(chan provider.StreamChunk, 2)
	s.chunks <- provider.StreamChunk{Content: "member findings: verified the seams"}
	close(s.chunks)
	return nil
}

// emptyChildStream buffers only a Done chunk, modelling a delegate
// that finished without any substantive response.
func (s *DelegationIntegritySteps) emptyChildStream() error {
	s.chunks = make(chan provider.StreamChunk, 2)
	s.chunks <- provider.StreamChunk{Done: true}
	close(s.chunks)
	return nil
}

// cancelParent records that the parent stream context must already be
// cancelled when collection runs, reproducing the false-negative seam
// where a parent cancellation masks completed child work.
func (s *DelegationIntegritySteps) cancelParent() error {
	s.cancelWanted = true
	return nil
}

// noOp marks a precondition that the scenario setup already encodes,
// such as the retry budget being spent.
func (s *DelegationIntegritySteps) noOp() error {
	return nil
}

// collectResult drives collectWithProgress for the wrapped delegate
// under the scenario's parent context, exercising the same completion
// path production delegates use once their stream finishes.
func (s *DelegationIntegritySteps) collectResult() error {
	if s.chunks == nil {
		return fmt.Errorf("no child stream configured")
	}
	parent, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	parent = engine.WithStreamOutput(parent, make(chan provider.StreamChunk, 10))
	dt := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "coordinator")
	if s.cancelWanted {
		cancel()
	}
	res, err := engine.CollectDelegationCompletion(parent, dt, s.chunks, time.Now())
	s.result = res
	s.collectErr = err
	return nil
}

// assertSuccessWithOutput pins the fail-open policy: completed child
// work reports success with its output even under parent cancellation.
func (s *DelegationIntegritySteps) assertSuccessWithOutput() error {
	if s.collectErr != nil {
		return fmt.Errorf("expected success, got error: %v", s.collectErr)
	}
	if s.result.Response() == "" {
		return fmt.Errorf("expected child output in the collected result")
	}
	return nil
}

// assertErrNil pins that parent cancellation alone yields no error
// once the child stream has closed with output.
func (s *DelegationIntegritySteps) assertErrNil() error {
	if s.collectErr != nil {
		return fmt.Errorf("expected nil error, got: %v", s.collectErr)
	}
	return nil
}

// assertEmptyTerminalError pins the fail-closed policy: an empty
// completed response surfaces a terminal error after retries.
func (s *DelegationIntegritySteps) assertEmptyTerminalError() error {
	if s.collectErr == nil {
		return fmt.Errorf("expected a terminal empty-response error, got nil")
	}
	return nil
}

// assertNotCompleted pins that an empty completion is never reported
// as a normal success.
func (s *DelegationIntegritySteps) assertNotCompleted() error {
	if s.collectErr == nil && !engine.HasSubstantiveOutputForTest([]byte(s.result.Response())) {
		return fmt.Errorf("empty completion must not be reported as a normal success")
	}
	return nil
}
