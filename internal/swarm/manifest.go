package swarm

import (
	"fmt"
	"strings"
)

// SchemaVersionV1 is the first stable swarm-manifest schema version. The
// loader rejects manifests that omit it or use an unrecognised value so
// future revisions can roll out behind a version bump rather than silent
// drift.
const SchemaVersionV1 = "1.0.0"

// gateKindBuiltinPrefix and gateKindExtPrefix are the two accepted gate
// kind families per §1 of the addendum. The dispatch surface that turns
// a builtin into an in-process call (and an ext into a gRPC harness
// call) is T-swarm-3; this package only validates the prefix shape.
const (
	gateKindBuiltinPrefix = "builtin:"
	gateKindExtPrefix     = "ext:"
)

// Manifest is the in-memory representation of a swarm manifest YAML
// file. The field tags mirror the on-disk YAML (snake_case) so the
// gopkg.in/yaml.v3 unmarshaller maps directly onto this struct without
// a custom UnmarshalYAML.
type Manifest struct {
	// SchemaVersion declares the schema revision the manifest targets.
	// Today only "1.0.0" is accepted; future schema bumps should keep
	// older manifests loading by adding compatibility shims here.
	SchemaVersion string `json:"schema_version" yaml:"schema_version"`

	// ID is the globally unique swarm identifier. The §1 rule requires
	// uniqueness across both the agent registry and the swarm registry
	// so a `@<id>` mention can resolve unambiguously at chat time.
	ID string `json:"id" yaml:"id"`

	// Description is a human-readable purpose blurb. Optional but
	// strongly encouraged for discoverability.
	Description string `json:"description,omitempty" yaml:"description,omitempty"`

	// Lead names the agent or swarm that runs first when the swarm is
	// invoked via @Lead. May reference an agent id (the common case)
	// or another swarm id (sub-swarm composition per §4).
	Lead string `json:"lead" yaml:"lead"`

	// Members is the explicit roster the lead can delegate to during
	// the run. Entries may be agent ids or swarm ids. The roster
	// shadows the lead's normal delegation.allowlist for the duration
	// of the swarm so the lead's prompt does not need swarm-specific
	// edits.
	Members []string `json:"members" yaml:"members"`

	// Harness configures the swarm-runner-level execution policy:
	// parallel-vs-sequential member dispatch, the parallelism ceiling,
	// and the swarm-scoped gates evaluated at swarm/member boundaries.
	Harness HarnessConfig `json:"harness" yaml:"harness"`

	// Context configures coordination_store namespacing for the run.
	// When ChainPrefix is empty the swarm id is used; pinning it
	// explicitly lets users keep documented chain keys stable across
	// renames.
	Context ContextConfig `json:"context" yaml:"context"`
}

// HarnessConfig groups the swarm-runner-level execution settings on a
// manifest. Today the runtime is sequential-only — Parallel and
// MaxParallel are read by §T37 once parallel exec lands, but the
// loader/validator already accept the fields so manifests written
// against the published schema are forward-compatible.
type HarnessConfig struct {
	// Parallel selects parallel member dispatch. False (the default)
	// runs members sequentially in roster order.
	Parallel bool `json:"parallel,omitempty" yaml:"parallel,omitempty"`

	// MaxParallel caps concurrent member fan-out when Parallel is
	// true. Zero means "no swarm-level cap"; the global concurrency
	// ceilings on the engine still apply.
	MaxParallel int `json:"max_parallel,omitempty" yaml:"max_parallel,omitempty"`

	// Gates is the ordered list of swarm-scoped gates evaluated at
	// swarm and member boundaries. Each entry's Kind selects the
	// dispatch family ("builtin:*" / "ext:*") consumed by T-swarm-3.
	Gates []GateSpec `json:"gates,omitempty" yaml:"gates,omitempty"`
}

// GateSpec is one entry in a swarm's harness.gates list. The fields
// mirror the §1 schema so a YAML round-trip preserves every key the
// runtime cares about; unknown keys on disk are ignored by yaml.v3 by
// default which keeps forward-compatibility with deferred amendments
// (precedence, failurePolicy, timeout from §7 A1/A6).
type GateSpec struct {
	// Name uniquely identifies the gate inside the manifest. Used for
	// log/event correlation and for collision detection against
	// agent-level gate names (see §3 of the addendum).
	Name string `json:"name" yaml:"name"`

	// Kind selects the gate runner. Must start with "builtin:" or
	// "ext:". The validator only checks the prefix today; full
	// dispatch validation is T-swarm-3 (builtins) and the Extension
	// API v1 plan (ext) respectively.
	Kind string `json:"kind" yaml:"kind"`

	// SchemaRef is consumed by builtin:result-schema gates to look up
	// a registered output schema (e.g. "review-verdict-v1"). Other
	// kinds may ignore it.
	SchemaRef string `json:"schema_ref,omitempty" yaml:"schema_ref,omitempty"`

	// When selects the boundary at which the gate fires. The four §3
	// values are "pre" / "post" / "pre-member" / "post-member"; an
	// empty string means "pre" by precedent. The loader does not
	// enforce the enum yet — gate dispatch (T-swarm-3) catches
	// unknown values at runtime so the same string surface stays
	// authoritative.
	When string `json:"when,omitempty" yaml:"when,omitempty"`

	// Target scopes a member-boundary gate to a single member id.
	// Only meaningful for When="pre-member" / When="post-member".
	Target string `json:"target,omitempty" yaml:"target,omitempty"`
}

