// Package swarm carries the runtime types T-swarm-2 needs to invoke a
// swarm via the existing `@<id>` chat-input parser and the `--agent`
// CLI flag.
//
// The resolver consumes T-swarm-1's concrete *Registry and *Manifest
// directly — no extra interface layer. Test fakes are expected to
// build real *Registry instances via NewRegistry + Register, which is
// no harder than constructing an interface fake.
package swarm

import "context"

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
