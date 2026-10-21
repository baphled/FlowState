package engine

import (
	"github.com/baphled/flowstate/internal/plugin/events"
)

// ErrDelegationNotAllowedForTest exposes the pre-spawn can_delegate gate
// error so app-level qualification specs can assert the exact rejection.
//
// Returns:
//   - The sentinel error returned when delegation.CanDelegate is false.
//
// Side effects:
//   - None.
func ErrDelegationNotAllowedForTest() error { return errDelegationNotAllowed }

// ErrDepthLimitExceededForTest exposes the engine-owned-lineage depth
// gate error for app-level qualification specs.
//
// Returns:
//   - The sentinel error returned when engine-owned depth reaches MaxDepth.
//
// Side effects:
//   - None.
func ErrDepthLimitExceededForTest() error { return errDepthLimitExceeded }

// ErrBudgetLimitExceededForTest exposes the budget gate error for
// app-level qualification specs.
//
// Returns:
//   - The sentinel error returned when active delegations reach the budget.
//
// Side effects:
//   - None.
func ErrBudgetLimitExceededForTest() error { return errBudgetLimitExceeded }

// WithBackgroundManagerForTest swaps the background manager consulted
// by the spawn-limit budget gate so specs can saturate ActiveCount.
//
// Expected:
//   - m is the replacement BackgroundTaskManager (may be nil).
//
// Returns:
//   - The receiver for chaining.
//
// Side effects:
//   - Replaces d.backgroundManager.
func (d *DelegateTool) WithBackgroundManagerForTest(m *BackgroundTaskManager) *DelegateTool {
	d.backgroundManager = m
	return d
}

// LastChildSessionIDForTest returns the child session id captured by
// the most recent delegation.started publish, for parentage asserts.
//
// Returns:
//   - The latest child session id, empty before any delegation.
//
// Side effects:
//   - None (read-only).
func (d *DelegateTool) LastChildSessionIDForTest() string {
	return d.lastChildSessionID
}

// DelegationStartedEventNameForTest returns the delegation.started bus
// topic constant.
//
// Returns:
//   - The "delegation.started" topic string.
//
// Side effects:
//   - None.
func DelegationStartedEventNameForTest() string { return events.EventDelegationStarted }

// DelegationCompletedEventNameForTest returns the delegation.completed
// bus topic constant.
//
// Returns:
//   - The "delegation.completed" topic string.
//
// Side effects:
//   - None.
func DelegationCompletedEventNameForTest() string { return events.EventDelegationCompleted }
