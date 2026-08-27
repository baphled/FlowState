package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// ErrEmptyDelegateResponse reports a delegate that drained its stream and
// passed its gates yet produced no substantive output after the bounded
// retry budget. Callers must treat it as terminal — an empty completion is
// never reported as a normal success.
var ErrEmptyDelegateResponse = errors.New("delegate response empty or non-substantive after retries")

// CollectPolicy sets the completion-integrity behaviour for child-stream
// collection during a delegation.
//
// The zero value enables the historical behaviour (parent cancellation wins,
// empty completions pass) so existing surfaces stay source-compatible;
// production call sites opt in to the trustworthy defaults via
// DefaultCollectPolicy.
type CollectPolicy struct {
	// DetachOnParentCancel reports whether a child stream keeps being
	// collected after the parent context is cancelled. When true,
	// completed child work is reported as success rather than surfacing
	// as "context canceled".
	DetachOnParentCancel bool

	// FailClosedOnEmpty reports whether an empty, non-substantive
	// completion fails with ErrEmptyDelegateResponse instead of being
	// returned as a silent success.
	FailClosedOnEmpty bool
}

// DefaultCollectPolicy returns the trustworthy collection policy: child
// completion state outranks parent cancellation, and empty completions fail
// closed.
//
// Expected: none.
//
// Returns: the policy production collection call sites apply.
//
// Side effects: none.
func DefaultCollectPolicy() CollectPolicy {
	return CollectPolicy{DetachOnParentCancel: true, FailClosedOnEmpty: true}
}

// ApplyCollectPolicy wraps parentCtx with the policy's cancellation
// semantics and returns the context a child collector should use together
// with its cancellation function.
//
// Expected:
//   - parentCtx is the parent stream context.
//
// Returns:
//   - A collection context detached from parent cancellation when
//     DetachOnParentCancel is set, otherwise a cancellable child of
//     parentCtx, plus the matching cancel function.
//
// Side effects:
//   - None.
func (p CollectPolicy) ApplyCollectPolicy(parentCtx context.Context) (context.Context, context.CancelFunc) {
	if p.DetachOnParentCancel {
		return context.WithCancel(context.WithoutCancel(parentCtx))
	}
	return context.WithCancel(parentCtx)
}

// TerminalEmptyResponseError converts an empty completion into the
// fail-closed terminal error when the policy demands it.
//
// Expected:
//   - response is the accumulated child response text.
//
// Returns:
//   - ErrEmptyDelegateResponse when FailClosedOnEmpty is set and the
//     response carries no substantive output; nil otherwise.
//
// Side effects:
//   - None.
func (p CollectPolicy) TerminalEmptyResponseError(response string) error {
	if p.FailClosedOnEmpty && !hasSubstantiveOutput([]byte(response)) {
		return fmt.Errorf("%w", ErrEmptyDelegateResponse)
	}
	return nil
}

// DelegationResultForTest re-exports delegationResult so external test
// packages and the BDD suite can inspect a collected delegation result.
type DelegationResultForTest = delegationResult

// CollectWithProgressForTest re-exports collectWithProgress for external
// test packages.
//
// Expected:
//   - ctx carries the parent output channel for progress delivery.
//   - d is the delegate tool under test.
//   - chunks is the scripted child stream.
//   - startedAt is the delegation start time.
//
// Returns:
//   - The collected delegation result and any stream error.
//
// Side effects:
//   - Emits ProgressEvents as collectWithProgress does.
func CollectWithProgressForTest(ctx context.Context, d *DelegateTool, chunks <-chan provider.StreamChunk, startedAt time.Time) (DelegationResultForTest, error) {
	return d.collectWithProgress(ctx, chunks, startedAt)
}

// HasSubstantiveOutputForTest re-exports hasSubstantiveOutput so external
// test packages can assert the empty-response policy without duplicating the
// predicate.
//
// Expected:
//   - val is the candidate response bytes.
//
// Returns:
//   - True when the value carries content beyond whitespace or an empty
//     JSON container.
//
// Side effects:
//   - None.
func HasSubstantiveOutputForTest(val []byte) bool {
	return hasSubstantiveOutput(val)
}

// CollectDelegationCompletion collects a completed child stream the way
// the production delegation dispatch path does, applying the trustworthy
// completion policy: child completion state outranks parent cancellation
// once output exists, and an empty completion fails closed.
//
// Expected:
//   - ctx is the parent stream context.
//   - d, chunks, and startedAt match collectWithProgress.
//
// Returns:
//   - The collected result plus any stream, cancellation, or terminal
//     empty-response error.
//
// Side effects:
//   - Emits ProgressEvents as collectWithProgress does.
func CollectDelegationCompletion(ctx context.Context, d *DelegateTool, chunks <-chan provider.StreamChunk, startedAt time.Time) (DelegationResultForTest, error) {
	policy := DefaultCollectPolicy()
	collectCtx, collectCancel := policy.ApplyCollectPolicy(ctx)
	defer collectCancel()
	res, err := d.collectWithProgress(collectCtx, chunks, startedAt)
	if err != nil {
		return DelegationResultForTest{}, err
	}
	if emptyErr := policy.TerminalEmptyResponseError(res.response); emptyErr != nil {
		return DelegationResultForTest{}, emptyErr
	}
	return res, nil
}

// Response returns the accumulated response text of a collected delegation
// result.
//
// Expected:
//   - r is a collected delegation result.
//
// Returns:
//   - The accumulated child response text.
//
// Side effects:
//   - None.
func (r DelegationResultForTest) Response() string { return r.response }

// Truncated reports whether the collected response was truncated.
//
// Expected:
//   - r is a collected delegation result.
//
// Returns:
//   - True when the accumulated response exceeded the truncation ceiling.
//
// Side effects:
//   - None.
func (r DelegationResultForTest) Truncated() bool { return r.truncated }

// ToolCallCount returns the total number of chunks observed during
// collection.
//
// Expected:
//   - r is a collected delegation result.
//
// Returns:
//   - The number of child stream chunks observed.
//
// Side effects:
//   - None.
func (r DelegationResultForTest) ToolCallCount() int { return r.toolCalls }
