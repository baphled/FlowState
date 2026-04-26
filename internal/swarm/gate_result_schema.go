package swarm

import (
	"context"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/coordination"
)

// reviewerOutputKey is the canonical coord-store sub-key the
// plan-reviewer agent writes its verdict under (see
// coordination/persisting_store.go and the planner workflow). The
// result-schema runner falls back to this key when the target member
// is "plan-reviewer" so the planning-loop reference swarm works
// without further wiring. Other members default to
// DefaultMemberOutputKey.
const reviewerOutputKey = "review"

// resultSchemaRunner implements GateRunner for kind:
// "builtin:result-schema". It validates the most-recent value the
// target member wrote to the coordination_store at
// "<chainPrefix>/<memberID>/<output-key>" against a JSON Schema
// looked up in the in-process registry by gate.SchemaRef.
//
// Phase 1 caveats (TODO list mirrored on top-level package doc):
//
//   - The output-key convention is hard-coded — see resolveOutputKey.
//   - The schema lookup is a registry-by-name only; Phase 2 will
//     plug a SchemaResolver in here so on-disk schemas/ files load
//     without re-compiling.
type resultSchemaRunner struct{}

// NewResultSchemaRunner returns the production result-schema runner.
// It carries no state; the same instance is safe to share across
// goroutines.
//
// Returns:
//   - A GateRunner whose Run validates JSON output against a
//     registered schema.
//
// Side effects:
//   - None.
func NewResultSchemaRunner() GateRunner {
	return resultSchemaRunner{}
}

// Run is the GateRunner entry point. It looks up the schema, reads
// the member output from the coord-store, decodes it as JSON, and
// validates against the schema. Every failure path returns a
// *GateError so the swarm runner can halt with a structured surface.
//
// Expected:
//   - gate.Kind == "builtin:result-schema" (the dispatcher only routes
//     this runner for that kind).
//   - gate.SchemaRef is non-empty; an empty value short-circuits to a
//     "missing schema_ref" gate failure.
//   - args.CoordStore is non-nil in production wiring; nil short-
//     circuits to a "coordination store unavailable" gate failure.
//
// Returns:
//   - nil when validation passes.
//   - A *GateError describing the first failing precondition or the
//     schema validation failure.
//
// Side effects:
//   - Reads exactly one key from args.CoordStore. No writes.
func (resultSchemaRunner) Run(ctx context.Context, gate GateSpec, args GateArgs) error {
	if err := preflightGate(gate, args); err != nil {
		return err
	}
	resolved, ok := LookupSchema(gate.SchemaRef)
	if !ok {
		return newGateFailure(gate, args, fmt.Sprintf("schema_ref %q is not registered", gate.SchemaRef), nil)
	}
	payload, err := readMemberOutput(args, gate.Target)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}
	instance, err := decodeJSONInstance(payload)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}
	if err := resolved.Validate(instance); err != nil {
		return newGateFailure(gate, args, fmt.Sprintf("schema validation failed: %s", err.Error()), err)
	}
	return nil
}

// preflightGate enforces the runner-level invariants before any work
// hits the coord-store: a missing schema_ref or a nil store both
// produce typed *GateError surfaces so the swarm runner can halt
// uniformly without sniffing the underlying cause.
//
// Expected:
//   - gate is the GateSpec being dispatched.
//   - args carries the runtime state.
//
// Returns:
//   - nil when both schema_ref and store are populated.
//   - A *GateError with the first failing precondition otherwise.
//
// Side effects:
//   - None.
func preflightGate(gate GateSpec, args GateArgs) error {
	if gate.SchemaRef == "" {
		return newGateFailure(gate, args, "missing schema_ref on builtin:result-schema gate", nil)
	}
	if args.CoordStore == nil {
		return newGateFailure(gate, args, "coordination store unavailable", nil)
	}
	return nil
}

