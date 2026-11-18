// Package app owns receipt validation against the M1 acceptance
// receipt schema (ACCEPTANCE-RECEIPTS.md): structural checks, digest
// shape, and gate consistency. Validation is offline and
// deterministic; resolving Dropbox references happens elsewhere.
package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// ErrReceiptInvalid marks receipts failing schema validation (code
// receipt_invalid, exit 8).
var ErrReceiptInvalid = errors.New("receipt_invalid")

// receiptCapabilities is the closed capability enum of the schema.
var receiptCapabilities = map[string]bool{
	"M1-SESSION": true, "M1-PROVIDER": true, "M1-EVENTS": true,
	"M1-TOOLS": true, "M1-TERMINAL": true, "M1-COMPOSITION": true,
	"M1-SELFTEST": true, "M1-VALIDATION": true,
}

var (
	sha40 = regexp.MustCompile(`^[0-9a-f]{40}$`)
	sha64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
	// dateTime is RFC3339 with mandatory T separator.
	dateTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$`)
)

// ValidateReceiptFile validates one receipt file; failures wrap
// ErrReceiptInvalid.
func ValidateReceiptFile(path string) error {
	raw, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("%w: cannot read %q: %v", ErrReceiptInvalid, path, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("%w: %q is not a JSON object: %v", ErrReceiptInvalid, path, err)
	}
	if err := validateReceipt(doc); err != nil {
		return fmt.Errorf("%w: %q: %v", ErrReceiptInvalid, path, err)
	}
	return nil
}

// validateReceipt enforces the m1-acceptance-receipt-v2 schema by hand.
func validateReceipt(doc map[string]any) error {
	if v, _ := doc["schema_version"].(string); v != "m1-acceptance-receipt-v2" {
		return errors.New("schema_version must be m1-acceptance-receipt-v2")
	}
	if capID, _ := doc["capability_id"].(string); !receiptCapabilities[capID] {
		return errors.New("capability_id is not in the M1 capability enum")
	}
	for _, key := range []string{"repo_state", "author_identity", "evidence",
		"gate_outcome", "observed_model", "review", "validator_run",
		"metadata_ref", "artefact_root"} {
		if _, present := doc[key]; !present {
			return fmt.Errorf("missing required field %q", key)
		}
	}
	if err := validateRepoState(doc["repo_state"]); err != nil {
		return err
	}
	if author, _ := doc["author_identity"].(string); author == "" {
		return errors.New("author_identity must not be empty")
	}
	outcome, _ := doc["gate_outcome"].(string)
	if outcome != "pass" && outcome != "fail" {
		return errors.New("gate_outcome must be pass or fail")
	}
	if evs, ok := doc["evidence"].([]any); !ok || len(evs) == 0 {
		return errors.New("evidence must be a non-empty array")
	} else if err := validateEvidence(evs); err != nil {
		return err
	}
	if err := validateObservedModel(doc["observed_model"]); err != nil {
		return err
	}
	if err := validateReview(doc["review"], outcome); err != nil {
		return err
	}
	return validateValidatorRun(doc["validator_run"])
}

// validateRepoState checks the repo_state block's required fields,
// digest shapes and dirty flag.
func validateRepoState(v any) error {
	state, _ := v.(map[string]any)
	if state == nil {
		return errors.New("repo_state must be an object")
	}
	for _, key := range []string{"commit_sha", "manifest_sha256", "job_id"} {
		if _, present := state[key]; !present {
			return fmt.Errorf("repo_state missing required field %q", key)
		}
	}
	if dirty, present := state["dirty"]; present && dirty != false {
		return errors.New("repo_state.dirty must be false")
	}
	if sha, _ := state["commit_sha"].(string); !sha40.MatchString(sha) {
		return errors.New("repo_state.commit_sha must be a 40-char hex sha")
	}
	sha, _ := state["manifest_sha256"].(string)
	if !sha64.MatchString(sha) {
		return errors.New("repo_state.manifest_sha256 must be a 64-char hex sha")
	}
	return nil
}

// validateEvidence checks each command evidence row's required
// fields, digest shapes and capture timestamp.
func validateEvidence(evs []any) error {
	for i, item := range evs {
		row, _ := item.(map[string]any)
		if row == nil {
			return fmt.Errorf("evidence[%d] must be an object", i)
		}
		if cmd, _ := row["command"].(string); cmd == "" {
			return fmt.Errorf("evidence[%d].command must not be empty", i)
		}
		for _, key := range []string{"expected_exit_code", "exit_code"} {
			if _, ok := row[key].(float64); !ok {
				return fmt.Errorf("evidence[%d].%s must be an integer", i, key)
			}
		}
		for _, key := range []string{"stdout", "stderr"} {
			if err := validateArtifact(row[key], fmt.Sprintf("evidence[%d].%s", i, key)); err != nil {
				return err
			}
		}
		if ts, _ := row["captured_at_utc"].(string); !dateTime.MatchString(ts) {
			return fmt.Errorf("evidence[%d].captured_at_utc must be RFC3339 date-time", i)
		}
	}
	return nil
}

// validateArtifact checks an {path, sha256} object reference.
func validateArtifact(v any, where string) error {
	art, _ := v.(map[string]any)
	if art == nil {
		return fmt.Errorf("%s must be an object", where)
	}
	if p, _ := art["path"].(string); p == "" {
		return fmt.Errorf("%s.path must not be empty", where)
	}
	if sha, _ := art["sha256"].(string); !sha64.MatchString(sha) {
		return fmt.Errorf("%s.sha256 must be a 64-char hex sha", where)
	}
	return nil
}

// validateObservedModel checks the observed attribution block.
func validateObservedModel(v any) error {
	m, _ := v.(map[string]any)
	if m == nil {
		return errors.New("observed_model must be an object")
	}
	for _, key := range []string{"provider", "model_id", "harness", "source"} {
		if s, _ := m[key].(string); s == "" {
			return fmt.Errorf("observed_model.%s must not be empty", key)
		}
	}
	return nil
}

// validateReview checks the review block; a passing gate requires an
// accepting verdict (a rejecting review cannot admit a capability).
func validateReview(v any, outcome string) error {
	r, _ := v.(map[string]any)
	if r == nil {
		return errors.New("review must be an object")
	}
	for _, key := range []string{"reviewer_identity", "evidence_ref"} {
		if s, _ := r[key].(string); s == "" {
			return fmt.Errorf("review.%s must not be empty", key)
		}
	}
	if sha, _ := r["candidate_sha"].(string); !sha40.MatchString(sha) {
		return errors.New("review.candidate_sha must be a 40-char hex sha")
	}
	verdict, _ := r["verdict"].(string)
	if verdict != "accept" && verdict != "reject" {
		return errors.New("review.verdict must be accept or reject")
	}
	if outcome == "pass" && verdict != "accept" {
		return errors.New("gate_outcome pass requires review.verdict accept")
	}
	return nil
}

// validateValidatorRun checks the documentation-validator block; the
// docs validator must have exited 0 for an admissible receipt.
func validateValidatorRun(v any) error {
	w, _ := v.(map[string]any)
	if w == nil {
		return errors.New("validator_run must be an object")
	}
	if code, ok := w["exit_code"].(float64); !ok || int(code) != 0 {
		return errors.New("validator_run.exit_code must be 0")
	}
	if sha, _ := w["docs_pack_manifest_sha256"].(string); !sha64.MatchString(sha) {
		return errors.New("validator_run.docs_pack_manifest_sha256 must be a 64-char hex sha")
	}
	return validateArtifact(w["artefact"], "validator_run.artefact")
}
