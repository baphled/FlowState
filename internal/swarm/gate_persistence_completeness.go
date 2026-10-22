package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PersistenceCompletenessGateKind is the manifest kind string for the
// persistence/completeness pre-check gate. It hard-blocks the swarm's
// synthesis/delivery phase (deploy it with when="post" or
// when="post-member" + failurePolicy=halt) unless every gated member
// has a non-empty coordination-store entry under the chain prefix.
//
// Rationale (tasks/swarm-persistence-completeness-gate.md, 2026-09-03
// n-vyro.io dd-swarm run): a Tech-Lead with a registered HIGH-severity
// gate produced no coordination-store key at all and the run still
// delivered; outputs were silently lost on member restart because
// nothing verified persistence before synthesis. This gate closes that
// hole at the dispatcher layer — generic across swarms, driven purely
// by the manifest's policy block.
const PersistenceCompletenessGateKind = "builtin:persistence-completeness"

// memberPlaceholder is the template token a persistence policy's
// output_key uses to address each required member's key slot (e.g.
// "{chainID}/{member}/verdict") so one policy line covers every
// gated member.
const memberPlaceholder = "{member}"

// PersistenceCompletenessPolicy is the policy block consumed by the
// persistence-completeness runner. All configuration is manifest-
// supplied; the runner has zero domain knowledge per the generic gate
// engine convention (see keyword-coverage / dd-report-completeness).
type PersistenceCompletenessPolicy struct {
	// RequiredMembers lists the gated member ids that MUST have
	// persisted output before synthesis may proceed. Each entry is
	// checked against "<chainPrefix>/<member>/<key>". When empty the
	// runner fails closed with a configuration error — a persistence
	// gate with no members to check is a manifest bug.
	RequiredMembers []string `json:"required_members" yaml:"required_members"`

	// OutputKey is the coord-store sub-key each member's output is
	// expected under. Defaults to DefaultMemberOutputKey ("output") so
	// manifests without explicit per-member keys keep working. A
	// "{chainID}" template is substituted with args.ChainID exactly as
	// the result-schema runner resolves its own keys.
	OutputKey string `json:"output_key" yaml:"output_key"`

	// MinBytes is the minimum non-whitespace payload size a persisted
	// entry must have to count as "complete". Defaults to 1 (non-empty)
	// per the task spec's "non-empty coordination-store entry" wording.
	MinBytes int `json:"min_bytes" yaml:"min_bytes"`

	// MediationReasonKey is the coord-store sub-key under the
	// COORDINATOR member where coordinator-mediated persistence
	// annotations live. When a gated member's own key is absent, the
	// runner accepts an explicit mediation record: an entry at
	// "<chainPrefix>/coordinator/<MediationReasonKey>/<member>" whose
	// payload carries a non-empty "reason". This is the sanctioned
	// escape hatch for members whose session lacked the
	// coordination_store tool (the 2026-09-03 Tech-Lead re-run). Empty
	// disables the mediation path entirely (strict mode).
	MediationReasonKey string `json:"mediation_reason_key" yaml:"mediation_reason_key"`
}

// mediationAnnotation is the JSON shape of a coordinator-mediated
// persistence record. Reason is mandatory — an empty reason is treated
// as absent per the BDD acceptance criteria.
type mediationAnnotation struct {
	Reason string `json:"reason"`
	By     string `json:"by,omitempty"`
}

// persistenceCompletenessRunner implements GateRunner for kind
// "builtin:persistence-completeness". It iterates the policy's
// required members, probes each contracted coord-store key, and fails
// with an aggregated *GateError naming every missing or empty member +
// key so the lead can re-dispatch exactly the deficient members.
type persistenceCompletenessRunner struct{}

// NewPersistenceCompletenessRunner returns a GateRunner for
// PersistenceCompletenessGateKind. The runner is stateless; the
// constructor exists so buildSwarmGateRunner registration reads
// uniformly alongside the other builtin constructors.
//
// Expected: no parameters.
// Returns: result of NewPersistenceCompletenessRunner.
// Side effects: None.
func NewPersistenceCompletenessRunner() GateRunner {
	return &persistenceCompletenessRunner{}
}

