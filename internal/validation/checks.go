package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// AllowedPathPrefixes is the closed VA-D1 path allowlist for M1 changes.
var AllowedPathPrefixes = []string{
	"cmd/flowstate/", "internal/app/", "internal/config/", "internal/runtime/",
	"internal/session/", "internal/provider/", "internal/tool/", "internal/store/",
	"internal/storage/", "internal/oauth/", "internal/validation/",
}

// T019Pass is the prerequisite state required before implementation (VA-D1).
const T019Pass = "T019"

// ValidateWorkPacket checks VA-D0: schema validity and proposed-not-admitted.
func ValidateWorkPacket(p WorkPacket) []string {
	var violations []string
	if p.SchemaVersion == "" {
		violations = append(violations, "packet schema_version must not be empty")
	}
	if len(p.Sources) == 0 {
		violations = append(violations, "packet sources must be known and non-empty")
	}
	if !sha1Pattern.MatchString(p.BaseSHA) {
		violations = append(violations, "packet base_sha must be a 40 hex sha")
	}
	if len(p.Tools) == 0 {
		violations = append(violations, "packet tools must be known and non-empty")
	}
	if len(p.Scope) == 0 {
		violations = append(violations, "packet scope must be known and non-empty")
	}
	if p.BudgetTokens <= 0 {
		violations = append(violations, "packet budget must be known and positive")
	}
	if p.Admitted {
		violations = append(violations, "proposed packet must not be admitted")
	}
	return violations
}

// ValidateCandidatePaths checks the closed allowed-paths obligation of VA-D1.
func ValidateCandidatePaths(changed []string) []string {
	var violations []string
	for _, path := range changed {
		if !pathAllowed(path) {
			violations = append(violations, fmt.Sprintf("path outside closed allowlist: %s", path))
		}
	}
	return violations
}

// ValidateCandidateCapability checks the known-capability and T019
// prerequisite obligations of VA-D1.
func ValidateCandidateCapability(capabilityID, t019 string) []string {
	var violations []string
	if !CapabilityIDs[capabilityID] {
		violations = append(violations, "invented capability id: "+capabilityID)
	}
	if t019 != T019Pass {
		violations = append(violations, "T019 prerequisite not passed before implementation")
	}
	return violations
}

// ValidateTests checks VA-D2: command/exit agreement and no undefined,
// skipped or empty test selection.
func ValidateTests(evidence []EvidenceItem, selected []string, skipped map[string]bool) []string {
	var violations []string
	for i, e := range evidence {
		if e.Command == "" {
			violations = append(violations, fmt.Sprintf("evidence[%d].command must be retained", i))
		}
		if e.ExitCode != e.ExpectedExitCode {
			violations = append(violations, fmt.Sprintf("evidence[%d] exit %d differs from expected %d", i, e.ExitCode, e.ExpectedExitCode))
		}
	}
	if len(selected) == 0 {
		violations = append(violations, "test selection must not be empty")
	}
	for _, name := range selected {
		if name == "" {
			violations = append(violations, "undefined test present in selection")
		}
		if skipped[name] {
			violations = append(violations, "skipped test present in selection: "+name)
		}
	}
	return violations
}

// ValidateReview checks VA-D3 (GF-R05): a real separately identifiable
// reviewer, distinct from the author, that resolves and reviewed the exact
// candidate and evidence; anything missing or unverifiable is rejected.
func ValidateReview(id ReviewIdentity) []string {
	var violations []string
	if id.ReviewerIdentity == "" {
		violations = append(violations, "reviewer identity missing")
	}
	if !id.ReviewerResolves {
		violations = append(violations, "reviewer identity does not resolve")
	}
	if id.ReviewerIdentity == id.AuthorIdentity {
		violations = append(violations, "self-review rejected")
	}
	if !id.EvidenceReviewed {
		violations = append(violations, "exact candidate and evidence not reviewed")
	}
	if id.Verdict != "accept" && id.Verdict != "reject" {
		violations = append(violations, "review verdict must be accept or reject")
	}
	return violations
}

// ManifestDigest returns the hex sha256 of the given manifest bytes.
func ManifestDigest(manifest []byte) string {
	sum := sha256.Sum256(manifest)
	return hex.EncodeToString(sum[:])
}

// ValidateManifestFreshness checks GF-R03: the claimed manifest digest must
// equal the digest of the current manifest bytes; mismatch means stale.
func ValidateManifestFreshness(claimed string, manifest []byte) []string {
	if claimed != ManifestDigest(manifest) {
		return []string{"stale manifest: claimed digest differs from current manifest bytes"}
	}
	return nil
}

// ValidateArtefacts checks VA-D4 artefact digest verification: every artefact
// must be present and its claimed digest must match the observed digest.
func ValidateArtefacts(artefacts []ArtefactVerification) []string {
	var violations []string
	for _, a := range artefacts {
		if !a.Present {
			violations = append(violations, "missing artefact: "+a.Path)
			continue
		}
		if a.ClaimedSHA256 != a.ActualSHA256 {
			violations = append(violations, "tampered artefact digest: "+a.Path)
		}
	}
	return violations
}

// ValidateAcceptance checks VA-D4: accepted receipt plus clean exact candidate
// plus fresh manifest plus verified artefacts plus index agreement.
func ValidateAcceptance(r Receipt, c Candidate, id ReviewIdentity, artefacts []ArtefactVerification, index map[string]string) []string {
	var violations []string
	if r.GateOutcome != "pass" || r.Review.Verdict != "accept" {
		violations = append(violations, "capability receipt is not accepted")
	}
	if c.CommitSHA != r.RepoState.CommitSHA {
		violations = append(violations, "candidate sha differs from receipt repo state")
	}
	if id.CandidateSHA != r.RepoState.CommitSHA {
		violations = append(violations, "reviewed candidate sha differs from receipt repo state")
	}
	if c.Dirty {
		violations = append(violations, "candidate tree is dirty")
	}
	violations = append(violations, ValidateManifestFreshness(c.ManifestSHA256, c.ManifestBytes)...)
	violations = append(violations, ValidateArtefacts(artefacts)...)
	violations = append(violations, CheckReceiptIndex([]Receipt{r}, index)...)
	return violations
}

func pathAllowed(path string) bool {
	for _, prefix := range AllowedPathPrefixes {
		if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}
