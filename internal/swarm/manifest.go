package swarm

import (
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
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

// Precedence enumerates the four ratified gate-precedence levels from
// addendum §7 A6. Higher precedence gates run before lower precedence
// gates within the same lifecycle point; ties preserve manifest
// declaration order (the modal-registry pattern A6 cites). The string
// representation matches the YAML enum so a manifest round-trip is
// lossless.
type Precedence string

const (
	// PrecedenceCritical names the highest precedence — security
	// gates per A6's table. Runs before HIGH/MEDIUM/LOW.
	PrecedenceCritical Precedence = "CRITICAL"
	// PrecedenceHigh names the second-highest — safety gates per A6.
	PrecedenceHigh Precedence = "HIGH"
	// PrecedenceMedium is the default precedence applied when a gate
	// omits the field. Quality gates per A6.
	PrecedenceMedium Precedence = "MEDIUM"
	// PrecedenceLow names the lowest precedence — optional checks
	// per A6.
	PrecedenceLow Precedence = "LOW"
)

// DefaultPrecedence is applied when a gate omits the field in YAML.
// Pinned at MEDIUM per A6's table so existing manifests written
// against the v1 schema retain their original ordering semantics.
const DefaultPrecedence = PrecedenceMedium

// legalPrecedences is the canonical lookup used by validatePrecedence
// to reject unknown enum values at load time. The empty string is
// accepted (treated as MEDIUM via DefaultPrecedence) so older
// manifests remain valid.
var legalPrecedences = map[Precedence]struct{}{
	PrecedenceCritical: {},
	PrecedenceHigh:     {},
	PrecedenceMedium:   {},
	PrecedenceLow:      {},
}

// FailurePolicy enumerates the per-gate failure-handling modes from
// addendum §7 A1. The default is halt (matching today's behaviour
// where every gate failure halts the swarm); continue and warn relax
// that for non-critical checks. Stored as the YAML enum string for a
// lossless manifest round-trip.
type FailurePolicy string

const (
	// FailurePolicyHalt halts the swarm on the first gate failure.
	// This is today's behaviour and the default when the manifest
	// omits failurePolicy.
	FailurePolicyHalt FailurePolicy = "halt"
	// FailurePolicyContinue records the failure but allows subsequent
	// gates and members to run. Used for non-blocking validation
	// (e.g. observability sampling).
	FailurePolicyContinue FailurePolicy = "continue"
	// FailurePolicyWarn behaves like continue but tags the failure
	// as a warning so downstream surfaces can render it differently.
	FailurePolicyWarn FailurePolicy = "warn"
)

// DefaultFailurePolicy is applied when a gate omits the field. Halt
// preserves backwards compatibility with manifests that pre-date A1.
const DefaultFailurePolicy = FailurePolicyHalt

// legalFailurePolicies is the canonical lookup used by
// validateFailurePolicy. The empty string is accepted (treated as
// halt via DefaultFailurePolicy) so legacy manifests stay valid.
var legalFailurePolicies = map[FailurePolicy]struct{}{
	FailurePolicyHalt:     {},
	FailurePolicyContinue: {},
	FailurePolicyWarn:     {},
}

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

	// SwarmType selects the per-type depth default per addendum §7 A4.
	// Legal values: SwarmTypeAnalysis (default) / SwarmTypeCodegen /
	// SwarmTypeOrchestration. An empty value is treated as analysis so
	// existing manifests stay forward-compatible without an edit.
	SwarmType SwarmType `json:"swarm_type,omitempty" yaml:"swarm_type,omitempty"`

	// MaxDepth is an optional manifest-level override for the
	// delegation-depth ceiling. Zero means "use the per-type default
	// from DefaultMaxDepthForType(SwarmType)"; a positive value pins
	// the cap regardless of swarm_type. Negative values are rejected
	// at validation time.
	MaxDepth int `json:"max_depth,omitempty" yaml:"max_depth,omitempty"`

	// Harness configures the swarm-runner-level execution policy:
	// parallel-vs-sequential member dispatch, the parallelism ceiling,
	// and the swarm-scoped gates evaluated at swarm/member boundaries.
	Harness HarnessConfig `json:"harness" yaml:"harness"`

	// Context configures coordination_store namespacing for the run.
	// When ChainPrefix is empty the swarm id is used; pinning it
	// explicitly lets users keep documented chain keys stable across
	// renames.
	Context ContextConfig `json:"context" yaml:"context"`

	// Retry configures the per-member retry policy applied when a
	// dispatch returns a CategoryRetryable error. Pointer so the
	// loader can distinguish "block omitted" (nil; defaults apply)
	// from "block present with explicit zeros" (validated). See §7 A2
	// of the swarm-manifest addendum.
	Retry *RetryPolicy `json:"retry,omitempty" yaml:"retry,omitempty"`

	// CircuitBreaker configures the swarm-wide circuit breaker the
	// runner trips when consecutive retryable failures hit Threshold.
	// Same nil-vs-zero contract as Retry above.
	CircuitBreaker *CircuitBreakerConfig `json:"circuit_breaker,omitempty" yaml:"circuit_breaker,omitempty"`
}