// ContextConfig holds the coordination-store namespacing override.
// Kept as its own struct so future amendments (per §7 A7's resource
// hierarchy) can grow context-related knobs without breaking the
// top-level Manifest YAML shape.
type ContextConfig struct {
	// ChainPrefix overrides the coordination_store chain namespace
	// for runs of this swarm. Empty = use the swarm id.
	ChainPrefix string `json:"chain_prefix,omitempty" yaml:"chain_prefix,omitempty"`
}

// Validator resolves agent and swarm ids referenced from a manifest
// during Validate(). The interface lets the swarm package stay
// decoupled from the agent registry — callers wrap their concrete
// agent.Registry with a tiny adapter rather than the swarm package
// importing it.
type Validator interface {
	// HasAgent reports whether id is a registered agent.
	HasAgent(id string) bool
	// HasSwarm reports whether id is a registered swarm OTHER than
	// the one currently being validated. Implementations should
	// exclude the manifest's own id so the ordinary "lead must be a
	// known agent or swarm" check does not block self-reference; the
	// dedicated cycle check in Validate diagnoses self-reference with
	// a clearer message.
	HasSwarm(id string) bool
}

// noopValidator is the zero-value Validator returned to Validate when
// the caller passes nil. It accepts no agents and no swarms so the
// "lead must resolve" / "members must resolve" rules still fire and
// the call site sees a deterministic error. Using a sentinel rather
// than a nil-check keeps the Validate body focused on the actual
// rules.
type noopValidator struct{}

// HasAgent always returns false because the no-op validator has no
// registry to consult. Callers that legitimately want every id to
// resolve should pass a Validator backed by their real registries.
//
// Expected:
//   - The id parameter is ignored.
//
// Returns:
//   - false unconditionally.
//
// Side effects:
//   - None.
func (noopValidator) HasAgent(string) bool { return false }

// HasSwarm always returns false for the same reason as HasAgent —
// the no-op validator carries no registry state.
//
// Expected:
//   - The id parameter is ignored.
//
// Returns:
//   - false unconditionally.
//
// Side effects:
//   - None.
func (noopValidator) HasSwarm(string) bool { return false }

// ValidationError is the typed error returned from Validate when the
// manifest fails one of the §1 rules. Carrying Field on the surface
// keeps the error machine-inspectable (tests assert on Field rather
// than substring-matching the message).
type ValidationError struct {
	Field   string
	Message string
}

// Error renders the validation failure in "field: message" form to
// match the agent package's ValidationError convention so multi-error
// aggregation in the loader stays consistent across registries.
//
// Returns:
//   - The string "<field>: <message>" describing the failure.
//
// Side effects:
//   - None.
func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Validate enforces the §1 rules from the addendum:
//   - schema_version is present and recognised
//   - id is non-empty
//   - lead is non-empty and resolves to an agent or another swarm
//   - members all resolve to agents or swarms
//   - the manifest does not list itself as a member (self-reference)
//   - each gate kind starts with "builtin:" or "ext:"
//
// The Validator parameter is the agent + swarm registry adapter the
// caller has on hand. Pass nil at file-load time when the registries
// have not been populated yet — the rules that need a registry then
// degrade to id-format checks (cycle/self-reference still fires) and
// the registry-aware checks run a second time when the registry-aware
// LoadDir / NewRegistryFromDir paths re-validate after registration.
//
// Returns nil on success or the first failing rule wrapped in a
// *ValidationError; loaders aggregate per-file failures themselves so
// returning a single error keeps the rule order auditable.
//
// Expected:
//   - m is a non-nil Manifest pointer freshly unmarshalled from YAML.
//   - v is either a Validator backed by real registries or nil; nil
//     is treated as the no-op validator and skips registry-aware
//     resolution checks.
//
// Returns:
//   - nil on success.
//   - A *ValidationError naming the first failing rule otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) Validate(v Validator) error {
	if v == nil {
		v = noopValidator{}
	}

	if err := m.validateScalars(); err != nil {
		return err
	}

	if err := m.validateSelfReference(); err != nil {
		return err
	}

	if err := m.validateLead(v); err != nil {
		return err
	}

	if err := m.validateMembers(v); err != nil {
		return err
	}

	return m.validateGates()
}

