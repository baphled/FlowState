package swarm

import (
	"context"
	"strings"
	"testing"
)

func tsGate(policy map[string]any) GateSpec {
	if policy == nil {
		policy = map[string]any{}
	}
	return GateSpec{
		Name:   "target-specificity",
		Kind:   TargetSpecificityGateKind,
		When:   "post-member",
		Target: "security-engineer",
		Policy: policy,
	}
}

func TestTargetSpecificityPassesOnIdentifierMatch(t *testing.T) {
	matched, unmatched := matchTargetReferences(
		"Reviewed n-vyro.io repo, focussed on internal/api/auth.go and the vyro-platform module.",
		TargetSpecificityPolicy{TargetIdentifiers: []string{"n-vyro.io", "vyro-platform"}},
	)
	if len(matched) != 2 || len(unmatched) != 0 {
		t.Fatalf("expected both matched, got %v / %v", matched, unmatched)
	}
}

func TestTargetSpecificityFailsOnGenericVocabulary(t *testing.T) {
	matched, _ := matchTargetReferences(
		"The authentication layer should use secure session handling and validate all inputs.",
		TargetSpecificityPolicy{TargetIdentifiers: []string{"n-vyro.io", "vyro-platform"}},
	)
	if len(matched) != 0 {
		t.Fatalf("generic vocabulary must not match, got %v", matched)
	}
}

func TestTargetSpecificityCaseInsensitiveMatching(t *testing.T) {
	matched, _ := matchTargetReferences(
		"Audited N-VYRO.IO for injection flaws.",
		TargetSpecificityPolicy{TargetIdentifiers: []string{"n-vyro.io"}},
	)
	if len(matched) != 1 {
		t.Fatalf("case-insensitive match expected, got %v", matched)
	}
}

func TestTargetSpecificityCountsEvidenceCitationsAndPaths(t *testing.T) {
	matched, _ := matchTargetReferences(
		"Findings grounded in EVIDENCE-012 and EVIDENCE-004; see cmd/server/main.go.",
		TargetSpecificityPolicy{AcceptEvidenceCitations: true, AcceptFilePaths: true},
	)
	if len(matched) != 3 {
		t.Fatalf("expected 2 citations + 1 path, got %v", matched)
	}
}

func TestTargetSpecificityDisabledWhenNoCriteria(t *testing.T) {
	err := NewTargetSpecificityRunner().Run(context.Background(), tsGate(nil), GateArgs{SwarmID: "s"})
	if err != nil {
		t.Fatalf("zero policy disables the gate, got: %v", err)
	}
}

func TestRunTargetSpecificityFromInputUsesInlinePolicy(t *testing.T) {
	spec := tsGate(nil)
	err := runTargetSpecificityFromInput(context.Background(), spec, GateInput{
		SwarmID:  "s",
		MemberID: "security-engineer",
		Payload:  []byte("Generic boilerplate output with no grounding at all."),
		Policy: map[string]any{
			"target_identifiers": []string{"n-vyro.io"},
		},
	})
	if err == nil {
		t.Fatal("ungrounded output must fail the inline-policy path")
	}
	if !strings.Contains(err.Error(), "target-specific evidence") {
		t.Fatalf("reason should carry load-bearing phrase, got: %v", err)
	}
}

func TestTargetSpecificityPolicyDecodesFromSpecMap(t *testing.T) {
	gate := tsGate(map[string]any{
		"target_identifiers":        []string{"repo-a"},
		"min_references":            2,
		"accept_evidence_citations": true,
	})
	policy, err := targetSpecificityPolicyFromSpec(gate)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if len(policy.TargetIdentifiers) != 1 || policy.MinReferences != 2 || !policy.AcceptEvidenceCitations {
		t.Fatalf("unexpected policy: %+v", policy)
	}
}