// EffectiveRetryPolicy returns the manifest's RetryPolicy with empty
// fields filled in from the package defaults. The runner consults
// this method rather than the raw Retry field so behaviour stays
// stable when authors leave optional knobs unset. A nil Retry block
// yields an all-defaults policy with Jitter enabled.
//
// Returns:
//   - A RetryPolicy with all fields populated.
//
// Side effects:
//   - None.
func (m *Manifest) EffectiveRetryPolicy() RetryPolicy {
	if m.Retry == nil {
		return RetryPolicy{
			MaxAttempts:    DefaultRetryMaxAttempts,
			InitialBackoff: DefaultRetryInitialBackoff,
			MaxBackoff:     DefaultRetryMaxBackoff,
			Multiplier:     DefaultRetryMultiplier,
			Jitter:         true,
		}
	}
	out := *m.Retry
	if out.MaxAttempts < 1 {
		out.MaxAttempts = DefaultRetryMaxAttempts
	}
	if out.InitialBackoff <= 0 {
		out.InitialBackoff = DefaultRetryInitialBackoff
	}
	if out.MaxBackoff <= 0 {
		out.MaxBackoff = DefaultRetryMaxBackoff
	}
	if out.Multiplier <= 0 {
		out.Multiplier = DefaultRetryMultiplier
	}
	return out
}

// EffectiveCircuitBreaker returns the manifest's CircuitBreakerConfig
// with empty fields filled in from the package defaults. A nil block
// yields the all-defaults breaker configuration.
//
// Returns:
//   - A CircuitBreakerConfig with all fields populated.
//
// Side effects:
//   - None.
func (m *Manifest) EffectiveCircuitBreaker() CircuitBreakerConfig {
	if m.CircuitBreaker == nil {
		return CircuitBreakerConfig{
			Threshold:        DefaultBreakerThreshold,
			Cooldown:         DefaultBreakerCooldown,
			HalfOpenAttempts: DefaultBreakerHalfOpenAttempts,
		}
	}
	out := *m.CircuitBreaker
	if out.Threshold < 1 {
		out.Threshold = DefaultBreakerThreshold
	}
	if out.Cooldown <= 0 {
		out.Cooldown = DefaultBreakerCooldown
	}
	if out.HalfOpenAttempts < 1 {
		out.HalfOpenAttempts = DefaultBreakerHalfOpenAttempts
	}
	return out
}