// validateScalars enforces the trivial non-empty / version-recognised
// checks before the structural rules below depend on those fields
// being usable.
//
// Expected:
//   - m is a non-nil Manifest pointer.
//
// Returns:
//   - nil when schema_version, id and lead are all populated and
//     schema_version equals SchemaVersionV1.
//   - A *ValidationError naming the first failing field otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateScalars() error {
	if strings.TrimSpace(m.SchemaVersion) == "" {
		return &ValidationError{Field: "schema_version", Message: "required"}
	}
	if m.SchemaVersion != SchemaVersionV1 {
		return &ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("unsupported version %q (expected %q)", m.SchemaVersion, SchemaVersionV1),
		}
	}
	if strings.TrimSpace(m.ID) == "" {
		return &ValidationError{Field: "id", Message: "required"}
	}
	if strings.TrimSpace(m.Lead) == "" {
		return &ValidationError{Field: "lead", Message: "required"}
	}
	return nil
}

// validateSelfReference catches the trivial cycle case (a manifest
// listing its own id as a member or as its lead). The full SCC walk
// across registered manifests lives in cycleCheck below; this guard
// fires regardless of registry state so a freshly-authored manifest
// is rejected before it touches the registry.
//
// Expected:
//   - m is a non-nil Manifest pointer with ID populated.
//
// Returns:
//   - nil when no member or lead matches m.ID.
//   - A *ValidationError naming the offending field otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateSelfReference() error {
	if m.Lead == m.ID {
		return &ValidationError{
			Field:   "lead",
			Message: fmt.Sprintf("self-reference: lead %q matches manifest id", m.Lead),
		}
	}
	for i, member := range m.Members {
		if member == m.ID {
			return &ValidationError{
				Field:   fmt.Sprintf("members[%d]", i),
				Message: fmt.Sprintf("self-reference: member %q matches manifest id", member),
			}
		}
	}
	return nil
}

// validateLead enforces the "lead resolves to a registered agent or
// swarm" rule. When the validator is the no-op variant (registries
// unavailable at this stage) the registry checks return false and the
// caller surfaces the failure — the registry-aware re-validation in
// NewRegistryFromDir catches anything the file-load path could not.
//
// Expected:
//   - m is a non-nil Manifest pointer.
//   - v is a non-nil Validator (the caller substitutes noopValidator
//     for nil before calling).
//
// Returns:
//   - nil when the lead resolves to an agent, a swarm, or v is the
//     no-op validator (deferred to registry-aware validation later).
//   - A *ValidationError naming the lead field otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateLead(v Validator) error {
	if v.HasAgent(m.Lead) || v.HasSwarm(m.Lead) {
		return nil
	}
	if _, ok := v.(noopValidator); ok {
		// File-load path with no registry: skip registry-resolved
		// checks rather than emitting false positives. The
		// registry-aware re-validation in NewRegistryFromDir is the
		// authoritative pass.
		return nil
	}
	return &ValidationError{
		Field:   "lead",
		Message: fmt.Sprintf("%q does not resolve to a registered agent or swarm", m.Lead),
	}
}

// validateMembers enforces resolution for every roster entry. Behaves
// the same way as validateLead under a no-op validator so file-load
// validation stays decoupled from registry state.
//
// Expected:
//   - m is a non-nil Manifest pointer.
//   - v is a non-nil Validator.
//
// Returns:
//   - nil when every member resolves or v is the no-op validator.
//   - A *ValidationError naming the first unresolved member otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateMembers(v Validator) error {
	if _, ok := v.(noopValidator); ok {
		return nil
	}
	for i, member := range m.Members {
		if v.HasAgent(member) || v.HasSwarm(member) {
			continue
		}
		return &ValidationError{
			Field:   fmt.Sprintf("members[%d]", i),
			Message: fmt.Sprintf("%q does not resolve to a registered agent or swarm", member),
		}
	}
	return nil
}

// validateGates enforces the kind-prefix rule on every gate. The
// loader is intentionally lenient about the rest of the gate body
// (when/target/schema_ref) — those are the dispatch surface's
// responsibility (T-swarm-3 for builtins, Extension API v1 for ext).
//
// Expected:
//   - m is a non-nil Manifest pointer.
//
// Returns:
//   - nil when every gate has a non-empty name and a kind starting
//     with "builtin:" or "ext:".
//   - A *ValidationError naming the first failing gate field otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateGates() error {
	for i, gate := range m.Harness.Gates {
		if strings.TrimSpace(gate.Name) == "" {
			return &ValidationError{
				Field:   fmt.Sprintf("harness.gates[%d].name", i),
				Message: "required",
			}
		}
		if !strings.HasPrefix(gate.Kind, gateKindBuiltinPrefix) && !strings.HasPrefix(gate.Kind, gateKindExtPrefix) {
			return &ValidationError{
				Field:   fmt.Sprintf("harness.gates[%d].kind", i),
				Message: fmt.Sprintf("must start with %q or %q (got %q)", gateKindBuiltinPrefix, gateKindExtPrefix, gate.Kind),
			}
		}
	}
	return nil
}

