package swarm

import "fmt"

// SwarmType names the workload class a swarm is configured for. The
// type maps to a per-class delegation-depth default per addendum §7
// A4: analysis swarms stay flat (8), codegen swarms get the platform
// default (16), and orchestration swarms can nest deeper (32). The
// type is the manifest-level lever; a manifest can still pin its own
// MaxDepth to override the type default.
type SwarmType string

// SwarmType values accepted by the validator. Empty resolves to
// SwarmTypeAnalysis so existing pre-A4 manifests stay forward-
// compatible without an edit.
const (
	// SwarmTypeAnalysis is the default class. Flat structures with
	// shallow delegation chains (planner -> specialists). Depth cap 8.
	SwarmTypeAnalysis SwarmType = "analysis"

	// SwarmTypeCodegen is the platform-default class. Sub-swarm
	// composition is expected (codegen lead -> language sub-swarm ->
	// per-file specialists). Depth cap 16 — same as the platform-wide
	// raised default in addendum A4.
	SwarmTypeCodegen SwarmType = "codegen"

	// SwarmTypeOrchestration is the most-deeply-nested class. Lead
	// orchestrators that compose other swarms at multiple levels.
	// Depth cap 32 — the upper end of A4's configurable range.
	SwarmTypeOrchestration SwarmType = "orchestration"
)

// defaultMaxDepthByType pins the per-type ceilings from addendum §7
// A4. The map is the single source of truth for the resolution path
// AND the validator's enum check (its keys ARE the legal values), so
// adding a new swarm type means one edit here.
var defaultMaxDepthByType = map[SwarmType]int{
	SwarmTypeAnalysis:      8,
	SwarmTypeCodegen:       16,
	SwarmTypeOrchestration: 32,
}

// DefaultMaxDepthForType returns the addendum-A4 depth ceiling for a
// swarm type. An empty type is treated as SwarmTypeAnalysis so
// callers can pass m.SwarmType directly without a nil-check.
//
// Expected:
//   - swarmType is one of the SwarmType constants OR the empty
//     string. Unknown values fall through to the analysis default
//     because the validator catches unknowns at load time; the
//     resolver does not re-emit that error here.
//
// Returns:
//   - The per-type depth default (8 / 16 / 32).
//
// Side effects:
//   - None.
func DefaultMaxDepthForType(swarmType SwarmType) int {
	if swarmType == "" {
		return defaultMaxDepthByType[SwarmTypeAnalysis]
	}
	if depth, ok := defaultMaxDepthByType[swarmType]; ok {
		return depth
	}
	return defaultMaxDepthByType[SwarmTypeAnalysis]
}

// ResolveMaxDepth returns the effective delegation-depth ceiling for
// this manifest. Resolution order per addendum §7 A4:
//
//  1. An explicit positive Manifest.MaxDepth wins (manifest-level
//     override).
//  2. Otherwise the per-type default from DefaultMaxDepthForType is
//     returned.
//
// A zero or negative MaxDepth is treated as "unset" — the validator
// rejects negatives at load time, so a non-zero value reaching this
// path is always a deliberate operator choice.
//
// Expected:
//   - The manifest has been validated. Pre-validation calls return a
//     legal-but-possibly-stale value; production callers should run
//     Validate first.
//
// Returns:
//   - The effective max delegation depth.
//
// Side effects:
//   - None.
func (m *Manifest) ResolveMaxDepth() int {
	if m.MaxDepth > 0 {
		return m.MaxDepth
	}
	return DefaultMaxDepthForType(m.SwarmType)
}

// validateSwarmType rejects manifests whose swarm_type is set to a
// value outside the addendum-A4 enum. An empty SwarmType is allowed
// because the resolver maps it to SwarmTypeAnalysis; rejecting empty
// here would force every existing manifest to grow a swarm_type field
// just to reload.
//
// Expected:
//   - m is a non-nil Manifest pointer.
//
// Returns:
//   - nil when SwarmType is empty or one of the SwarmType constants.
//   - A *ValidationError naming the swarm_type field otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateSwarmType() error {
	if m.SwarmType == "" {
		return nil
	}
	if _, ok := defaultMaxDepthByType[m.SwarmType]; ok {
		return nil
	}
	return &ValidationError{
		Field:   "swarm_type",
		Message: fmt.Sprintf("unknown swarm type %q (expected %q, %q, or %q)", m.SwarmType, SwarmTypeAnalysis, SwarmTypeCodegen, SwarmTypeOrchestration),
	}
}

// validateMaxDepth rejects negative MaxDepth values. Zero is allowed
// because it means "unset — use the per-type default"; the resolver
// (ResolveMaxDepth) is the single point that translates an unset
// value into a ceiling.
//
// Expected:
//   - m is a non-nil Manifest pointer.
//
// Returns:
//   - nil when MaxDepth is >= 0.
//   - A *ValidationError naming the max_depth field otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateMaxDepth() error {
	if m.MaxDepth < 0 {
		return &ValidationError{
			Field:   "max_depth",
			Message: fmt.Sprintf("must be >= 0 (got %d)", m.MaxDepth),
		}
	}
	return nil
}
