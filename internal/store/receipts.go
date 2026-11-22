package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	sqlite "github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Receipt-insert sentinels; a rejected receipt writes nothing.
var (
	// ErrReceiptOutcomeInvalid rejects a gate outcome outside pass/fail.
	ErrReceiptOutcomeInvalid = errors.New("store: gate_outcome must be pass or fail")
	// ErrReceiptRefMissing rejects a receipt that names no document.
	ErrReceiptRefMissing = errors.New("store: receipt_ref must not be empty")
)

// NewReceiptLog binds the ORM to an existing database handle whose
// schema already holds the receipts table. The caller owns the
// handle and must keep it open while the log is used.
func NewReceiptLog(db *sql.DB) (*ReceiptLog, error) {
	g, err := gorm.Open(sqlite.New(sqlite.Config{Conn: db}), &gorm.Config{
		Logger:                 logger.Discard,
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, fmt.Errorf("store: open receipt orm: %w", err)
	}
	return &ReceiptLog{gdb: g}, nil
}

// Insert records one receipt index row. The gate outcome must be
// pass or fail (ErrReceiptOutcomeInvalid) and the receipt reference
// non-empty (ErrReceiptRefMissing); an empty RecordedAtUTC becomes
// the current UTC time in RFC 3339 form. The caller supplies the
// digest of the verified receipt document.
func (l *ReceiptLog) Insert(ctx context.Context, r Receipt) error {
	if r.GateOutcome != SwarmRunOutcomePass && r.GateOutcome != SwarmRunOutcomeFail {
		return fmt.Errorf("%w, got %q", ErrReceiptOutcomeInvalid, r.GateOutcome)
	}
	if r.ReceiptRef == "" {
		return ErrReceiptRefMissing
	}
	if r.RecordedAtUTC == "" {
		r.RecordedAtUTC = time.Now().UTC().Format(time.RFC3339)
	}
	row := receiptRow{
		ID: r.ID, CapabilityID: r.CapabilityID, CommitSHA: r.CommitSHA,
		ReceiptRef: r.ReceiptRef, ReceiptSHA256: r.ReceiptSHA256,
		GateOutcome: r.GateOutcome, RecordedAtUTC: r.RecordedAtUTC,
	}
	if err := l.gdb.WithContext(ctx).Create(&row).Error; err != nil {
		return fmt.Errorf("store: insert receipt: %w", err)
	}
	return nil
}