// cycleCheck walks the swarm-membership graph rooted at root and
// returns the cycle-listing ValidationError when the walk re-enters a
// swarm already on the current path. The walk treats agent ids as
// terminal (they cannot themselves recurse) and depth-limits at
// MaxCycleWalkDepth so a misconfigured registry cannot wedge the
// validator on pathological inputs.
//
// TODO(swarm-cycle): replace this depth-bounded DFS with the Tarjan
// SCC algorithm once the agent-platform plan §T38a lands its shared
// cycle-detection helper. Today there is no codebase implementation
// to reuse, so the focused walk below is what guards us; it correctly
// detects every cycle within MaxCycleWalkDepth and reports the full
// member list along the cycle.
//
// Expected:
//   - root is the manifest to start the walk from; nil is treated as
//     a no-op (acyclic).
//   - swarms is the id -> manifest snapshot of every registered swarm
//     to follow when a member id resolves to another swarm.
//
// Returns:
//   - nil when no cycle is reachable from root within MaxCycleWalkDepth.
//   - A *ValidationError naming every id along the cycle otherwise.
//
// Side effects:
//   - None.
func cycleCheck(root *Manifest, swarms map[string]*Manifest) error {
	if root == nil {
		return nil
	}
	visited := make(map[string]bool)
	stack := []string{root.ID}
	stackSet := map[string]bool{root.ID: true}

	if cycle := dfsForCycle(root, swarms, visited, stack, stackSet, 0); cycle != nil {
		return &ValidationError{
			Field:   "members",
			Message: fmt.Sprintf("cycle detected: %s", strings.Join(cycle, " -> ")),
		}
	}
	return nil
}

// MaxCycleWalkDepth bounds the DFS in cycleCheck. Picked at 64 because
// the addendum §7 A4 "depth cap" tops out at 32 for orchestration
// swarms, doubled here so a borderline-legal real graph still walks to
// completion before this guard fires. A misconfigured graph that hits
// this bound is treated as "cycle suspected" — the typed error names
// the depth so users distinguish a wedged walker from a real cycle.
const MaxCycleWalkDepth = 64

// dfsForCycle is the recursive worker for cycleCheck. Returns the full
// cycle path (including the repeating id at the end) when one is
// found; returns nil when the subtree rooted at `node` is acyclic. The
// stack/stackSet pair tracks the *current* DFS path so re-entry is
// detected without confusing it with a finished sibling subtree.
//
// Expected:
//   - node is a non-nil Manifest reachable through the swarms map.
//   - swarms / visited / stack / stackSet are all non-nil and consistent.
//   - depth is the current recursion depth, capped at MaxCycleWalkDepth.
//
// Returns:
//   - A slice listing every id on the cycle (or the depth-cap marker)
//     when a re-entry is found.
//   - nil when the subtree rooted at node is acyclic.
//
// Side effects:
//   - Mutates visited, stack, and stackSet during recursion; restores
//     stack/stackSet before returning so callers see a clean state.
func dfsForCycle(node *Manifest, swarms map[string]*Manifest, visited map[string]bool, stack []string, stackSet map[string]bool, depth int) []string {
	if depth > MaxCycleWalkDepth {
		// Treat depth-cap hits as a suspected cycle so users still get
		// a diagnostic; a non-cyclic real graph hits this only when the
		// nesting exceeds A4's worst-case configurable limit.
		return append(append([]string{}, stack...), fmt.Sprintf("<depth-cap %d>", MaxCycleWalkDepth))
	}
	visited[node.ID] = true
	for _, member := range node.Members {
		if stackSet[member] {
			// Found re-entry into the current path: emit the full
			// cycle list (every id from the first occurrence to the
			// repeating tail) so the user sees the loop's full shape.
			cycle := append([]string{}, stack...)
			cycle = append(cycle, member)
			return cycle
		}
		child, ok := swarms[member]
		if !ok {
			// Member resolves to an agent (or to nothing — the
			// registry-aware members check has already vetted that
			// independently). Either way, it cannot extend the cycle
			// from here.
			continue
		}
		if visited[child.ID] {
			continue
		}
		stack = append(stack, child.ID)
		stackSet[child.ID] = true
		if cycle := dfsForCycle(child, swarms, visited, stack, stackSet, depth+1); cycle != nil {
			return cycle
		}
		stack = stack[:len(stack)-1]
		delete(stackSet, child.ID)
	}
	return nil
}

