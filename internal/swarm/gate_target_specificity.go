package swarm

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// TargetSpecificityGateKind is the manifest kind string for the
// target-specificity gate. It complements keyword-coverage style
// gates by requiring the member's output to reference target-specific
// evidence drawn from the engagement brief (repo names, file paths,
// EVIDENCE-NNN citations) rather than generic domain vocabulary that
// any boilerplate would satisfy.
//
// Rationale (tasks/swarm-persistence-completeness-gate.md + the
// 2026-08-21 Swarm Determinism Gaps Review): the 2026-09-03 n-vyro.io
// dd-swarm run showed a Security-Engineer producing off-target generic
// output that still PASSED the keyword-group gates. Keyword coverage
// measures vocabulary, not grounding; this gate measures grounding.
const TargetSpecificityGateKind = "builtin:target-specificity"

// evidenceCitationPattern matches EVIDENCE-NNN style citations the DD
// evidence bundle assigns. Compiled once; safe for concurrent use.
var evidenceCitationPattern = regexp.MustCompile(`\bEVIDENCE-\d{3,}\b`)

// filePathPattern matches Unix-ish file-path references (at least one
// slash and a segment with a dot, or a leading ./ ../ /). Deliberately
// permissive: this gate's job is to prove target grounding, not to
// validate path syntax.
var filePathPattern = regexp.MustCompile(`(?:\.{0,2}/)?[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+\.[A-Za-z0-9]+`)

// TargetSpecificityPolicy is the policy block consumed by the
// target-specificity runner. All criteria come from the manifest —
// the engine has zero domain knowledge, consistent with the existing
// generic gate engines (keyword-coverage et al).
type TargetSpecificityPolicy struct {
	// TargetIdentifiers lists the target-specific strings (repo
	// names, domains, file paths, module names) supplied by the
	// engagement brief. At least MinReferences of them must appear in
	// the member output for the gate to pass. When empty the check is
	// DISABLED and the gate passes — backward-compatible default so a
	// swarm run before a brief is loaded does not hard-block.
	TargetIdentifiers []string `json:"target_identifiers" yaml:"target_identifiers"`

	// MinReferences is how many distinct target identifiers must be
	// referenced. Defaults to 1.
	MinReferences int `json:"min_references" yaml:"min_references"`

	// AcceptEvidenceCitations counts EVIDENCE-NNN citations toward the
	// reference total when true. Defaults to true — an evidence
	// citation is the strongest form of target grounding in the DD
	// workflow.
	AcceptEvidenceCitations bool `json:"accept_evidence_citations" yaml:"accept_evidence_citations"`

	// AcceptFilePaths counts distinct file-path-shaped references
	// toward the total when true. Defaults to true. Paths not listed
	// in TargetIdentifiers still count: a member citing
	// "internal/api/auth.go" has clearly opened the target repo even
	// if the brief did not enumerate that file.
	AcceptFilePaths bool `json:"accept_file_paths" yaml:"accept_file_paths"`

	// MinChars is a floor on output length so a two-word payload
	// naming one identifier cannot pass on technicality. Defaults to
	// 0 (no floor; length is the word-count gates' job).
	MinChars int `json:"min_chars" yaml:"min_chars"`
}

// targetSpecificityRunner implements GateRunner for kind
// "builtin:target-specificity". It reads the member output from the
// coord-store (same key-resolution rules as result-schema gates) and
// counts target-specific references.
type targetSpecificityRunner struct{}

// NewTargetSpecificityRunner returns a GateRunner for
// TargetSpecificityGateKind. Stateless; constructor exists for uniform
// registration in buildSwarmGateRunner.
//
// Expected: no parameters.
// Returns: result of NewTargetSpecificityRunner.
// Side effects: None.
func NewTargetSpecificityRunner() GateRunner {
	return &targetSpecificityRunner{}
}