// HarnessConfig groups the swarm-runner-level execution settings on a
// manifest. Parallel and MaxParallel feed swarm.DispatchMembers (the
// §T37 dispatch surface) which honours strict sequential mode by
// default and switches to bounded-fan-out goroutines when Parallel is
// true.
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

	// OutputKey is the coord-store sub-key under which the runner reads
	// the target member's output. The full lookup key is
	// "<chainPrefix>/<target>/<output_key>". Optional: when empty the
	// runner falls back to DefaultMemberOutputKey ("output"). Pinning an
	// explicit key keeps the gate readable from the manifest alone —
	// readers do not have to consult the agent prompt to learn which
	// coord-store slot the gate validates.
	OutputKey string `json:"output_key,omitempty" yaml:"output_key,omitempty"`

	// Precedence selects the evaluation tier per addendum §7 A6.
	// CRITICAL gates run before HIGH which run before MEDIUM which run
	// before LOW; ties preserve manifest order. Empty falls back to
	// DefaultPrecedence (MEDIUM) so legacy manifests keep their
	// declaration-order semantics.
	Precedence Precedence `json:"precedence,omitempty" yaml:"precedence,omitempty"`

	// FailurePolicy selects the per-gate failure handling per A1.
	// halt (default) stops the swarm on failure; continue records the
	// failure but lets the swarm continue; warn behaves like continue
	// and tags the failure as a warning for log/UI distinction.
	FailurePolicy FailurePolicy `json:"failurePolicy,omitempty" yaml:"failurePolicy,omitempty"`

	// Timeout caps a single gate's runtime. Zero means "no per-gate
	// timeout" (the dispatcher passes the parent context through
	// unchanged). On expiry the gate is treated as failed and
	// FailurePolicy decides whether the swarm halts. Parses YAML
	// scalars like "5s" / "30s" via the time.Duration unmarshaller
	// installed below.
	Timeout time.Duration `json:"timeout,omitempty" yaml:"timeout,omitempty"`
}

