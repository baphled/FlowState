package store

import "gorm.io/gorm"

// Receipt is one acceptance-receipt index row: it records a receipt
// document that was uploaded and read back byte-exact. The columns
// are those of the receipts table; RecordedAtUTC defaults to the
// insert time when empty.
type Receipt struct {
	ID            string
	CapabilityID  string
	CommitSHA     string
	ReceiptSHA256 string
	GateOutcome   string
	ReceiptRef    string
	RecordedAtUTC string
}

// ReceiptLog writes receipt index rows through the ORM onto a
// caller-owned database handle; the caller controls the handle's
// lifecycle. The receipts table must already exist.
type ReceiptLog struct {
	gdb *gorm.DB
}