// Run reads the member output and enforces the target-specificity
// contract. Generic vocabulary alone never passes: only configured
// target identifiers, EVIDENCE-NNN citations, and file-path references
// count toward MinReferences.
//
// Expected:
//   - gate.Kind == TargetSpecificityGateKind.
//   - args.CoordStore is non-nil; nil fails closed with a typed error.
//
// Returns:
//   - nil when the output references at least MinReferences
//     target-specific items (or the check is disabled — no configured
//     identifiers AND no citation/path counting possible).
//   - A *GateError whose reason mentions target-specific evidence and
//     lists the unmatched identifiers otherwise.
//
// Side effects:
//   - Read-only coord-store probes via readMemberOutput.
func (r *targetSpecificityRunner) Run(_ context.Context, gate GateSpec, args GateArgs) error {
	policy, err := targetSpecificityPolicyFromSpec(gate)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}
	// Disabled check (no identifiers, no citation/path counting) is
	// evaluated BEFORE the coord-store probe so a pre-brief run does
	// not fail on a missing key.
	if len(policy.TargetIdentifiers) == 0 && !policy.AcceptEvidenceCitations && !policy.AcceptFilePaths {
		return nil
	}
	if args.CoordStore == nil {
		return newGateFailure(gate, args, "coordination store unavailable", nil)
	}
	payload, err := readMemberOutput(gate, args)
	if err != nil {
		return newGateFailure(gate, args, err.Error(), err)
	}
	text := string(payload)

	matched, unmatched := matchTargetReferences(text, policy)
	required := policy.MinReferences
	if required <= 0 {
		required = 1
	}
	if policy.AcceptEvidenceCitations || policy.AcceptFilePaths || len(policy.TargetIdentifiers) > 0 {
		if len(matched) >= required && len(text) >= policy.MinChars {
			return nil
		}
	} else {
		return nil
	}
	if len(text) < policy.MinChars {
		return newGateFailure(gate, args, fmt.Sprintf("output too short: %d chars < min_chars %d; output does not reference target-specific evidence", len(text), policy.MinChars), nil)
	}
	return newGateFailure(gate, args, formatSpecificityFailure(gate, matched, unmatched, required), nil)
}

