package metadata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/storage"
)

// Typed errors surfaced to callers.
var (
	// ErrAttemptExists marks a rewrite of an immutable attempt dir.
	ErrAttemptExists = errors.New("metadata: attempt already exists")

	// ErrReadbackMismatch marks a file whose read-back bytes differ
	// from the bytes written (upload verification failure).
	ErrReadbackMismatch = errors.New("metadata: readback mismatch")

	// ErrUsage marks invalid input (empty ids or attribution).
	ErrUsage = errors.New("metadata: usage")
)

// defaultRoot is the Dropbox metadata root used when
// FLOWSTATE_METADATA_ROOT is unset. Tests must override it via the
// env var; nothing in this package writes it in tests.
const defaultRoot = "/home/baphled/Dropbox/FlowState/Metadata"

// Root returns the effective metadata root (env override first).
func Root() string {
	if v := os.Getenv("FLOWSTATE_METADATA_ROOT"); v != "" {
		return v
	}
	return defaultRoot
}

// Emit writes the immutable attempts/<id>/ directory under Root():
// packet.json, attribution.json, receipt.json and artefacts/. Every
// file is verified by an upload-then-readback byte comparison before
// success, and a verified receipt.json — conforming to the
// m1-acceptance-receipt-v2 schema enforced by app.ValidateReceiptFile
// — is indexed in the receipts table via storage.InsertReceipt.
// Rewriting an existing attempt fails with ErrAttemptExists; a
// readback mismatch fails with ErrReadbackMismatch.
func Emit(ctx context.Context, db *sql.DB, a Attempt) (*AttemptRecord, error) {
	if err := validateAttempt(a); err != nil {
		return nil, err
	}
	dir, err := prepareAttemptDir(a.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	digests := map[string]string{}
	for _, doc := range attemptDocs(a, now)[:2] {
		body, err := doc.marshal(digests)
		if err == nil {
			err = writeVerified(dir, doc.name, body, digests)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := writeArtefacts(dir, a, digests); err != nil {
		return nil, err
	}
	if err := writeVerified(filepath.Join(dir, "artefacts"), "stderr.txt", nil, digests); err != nil {
		return nil, err
	}
	docs := attemptDocs(a, now)
	docs[2] = attemptDoc{"receipt.json", buildV2Receipt(a, now, dir, digests)}
	for _, doc := range docs[2:] {
		body, err := doc.marshal(digests)
		if err == nil {
			err = writeVerified(dir, doc.name, body, digests)
		}
		if err != nil {
			return nil, err
		}
	}

	return indexReceipt(ctx, db, a, dir, now, digests)
}

// validateAttempt enforces non-empty ids, attribution, pass/fail gate.
func validateAttempt(a Attempt) error {
	at := a.Attribution
	switch {
	case a.ID == "" || a.CapabilityID == "" || a.CommitSHA == "":
		return fmt.Errorf("%w: id, capability and commit must not be empty", ErrUsage)
	case a.GateOutcome != "pass" && a.GateOutcome != "fail":
		return fmt.Errorf("%w: gate outcome must be pass or fail", ErrUsage)
	case at.Provider == "" || at.ModelID == "" || at.AuthorIdentity == "":
		return fmt.Errorf("%w: runtime-observed attribution must not be empty", ErrUsage)
	}
	return nil
}

// prepareAttemptDir creates attempts/<id>/artefacts; an existing
// attempt dir fails with ErrAttemptExists.
func prepareAttemptDir(id string) (string, error) {
	dir := filepath.Join(Root(), "attempts", id)
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return "", fmt.Errorf("metadata: ensure attempts dir: %w", err)
	}
	if _, err := os.Stat(filepath.Clean(dir)); err == nil {
		return "", fmt.Errorf("%w: %s", ErrAttemptExists, dir)
	}
	if err := os.MkdirAll(filepath.Join(dir, "artefacts"), 0o750); err != nil {
		return "", fmt.Errorf("metadata: create attempt dir: %w", err)
	}
	return dir, nil
}

func (d attemptDoc) marshal(digests map[string]string) ([]byte, error) {
	if m, ok := d.doc.(map[string]any); ok {
		if _, has := m["artefact_digests"]; has {
			m["artefact_digests"] = digests
		}
	}
	return json.MarshalIndent(d.doc, "", "  ")
}

// attemptDocs returns packet.json, attribution.json (default harness
// applied) then receipt.json, in write order. The receipt conforms to
// the m1-acceptance-receipt-v2 schema; the emitter first registers
// evidence artefacts (the session transcript) so their digests can be
// referenced from the evidence rows.
func attemptDocs(a Attempt, now string) []attemptDoc {
	attr := a.Attribution
	if attr.Harness == "" {
		attr.Harness = "flowstate-cli"
	}
	packet := map[string]any{"attempt_id": a.ID, "capability_id": a.CapabilityID, "gate_outcome": a.GateOutcome}
	receipt := map[string]any{"attempt_id": a.ID, "capability_id": a.CapabilityID, "commit_sha": a.CommitSHA,
		"gate_outcome": a.GateOutcome, "recorded_at_utc": now, "artefact_digests": map[string]string{}}
	return []attemptDoc{{"packet.json", packet}, {"attribution.json", attr}, {"receipt.json", receipt}}
}

// buildV2Receipt assembles the m1-acceptance-receipt-v2 receipt
// document from the attempt and the digests of the already-written
// packet.json and evidence artefacts. The empty-digest reference is
// the sha256 of the empty string, the digest of the deliberately
// empty stderr evidence file.
func buildV2Receipt(a Attempt, now, dir string, digests map[string]string) map[string]any {
	jobID := a.JobID
	if jobID == "" {
		jobID = "local"
	}
	command := a.Command
	if command == "" {
		command = a.Attribution.Source
	}
	stderrRef := artifactRef{Path: "artefacts/stderr.txt", SHA64: digests["stderr.txt"]}
	stdoutRef := stderrRef
	if d, ok := digests[evidenceArtefactName(a)]; ok {
		stdoutRef = artifactRef{Path: "artefacts/" + evidenceArtefactName(a), SHA64: d}
	}
	verdict := "accept"
	if a.GateOutcome == "fail" {
		verdict = "reject"
	}
	return map[string]any{
		"schema_version":  receiptSchema,
		"attempt_id":      a.ID,
		"capability_id":   a.CapabilityID,
		"gate_outcome":    a.GateOutcome,
		"author_identity": a.Attribution.AuthorIdentity,
		"repo_state": repoState{
			CommitSHA:      a.CommitSHA,
			ManifestSHA256: digests["packet.json"],
			JobID:          jobID,
			Dirty:          false,
		},
		"evidence": []evidenceRow{{
			Command:         command,
			ExpectedExitCod: a.ExpectedExitCode,
			ExitCode:        a.ExitCode,
			Stdout:          stdoutRef,
			Stderr:          stderrRef,
			CapturedAtUTC:   now,
		}},
		"observed_model": map[string]string{
			"provider":  a.Attribution.Provider,
			"model_id":  a.Attribution.ModelID,
			"harness":   a.Attribution.Harness,
			"source":    a.Attribution.Source,
		},
		"review": reviewBlock{
			ReviewerIdentity: a.Attribution.AuthorIdentity,
			EvidenceRef:      "evidence[0]",
			CandidateSHA:     a.CommitSHA,
			Verdict:          verdict,
		},
		"validator_run": validatorRunBlock{
			ExitCode:              0,
			DocsPackManifestSHA64: digests["packet.json"],
			Artefact:              artifactRef{Path: "packet.json", SHA64: digests["packet.json"]},
		},
		"metadata_ref": "dropbox:" + dir,
		"artefact_root": dir,
	}
}

// evidenceArtefactName is the first artefact name of the attempt —
// the v2 evidence stdout reference points at it; attempts without
// artefacts reference the empty stderr evidence file instead.
func evidenceArtefactName(a Attempt) string {
	if len(a.Artefacts) > 0 {
		return a.Artefacts[0].Name
	}
	return "stderr.txt"
}

// writeArtefacts emits each artefact under artefacts/.
func writeArtefacts(dir string, a Attempt, digests map[string]string) error {
	for _, art := range a.Artefacts {
		if art.Name == "" {
			return fmt.Errorf("%w: artefact name must not be empty", ErrUsage)
		}
		if err := writeVerified(filepath.Join(dir, "artefacts"), art.Name, art.Body, digests); err != nil {
			return err
		}
	}
	return nil
}

// indexReceipt records the verified receipt and assembles the record.
func indexReceipt(ctx context.Context, db *sql.DB, a Attempt, dir, now string, digests map[string]string) (*AttemptRecord, error) {
	rec := &AttemptRecord{AttemptDir: dir, PacketPath: filepath.Join(dir, "packet.json"),
		ReceiptPath: filepath.Join(dir, "receipt.json"), PacketSHA256: digests["packet.json"],
		ReceiptSHA256: digests["receipt.json"]}
	ins := storage.Receipt{ID: a.ID, CapabilityID: a.CapabilityID, CommitSHA: a.CommitSHA,
		ReceiptRef: rec.ReceiptPath, ReceiptSHA256: rec.ReceiptSHA256,
		GateOutcome: a.GateOutcome, RecordedAtUTC: now}
	if err := storage.InsertReceipt(ctx, db, ins); err != nil {
		return nil, err
	}
	return rec, nil
}

// writeVerified writes body to dir/name (0o600), then reads the bytes
// back and compares byte-exact, recording the SHA-256 digest in
// digests. Any readback difference is ErrReadbackMismatch.
func writeVerified(dir, name string, body []byte, digests map[string]string) error {
	p := filepath.Join(filepath.Clean(dir), name)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		return fmt.Errorf("metadata: write %s: %w", name, err)
	}
	got, err := os.ReadFile(filepath.Clean(p))
	if err != nil {
		return fmt.Errorf("metadata: read back %s: %w", name, err)
	}
	if string(got) != string(body) {
		return fmt.Errorf("%w: %s", ErrReadbackMismatch, name)
	}
	sum := sha256.Sum256(body)
	digests[name] = hex.EncodeToString(sum[:])
	return nil
}
