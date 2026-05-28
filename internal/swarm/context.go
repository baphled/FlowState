// Package swarm carries the runtime types T-swarm-2 needs to invoke a
// swarm via the existing `@<id>` chat-input parser and the `--agent`
// CLI flag.
//
// The resolver consumes T-swarm-1's concrete *Registry and *Manifest
// directly — no extra interface layer. Test fakes are expected to
// build real *Registry instances via NewRegistry + Register, which is
// no harder than constructing an interface fake.
package swarm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
)

// Context is the swarm-runtime envelope the runner constructs when an
// `@<swarm-id>` invocation lands. It travels into the lead engine via
// a context.Context value so engine-internal components (delegate
// tool, gate runner, activity-pane streamer) can read it without a
// new constructor argument on every site.
//
// IMMUTABILITY: Context values are immutable post-construction. The
// dispatcher hands the same Context value to N concurrent member
// closures (Task 2's fan-out + Task 5's sub-swarm recursion); any
// in-place mutation by a worker would race against its peers. Call
// sites that need to derive a child context (e.g. NestSubSwarm) MUST
// return a new value, not mutate the receiver.
//
// Fields mirror spec §2:
//   - SwarmID is the resolved swarm id (the user-facing name they
//     typed after `@`).
//   - LeadAgent is the agent id that fronts the swarm. It comes from
//     Manifest.Lead at construction time so the runner does not have
//     to hold the manifest pointer.
//   - Members is the delegation allowlist for the duration of the
//     run. It shadows the lead agent's normal delegation.allowlist
//     (spec §2).
//   - Gates is the harness-level gate slice carried verbatim from
//     the manifest. T-swarm-3 dispatches the post-member subset (the
//     only Phase 1 lifecycle point) after each matching member's
//     stream completes; pre / post / pre-member dispatch is Phase 2+.
//   - ChainPrefix is the coordination_store namespace prefix. Sub-
//     swarm composition (spec §4) layers child prefixes under
//     parents using `<parent>/<child>`. Defaults to the swarm id
//     when the manifest leaves it blank.
//   - Depth is the nesting level: 1 for the root context, 2 for the
//     first sub-swarm layer, etc. Set in NestSubSwarm so the
//     dispatcher can compare against Manifest.ResolveMaxDepth /
//     SpawnLimits.MaxTotalBudget without parsing the chain prefix.
type Context struct {
	SwarmID     string
	LeadAgent   string
	Members     []string
	Gates       []GateSpec
	ChainPrefix string
	Depth       int

	// ChainIDAssigned records that the engine stamped a per-run chainID
	// onto ChainPrefix at swarm start (AssignRunChainID), as opposed to
	// ChainPrefix carrying a static manifest prefix or the swarm-id
	// default. The post-swarm lifecycle uses this to decide whether
	// ChainPrefix is an authoritative per-run namespace it should publish
	// and gate against — when it is merely static, the publisher keeps its
	// suffix-scan fallback so legacy seeded-chain runs (no per-run id) are
	// unaffected. See ADR - Engine-Owned Workflow Mechanics (forward
	// decision).
	ChainIDAssigned bool
}

// NewContext constructs a Context from a resolved Manifest plus the
// id the user typed. The id is held verbatim (the user-facing handle)
// while LeadAgent / Members / ChainPrefix are pulled off the
// manifest. When the manifest leaves ChainPrefix blank the swarm id
// stands in (spec §1, `context.chain_prefix`).
//
// Expected:
//   - id is the swarm id resolved by Resolve; non-empty.
//   - m is the manifest the registry returned for id; non-nil.
//
// Returns:
//   - A populated Context. Gates carries m.Harness.Gates verbatim so
//     the swarm runner's gate dispatcher (T-swarm-3) sees the same
//     slice the manifest authored.
//
// Side effects:
//   - None.
func NewContext(id string, m *Manifest) Context {
	if m == nil {
		return Context{SwarmID: id}
	}
	prefix := m.Context.ChainPrefix
	if prefix == "" {
		prefix = id
	}
	return Context{
		SwarmID:     id,
		LeadAgent:   m.Lead,
		Members:     append([]string(nil), m.Members...),
		Gates:       append([]GateSpec(nil), m.Harness.Gates...),
		ChainPrefix: prefix,
		Depth:       1,
	}
}

