// Package swarm — gate dispatch surface (T-swarm-3).
//
// The swarm spec §3 defines two gate scopes that fire around member
// execution within a swarm run:
//
//   - Swarm-level orchestration gates (when: pre / post). Fire once at
//     swarm start and once at swarm end with no specific target.
//   - Agent-level behavioural-contract gates (when: pre-member /
//     post-member). Fire around each invocation of a specific target
//     agent within the swarm.
//
// Phase 2 (this revision) covers all four lifecycle points and adds the
// directory-based JSON-Schema discovery path on top of the Phase 1
// foundation.
//
// Current scope:
//
//   - One gate kind: "builtin:result-schema". Validates the most-recent
//     value the target member wrote to the coordination_store against a
//     JSON Schema named by GateSpec.SchemaRef. Schemas resolve through
//     an in-process registry (see RegisterSchema) seeded both
//     programmatically (SeedDefaultSchemas) and from
//     `${ConfigDir}/schemas/*.json` at app startup; the file-based pass
//     wins on collision so operators can override built-in seeds with
//     an explicit drop-in.
//   - Four lifecycle points: when="pre" / when="post" (swarm-level,
//     fired once around the member-iteration loop) and
//     when="pre-member" / when="post-member" (member-level, fired
//     around the targeted member's stream).
//   - Pass/fail semantics. On fail, the gate runner returns a typed
//     *GateError carrying the gate name, lifecycle point, and member
//     id. The swarm runner halts fail-fast; there is no retry or
//     rollback in Phase 2.
//
// Phase 3+ deferred (TODOs):
//
//   - kind: "ext:*" — dispatch through the Extension API v1 backend.
//     Needs the v1 spec to land first.
//   - Retry / rollback semantics on gate failure (currently fail-fast).
package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/baphled/flowstate/internal/coordination"
)

// Lifecycle point constants. Each value matches the §3 spec string the
// manifest uses for GateSpec.When; keeping them as named constants
// keeps callers that branch on the lifecycle obvious to read and lets
// the manifest validator pin the exact set of legal values.
const (
	// LifecyclePreSwarm fires ONCE at swarm start, before the first
	// member runs. Typical use: validate the swarm context envelope
	// (e.g. chain_prefix is non-empty, required coordination_store
	// keys are seeded by the user before delegation).
	LifecyclePreSwarm = "pre"

	// LifecyclePostSwarm fires ONCE at swarm end, after the last
	// member returns. Typical use: validate the final aggregated
	// state across members.
	LifecyclePostSwarm = "post"

	// LifecyclePreMember fires before a specific member runs. Has a
	// Target. Typical use: validate that prerequisite outputs from
	// upstream members exist in coord-store.
	LifecyclePreMember = "pre-member"

	// LifecyclePostMember fires after a specific member's stream
	// completes. Has a Target. Typical use: validate the member's
	// terminal output against a JSON Schema (Phase 1's seed case).
	LifecyclePostMember = "post-member"
)

// legalLifecyclePoints is the canonical set of "when" values the
// manifest validator and the runner accept. Lookup-by-map keeps the
// validate path O(1) without re-listing the constants.
var legalLifecyclePoints = map[string]struct{}{
	LifecyclePreSwarm:   {},
	LifecyclePostSwarm:  {},
	LifecyclePreMember:  {},
	LifecyclePostMember: {},
}

// IsSwarmLifecyclePoint reports whether when names a swarm-level
// (boundary) lifecycle point. Swarm-level gates fire once per swarm
// run and MUST NOT carry a Target; the manifest validator uses this
// helper to enforce that rule, and the runner uses it to know whether
// to fan out by member id.
//
// Expected:
//   - when is the GateSpec.When string from the manifest.
//
// Returns:
//   - true when when is "pre" or "post".
//   - false otherwise (including the empty string and unknown values).
//
// Side effects:
//   - None.
func IsSwarmLifecyclePoint(when string) bool {
	return when == LifecyclePreSwarm || when == LifecyclePostSwarm
}