// Run evaluates the persistence/completeness contract for every
// required member. A member passes when its contracted coord-store key
// exists with at least MinBytes of non-whitespace payload, OR (when
// the mediation path is enabled) an explicit coordinator-mediated
// write annotation with a non-empty reason covers it. Every failure is
// aggregated into one *GateError naming the member and key.
//
// Expected:
//   - gate.Kind == PersistenceCompletenessGateKind.
//   - args.CoordStore is non-nil; nil fails closed with a typed
//     "coordination store unavailable" error.
//
// Returns:
//   - nil when every required member has persisted, non-empty output.
//   - A *GateError listing each missing/empty member + key otherwise.
//
// Side effects:
//   - Read-only coord-store probes: one Exists + at most one Get per
//     required member, plus one probe per mediated member when the
//     mediation path is enabled.
func (r *persistenceCompletenessRunner) Run(_ context.Context, gate GateSpec, args GateArgs) error {
	if args.CoordStore == nil {
		return newGateFailure(gate, args, "coordination store unavailable", nil)
	}
	policy, err := persistencePolicyFromSpec(gate)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}
	if len(policy.RequiredMembers) == 0 {
		return newGateFailure(gate, args, "persistence-completeness gate requires a non-empty required_members policy", nil)
	}
	if policy.OutputKey == "" {
		policy.OutputKey = DefaultMemberOutputKey
	}
	if policy.MinBytes <= 0 {
		policy.MinBytes = 1
	}

	var problems []string
	for _, member := range policy.RequiredMembers {
		key := resolvePersistenceKey(policy, args, member)
		complete, err := memberOutputComplete(args, policy, member, key)
		if err != nil {
			return newGateFailure(gate, args, fmt.Sprintf("probing coord-store key %q: %s", key, err.Error()), err)
		}
		if complete {
			continue
		}
		mediated, reason := mediationCovers(args, policy, member)
		if mediated {
			continue
		}
		if reason != "" {
			problems = append(problems, fmt.Sprintf("member %q: key %q absent/empty and coordinator-mediated annotation has empty reason", member, key))
			continue
		}
		problems = append(problems, fmt.Sprintf("member %q: no non-empty coordination-store entry at key %q (persist before synthesis)", member, key))
	}
	if len(problems) > 0 {
		return newGateFailure(gate, args, fmt.Sprintf("persistence/completeness pre-check failed: %d of %d gated members missing persisted output: %s", len(problems), len(policy.RequiredMembers), strings.Join(problems, "; ")), nil)
	}
	return nil
}

// persistencePolicyFromSpec decodes the gate's policy block. The
// swarm-manifest GateSpec does not carry a typed Policy field (ext
// gates receive theirs via the subprocess manifest), so the policy is
// carried on GateSpec.SchemaRef-free conventions: builtins read it
// from GateSpec.Policy map when present, else from a JSON encoding of
// the manifest's raw policy block threaded via GateSpec output
// metadata. This runner accepts either the typed map on GateSpec.Policy
// or a JSON string on GateSpec.OutputKey policy override is not used.
//
// Expected:
//   - gate.Policy may be nil (yields the zero policy) or a
//     map[string]any decoded from the swarm manifest's policy block.
//
// Returns:
//   - The typed policy and nil error, or a decode error naming the
//     policy block.
//
// Side effects: None.
func persistencePolicyFromSpec(gate GateSpec) (PersistenceCompletenessPolicy, error) {
	var policy PersistenceCompletenessPolicy
	if gate.Policy == nil {
		return policy, nil
	}
	raw, err := json.Marshal(gate.Policy)
	if err != nil {
		return policy, fmt.Errorf("encoding persistence policy block: %w", err)
	}
	if err := json.Unmarshal(raw, &policy); err != nil {
		return policy, fmt.Errorf("decoding persistence policy block: %w", err)
	}
	return policy, nil
}