// AssignRunChainID stamps a per-run coordination-store namespace onto
// the Context so the engine — not the LLM — owns the chainID. The
// planning loop's prior failure mode was the lead/coordinator picking a
// free-form chainID in prose while members invented unrelated prefixes
// (`mental-health-swarm-design/*` ≠ the run's chain), so gates resolved
// one namespace while the deliverable scattered under another. Assigning
// the chainID at run start closes that drift class at the source rather
// than catching its downstream effects (see ADR - Engine-Owned Workflow
// Mechanics, forward decision).
//
// The assignment is deterministic for a given runID (a short, stable
// hash suffix anchored under the swarm id), so the same run resolves the
// same namespace on every read while two concurrent runs of the same
// swarm never collide on coord-store keys.
//
// BACKWARDS COMPAT: the per-run id is only assigned when ChainPrefix is
// still the manifest default — i.e. equal to SwarmID, which is what
// NewContext sets when the manifest leaves chain_prefix blank. An
// operator who pinned an explicit chain_prefix in the manifest made a
// deliberate namespacing choice; that prefix is honoured untouched. An
// empty runID is a no-op so callers without a stable run identifier keep
// the static default.
//
// Expected:
//   - runID is a stable per-run identifier (e.g. the session id);
//     empty is a no-op.
//
// Side effects:
//   - Mutates the receiver's ChainPrefix in place. Call BEFORE the
//     Context is shared with concurrent member closures (the dispatcher
//     assigns at run start, before SetSwarmContext / fan-out), so the
//     immutability contract for the in-flight Context still holds.
func (c *Context) AssignRunChainID(runID string) {
	if c == nil || runID == "" {
		return
	}
	// Only the manifest default (ChainPrefix == SwarmID) is replaced; an
	// explicit chain_prefix is the operator's choice. An empty SwarmID
	// (zero-value Context) has nothing to anchor under, so leave it.
	if c.SwarmID == "" || c.ChainPrefix != c.SwarmID {
		return
	}
	c.ChainPrefix = c.SwarmID + "-" + runChainIDSuffix(runID)
	c.ChainIDAssigned = true
}

// runChainIDSuffix derives a short, filesystem- and key-safe suffix from
// runID via a truncated SHA-256 so the per-run namespace is deterministic
// for a given run yet distinct across runs. Truncation to 12 hex chars
// (48 bits) keeps keys readable while leaving collision probability
// negligible for the per-swarm-run population.
func runChainIDSuffix(runID string) string {
	sum := sha256.Sum256([]byte(runID))
	return hex.EncodeToString(sum[:])[:12]
}

// SubSwarmPath returns the slash-delimited path used by the runner to
// label errors and structured logs (§7 A3 of the swarm-manifest
// addendum). The path equals ChainPrefix; nested sub-swarms produce
// their child path via NestSubSwarm. An empty receiver yields an
// empty string so callers can rely on simple non-empty checks.
//
// Returns:
//   - The swarm context's slash-delimited path.
//
// Side effects:
//   - None.
func (c Context) SubSwarmPath() string {
	return c.ChainPrefix
}

// NestSubSwarm builds a child Context whose ChainPrefix concatenates
// the receiver's path with childID under a "/" separator. Used at
// sub-swarm dispatch boundaries so the inner runner attaches the full
// parent/child trace to its errors. The receiver is unchanged.
//
// Expected:
//   - childID is the sub-swarm id; non-empty.
//
// Returns:
//   - A new Context whose ChainPrefix is "<parent>/<child>" (or just
//     "<child>" when the parent path is empty).
//
// Side effects:
//   - None.
func (c Context) NestSubSwarm(childID string) Context {
	out := c
	switch {
	case c.ChainPrefix == "":
		out.ChainPrefix = childID
	case childID == "":
		out.ChainPrefix = c.ChainPrefix
	default:
		out.ChainPrefix = c.ChainPrefix + "/" + childID
	}
	if out.Depth < 1 {
		out.Depth = 2
	} else {
		out.Depth = c.Depth + 1
	}
	return out
}