// targetSpecificityPolicyFromSpec decodes the gate's policy block from
// the manifest-supplied GateSpec.Policy map. Missing block yields the
// zero policy — which disables the check (no identifiers configured).
//
// Expected: gate.Policy may be nil or a map[string]any decoded from
// the swarm manifest's policy block.
// Returns: the typed policy and nil error, or a decode error.
// Side effects: None.
func targetSpecificityPolicyFromSpec(gate GateSpec) (TargetSpecificityPolicy, error) {
	var policy TargetSpecificityPolicy
	if gate.Policy == nil {
		return policy, nil
	}
	raw, err := json.Marshal(gate.Policy)
	if err != nil {
		return policy, fmt.Errorf("encoding target-specificity policy block: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if err := decoder.Decode(&policy); err != nil {
		return policy, fmt.Errorf("decoding target-specificity policy block: %w", err)
	}
	return policy, nil
}

// matchTargetReferences counts the distinct target-specific references
// present in text: configured identifiers (substring match, per
// keyword-coverage's default mode), optionally EVIDENCE-NNN citations,
// and optionally distinct file-path references. Returns the matched
// reference strings and the configured identifiers that were absent.
//
// Expected: text is the raw member output; policy may be zero-valued.
// Returns: (matched distinct references, unmatched configured ids).
// Side effects: None.
func matchTargetReferences(text string, policy TargetSpecificityPolicy) (matched []string, unmatched []string) {
	seen := map[string]struct{}{}
	lowerText := strings.ToLower(text)
	for _, id := range policy.TargetIdentifiers {
		if id == "" {
			continue
		}
		// Case-insensitive per the task contract: the brief may cite
		// "N-Vyro.io" while the member writes "n-vyro.io".
		if strings.Contains(lowerText, strings.ToLower(id)) {
			if _, dup := seen[id]; !dup {
				seen[id] = struct{}{}
				matched = append(matched, id)
			}
		} else {
			unmatched = append(unmatched, id)
		}
	}
	if policy.AcceptEvidenceCitations {
		for _, citation := range evidenceCitationPattern.FindAllString(text, -1) {
			if _, dup := seen[citation]; !dup {
				seen[citation] = struct{}{}
				matched = append(matched, citation)
			}
		}
	}
	if policy.AcceptFilePaths {
		for _, path := range filePathPattern.FindAllString(text, -1) {
			if _, dup := seen[path]; !dup {
				seen[path] = struct{}{}
				matched = append(matched, path)
			}
		}
	}
	sort.Strings(matched)
	return matched, unmatched
}

// formatSpecificityFailure renders the aggregated failure reason. The
// phrase "target-specific evidence" is load-bearing — BDD steps and
// operator greps pin on it.
//
// Expected: matched/unmatched from matchTargetReferences; required is
// the effective MinReferences.
// Returns: the formatted reason.
// Side effects: None.
func formatSpecificityFailure(gate GateSpec, matched, unmatched []string, required int) string {
	parts := []string{fmt.Sprintf("output does not reference target-specific evidence: found %d distinct target references, need %d", len(matched), required)}
	if len(unmatched) > 0 {
		parts = append(parts, fmt.Sprintf("unreferenced brief identifiers: %s", strings.Join(unmatched, ", ")))
	}
	if len(matched) > 0 {
		parts = append(parts, fmt.Sprintf("matched: %s", strings.Join(matched, ", ")))
	}
	parts = append(parts, "revise output to cite the engagement brief's target identifiers (repo names, file paths) or EVIDENCE-NNN citations")
	return strings.Join(parts, "; ")
}

// runTargetSpecificityFromInput adapts the legacy RunGate/GateInput
// path onto the target-specificity runner for callers that hold the
// payload inline rather than in the coordination store. The inline
// policy map overrides the spec's manifest-declared policy block so
// ext-style dispatchers that thread GateInput.Policy keep working.
//
// Expected:
//   - spec.Kind == TargetSpecificityGateKind.
//   - in.Payload may be empty; the runner decides whether that fails.
//
// Returns:
//   - nil or the runner's *GateError.
//
// Side effects: None (payload is supplied inline; no coord-store IO).
func runTargetSpecificityFromInput(ctx context.Context, spec GateSpec, in GateInput) error {
	if len(in.Policy) > 0 {
		spec.Policy = in.Policy
	}
	// When the caller supplies the payload inline (ext-gate
	// dispatchers thread GateInput.Payload), evaluate directly so the
	// legacy path works without a coord store; otherwise fall through
	// to the runner's coord-store probe.
	policy, err := targetSpecificityPolicyFromSpec(spec)
	if err != nil {
		return newGateFailure(spec, GateArgs{SwarmID: in.SwarmID, MemberID: in.MemberID}, err.Error(), err)
	}
	if len(policy.TargetIdentifiers) == 0 && !policy.AcceptEvidenceCitations && !policy.AcceptFilePaths {
		return nil
	}
	if len(in.Payload) > 0 {
		args := GateArgs{SwarmID: in.SwarmID, MemberID: in.MemberID}
		text := string(in.Payload)
		matched, unmatched := matchTargetReferences(text, policy)
		required := policy.MinReferences
		if required <= 0 {
			required = 1
		}
		if len(matched) >= required && len(text) >= policy.MinChars {
			return nil
		}
		if len(text) < policy.MinChars {
			return newGateFailure(spec, args, fmt.Sprintf("output too short: %d chars < min_chars %d; output does not reference target-specific evidence", len(text), policy.MinChars), nil)
		}
		return newGateFailure(spec, args, formatSpecificityFailure(spec, matched, unmatched, required), nil)
	}
	return (&targetSpecificityRunner{}).Run(ctx, spec, GateArgs{
		SwarmID:  in.SwarmID,
		MemberID: in.MemberID,
	})
}
