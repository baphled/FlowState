package swarm

import (
	"context"
	"errors"
	"sync"
)

// errNilMemberRunner is the typed error returned when a caller passes a
// nil MemberRunner to DispatchMembers. Surfaced as a sentinel so future
// integrators can branch on it; today the production wiring constructs
// the runner inline, so this only fires from misconfigured tests.
var errNilMemberRunner = errors.New("swarm.DispatchMembers: runner must be non-nil")

// MemberRunner is the per-member work the dispatcher invokes for each
// roster entry. The returned error is what the dispatcher considers the
// member's verdict — a non-nil error from the runner halts in-flight
// peers and surfaces back as the DispatchMembers error.
//
// The supplied context is the dispatcher's per-call context. In
// parallel mode it is derived from a shared cancellation context so
// that a peer's failure can cancel siblings. Implementations MUST
// propagate ctx into any blocking call (Stream, coordination_store
// reads, etc.) so the cancellation reaches workers in flight.
type MemberRunner func(ctx context.Context, memberID string) error

// MemberPostHook is the optional per-member callback DispatchMembers
// fires immediately after the runner returns for that member. It runs
// on the worker goroutine before the worker releases its semaphore
// slot, so a non-nil return from the hook is treated as a member-level
// failure: in-flight peers are cancelled and the hook's error becomes
// the dispatch error.
//
// The runErr parameter carries whatever the MemberRunner itself
// returned. Hooks that gate on success only should early-return when
// runErr != nil; hooks that always run (e.g. event emission) ignore the
// runErr parameter.
type MemberPostHook func(ctx context.Context, memberID string, runErr error) error

// DispatchOptions configures DispatchMembers. The zero value is the
// historical sequential mode the swarm runtime shipped before §T37 —
// callers that opt into parallel mode set Parallel and (optionally)
// MaxParallel.
type DispatchOptions struct {
	// Parallel selects parallel member dispatch. False (the zero
	// value) preserves the historical strict-sequential behaviour:
	// member N+1 starts only after member N's runner returns.
	Parallel bool

	// MaxParallel caps concurrent fan-out when Parallel is true.
	// Values <= 0 are treated as "no swarm-level cap" — the
	// dispatcher uses len(members) as the effective ceiling so every
	// member can run concurrently. The dispatcher does not consult
	// the engine's global concurrency ceilings; those are layered on
	// top by the caller (typically the lead engine's spawn-limits
	// plumbing).
	MaxParallel int

	// PostMember is the hook DispatchMembers calls immediately after
	// each member's runner returns, on the same goroutine. A non-nil
	// return halts in-flight peers via context cancellation. Optional;
	// nil disables the per-member hook surface entirely.
	//
	// The hook MUST fire as soon as the member completes — not after
	// the whole batch — so post-member gates can validate per-member
	// outputs while peers are still running. This is the spec contract
	// for §T37 + T-swarm-3 dual-scope gates.
	PostMember MemberPostHook
}

// DispatchMembers runs runner against every entry in members and
// returns the first error it observes. The mode (sequential vs
// parallel) and the parallelism cap are taken from opts.
//
// # Concurrency semantics
//
// DispatchMembers is the §T37 dispatch surface. It accepts a roster
// and a per-member work function, and runs them either strictly
// sequentially (opts.Parallel == false, the default and historical
// behaviour) or concurrently with an optional cap on in-flight workers
// (opts.Parallel == true, cap from opts.MaxParallel).
//
// The implementation chooses a buffered-channel semaphore over
// errgroup.SetLimit because the function intentionally exposes a
// PostMember hook that fires on the worker goroutine before the slot
// is released. errgroup.SetLimit would conflate "release the limit
// slot" with "report the goroutine done", which would let a peer slip
// past the cap during the post-hook window. The semaphore ALSO keeps
// the cancellation contract explicit: when any worker (runner or
// post-hook) returns a non-nil error, the dispatcher cancels the
// shared child context so in-flight peers observe ctx.Done() at their
// next selectable site, then waits for every worker to finish before
// returning the first error captured. This matches the spec's "gate
// failure on member X cancels in-flight peers" requirement without
// blocking the synthesis (post-swarm) caller — synthesis runs only
// after DispatchMembers returns.
//
// # Error semantics
//
// Only the first error wins (errgroup-style). Subsequent errors are
// dropped — peers cancelled by the first failure surface
// context.Canceled, which is informational noise that would drown out
// the real cause if returned. Callers that need every error per
// member can layer a recording PostMember hook on top.
//
// Expected:
//   - ctx is the parent context. A pre-cancelled ctx returns its
//     Err() immediately without invoking runner.
//   - members is the ordered roster. Empty / nil yields a nil error.
//   - runner is non-nil. A nil runner returns errNilMemberRunner.
//   - opts is consumed by value; zero value = strict sequential.
//
// Returns:
//   - nil when every member's runner (and PostMember hook, if set)
//     returns nil.
//   - The first non-nil error otherwise. Peers that observed cancel
//     surface as context.Canceled to their own runners but do not win
//     the error race — only the originating failure is returned.
//
// Side effects:
//   - Spawns up to MaxParallel goroutines (parallel mode) or zero
//     additional goroutines (sequential mode). All workers are joined
//     before DispatchMembers returns.
func DispatchMembers(ctx context.Context, members []string, runner MemberRunner, opts DispatchOptions) error {
	if runner == nil {
		return errNilMemberRunner
	}
	if len(members) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !opts.Parallel {
		return dispatchSequential(ctx, members, runner, opts.PostMember)
	}
	return dispatchParallel(ctx, members, runner, opts)
}