// AllowlistMembers returns the delegation allowlist the runner should
// install for the duration of this swarm run. It is a copy of
// Members so callers can mutate it (e.g. extend with the lead's own
// id when the runner needs self-delegation) without aliasing the
// manifest's slice. An empty Members yields an empty (non-nil) slice
// so callers can append without a nil-guard.
//
// Expected:
//   - Receiver may be the zero value; an empty slice is returned.
//
// Returns:
//   - A defensive copy of c.Members; never nil.
//
// Side effects:
//   - None.
func (c Context) AllowlistMembers() []string {
	out := make([]string, len(c.Members))
	copy(out, c.Members)
	return out
}

// contextKey is the unexported key under which a *Context is stored
// on a context.Context. Unexported so external packages cannot stuff
// a different type under the same key and confuse FromContext.
type contextKey struct{}

// WithContext returns a child context that carries swarmCtx. The lead
// engine (and the delegate tool, gate runner, etc.) reads it back via
// FromContext. Passing a zero Context is supported but not useful —
// the caller is signalling "no swarm" by simply not calling this
// helper.
//
// Expected:
//   - parent is non-nil. context.Background() is acceptable.
//
// Returns:
//   - A derived context.Context carrying swarmCtx as a value.
//
// Side effects:
//   - None.
func WithContext(parent context.Context, swarmCtx Context) context.Context {
	return context.WithValue(parent, contextKey{}, &swarmCtx)
}

// FromContext extracts the *Context the runner attached via
// WithContext. The found flag distinguishes "no swarm in flight" from
// "swarm in flight with zero-value state" — the latter would never
// happen via WithContext but the explicit flag keeps the contract
// honest for callers building their own keys (which they should not).
//
// Expected:
//   - ctx may be nil; returns (nil, false) in that case.
//
// Returns:
//   - The carried *Context and true when one was attached.
//   - (nil, false) when no Context is in the value chain.
//
// Side effects:
//   - None.
func FromContext(ctx context.Context) (*Context, bool) {
	if ctx == nil {
		return nil, false
	}
	v, ok := ctx.Value(contextKey{}).(*Context)
	if !ok || v == nil {
		return nil, false
	}
	return v, true
}

// scopeKey is the unexported ctx-value key for the per-turn swarm
// scope marker. Distinct from contextKey because WithScope can attach
// a nil *Context to mean "this turn is explicitly standalone" — the
// delegate gate uses the marker to short-circuit the engine-state
// fallback that would otherwise leak cross-session swarm context.
//
// See planner session 39de3ab5-6173-4baf-9e20-7514a326bd3c: the
// shared dispatchEngine.swarmContext field was mutated mid-turn
// (between successive delegate calls) by a concurrent session, and
// the planner's gate read the polluted value because it consulted
// engine state directly. Threading the dispatch-time scope through
// ctx makes the gate's view of the swarm-roster immune to any
// concurrent engine-state writes that land after the turn begins.
type scopeKey struct{}

// scopeMarker wraps the per-turn scope so a nil *Context is
// distinguishable from "no scope attached" — Go's untyped nil
// interface assertions cannot make that distinction directly.
type scopeMarker struct {
	swarmCtx *Context
}

// WithScope attaches the dispatch-time swarm scope to ctx. Unlike
// WithContext, sc may be nil — that explicitly marks the turn as
// standalone (no swarm). ScopeFromContext later returns the same
// (*Context, scoped=true) pair regardless of whether sc was non-nil,
// so the delegate gate can read the dispatcher's decision off ctx
// directly without consulting shared engine state.
//
// Expected:
//   - parent is non-nil. context.Background() is acceptable.
//   - sc may be nil (explicit standalone marker) or non-nil.
//
// Returns:
//   - A derived context.Context carrying the scope marker.
//
// Side effects:
//   - None.
func WithScope(parent context.Context, sc *Context) context.Context {
	return context.WithValue(parent, scopeKey{}, scopeMarker{swarmCtx: sc})
}

