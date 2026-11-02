package storage

import (
	"context"
	"fmt"
	"time"
)

// InsertReceipt records one acceptance-receipt index row in the
// existing receipts table. No migration is needed (schema v1).
// gate_outcome must be pass or fail; the caller supplies the digest
// of the verified receipt document.
func InsertReceipt(ctx context.Context, db QueryExecer, r Receipt) error {
	if r.GateOutcome != "pass" && r.GateOutcome != "fail" {
		return fmt.Errorf("storage: gate_outcome must be pass or fail, got %q", r.GateOutcome)
	}
	if r.RecordedAtUTC == "" {
		r.RecordedAtUTC = time.Now().UTC().Format(time.RFC3339)
	}
	if r.ReceiptRef == "" {
		return fmt.Errorf("storage: receipt_ref must not be empty")
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO receipts (id, capability_id, commit_sha, receipt_ref, receipt_sha256, gate_outcome, recorded_at_utc)
		 VALUES (?,?,?,?,?,?,?)`,
		r.ID, r.CapabilityID, r.CommitSHA, r.ReceiptRef, r.ReceiptSHA256, r.GateOutcome, r.RecordedAtUTC)
	if err != nil {
		return fmt.Errorf("storage: insert receipt: %w", err)
	}
	return nil
}
