package validation

import "regexp"

var (
	sha1Pattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	metadataRefPattern  = regexp.MustCompile(`^dropbox:/FlowState/Metadata/attempts/[A-Za-z0-9][A-Za-z0-9._-]*/receipt\.json$`)
	artefactRootPattern = regexp.MustCompile(`^dropbox:/FlowState/Metadata/attempts/[A-Za-z0-9][A-Za-z0-9._-]*/artefacts$`)
)

// CapabilityIDs is the closed M1 capability enum from the receipt schema.
var CapabilityIDs = map[string]bool{
	"M1-SESSION": true, "M1-PROVIDER": true, "M1-EVENTS": true, "M1-TOOLS": true,
	"M1-TERMINAL": true, "M1-COMPOSITION": true, "M1-DOGFOOD": true, "M1-VALIDATION": true,
}

// ValidateReceipt checks a receipt against the m1-acceptance-receipt-v2
// obligations and returns every violation found; an empty slice means valid.
func ValidateReceipt(r Receipt) []string {
	violations := validateReceiptHeader(r)
	if len(r.Evidence) == 0 {
		violations = append(violations, "evidence must contain at least one item")
	}
	for i, e := range r.Evidence {
		violations = append(violations, validateEvidenceItem(i, e)...)
	}
	violations = append(violations, validateReceiptBody(r)...)
	return violations
}

// validateReceiptHeader checks the schema, capability and repo-state fields.
func validateReceiptHeader(r Receipt) []string {
	var violations []string
	if r.SchemaVersion != "m1-acceptance-receipt-v2" {
		violations = append(violations, "schema_version must be m1-acceptance-receipt-v2")
	}
	if !CapabilityIDs[r.CapabilityID] {
		violations = append(violations, "capability_id not in closed M1 enum")
	}
	if !sha1Pattern.MatchString(r.RepoState.CommitSHA) {
		violations = append(violations, "repo_state.commit_sha must be 40 hex chars")
	}
	if r.RepoState.Dirty {
		violations = append(violations, "repo_state.dirty must be false")
	}
	if !sha256Pattern.MatchString(r.RepoState.ManifestSHA256) {
		violations = append(violations, "repo_state.manifest_sha256 must be 64 hex chars")
	}
	if r.RepoState.JobID == "" {
		violations = append(violations, "repo_state.job_id must not be empty")
	}
	if r.AuthorIdentity == "" {
		violations = append(violations, "author_identity must not be empty")
	}
	return violations
}

// validateReceiptBody checks the gate outcome, model, review and validator run.
func validateReceiptBody(r Receipt) []string {
	var violations []string
	if r.GateOutcome != "pass" && r.GateOutcome != "fail" {
		violations = append(violations, "gate_outcome must be pass or fail")
	}
	violations = append(violations, validateObservedModel(r.ObservedModel)...)
	violations = append(violations, validateReceiptReview(r.Review)...)
	if r.ValidatorRun.ExitCode != 0 {
		violations = append(violations, "validator_run.exit_code must be 0")
	}
	if !sha256Pattern.MatchString(r.ValidatorRun.DocsPackManifestSHA256) {
		violations = append(violations, "validator_run.docs_pack_manifest_sha256 must be 64 hex chars")
	}
	violations = append(violations, validateArtefactRef("validator_run.artefact", r.ValidatorRun.Artefact)...)
	if !metadataRefPattern.MatchString(r.MetadataRef) {
		violations = append(violations, "metadata_ref must match dropbox attempts receipt path")
	}
	if !artefactRootPattern.MatchString(r.ArtefactRoot) {
		violations = append(violations, "artefact_root must match dropbox attempts artefacts path")
	}
	return violations
}

// CheckReceiptIndex verifies the receipt files agree with the DB receipt
// index keyed by capability id to metadata ref; any disagreement is a
// violation (VA-D4 index agreement).
func CheckReceiptIndex(receipts []Receipt, index map[string]string) []string {
	var violations []string
	seen := map[string]string{}
	for _, r := range receipts {
		seen[r.CapabilityID] = r.MetadataRef
		if ref, ok := index[r.CapabilityID]; ok && ref != r.MetadataRef {
			violations = append(violations, "index disagrees with receipt "+r.CapabilityID)
		}
	}
	for capability := range index {
		if _, ok := seen[capability]; !ok {
			violations = append(violations, "index entry missing receipt file "+capability)
		}
	}
	for capability := range seen {
		if _, ok := index[capability]; !ok {
			violations = append(violations, "receipt file missing from index "+capability)
		}
	}
	return violations
}

func validateEvidenceItem(i int, e EvidenceItem) []string {
	var violations []string
	label := "evidence["
	violations = appendIf(violations, e.Command == "", label+itoa(i)+"].command must not be empty")
	violations = append(violations, validateArtefactRef(label+itoa(i)+"].stdout", e.Stdout)...)
	violations = append(violations, validateArtefactRef(label+itoa(i)+"].stderr", e.Stderr)...)
	if e.CapturedAtUTC == "" {
		violations = append(violations, label+itoa(i)+"].captured_at_utc must not be empty")
	}
	return violations
}

func validateArtefactRef(label string, ref ArtefactRef) []string {
	var violations []string
	violations = appendIf(violations, ref.Path == "", label+".path must not be empty")
	violations = appendIf(violations, !sha256Pattern.MatchString(ref.SHA256), label+".sha256 must be 64 hex chars")
	return violations
}

func validateObservedModel(m ObservedModel) []string {
	var violations []string
	violations = appendIf(violations, m.Provider == "", "observed_model.provider must not be empty")
	violations = appendIf(violations, m.ModelID == "", "observed_model.model_id must not be empty")
	violations = appendIf(violations, m.Harness == "", "observed_model.harness must not be empty")
	violations = appendIf(violations, m.Source == "", "observed_model.source must not be empty")
	return violations
}

func validateReceiptReview(rv Review) []string {
	var violations []string
	violations = appendIf(violations, rv.ReviewerIdentity == "", "review.reviewer_identity must not be empty")
	violations = appendIf(violations, rv.EvidenceRef == "", "review.evidence_ref must not be empty")
	violations = appendIf(violations, !sha1Pattern.MatchString(rv.CandidateSHA), "review.candidate_sha must be 40 hex chars")
	violations = appendIf(violations, rv.Verdict != "accept" && rv.Verdict != "reject", "review.verdict must be accept or reject")
	return violations
}