// ScopeFromContext extracts the per-turn scope attached by WithScope.
// The scoped flag distinguishes "dispatcher attached scope" from "no
// scope attached" — the latter is the legacy contract (tests + bare-
// engine paths that haven't migrated) and tells callers to fall back
// to engine-state lookup. When scoped is true, the returned *Context
// is authoritative: nil means "this turn is standalone" and the gate
// MUST NOT consult engine state.
//
// Expected:
//   - ctx may be nil; returns (nil, false) in that case.
//
// Returns:
//   - (sc, true) when WithScope attached a scope to this ctx; sc may
//     be nil (explicit standalone marker).
//   - (nil, false) when no scope is attached (legacy fallback path).
//
// Side effects:
//   - None.
func ScopeFromContext(ctx context.Context) (*Context, bool) {
	if ctx == nil {
		return nil, false
	}
	marker, ok := ctx.Value(scopeKey{}).(scopeMarker)
	if !ok {
		return nil, false
	}
	return marker.swarmCtx, true
}

// Kind labels the resolver's verdict for a `@<id>` lookup. The
// resolver does not return the manifest pointer because callers in
// the chat intent path want only the routing decision; the runner
// path that needs the manifest constructs the Context itself.
type Kind int

const (
	// KindNone means the id resolved to neither registry. Callers
	// surface this as the user-facing "no agent or swarm named
	// '<id>'" error per spec §2.
	KindNone Kind = iota
	// KindAgent means the id matched an entry in the agent registry
	// (via either Get or GetByNameOrAlias — see Resolve).
	KindAgent
	// KindSwarm means the id matched an entry in the swarm registry.
	KindSwarm
)

// HasAgent reports whether name resolves in the agent registry
// either by id or by name/alias. Returning a bool keeps the resolver
// independent of agent.Registry's concrete *Manifest return type;
// pulling agent.Manifest into this package would create a one-way
// dependency we do not need.
//
// Callers wire this from agent.Registry like:
//
//	hasAgent := func(id string) bool {
//	    if reg == nil { return false }
//	    if _, ok := reg.Get(id); ok { return true }
//	    _, ok := reg.GetByNameOrAlias(id)
//	    return ok
//	}
type HasAgent func(id string) bool

// Resolve consults the agent registry first, then the swarm registry,
// per the spec §2 precedence rule (the global-uniqueness guarantee in
// §1 makes order defensive only — at most one match is ever
// possible).
//
// The signature takes a function for the agent check so callers can
// pass either Get or GetByNameOrAlias (or compose both) without
// dragging agent.Registry's *Manifest return type into this package.
//
// Expected:
//   - id is the user-typed id, without the leading `@`.
//   - hasAgent reports agent-registry membership; nil treats the
//     agent registry as empty.
//   - swarmReg is the swarm registry; nil treats it as empty.
//
// Returns:
//   - kind is KindAgent / KindSwarm / KindNone.
//   - manifest is the swarm *Manifest when kind == KindSwarm; nil
//     otherwise. Callers that resolved to KindAgent already hold a
//     handle to the agent registry and re-fetch the agent manifest
//     themselves (the shapes differ).
//
// Side effects:
//   - None.
func Resolve(id string, hasAgent HasAgent, swarmReg *Registry) (Kind, *Manifest) {
	if id == "" {
		return KindNone, nil
	}
	if hasAgent != nil && hasAgent(id) {
		// Auto-dispatch override: when the matched agent is also the
		// sole lead of a swarm that opted into AutoDispatchOnLead, the
		// resolver promotes the verdict to KindSwarm so the runner
		// installs swarmCtx and the lead's engine renders its swarm-
		// leadership block. Multi-swarm collisions (more than one
		// auto-dispatch candidate) leave the verdict as KindAgent —
		// see Registry.AutoDispatchSwarmFor for the disambiguation
		// rule. Zero matches falls through to today's behaviour.
		if swarmReg != nil {
			if m, ok := swarmReg.AutoDispatchSwarmFor(id); ok {
				return KindSwarm, m
			}
		}
		return KindAgent, nil
	}
	if swarmReg != nil {
		if m, ok := swarmReg.Get(id); ok && m != nil {
			return KindSwarm, m
		}
	}
	return KindNone, nil
}

// NotFoundError is the structured error the chat-input parser and
// the CLI flag-resolver surface when neither registry knows the id.
// The message is fixed by spec §2's validation requirement so the
// activity-pane regex tests for the swarm spec can pin it.
type NotFoundError struct {
	ID string
}

// Error returns the canonical "no agent or swarm named '<id>'"
// message from spec §2. Fixed wording so callers can match on it.
func (e *NotFoundError) Error() string {
	return "no agent or swarm named \"" + e.ID + "\""
}