// resolvePersistenceKey builds the contracted coord-store key for one
// member, substituting the {chainID} template exactly as
// candidateKeys does for result-schema gates.
//
// Expected: policy.OutputKey is non-empty (defaults applied by Run).
// Returns: the joined key.
// Side effects: None.
func resolvePersistenceKey(policy PersistenceCompletenessPolicy, args GateArgs, member string) string {
	if strings.Contains(policy.OutputKey, memberPlaceholder) {
		if args.ChainID == "" {
			return joinKey(args.ChainPrefix, strings.ReplaceAll(strings.ReplaceAll(policy.OutputKey, memberPlaceholder, member), chainIDPlaceholder+"/", ""))
		}
		return strings.ReplaceAll(strings.ReplaceAll(policy.OutputKey, memberPlaceholder, member), chainIDPlaceholder, args.ChainID)
	}
	if strings.Contains(policy.OutputKey, chainIDPlaceholder) {
		if args.ChainID == "" {
			// Bootstrap: lead has not allocated a chain yet. Fall
			// back to the static swarm namespace so the probe lands
			// in the pinned prefix rather than a literal template.
			return joinKey(args.ChainPrefix, member, strings.ReplaceAll(policy.OutputKey, chainIDPlaceholder+"/", ""))
		}
		// chainID-templated keys are lead-allocated namespaces in
		// their own right (mirroring candidateKeys): the substituted
		// output_key IS the coord-store key; no ChainPrefix join.
		return strings.ReplaceAll(policy.OutputKey, chainIDPlaceholder, args.ChainID)
	}
	return joinKey(args.ChainPrefix, member, policy.OutputKey)
}

// memberOutputComplete reports whether the member's contracted key
// holds at least MinBytes of non-whitespace payload.
//
// Expected: key is the fully resolved coord-store key.
// Returns: true when the entry exists and meets MinBytes.
// Side effects: one Exists + one Get on a hit.
func memberOutputComplete(args GateArgs, policy PersistenceCompletenessPolicy, member string, key string) (bool, error) {
	exists, err := args.CoordStore.Exists(key)
	if err != nil || !exists {
		return false, err
	}
	payload, err := args.CoordStore.Get(key)
	if err != nil {
		return false, err
	}
	return countNonWhitespace(payload) >= policy.MinBytes, nil
}

// mediationCovers reports whether an explicit coordinator-mediated
// annotation with a non-empty reason covers the member's absent
// output. The second return is the annotation's reason; it is non-empty
// only when an annotation exists, letting Run distinguish "mediation
// present but reason empty" (fail) from "no mediation" (fail with the
// plain missing-key message).
//
// Expected: policy.MediationReasonKey non-empty when mediation should
// be consulted; empty short-circuits to (false, "").
// Returns: (covered, reason).
// Side effects: at most one Exists + one Get.
func mediationCovers(args GateArgs, policy PersistenceCompletenessPolicy, member string) (bool, string) {
	if policy.MediationReasonKey == "" {
		return false, ""
	}
	key := joinKey(args.ChainPrefix, "coordinator", policy.MediationReasonKey, member)
	exists, err := args.CoordStore.Exists(key)
	if err != nil || !exists {
		return false, ""
	}
	raw, err := args.CoordStore.Get(key)
	if err != nil {
		return false, ""
	}
	var ann mediationAnnotation
	if err := json.Unmarshal(raw, &ann); err != nil {
		return false, ""
	}
	if strings.TrimSpace(ann.Reason) == "" {
		return false, "present-but-empty"
	}
	return true, ann.Reason
}

// countNonWhitespace counts bytes excluding ASCII whitespace so a
// key written with padding or blank lines cannot satisfy MinBytes.
//
// Expected: payload may be nil or empty.
// Returns: the non-whitespace byte count.
// Side effects: None.
func countNonWhitespace(payload []byte) int {
	n := 0
	for _, b := range payload {
		switch b {
		case ' ', '\t', '\n', '\r', '\v', '\f':
		default:
			n++
		}
	}
	return n
}
