package streaming

import "fmt"

// VerbosityLevel defines the verbosity setting for event emission.
// Higher levels include more events.
type VerbosityLevel int

const (
	// Minimal verbosity emits only high-level events: status transitions,
	// plan artifacts, and review verdicts.
	Minimal VerbosityLevel = iota
	// Standard verbosity includes all minimal events plus tool calls
	// and delegation events.
	Standard
	// Verbose verbosity emits all event types.
	Verbose
)

// String returns the string representation of the verbosity level.
//
// Returns:
//   - A string representation of the verbosity level ("minimal", "standard", or "verbose").
//
// Side effects:
//   - None.
func (v VerbosityLevel) String() string {
	switch v {
	case Minimal:
		return "minimal"
	case Standard:
		return "standard"
	case Verbose:
		return "verbose"
	default:
		return fmt.Sprintf("verbosity(%d)", v)
	}
}

// VerbosityFilter determines which events should be emitted based on
// the configured verbosity level.
type VerbosityFilter struct {
	Level VerbosityLevel
}

// NewVerbosityFilter creates a new VerbosityFilter with the specified level.
//
// Expected:
//   - level must be a valid VerbosityLevel (Minimal, Standard, or Verbose).
//
// Returns:
//   - A new VerbosityFilter configured with the specified level.
//
// Side effects:
//   - None.
func NewVerbosityFilter(level VerbosityLevel) *VerbosityFilter {
	return &VerbosityFilter{Level: level}
}

// ShouldEmit determines whether the given event should be emitted
// based on the current verbosity level.
//
// Expected:
//   - event must be a non-nil Event implementation.
//
// Returns:
//   - true if the event should be emitted at the current verbosity level,
//     false otherwise.
//
// Side effects:
//   - None.
func (f *VerbosityFilter) ShouldEmit(event Event) bool {
	switch f.Level {
	case Minimal:
		return f.shouldEmitMinimal(event)
	case Standard:
		return f.shouldEmitStandard(event)
	case Verbose:
		return true
	default:
		return false
	}
}

// shouldEmitMinimal determines if an event should be emitted at minimal verbosity.
//
// At minimal verbosity, only status_transition, plan_artifact, and review_verdict
// events are emitted.
//
// Expected:
//   - event must be a non-nil Event implementation.
//
// Returns:
//   - true for status_transition, plan_artifact, or review_verdict events;
//     false for all other events.
//
// Side effects:
//   - None.
func (f *VerbosityFilter) shouldEmitMinimal(event Event) bool {
	switch event.EventType() {
	case "status_transition", "plan_artifact", "review_verdict":
		return true
	default:
		return false
	}
}

// shouldEmitStandard determines if an event should be emitted at standard verbosity.
//
// At standard verbosity, all minimal events plus tool_call and delegation
// events are emitted.
//
// Expected:
//   - event must be a non-nil Event implementation.
//
// Returns:
//   - true for status_transition, plan_artifact, review_verdict, tool_call,
//     or delegation events; false for all other events.
//
// Side effects:
//   - None.
func (f *VerbosityFilter) shouldEmitStandard(event Event) bool {
	switch event.EventType() {
	case "status_transition", "plan_artifact", "review_verdict",
		"tool_call", "delegation":
		return true
	default:
		return false
	}
}