// dispatchSequential runs members one at a time in roster order. A
// runner or post-member failure halts the loop and surfaces the error;
// downstream members are skipped. Sequential mode does not derive a
// child context — the caller's cancellation propagates directly into
// the runner — so failure semantics match the historical pre-§T37
// behaviour.
//
// Expected:
//   - ctx is the parent context.
//   - members is non-empty.
//   - runner is non-nil.
//   - postMember may be nil.
//
// Returns:
//   - nil when every member completes successfully.
//   - The first non-nil error from runner or postMember otherwise.
//
// Side effects:
//   - Calls runner once per member in order.
func dispatchSequential(ctx context.Context, members []string, runner MemberRunner, postMember MemberPostHook) error {
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}
		runErr := runner(ctx, member)
		if postMember != nil {
			if hookErr := postMember(ctx, member, runErr); hookErr != nil {
				return hookErr
			}
		}
		if runErr != nil {
			return runErr
		}
	}
	return nil
}

// dispatchParallel fans members out into worker goroutines bounded by
// the effective parallelism limit (see effectiveParallelism). Each
// worker drains a single token from the buffered semaphore before
// running, returns the token after the post-member hook resolves, and
// reports back through a single shared error channel. The first error
// observed cancels the shared child context so in-flight peers
// short-circuit at their next ctx-selectable site.
//
// Expected:
//   - ctx is the parent context (already validated as not cancelled).
//   - members is non-empty.
//   - runner is non-nil.
//   - opts.Parallel is true.
//
// Returns:
//   - nil when every worker (runner + post-member hook) reports nil.
//   - The first non-nil error otherwise.
//
// Side effects:
//   - Spawns one goroutine per member; joins all of them before
//     returning. Cancels a derived context on the first error so peers
//     can observe and unwind.
func dispatchParallel(ctx context.Context, members []string, runner MemberRunner, opts DispatchOptions) error {
	limit := effectiveParallelism(opts.MaxParallel, len(members))
	sem := make(chan struct{}, limit)

	derivedCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg       sync.WaitGroup
		errMu    sync.Mutex
		firstErr error
	)
	captureErr := func(err error) {
		if err == nil {
			return
		}
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
	}

	for _, member := range members {
		select {
		case <-derivedCtx.Done():
			wg.Wait()
			captureErr(derivedCtx.Err())
			return firstErr
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(memberID string) {
			defer wg.Done()
			defer func() { <-sem }()
			runErr := runner(derivedCtx, memberID)
			if opts.PostMember != nil {
				if hookErr := opts.PostMember(derivedCtx, memberID, runErr); hookErr != nil {
					captureErr(hookErr)
					return
				}
			}
			captureErr(runErr)
		}(member)
	}

	wg.Wait()
	return firstErr
}

// effectiveParallelism resolves the parallelism cap the parallel
// dispatcher uses. A non-positive max means "no swarm-level cap"; the
// dispatcher uses the roster size so every member can run concurrently
// without an artificial bound. A max larger than the roster is also
// shrunk to roster size so the buffered-channel semaphore never sits
// with idle slots that no goroutine will fill.
//
// Expected:
//   - max is the user-supplied opts.MaxParallel.
//   - rosterSize is len(members); always > 0 because the empty-roster
//     short-circuit fires above.
//
// Returns:
//   - The effective ceiling: rosterSize when max <= 0 or max >
//     rosterSize, max otherwise.
//
// Side effects:
//   - None.
func effectiveParallelism(max, rosterSize int) int {
	if max <= 0 || max > rosterSize {
		return rosterSize
	}
	return max
}
