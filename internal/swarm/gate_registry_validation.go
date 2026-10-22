package swarm

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/gates"
)

// BuiltinGateKinds lists every builtin gate kind the production
// MultiRunner registers at app startup (internal/app buildSwarmGateRunner).
// ValidateGateKindsRegistered consults this list so a manifest
// referencing a builtin: kind that no runner implements fails at load
// time rather than at dispatch time.
//
// Expected:
//   - Kept in sync with buildSwarmGateRunner's Register calls.
//
// Returns: result of BuiltinGateKinds.
// Side effects: None.
func BuiltinGateKinds() []string {
	return []string{
		"builtin:result-schema",
		EvidenceGroundingGateKind,
		ArtifactPublishedGateKind,
		FSPollutionGateKind,
		PersistenceCompletenessGateKind,
		TargetSpecificityGateKind,
	}
}

// IsGateKindRegistered reports whether kind resolves to a registered
// gate backend: either a builtin kind listed by BuiltinGateKinds or an
// ext: kind whose short name exists in the ext-gate registry.
//
// Expected:
//   - kind is a full gate kind string ("builtin:x" or "ext:x").
//
// Returns:
//   - true when the kind resolves to a runnable backend.
//   - false otherwise (including malformed kinds).
//
// Side effects: None.
func IsGateKindRegistered(kind string) bool {
	if strings.HasPrefix(kind, gateKindBuiltinPrefix) {
		for _, b := range BuiltinGateKinds() {
			if kind == b {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(kind, gateKindExtPrefix) {
		_, ok := LookupExtGate(strings.TrimPrefix(kind, gateKindExtPrefix))
		return ok
	}
	return false
}

// ValidateBuiltinGateKindsRegistered checks every builtin: gate kind
// in specs against BuiltinGateKinds and fails fast on the first kind
// no runner implements. Manifest.Validate calls this at load time;
// ext: kinds are excluded because the ext registry is populated at
// app boot (after discovery) rather than at manifest parse time, so
// load-time validation cannot know which ext gates exist. Use
// ValidateGateKindsRegistered at boot for the full check.
//
// Expected:
//   - specs is the gate slice from a swarm or agent manifest.
//
// Returns:
//   - nil when every builtin: kind resolves to a registered backend.
//   - An error naming the gate and its unregistered kind otherwise.
//
// Side effects: None.
func ValidateBuiltinGateKindsRegistered(specs []GateSpec) error {
	for _, gate := range specs {
		if !strings.HasPrefix(gate.Kind, gateKindBuiltinPrefix) {
			continue
		}
		if IsGateKindRegistered(gate.Kind) {
			continue
		}
		return fmt.Errorf("gate %q references unregistered gate kind %q", gate.Name, gate.Kind)
	}
	return nil
}

// ValidateGateKindsRegistered checks every gate kind in specs against
// the registered backends (builtin list + ext registry) and fails fast
// on the first unregistered kind. This closes the referenced-but-
// unregistered gap (precedent: ext:mental-health-safety, fixed
// 2026-08-18) by moving the failure from dispatch time to load time.
//
// Expected:
//   - specs is the gate slice from a swarm or agent manifest.
//
// Returns:
//   - nil when every kind resolves to a registered backend.
//   - An error naming the gate and its unregistered kind otherwise.
//
// Side effects: None.
func ValidateGateKindsRegistered(specs []GateSpec) error {
	for _, gate := range specs {
		if IsGateKindRegistered(gate.Kind) {
			continue
		}
		return fmt.Errorf("gate %q references unregistered gate kind %q", gate.Name, gate.Kind)
	}
	return nil
}

// ValidateDiscoveredExtGateKinds verifies that every ext: gate kind
// referenced by manifests has a corresponding discovered external gate
// manifest available. Operators call this after gates.Discover so a
// misconfigured gates directory fails boot with a clear error instead
// of a silent dispatch-time failure.
//
// Expected:
//   - specs is the gate slice from a swarm or agent manifest.
//   - discovered is the list of external gate manifests found on disk.
//
// Returns:
//   - nil when every ext: reference resolves to a discovered manifest.
//   - An error naming the first dangling reference otherwise.
//
// Side effects: None.
func ValidateDiscoveredExtGateKinds(specs []GateSpec, discovered []gates.Manifest) error {
	known := make(map[string]struct{}, len(discovered))
	for _, m := range discovered {
		known[m.Name] = struct{}{}
	}
	for _, gate := range specs {
		if !strings.HasPrefix(gate.Kind, gateKindExtPrefix) {
			continue
		}
		short := strings.TrimPrefix(gate.Kind, gateKindExtPrefix)
		if _, ok := known[short]; ok {
			continue
		}
		return fmt.Errorf("gate %q references ext gate %q which is not present in the gates directory", gate.Name, gate.Kind)
	}
	return nil
}

// ValidateRegistryGateKinds checks every swarm manifest in reg against
// the full gate-kind registry (builtins plus ext gates registered at
// boot). App startup calls this after RegisterDiscoveredGates so a
// swarm referencing an unregistered gate kind aborts boot with a clear
// error — the fail-fast contract closing the ext:mental-health-safety
// precedent.
//
// Expected:
//   - reg is a populated swarm Registry (nil is a no-op).
//
// Returns:
//   - nil when every registered swarm's gate kinds resolve.
//   - An error naming the offending swarm, gate, and kind otherwise.
//
// Side effects: None.
func ValidateRegistryGateKinds(reg *Registry) error {
	if reg == nil {
		return nil
	}
	for _, m := range reg.List() {
		if err := ValidateGateKindsRegistered(m.Harness.Gates); err != nil {
			return fmt.Errorf("swarm %q: %w", m.ID, err)
		}
	}
	return nil
}