// UnmarshalYAML parses a GateSpec from YAML and translates the
// Timeout field from a duration scalar (e.g. "5s") to time.Duration.
// time.Duration's default yaml unmarshaller treats the field as an
// integer count of nanoseconds; A1's spec text uses the human
// "5s" / "30s" form so the custom unmarshaller bridges them.
//
// Expected:
//   - value is a YAML mapping node produced by gopkg.in/yaml.v3 for a
//     single gate entry.
//
// Returns:
//   - nil on a successful parse.
//   - The wrapped yaml.v3 / time.ParseDuration error otherwise.
//
// Side effects:
//   - Mutates the receiver's fields in place.
func (g *GateSpec) UnmarshalYAML(value *yaml.Node) error {
	type rawGateSpec struct {
		Name          string        `yaml:"name"`
		Kind          string        `yaml:"kind"`
		SchemaRef     string        `yaml:"schema_ref"`
		When          string        `yaml:"when"`
		Target        string        `yaml:"target"`
		OutputKey     string        `yaml:"output_key"`
		Precedence    Precedence    `yaml:"precedence"`
		FailurePolicy FailurePolicy `yaml:"failurePolicy"`
		Timeout       string        `yaml:"timeout"`
	}
	var raw rawGateSpec
	if err := value.Decode(&raw); err != nil {
		return err
	}
	g.Name = raw.Name
	g.Kind = raw.Kind
	g.SchemaRef = raw.SchemaRef
	g.When = raw.When
	g.Target = raw.Target
	g.OutputKey = raw.OutputKey
	g.Precedence = raw.Precedence
	g.FailurePolicy = raw.FailurePolicy
	if raw.Timeout != "" {
		d, err := time.ParseDuration(raw.Timeout)
		if err != nil {
			return fmt.Errorf("gate %q: parsing timeout %q: %w", raw.Name, raw.Timeout, err)
		}
		g.Timeout = d
	}
	return nil
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

	if err := m.validateSwarmType(); err != nil {
		return err
	}

	if err := m.validateMaxDepth(); err != nil {
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

	if err := m.validateGates(); err != nil {
		return err
	}

	return m.validateResilience()
}

// validateResilience enforces the §7 A2 rules on the optional retry
// and circuit-breaker blocks. Authors may omit either block entirely
// (defaults apply via EffectiveRetryPolicy / EffectiveCircuitBreaker);
// when they include a block, every populated field must be sensible
// (no zero-or-negative attempts, no negative thresholds, etc).
func (m *Manifest) validateResilience() error {
	if err := validateRetryBlock(m.Retry); err != nil {
		return err
	}
	return validateBreakerBlock(m.CircuitBreaker)
}

// validateRetryBlock enforces the per-field invariants on a present
// retry block. A nil receiver short-circuits to nil because "block
// omitted" inherits package defaults.
func validateRetryBlock(r *RetryPolicy) error {
	if r == nil {
		return nil
	}
	if r.MaxAttempts < 1 {
		return &ValidationError{Field: "retry.max_attempts", Message: "must be >= 1"}
	}
	if r.InitialBackoff < 0 {
		return &ValidationError{Field: "retry.initial_backoff", Message: "must be non-negative"}
	}
	if r.MaxBackoff < 0 {
		return &ValidationError{Field: "retry.max_backoff", Message: "must be non-negative"}
	}
	if r.Multiplier < 0 {
		return &ValidationError{Field: "retry.multiplier", Message: "must be non-negative"}
	}
	return nil
}

// validateBreakerBlock enforces the per-field invariants on a present
// circuit-breaker block. A nil receiver short-circuits to nil for the
// same reason as validateRetryBlock.
func validateBreakerBlock(b *CircuitBreakerConfig) error {
	if b == nil {
		return nil
	}
	if b.Threshold < 1 {
		return &ValidationError{Field: "circuit_breaker.threshold", Message: "must be >= 1"}
	}
	if b.Cooldown < 0 {
		return &ValidationError{Field: "circuit_breaker.cooldown", Message: "must be non-negative"}
	}
	if b.HalfOpenAttempts < 0 {
		return &ValidationError{Field: "circuit_breaker.half_open_attempts", Message: "must be >= 1"}
	}
	return nil
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

// validateGates enforces the kind-prefix and lifecycle rules on every
// gate. The kind family selects the dispatcher (T-swarm-3 for
// builtins, Extension API v1 for ext); the lifecycle rules pin the
// pairing between GateSpec.When and GateSpec.Target so the runner can
// dispatch unambiguously without re-validating manifest shape at
// firing time.
//
// Lifecycle rules (T-swarm-3 Phase 2):
//   - When may be empty (legacy, treated as "pre" by the runner) or
//     one of the four §3 values: "pre" / "post" / "pre-member" /
//     "post-member". Unknown values are rejected at load time so the
//     activity-pane error surface stays terse.
//   - Swarm-level points ("pre" / "post") MUST NOT carry a Target —
//     they fire once at swarm boundaries and have no per-member fan-
//     out. A non-empty Target paired with a swarm-level When is a
//     manifest authoring mistake; rejecting it at load time prevents
//     the runner from silently fanning out a swarm gate per member.
//   - Member-level points ("pre-member" / "post-member") MUST carry a
//     Target so the runner knows which member's stream to wrap.
//
// Expected:
//   - m is a non-nil Manifest pointer.
//
// Returns:
//   - nil when every gate has a non-empty name, a kind starting with
//     "builtin:" or "ext:", and a When/Target pairing that matches
//     the lifecycle rules above.
//   - A *ValidationError naming the first failing gate field otherwise.
//
// Side effects:
//   - None.
func (m *Manifest) validateGates() error {
	for i, gate := range m.Harness.Gates {
		if err := validateGateScalars(i, gate); err != nil {
			return err
		}
		if err := validateGateLifecycle(i, gate); err != nil {
			return err
		}
	}
	return nil
}

// validateGateScalars enforces the non-lifecycle invariants on a
// single gate (non-empty name, valid kind prefix). Pulled into a
// helper so validateGates' loop body stays focused on the per-gate
// pipeline.
//
// Expected:
//   - i is the gate's index in harness.gates (used only for error
//     field annotation).
//   - gate is the GateSpec being validated.
//
// Returns:
//   - nil when name and kind pass their checks.
//   - A *ValidationError naming the first failing field otherwise.
//
// Side effects:
//   - None.
func validateGateScalars(i int, gate GateSpec) error {
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
	if err := validateGatePrecedence(i, gate); err != nil {
		return err
	}
	if err := validateGateFailurePolicy(i, gate); err != nil {
		return err
	}
	return validateGateTimeout(i, gate)
}

// validateGatePrecedence rejects any precedence string that is neither
// empty (treated as the default at runtime) nor one of the four
// ratified A6 levels. Holding the rule here means the gate-runner
// never sees an unknown precedence at firing time.
//
// Expected:
//   - i is the gate index in harness.gates.
//   - gate is the GateSpec being validated.
//
// Returns:
//   - nil when precedence is empty or a legal A6 enum value.
//   - A *ValidationError naming the precedence field otherwise.
//
// Side effects:
//   - None.
func validateGatePrecedence(i int, gate GateSpec) error {
	if gate.Precedence == "" {
		return nil
	}
	if _, ok := legalPrecedences[gate.Precedence]; !ok {
		return &ValidationError{
			Field:   fmt.Sprintf("harness.gates[%d].precedence", i),
			Message: fmt.Sprintf("unknown precedence %q (expected CRITICAL, HIGH, MEDIUM, or LOW)", gate.Precedence),
		}
	}
	return nil
}

// validateGateFailurePolicy rejects any failurePolicy string that is
// neither empty (treated as halt at runtime) nor one of the three A1
// modes. Catching this at load means the dispatcher's policy switch
// never sees an unknown value.
//
// Expected:
//   - i is the gate index in harness.gates.
//   - gate is the GateSpec being validated.
//
// Returns:
//   - nil when failurePolicy is empty or a legal A1 enum value.
//   - A *ValidationError naming the failurePolicy field otherwise.
//
// Side effects:
//   - None.
func validateGateFailurePolicy(i int, gate GateSpec) error {
	if gate.FailurePolicy == "" {
		return nil
	}
	if _, ok := legalFailurePolicies[gate.FailurePolicy]; !ok {
		return &ValidationError{
			Field:   fmt.Sprintf("harness.gates[%d].failurePolicy", i),
			Message: fmt.Sprintf("unknown failurePolicy %q (expected halt, continue, or warn)", gate.FailurePolicy),
		}
	}
	return nil
}

// validateGateTimeout rejects negative timeouts. Zero is a legal
// sentinel meaning "no per-gate deadline"; positive values are
// applied verbatim by Dispatch via context.WithTimeout.
//
// Expected:
//   - i is the gate index in harness.gates.
//   - gate is the GateSpec being validated.
//
// Returns:
//   - nil when timeout is zero or positive.
//   - A *ValidationError naming the timeout field when negative.
//
// Side effects:
//   - None.
func validateGateTimeout(i int, gate GateSpec) error {
	if gate.Timeout < 0 {
		return &ValidationError{
			Field:   fmt.Sprintf("harness.gates[%d].timeout", i),
			Message: fmt.Sprintf("timeout must be non-negative (got %s)", gate.Timeout),
		}
	}
	return nil
}

// validateGateLifecycle enforces the When/Target pairing rules per
// the package-level lifecycle constants. An empty When is permitted
// (the runner defaults to "pre" by precedent — see GateSpec.When
// godoc); any other unknown value is rejected so the runner does not
// have to fall back at firing time.
//
// Expected:
//   - i is the gate's index in harness.gates.
//   - gate is the GateSpec being validated.
//
// Returns:
//   - nil when the lifecycle / target pairing is legal.
//   - A *ValidationError naming the offending field otherwise.
//
// Side effects:
//   - None.
func validateGateLifecycle(i int, gate GateSpec) error {
	if gate.When == "" {
		return nil
	}
	if _, ok := legalLifecyclePoints[gate.When]; !ok {
		return &ValidationError{
			Field:   fmt.Sprintf("harness.gates[%d].when", i),
			Message: fmt.Sprintf("unknown lifecycle point %q (expected one of \"pre\" / \"post\" / \"pre-member\" / \"post-member\")", gate.When),
		}
	}
	if IsSwarmLifecyclePoint(gate.When) && strings.TrimSpace(gate.Target) != "" {
		return &ValidationError{
			Field:   fmt.Sprintf("harness.gates[%d].target", i),
			Message: fmt.Sprintf("swarm-level gate (when=%q) must not specify a target (got %q)", gate.When, gate.Target),
		}
	}
	if IsMemberLifecyclePoint(gate.When) && strings.TrimSpace(gate.Target) == "" {
		return &ValidationError{
			Field:   fmt.Sprintf("harness.gates[%d].target", i),
			Message: fmt.Sprintf("member-level gate (when=%q) requires a target", gate.When),
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