// IsMemberLifecyclePoint reports whether when names a member-level
// (per-target) lifecycle point. Member-level gates require a non-empty
// Target so the runner knows which member's stream to wrap.
//
// Expected:
//   - when is the GateSpec.When string from the manifest.
//
// Returns:
//   - true when when is "pre-member" or "post-member".
//   - false otherwise.
//
// Side effects:
//   - None.
func IsMemberLifecyclePoint(when string) bool {
	return when == LifecyclePreMember || when == LifecyclePostMember
}

// DefaultMemberOutputKey is the coord-store sub-key the result-schema
// runner reads when the manifest does not pin one explicitly. A member
// writes its terminal output under "<chainPrefix>/<memberID>/<key>",
// where <key> is GateSpec.OutputKey when set on the manifest, the
// legacy "review" convention for plan-reviewer, or this default
// otherwise. See candidateKeys in gate_result_schema.go for the full
// resolution order.
const DefaultMemberOutputKey = "output"

// GateRunner dispatches a single gate against the runtime state and
// reports pass/fail via a returned error. Phase 1 has exactly one
// production implementation (builtin:result-schema) plus the
// MultiRunner that selects between registered backends by Kind prefix.
//
// Implementations are expected to be cheap (no network calls in Phase
// 1) so they can fire synchronously inside the swarm runner without
// stalling the streaming hot path.
type GateRunner interface {
	// Run validates the gate against args. Returns nil on pass; a
	// non-nil error on fail. Implementations SHOULD return a *GateError
	// (or wrap one with errors.As-friendly semantics) so the swarm
	// runner can surface the structured failure detail to users.
	Run(ctx context.Context, gate GateSpec, args GateArgs) error
}

// GateArgs carries everything a runner needs to evaluate a gate
// without leaking the engine type into the swarm package. The runner
// reads the latest member-output value from CoordStore using
// ChainPrefix + MemberID; the SwarmID is included for log/event
// correlation in the GateError surface.
type GateArgs struct {
	// SwarmID is the resolved swarm id (the user-facing name typed
	// after `@`). Used in GateError messages for log correlation.
	SwarmID string

	// ChainPrefix is the coordination_store namespace prefix the
	// runner should use when reading member outputs. Comes from
	// swarm.Context.ChainPrefix; never empty when the runner is
	// dispatched from a real swarm run.
	ChainPrefix string

	// MemberID is the agent id whose stream just completed. For
	// post-member gates, this MUST equal the matching gate's Target;
	// the swarm runner is responsible for that filter.
	MemberID string

	// CoordStore is a read handle on the active coordination store.
	// The runner only calls Get; it never writes. A nil store is
	// treated as "no value available" and surfaces as a typed
	// *GateError with reason "coordination store unavailable".
	CoordStore coordination.Store
}

// GateError is the structured failure type returned from gate runners.
// The swarm runner halts on a *GateError without retry — the fields
// are intentionally explicit so user-facing surfaces (TUI activity
// pane, CLI stderr) can format the failure without parsing.
type GateError struct {
	// GateName is the manifest-supplied name of the failing gate.
	GateName string

	// GateKind is the kind string (e.g. "builtin:result-schema") so
	// log filters can group failures by family.
	GateKind string

	// When is the lifecycle point at which the gate fired ("pre",
	// "post", "pre-member", "post-member"). Surfaced on the error so
	// log readers can tell a swarm-boundary failure from a per-member
	// failure without inspecting the manifest.
	When string

	// SwarmID identifies the swarm run that produced the failure.
	SwarmID string

	// MemberID is the agent whose output failed validation. Empty for
	// swarm-level ("pre" / "post") gates because those have no target.
	MemberID string

	// Reason is a short human-readable explanation of the failure
	// (e.g. "schema validation failed: required property "verdict"
	// missing"). Callers concatenate this onto the gate context when
	// reporting to users.
	Reason string

	// Cause is the underlying error when one exists (e.g. the
	// jsonschema validation error). Surfaced via Unwrap so
	// errors.Is / errors.As work transparently.
	Cause error

	// ExtEvidence is populated by ext:<name> gate failures and carries
	// the snippet/source/similarity payload the gate returned alongside
	// its pass:false verdict. Empty for builtin gates.
	ExtEvidence []ExtGateEvidence
}