// readMemberOutput pulls the most-recent member output from the
// coord-store. Phase 1 looks up two key shapes in order: the
// chain-prefixed reviewer key first (matching the existing
// approval-callback convention) then the generic
// "<chainPrefix>/<memberID>/output" fallback. The first hit wins.
//
// Expected:
//   - args.CoordStore is non-nil (preflightGate has already checked).
//   - memberID is the agent id whose output is being validated.
//
// Returns:
//   - The byte payload and nil on success.
//   - nil and a wrapped error when neither candidate key exists.
//
// Side effects:
//   - Calls args.CoordStore.Get; no writes.
func readMemberOutput(args GateArgs, memberID string) ([]byte, error) {
	for _, key := range candidateKeys(args.ChainPrefix, memberID) {
		payload, err := args.CoordStore.Get(key)
		if err == nil {
			return payload, nil
		}
		if !errors.Is(err, coordination.ErrKeyNotFound) {
			return nil, fmt.Errorf("reading coord-store key %q: %w", key, err)
		}
	}
	return nil, fmt.Errorf("no member output found at %v",
		candidateKeys(args.ChainPrefix, memberID))
}

// candidateKeys lists the coord-store keys the result-schema runner
// will probe for memberID's terminal output, in priority order. The
// list is stable so tests can pin the lookup ordering.
//
// Expected:
//   - chainPrefix is the swarm's coord-store namespace; may be empty
//     (in which case the keys collapse to "<memberID>/<key>").
//   - memberID is the agent id whose output is sought.
//
// Returns:
//   - A slice of candidate keys; never nil.
//
// Side effects:
//   - None.
func candidateKeys(chainPrefix, memberID string) []string {
	subKey := resolveOutputKey(memberID)
	keys := []string{
		joinKey(chainPrefix, memberID, subKey),
	}
	if subKey != DefaultMemberOutputKey {
		keys = append(keys, joinKey(chainPrefix, memberID, DefaultMemberOutputKey))
	}
	return keys
}

// resolveOutputKey returns the coord-store sub-key the named member
// canonically writes its output under. Today the plan-reviewer
// convention uses "review" (matching the approval-callback wiring in
// coordination/persisting_store.go); every other agent falls back to
// the generic "output" key. Phase 2 will replace this with an
// explicit GateSpec.OutputKey field on the manifest.
//
// Expected:
//   - memberID is the agent id whose output is sought.
//
// Returns:
//   - The coord-store sub-key.
//
// Side effects:
//   - None.
func resolveOutputKey(memberID string) string {
	if memberID == "plan-reviewer" {
		return reviewerOutputKey
	}
	return DefaultMemberOutputKey
}

// joinKey builds a coord-store key from the parts, skipping empty
// segments so an empty chainPrefix yields "<memberID>/<sub>" rather
// than a leading slash. The store is permissive about key shape but
// downstream tooling (List() prefix walks, log filters) prefers a
// stable form.
//
// Expected:
//   - parts contains at least the memberID and sub-key segments.
//
// Returns:
//   - The joined key.
//
// Side effects:
//   - None.
func joinKey(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += "/" + p
	}
	return out
}

// newGateFailure constructs the canonical *GateError surface for a
// failing builtin:result-schema run. Callers always go through this
// helper so the GateName / GateKind / scope fields are populated
// consistently.
//
// Expected:
//   - gate is the GateSpec being dispatched.
//   - args carries the runtime state.
//   - reason is the user-facing message.
//   - cause may be nil when no underlying error exists.
//
// Returns:
//   - A populated *GateError.
//
// Side effects:
//   - None.
func newGateFailure(gate GateSpec, args GateArgs, reason string, cause error) *GateError {
	return &GateError{
		GateName: gate.Name,
		GateKind: gate.Kind,
		When:     gate.When,
		SwarmID:  args.SwarmID,
		MemberID: args.MemberID,
		Reason:   reason,
		Cause:    cause,
	}
}