// Error renders the structured fields in a stable "gate <name> (<when>
// [<target>]) failed for <scope>: <reason>" shape so test matchers can
// pin the full message format. The lifecycle point is included on the
// front so "post-member explorer" and "pre-swarm" failures are
// distinguishable at a glance in logs and the CLI failure surface.
//
// Returns:
//   - The formatted error string.
//
// Side effects:
//   - None.
func (e *GateError) Error() string {
	if e == nil {
		return "<nil GateError>"
	}
	descriptor := e.GateKind
	if e.When != "" && e.MemberID != "" {
		descriptor = fmt.Sprintf("%s %s %s", e.GateKind, e.When, e.MemberID)
	} else if e.When != "" {
		descriptor = fmt.Sprintf("%s %s", e.GateKind, e.When)
	}
	scope := fmt.Sprintf("swarm %q", e.SwarmID)
	if e.MemberID != "" {
		scope = fmt.Sprintf("member %q in swarm %q", e.MemberID, e.SwarmID)
	}
	return fmt.Sprintf("gate %q (%s) failed for %s: %s", e.GateName, descriptor, scope, e.Reason)
}

// Unwrap exposes the underlying cause so errors.Is / errors.As work
// across the failure boundary. Returns nil when no cause is attached.
//
// Returns:
//   - The wrapped error or nil.
//
// Side effects:
//   - None.
func (e *GateError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// MultiRunner dispatches gates by Kind to registered backend runners.
// Phase 1 registers exactly one backend ("builtin:result-schema");
// future phases register the Extension API v1 dispatcher under
// "ext:*" and any additional builtins as they land.
//
// The dispatch is exact-match on Kind today. The validator (see
// validateGates in manifest.go) already enforces the "builtin:" /
// "ext:" prefix, so a Kind that reaches the runner is well-formed —
// MultiRunner only has to look it up.
type MultiRunner struct {
	mu       sync.RWMutex
	backends map[string]GateRunner
}

// NewMultiRunner returns a MultiRunner with no backends registered.
// Callers chain Register calls before handing it to the swarm runner.
//
// Returns:
//   - An empty *MultiRunner ready for Register.
//
// Side effects:
//   - None.
func NewMultiRunner() *MultiRunner {
	return &MultiRunner{backends: make(map[string]GateRunner)}
}

// Register installs runner under kind. A second Register call with
// the same kind overwrites the previous entry — the production wiring
// only registers each kind once at app-startup, so overwrite
// semantics keep tests cheap (no "already registered" plumbing).
//
// Expected:
//   - kind is non-empty (e.g. "builtin:result-schema"). Empty kinds
//     are silently ignored so a misconfigured caller cannot wedge the
//     dispatcher.
//   - runner is non-nil. Nil runners are silently ignored for the
//     same reason.
//
// Side effects:
//   - Stores the runner under m's internal map under the write lock.
func (m *MultiRunner) Register(kind string, runner GateRunner) {
	if kind == "" || runner == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.backends[kind] = runner
}

// Run dispatches gate to the registered runner whose key matches
// gate.Kind. Returns a *GateError with reason "no runner registered
// for kind <kind>" when no backend is registered — that surface is
// the same one used for runtime validation failures so callers can
// branch uniformly on *GateError.
//
// Expected:
//   - gate is a populated GateSpec the swarm runner has already
//     filtered by lifecycle / target.
//   - args carries a non-nil CoordStore in production wiring; nil is
//     allowed and surfaces as a typed gate failure rather than a
//     panic.
//
// Returns:
//   - nil when the dispatched runner reports pass.
//   - A *GateError when no runner is registered for the kind, or
//     when the dispatched runner reports fail.
//
// Side effects:
//   - Calls the registered runner's Run, which may read from the
//     coordination store inside args.
func (m *MultiRunner) Run(ctx context.Context, gate GateSpec, args GateArgs) error {
	m.mu.RLock()
	runner, ok := m.backends[gate.Kind]
	m.mu.RUnlock()
	if !ok {
		return &GateError{
			GateName: gate.Name,
			GateKind: gate.Kind,
			When:     gate.When,
			SwarmID:  args.SwarmID,
			MemberID: args.MemberID,
			Reason:   fmt.Sprintf("no runner registered for kind %q", gate.Kind),
		}
	}
	return runner.Run(ctx, gate, args)
}

// schemaRegistry is the Phase 1 in-process JSON-schema lookup table.
// Keys are the SchemaRef strings appearing on GateSpec.SchemaRef in
// manifests (e.g. "review-verdict-v1"); values are pre-resolved
// jsonschema documents. The registry is concurrency-safe because the
// CLI may register schemas during app construction while a long-lived
// engine reads them from a background goroutine.
//
// Phase 2 will replace the global with a SchemaResolver interface so
// the schemas/ directory loader can plug in without taking a build-
// time dependency on this package.
var schemaRegistry = struct {
	mu      sync.RWMutex
	schemas map[string]*jsonschema.Resolved
}{
	schemas: make(map[string]*jsonschema.Resolved),
}

// RegisterSchema installs schema under name in the Phase 1 registry.
// A second call with the same name overwrites the prior entry — the
// production wiring registers each schema once at app construction.
//
// Expected:
//   - name is the SchemaRef string the manifest references; non-empty.
//   - schema is a non-nil jsonschema.Schema. The caller pre-validates
//     by calling schema.Resolve before Register; the registered value
//     is the resolved form so runners do not pay re-resolution cost
//     on every gate dispatch.
//
// Returns:
//   - nil on success.
//   - An error when name is empty or schema is nil. Errors here are
//     programmer mistakes — the production wiring keeps the call
//     site to a small, audited block.
//
// Side effects:
//   - Stores the resolved schema under schemaRegistry's write lock.
func RegisterSchema(name string, schema *jsonschema.Schema) error {
	if name == "" {
		return errors.New("swarm.RegisterSchema: name must be non-empty")
	}
	if schema == nil {
		return errors.New("swarm.RegisterSchema: schema must be non-nil")
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("swarm.RegisterSchema: resolving %q: %w", name, err)
	}
	schemaRegistry.mu.Lock()
	defer schemaRegistry.mu.Unlock()
	schemaRegistry.schemas[name] = resolved
	return nil
}

// LookupSchema returns the resolved schema registered under name and
// a presence boolean. Used by gate_result_schema.go to validate
// member outputs without re-resolving the schema each call.
//
// Expected:
//   - name is a non-empty SchemaRef.
//
// Returns:
//   - The resolved schema and true when registered.
//   - (nil, false) when name is unknown.
//
// Side effects:
//   - None (read-only access under the registry's RLock).
func LookupSchema(name string) (*jsonschema.Resolved, bool) {
	schemaRegistry.mu.RLock()
	defer schemaRegistry.mu.RUnlock()
	s, ok := schemaRegistry.schemas[name]
	return s, ok
}

// RegisteredSchemaNames returns a sorted snapshot of the schema
// registry's known names. Pulled out so UI surfaces (the /swarm
// builder's gate-schema picker, future config dumps) can enumerate the
// SchemaRef values a manifest may legally cite without reaching into
// the registry's private map. The slice is freshly allocated and safe
// for the caller to mutate.
//
// Returns:
//   - A sorted slice of the registered schema names; nil when empty.
//
// Side effects:
//   - None (read-only access under the registry's RLock).
func RegisteredSchemaNames() []string {
	schemaRegistry.mu.RLock()
	defer schemaRegistry.mu.RUnlock()
	if len(schemaRegistry.schemas) == 0 {
		return nil
	}
	out := make([]string, 0, len(schemaRegistry.schemas))
	for name := range schemaRegistry.schemas {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ClearSchemasForTest empties the Phase 1 registry. Tests use this in
// BeforeEach so a stray Register call from a sibling spec does not
// leak across test boundaries. Not exported under a non-_test name
// because production code never needs to clear the registry.
//
// Side effects:
//   - Replaces schemaRegistry.schemas with an empty map under the
//     write lock.
func ClearSchemasForTest() {
	schemaRegistry.mu.Lock()
	defer schemaRegistry.mu.Unlock()
	schemaRegistry.schemas = make(map[string]*jsonschema.Resolved)
}

// PostMemberGatesFor returns the gates from spec slice whose When is
// "post-member" and whose Target matches memberID. Thin wrapper over
// MemberGatesFor preserved for backwards compatibility with callers
// that pre-date the multi-lifecycle expansion.
//
// Expected:
//   - gates may be nil or empty; an empty slice is returned in that
//     case so callers can range over the result without a nil-guard.
//   - memberID is the agent id whose stream just completed.
//
// Returns:
//   - A new slice of matched gates; never nil.
//
// Side effects:
//   - None.
func PostMemberGatesFor(gates []GateSpec, memberID string) []GateSpec {
	return MemberGatesFor(gates, LifecyclePostMember, memberID)
}

// MemberGatesFor returns the gates whose When matches when (a member-
// level lifecycle point) and whose Target matches memberID. Used by
// the swarm runner to filter the manifest's gate slice down to the
// set firing around a specific member's stream.
//
// Expected:
//   - gates may be nil or empty; an empty slice is returned in that
//     case.
//   - when is "pre-member" or "post-member"; passing any other value
//     yields an empty slice (no member-level dispatch happens for the
//     swarm-level points).
//   - memberID is the agent id the runner is wrapping.
//
// Returns:
//   - A new slice of matched gates; never nil.
//
// Side effects:
//   - None.
func MemberGatesFor(gates []GateSpec, when, memberID string) []GateSpec {
	out := make([]GateSpec, 0)
	if !IsMemberLifecyclePoint(when) {
		return out
	}
	for _, g := range gates {
		if g.When == when && g.Target == memberID {
			out = append(out, g)
		}
	}
	return SortGatesByPrecedence(out)
}

// SwarmGatesFor returns the gates whose When matches when (a swarm-
// level lifecycle point). Swarm-level gates do not carry a Target —
// the manifest validator rejects manifests that violate that rule —
// so a single filter on When suffices.
//
// Expected:
//   - gates may be nil or empty; an empty slice is returned in that
//     case.
//   - when is "pre" or "post"; passing any other value yields an
//     empty slice.
//
// Returns:
//   - A new slice of matched gates; never nil.
//
// Side effects:
//   - None.
func SwarmGatesFor(gates []GateSpec, when string) []GateSpec {
	out := make([]GateSpec, 0)
	if !IsSwarmLifecyclePoint(when) {
		return out
	}
	for _, g := range gates {
		if g.When == when {
			out = append(out, g)
		}
	}
	return SortGatesByPrecedence(out)
}

// GateInput is the runtime payload supplied to a gate at dispatch.
// runBuiltinGate validates Payload directly against the schema named
// by the GateSpec; the ext: arm forwards it onto the subprocess via
// DispatchExt. The shape is intentionally narrow — keep it close to
// the wire-shape ExtGateRequest exposes so the dispatch switch is a
// straight projection rather than a translation.
type GateInput struct {
	MemberID string
	Payload  []byte
	Policy   map[string]any
}

// RunGateForTest is exposed only for in-package and adjacent
// (swarm_test) tests that need to exercise the kind-routing
// dispatcher in isolation. Production code reaches the same logic
// through the engine's delegation path, which keeps using the
// MultiRunner+GateArgs surface to read coord-store state.
func RunGateForTest(ctx context.Context, spec GateSpec, in GateInput) error {
	return runGateByKind(ctx, spec, in)
}

// runGateByKind is the dispatch switch routing spec.Kind to either the
// in-process builtin runner or the ext:* DispatchExt path. New gate
// families plug in here; everything else is bookkeeping.
func runGateByKind(ctx context.Context, spec GateSpec, in GateInput) error {
	switch {
	case strings.HasPrefix(spec.Kind, gateKindBuiltinPrefix):
		return runBuiltinGate(ctx, spec, in)
	case strings.HasPrefix(spec.Kind, gateKindExtPrefix):
		return DispatchExt(ctx, spec.Kind, ExtGateRequest{
			MemberID: in.MemberID,
			When:     spec.When,
			Payload:  in.Payload,
			Policy:   in.Policy,
		})
	default:
		return fmt.Errorf("gate %q: unknown kind family %q", spec.Name, spec.Kind)
	}
}

// runBuiltinGate handles the builtin:result-schema family. It
// validates in.Payload directly against the schema named by
// spec.SchemaRef. Failure paths return a *GateError shaped the same
// way the MultiRunner+CoordStore path does so callers downstream of
// runGateByKind can branch uniformly on *GateError.
func runBuiltinGate(_ context.Context, spec GateSpec, in GateInput) error {
	if spec.Kind != "builtin:result-schema" {
		return fmt.Errorf("gate %q: unsupported builtin kind %q", spec.Name, spec.Kind)
	}
	if spec.SchemaRef == "" {
		return &GateError{
			GateName: spec.Name,
			GateKind: spec.Kind,
			When:     spec.When,
			MemberID: in.MemberID,
			Reason:   "missing schema_ref on builtin:result-schema gate",
		}
	}
	resolved, ok := LookupSchema(spec.SchemaRef)
	if !ok {
		return &GateError{
			GateName: spec.Name,
			GateKind: spec.Kind,
			When:     spec.When,
			MemberID: in.MemberID,
			Reason:   fmt.Sprintf("schema_ref %q is not registered", spec.SchemaRef),
		}
	}
	instance, err := decodeJSONInstance(in.Payload)
	if err != nil {
		return &GateError{
			GateName: spec.Name,
			GateKind: spec.Kind,
			When:     spec.When,
			MemberID: in.MemberID,
			Reason:   err.Error(),
			Cause:    err,
		}
	}
	if err := resolved.Validate(instance); err != nil {
		return &GateError{
			GateName: spec.Name,
			GateKind: spec.Kind,
			When:     spec.When,
			MemberID: in.MemberID,
			Reason:   fmt.Sprintf("schema validation failed: %s", err.Error()),
			Cause:    err,
		}
	}
	return nil
}

// decodeJSONInstance unmarshals a coord-store payload into the
// shape jsonschema.Resolved.Validate expects (a tree of map / slice /
// scalar values). Pulled into a helper so the result-schema runner
// stays focused on the validation flow.
//
// Expected:
//   - payload is the raw byte slice read from the coordination store.
//
// Returns:
//   - The decoded value (typically map[string]any) and nil on success.
//   - nil and a wrapped error when payload is not valid JSON.
//
// Side effects:
//   - None.
func decodeJSONInstance(payload []byte) (any, error) {
	var instance any
	if err := json.Unmarshal(payload, &instance); err != nil {
		return nil, fmt.Errorf("decoding member output as JSON: %w", err)
	}
	return instance, nil
}
